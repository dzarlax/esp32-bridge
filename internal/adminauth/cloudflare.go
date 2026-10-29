package adminauth

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxCertsBody = 1 << 20

type rsaKey struct{ key *rsa.PublicKey }
type jwks struct {
	Keys []jwk `json:"keys"`
}
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type accessClaims struct {
	Issuer    string          `json:"iss"`
	Audience  json.RawMessage `json:"aud"`
	Expires   json.Number     `json:"exp"`
	NotBefore json.Number     `json:"nbf"`
	Email     string          `json:"email"`
}

func (a *Auth) validateAssertion(r *http.Request, expectedEmail string) bool {
	email, ok := a.assertionEmail(r)
	return ok && (expectedEmail == "" || strings.EqualFold(email, expectedEmail))
}

func (a *Auth) assertionEmail(r *http.Request) (string, bool) {
	if a.teamDomain == "" {
		return "", false
	}
	values := r.Header.Values("Cf-Access-Jwt-Assertion")
	if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 16*1024 {
		return "", false
	}
	parts := strings.Split(values[0], ".")
	if len(parts) != 3 {
		return "", false
	}
	headerBytes, err1 := base64.RawURLEncoding.DecodeString(parts[0])
	payloadBytes, err2 := base64.RawURLEncoding.DecodeString(parts[1])
	signature, err3 := base64.RawURLEncoding.DecodeString(parts[2])
	if err1 != nil || err2 != nil || err3 != nil || len(headerBytes) > 2048 || len(payloadBytes) > 8192 {
		return "", false
	}
	var header struct {
		Algorithm string   `json:"alg"`
		KeyID     string   `json:"kid"`
		Type      string   `json:"typ"`
		Critical  []string `json:"crit"`
	}
	if json.Unmarshal(headerBytes, &header) != nil || header.Algorithm != "RS256" || header.KeyID == "" || len(header.Critical) != 0 {
		return "", false
	}
	key, ok := a.signingKey(header.KeyID)
	if !ok {
		return "", false
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key.key, crypto.SHA256, digest[:], signature) != nil {
		return "", false
	}
	var claims accessClaims
	d := json.NewDecoder(strings.NewReader(string(payloadBytes)))
	d.UseNumber()
	if d.Decode(&claims) != nil || claims.Issuer != a.teamDomain {
		return "", false
	}
	audiences, ok := parseAudiences(claims.Audience)
	if !ok {
		return "", false
	}
	matched := false
	for _, aud := range audiences {
		if aud == a.audience {
			matched = true
			break
		}
	}
	if !matched {
		return "", false
	}
	now := time.Now().Unix()
	exp, err := strconv.ParseInt(string(claims.Expires), 10, 64)
	if err != nil || exp <= now {
		return "", false
	}
	if claims.NotBefore != "" {
		nbf, err := strconv.ParseInt(string(claims.NotBefore), 10, 64)
		if err != nil || nbf > now+30 {
			return "", false
		}
	}
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if !validEmail(email) {
		return "", false
	}
	if _, allowed := a.emails[email]; !allowed {
		return "", false
	}
	return email, true
}

func parseAudiences(raw json.RawMessage) ([]string, bool) {
	var one string
	if json.Unmarshal(raw, &one) == nil && one != "" {
		return []string{one}, true
	}
	var many []string
	if json.Unmarshal(raw, &many) != nil || len(many) == 0 {
		return nil, false
	}
	return many, true
}

func (a *Auth) signingKey(kid string) (*rsaKey, bool) {
	now := time.Now()
	a.mu.Lock()
	keysAt := a.keysAt
	keys, fresh := a.keys, now.Sub(keysAt) < 30*time.Minute
	key, found := keys[kid]
	a.mu.Unlock()
	if fresh && found {
		return &key, true
	}
	if fresh && !found {
		// Refresh for an unknown kid at most once per minute to handle key
		// rotation without letting attacker-controlled kids trigger fetches.
		if now.Sub(keysAt) < time.Minute {
			return nil, false
		}
		if err := a.refreshKeys(); err != nil {
			return nil, false
		}
	} else if err := a.refreshKeys(); err != nil {
		return nil, false
	}
	a.mu.Lock()
	key, found = a.keys[kid]
	a.mu.Unlock()
	return &key, found
}

func (a *Auth) refreshKeys() error {
	a.keyFetchMu.Lock()
	defer a.keyFetchMu.Unlock()
	// A concurrent request may have refreshed while this request waited.
	a.mu.Lock()
	keysAt, attemptAt := a.keysAt, a.keyAttemptAt
	a.mu.Unlock()
	if time.Since(keysAt) < time.Minute {
		return nil
	}
	if !attemptAt.IsZero() && time.Since(attemptAt) < time.Minute {
		return errors.New("Cloudflare Access key refresh is cooling down")
	}
	a.mu.Lock()
	a.keyAttemptAt = time.Now()
	a.mu.Unlock()
	u := a.teamDomain + "/cdn-cgi/access/certs"
	ctxReq, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	ctxReq.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(ctxReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("Cloudflare Access keys unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCertsBody+1))
	if err != nil || len(body) > maxCertsBody {
		return errors.New("invalid Cloudflare Access keys response")
	}
	var set jwks
	if json.Unmarshal(body, &set) != nil || len(set.Keys) == 0 || len(set.Keys) > 8 {
		return errors.New("invalid Cloudflare Access keys response")
	}
	keys := make(map[string]rsaKey, len(set.Keys))
	for _, item := range set.Keys {
		if item.Kty != "RSA" || item.Kid == "" || (item.Use != "" && item.Use != "sig") || (item.Alg != "" && item.Alg != "RS256") {
			continue
		}
		nBytes, nErr := base64.RawURLEncoding.DecodeString(item.N)
		eBytes, eErr := base64.RawURLEncoding.DecodeString(item.E)
		if nErr != nil || eErr != nil || len(nBytes) < 128 || len(nBytes) > 1024 || len(eBytes) == 0 || len(eBytes) > 4 {
			continue
		}
		e := new(big.Int).SetBytes(eBytes)
		if !e.IsInt64() || e.Int64() < 3 || e.Int64()%2 == 0 {
			continue
		}
		keys[item.Kid] = rsaKey{key: &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(e.Int64())}}
	}
	if len(keys) == 0 {
		return errors.New("Cloudflare Access response has no usable signing keys")
	}
	a.mu.Lock()
	a.keys, a.keysAt = keys, time.Now()
	a.mu.Unlock()
	return nil
}
