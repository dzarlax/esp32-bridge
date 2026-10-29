package adminauth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testPassword = "local-test-password"

func passwordConfig(t *testing.T) Config {
	t.Helper()
	hash, err := HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "admin.hash")
	if err := os.WriteFile(file, []byte(hash+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return Config{BaseURL: "https://bridge.example.test", PasswordHashFile: file}
}

func TestConfigModes(t *testing.T) {
	base := Config{BaseURL: "https://bridge.example.test"}
	if _, err := New(base, nil); err == nil {
		t.Fatal("expected no-auth config to fail closed")
	}
	pwd := passwordConfig(t)
	if _, err := New(pwd, nil); err != nil {
		t.Fatalf("password-only: %v", err)
	}
	permissiveHash := filepath.Join(t.TempDir(), "permissive.hash")
	hashBytes, err := os.ReadFile(pwd.PasswordHashFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(permissiveHash, hashBytes, 0644); err != nil {
		t.Fatal(err)
	}
	badPasswordConfig := pwd
	badPasswordConfig.PasswordHashFile = permissiveHash
	if _, err := New(badPasswordConfig, nil); err == nil {
		t.Fatal("accepted group-readable password hash file")
	}
	cf := Config{BaseURL: base.BaseURL, CloudflareTeamDomain: "https://bridge-team.cloudflareaccess.com", CloudflareAudience: "aud-123", CloudflareEmails: []string{"admin@example.test"}}
	if _, err := New(cf, nil); err != nil {
		t.Fatalf("Cloudflare-only: %v", err)
	}
	cf.PasswordHashFile = pwd.PasswordHashFile
	if _, err := New(cf, nil); err != nil {
		t.Fatalf("both auth methods: %v", err)
	}
	partialCF := cf
	partialCF.PasswordHashFile = ""
	partialCF.CloudflareEmails = nil
	if _, err := New(partialCF, nil); err == nil {
		t.Fatal("accepted Cloudflare config without an email allowlist")
	}

	loginMux := http.NewServeMux()
	cloudflareAuth, err := New(cf, nil)
	if err != nil {
		t.Fatal(err)
	}
	cloudflareAuth.Register(loginMux)
	redirectReq := httptest.NewRequest(http.MethodGet, "/api/admin/v1/auth/cloudflare", nil)
	redirect := httptest.NewRecorder()
	loginMux.ServeHTTP(redirect, redirectReq)
	if redirect.Code != http.StatusFound || !strings.HasPrefix(redirect.Header().Get("Location"), "https://bridge-team.cloudflareaccess.com/cdn-cgi/access/login/bridge.example.test?redirect_url=https%3A%2F%2Fbridge.example.test") {
		t.Fatalf("unexpected Cloudflare login redirect: %d %q", redirect.Code, redirect.Header().Get("Location"))
	}
	for _, bad := range []string{"http://bridge.example.test", "https://user:pass@bridge.example.test", "https://bridge.example.test/path"} {
		c := pwd
		c.BaseURL = bad
		if _, err := New(c, nil); err == nil {
			t.Errorf("accepted insecure/invalid base URL %q", bad)
		}
	}
}

func TestPasswordLoginCSRFLogoutAndExpiry(t *testing.T) {
	a, err := New(passwordConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a.Register(mux)
	mux.Handle("/api/admin/v1/settings", a.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := srv.Client()
	origin := "https://bridge.example.test"
	login := postJSON(t, client, srv.URL+"/api/admin/v1/login", origin, `{"password":"`+testPassword+`"}`, nil)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login: %d %s", login.StatusCode, readBody(t, login))
	}
	var session Session
	if err := json.NewDecoder(login.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	login.Body.Close()
	if !session.Authenticated || !session.PasswordEnabled || session.CloudflareEnabled || session.Identity == nil || session.Identity.Method != "password" || session.CSRFToken == "" {
		t.Fatalf("bad login session: %#v", session)
	}
	cookies := login.Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("bad local session cookie: %#v", cookies)
	}
	noCSRF := postWithCookie(t, client, srv.URL+"/api/admin/v1/settings", origin, cookies[0], "")
	if noCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF: %d", noCSRF.StatusCode)
	}
	noCSRF.Body.Close()
	wrongOrigin := postWithCookie(t, client, srv.URL+"/api/admin/v1/settings", "https://evil.test", cookies[0], session.CSRFToken)
	if wrongOrigin.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong origin: %d", wrongOrigin.StatusCode)
	}
	wrongOrigin.Body.Close()
	good := postWithCookie(t, client, srv.URL+"/api/admin/v1/settings", origin, cookies[0], session.CSRFToken)
	if good.StatusCode != http.StatusNoContent {
		t.Fatalf("authorized POST: %d", good.StatusCode)
	}
	good.Body.Close()
	logout := postWithCookie(t, client, srv.URL+"/api/admin/v1/logout", origin, cookies[0], session.CSRFToken)
	if logout.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: %d %s", logout.StatusCode, readBody(t, logout))
	}
	logout.Body.Close()
	check, err := http.NewRequest(http.MethodGet, srv.URL+"/api/admin/v1/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	check.AddCookie(cookies[0])
	resp, err := client.Do(check)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var loggedOut Session
	if err := json.NewDecoder(resp.Body).Decode(&loggedOut); err != nil {
		t.Fatal(err)
	}
	if loggedOut.Authenticated {
		t.Fatal("logout left an active session")
	}

	// Expiry is enforced from the server-side record even when a client keeps
	// its cookie.
	record, token, ok := a.newSession(Identity{Method: "password"})
	if !ok {
		t.Fatal("could not create test session")
	}
	record.expires = time.Now().Add(-time.Second)
	a.mu.Lock()
	a.sessions[token] = record
	a.mu.Unlock()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	if _, ok := a.sessionFromRequest(req); ok {
		t.Fatal("expired session accepted")
	}
}

func TestLoginRateLimitUsesPeerNotForwardedHeader(t *testing.T) {
	a, err := New(passwordConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "192.0.2.25:1234"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	peer := clientAddress(r)
	if peer != "192.0.2.25" {
		t.Fatalf("used spoofable forwarding header: %q", peer)
	}
	for i := 0; i < 10; i++ {
		if !a.allowLogin(peer) {
			t.Fatalf("attempt %d unexpectedly blocked", i+1)
		}
	}
	if a.allowLogin(peer) {
		t.Fatal("11th attempt should be blocked")
	}
	if !a.allowLogin("198.51.100.1") {
		t.Fatal("separate direct peer was blocked")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCloudflareAssertionValidationAndCertCache(t *testing.T) {
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	kid := "test-key-1"
	n := base64.RawURLEncoding.EncodeToString(private.PublicKey.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(private.PublicKey.E)).Bytes())
	var fetches atomic.Int32
	certs, _ := json.Marshal(jwks{Keys: []jwk{{Kty: "RSA", Kid: kid, Use: "sig", Alg: "RS256", N: n, E: e}}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		if r.URL.Path != "/cdn-cgi/access/certs" {
			t.Errorf("unexpected cert path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(certs)
	}))
	defer server.Close()
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		copyReq := req.Clone(req.Context())
		copyReq.URL.Scheme = "http"
		copyReq.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return http.DefaultTransport.RoundTrip(copyReq)
	})}
	cfg := Config{BaseURL: "https://bridge.example.test", CloudflareTeamDomain: "https://bridge-team.cloudflareaccess.com", CloudflareAudience: "aud-123", CloudflareEmails: []string{"admin@example.test"}}
	a, err := New(cfg, client)
	if err != nil {
		t.Fatal(err)
	}
	valid := signToken(t, private, kid, cfg.CloudflareTeamDomain, "aud-123", "admin@example.test", time.Now().Add(time.Hour))
	for _, tc := range []struct {
		name, token string
		ok          bool
	}{
		{"valid", valid, true},
		{"expired", signToken(t, private, kid, cfg.CloudflareTeamDomain, "aud-123", "admin@example.test", time.Now().Add(-time.Minute)), false},
		{"wrong audience", signToken(t, private, kid, cfg.CloudflareTeamDomain, "other", "admin@example.test", time.Now().Add(time.Hour)), false},
		{"wrong issuer", signToken(t, private, kid, "https://attacker.cloudflareaccess.com", "aud-123", "admin@example.test", time.Now().Add(time.Hour)), false},
		{"not allowlisted", signToken(t, private, kid, cfg.CloudflareTeamDomain, "aud-123", "other@example.test", time.Now().Add(time.Hour)), false},
		{"forged signature", tamperSignature(t, valid), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.Header.Set("Cf-Access-Jwt-Assertion", tc.token)
			got, ok := a.assertionEmail(r)
			if ok != tc.ok {
				t.Fatalf("ok=%t, want %t; email=%q", ok, tc.ok, got)
			}
			if tc.ok && got != "admin@example.test" {
				t.Fatalf("email=%q", got)
			}
		})
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Cf-Access-Jwt-Assertion", valid)
	r.Header.Set("Cf-Access-Email", "admin@example.test")
	if got, ok := a.assertionEmail(r); !ok || got != "admin@example.test" {
		t.Fatal("untrusted email header affected assertion validation")
	}
	if fetches.Load() != 1 {
		t.Fatalf("cert endpoint fetched %d times, want cached single fetch", fetches.Load())
	}
}

func TestConcurrentUnknownKIDRefreshSynchronizesCacheTimestamp(t *testing.T) {
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const kid = "rotated-key"
	certs, err := json.Marshal(jwks{Keys: []jwk{{Kty: "RSA", Kid: kid, Use: "sig", Alg: "RS256", N: base64.RawURLEncoding.EncodeToString(private.PublicKey.N.Bytes()), E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(private.PublicKey.E)).Bytes())}}})
	if err != nil {
		t.Fatal(err)
	}
	var fetches atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		fetches.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(certs))), Request: req}, nil
	})}
	a, err := New(Config{BaseURL: "https://bridge.example.test", CloudflareTeamDomain: "https://bridge-team.cloudflareaccess.com", CloudflareAudience: "aud", CloudflareEmails: []string{"admin@example.test"}}, client)
	if err != nil {
		t.Fatal(err)
	}

	// Model repeated cache expiry/unknown-kid observations while lookups run.
	// All timestamp writes use the same mutex as refreshKeys does.
	stop := make(chan struct{})
	var writers sync.WaitGroup
	writers.Add(1)
	go func() {
		defer writers.Done()
		for i := 0; i < 500; i++ {
			select {
			case <-stop:
				return
			default:
			}
			a.mu.Lock()
			a.keysAt = time.Now().Add(-31 * time.Minute)
			a.keyAttemptAt = time.Time{}
			a.keys = map[string]rsaKey{}
			a.mu.Unlock()
			runtime.Gosched()
		}
	}()

	var lookups sync.WaitGroup
	for i := 0; i < 24; i++ {
		lookups.Add(1)
		go func() {
			defer lookups.Done()
			for j := 0; j < 80; j++ {
				_, _ = a.signingKey(kid)
			}
		}()
	}
	lookups.Wait()
	close(stop)
	writers.Wait()
	if fetches.Load() == 0 {
		t.Fatal("unknown kid did not trigger a cert refresh")
	}
}

func TestCloudflareSessionRequiresAssertionOnEveryRequest(t *testing.T) {
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	kid := "test-key"
	certs, _ := json.Marshal(jwks{Keys: []jwk{{Kty: "RSA", Kid: kid, Use: "sig", Alg: "RS256", N: base64.RawURLEncoding.EncodeToString(private.PublicKey.N.Bytes()), E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(private.PublicKey.E)).Bytes())}}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(certs) }))
	defer server.Close()
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req = req.Clone(req.Context())
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return http.DefaultTransport.RoundTrip(req)
	})}
	cfg := Config{BaseURL: "https://bridge.example.test", CloudflareTeamDomain: "https://bridge-team.cloudflareaccess.com", CloudflareAudience: "aud", CloudflareEmails: []string{"admin@example.test"}}
	a, err := New(cfg, client)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a.Register(mux)
	mux.Handle("/api/admin/v1/settings", a.Require(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	token := signToken(t, private, kid, cfg.CloudflareTeamDomain, "aud", "admin@example.test", time.Now().Add(time.Hour))
	sessionReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/admin/v1/session", nil)
	sessionReq.Header.Set("Cf-Access-Jwt-Assertion", token)
	resp, err := srv.Client().Do(sessionReq)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var session Session
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	if !session.Authenticated || session.Identity == nil || session.Identity.Method != "cloudflare" {
		t.Fatalf("session=%#v", session)
	}
	cookie := resp.Cookies()[0]
	withoutAssertion, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/admin/v1/settings", nil)
	withoutAssertion.AddCookie(cookie)
	denied, err := srv.Client().Do(withoutAssertion)
	if err != nil {
		t.Fatal(err)
	}
	denied.Body.Close()
	if denied.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cookie bypassed Access assertion: %d", denied.StatusCode)
	}
	withAssertion, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/admin/v1/settings", nil)
	withAssertion.AddCookie(cookie)
	withAssertion.Header.Set("Cf-Access-Jwt-Assertion", token)
	allowed, err := srv.Client().Do(withAssertion)
	if err != nil {
		t.Fatal(err)
	}
	allowed.Body.Close()
	if allowed.StatusCode != http.StatusNoContent {
		t.Fatalf("valid assertion denied: %d", allowed.StatusCode)
	}

	// The callback establishes a session and returns to the actual panel.
	callback := httptest.NewRequest(http.MethodGet, "/api/admin/v1/auth/cloudflare", nil)
	callback.Header.Set("Cf-Access-Jwt-Assertion", token)
	callbackReply := httptest.NewRecorder()
	mux.ServeHTTP(callbackReply, callback)
	if callbackReply.Code != http.StatusSeeOther || callbackReply.Header().Get("Location") != "/settings/" {
		t.Fatal("callback did not return to settings")
	}
	// The signed application cookie authenticates API calls outside Access's
	// callback path, but a forged or ambiguous token must never fall back.
	for _, tc := range []struct {
		name, value, header string
		duplicate           bool
		want                int
	}{
		{"application cookie", token, "", false, 204},
		{"forged cookie", "forged", "", false, 401},
		{"wrong audience cookie", signToken(t, private, kid, cfg.CloudflareTeamDomain, "other", "admin@example.test", time.Now().Add(time.Hour)), "", false, 401},
		{"expired cookie", signToken(t, private, kid, cfg.CloudflareTeamDomain, "aud", "admin@example.test", time.Now().Add(-time.Hour)), "", false, 401},
		{"invalid assertion overrides cookie", token, "forged", false, 401},
		{"ambiguous cookie", token, "", true, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/admin/v1/settings", nil)
			req.AddCookie(cookie)
			req.AddCookie(&http.Cookie{Name: "CF_Authorization", Value: tc.value})
			if tc.header != "" {
				req.Header.Set("Cf-Access-Jwt-Assertion", tc.header)
			}
			if tc.duplicate {
				req.AddCookie(&http.Cookie{Name: "CF_Authorization", Value: token})
			}
			out := httptest.NewRecorder()
			mux.ServeHTTP(out, req)
			if out.Code != tc.want {
				t.Fatalf("got %d want %d", out.Code, tc.want)
			}
		})
	}
}

func postJSON(t *testing.T, c *http.Client, target, origin, body string, cookie *http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func postWithCookie(t *testing.T, c *http.Client, target, origin string, cookie *http.Cookie, csrf string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", origin)
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	req.AddCookie(cookie)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func signToken(t *testing.T, key *rsa.PrivateKey, kid, issuer, audience, email string, expires time.Time) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{"iss": issuer, "aud": []string{audience}, "exp": expires.Unix(), "nbf": time.Now().Add(-time.Minute).Unix(), "email": email})
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func tamperSignature(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	sig[0] ^= 0x80
	parts[2] = base64.RawURLEncoding.EncodeToString(sig)
	return strings.Join(parts, ".")
}
