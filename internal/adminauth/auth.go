// Package adminauth provides independent password and Cloudflare Access
// authentication for the Bridge administration API.
package adminauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookie = "bridge_admin_session"
	sessionTTL    = 8 * time.Hour
	maxSessions   = 4096
	maxBody       = 4096
)

type Config struct {
	BaseURL              string
	PasswordHashFile     string
	CloudflareTeamDomain string
	CloudflareAudience   string
	CloudflareEmails     []string
}

type Identity struct {
	Method string `json:"method"`
	Email  string `json:"email,omitempty"`
}

type Session struct {
	Authenticated     bool      `json:"authenticated"`
	PasswordEnabled   bool      `json:"password_enabled"`
	CloudflareEnabled bool      `json:"cloudflare_enabled"`
	CSRFToken         string    `json:"csrf_token,omitempty"`
	Identity          *Identity `json:"identity,omitempty"`
}

type sessionRecord struct {
	identity Identity
	csrf     string
	expires  time.Time
}

type failureWindow struct {
	started time.Time
	count   int
}

type Auth struct {
	baseURL      *url.URL
	passwordHash string
	teamDomain   string
	audience     string
	emails       map[string]struct{}
	client       *http.Client

	mu           sync.Mutex
	sessions     map[string]sessionRecord
	failures     map[string]failureWindow
	keys         map[string]rsaKey
	keysAt       time.Time
	keyAttemptAt time.Time
	keyFetchMu   sync.Mutex
}

func New(cfg Config, client *http.Client) (*Auth, error) {
	base, err := parseBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}

	passwordConfigured := cfg.PasswordHashFile != ""
	passwordHash := ""
	if passwordConfigured {
		passwordHash, err = readPasswordHash(cfg.PasswordHashFile)
		if err != nil {
			return nil, err
		}
	}

	cfAny := cfg.CloudflareTeamDomain != "" || cfg.CloudflareAudience != "" || len(cfg.CloudflareEmails) != 0
	team, audience := "", ""
	emails := make(map[string]struct{})
	if cfAny {
		team, err = parseTeamDomain(cfg.CloudflareTeamDomain)
		if err != nil || strings.TrimSpace(cfg.CloudflareAudience) == "" || len(cfg.CloudflareEmails) == 0 {
			return nil, errors.New("Cloudflare Access requires a valid team domain, audience, and email allowlist")
		}
		audience = strings.TrimSpace(cfg.CloudflareAudience)
		for _, email := range cfg.CloudflareEmails {
			email = strings.ToLower(strings.TrimSpace(email))
			if !validEmail(email) {
				return nil, errors.New("invalid Cloudflare Access email allowlist")
			}
			emails[email] = struct{}{}
		}
	}
	if !passwordConfigured && !cfAny {
		return nil, errors.New("admin authentication must be configured")
	}
	if client == nil {
		client = &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	} else {
		// Do not permit redirects from the fixed Cloudflare key endpoint.
		clone := *client
		clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		if clone.Timeout == 0 || clone.Timeout > 4*time.Second {
			clone.Timeout = 4 * time.Second
		}
		client = &clone
	}
	return &Auth{baseURL: base, passwordHash: passwordHash, teamDomain: team, audience: audience, emails: emails, client: client, sessions: make(map[string]sessionRecord), failures: make(map[string]failureWindow)}, nil
}

func (a *Auth) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/admin/v1/session", a.handleSession)
	mux.HandleFunc("/api/admin/v1/login", a.handleLogin)
	mux.HandleFunc("/api/admin/v1/logout", a.handleLogout)
	mux.HandleFunc("/api/admin/v1/auth/cloudflare", a.handleCloudflareLogin)
}

// Require protects a handler using the server-side admin session. Cloudflare
// sessions also require a fresh, valid Access assertion on every request.
func (a *Auth) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setNoStore(w)
		record, ok := a.sessionFromRequest(r)
		if !ok || (record.identity.Method == "cloudflare" && !a.validateAssertion(r, record.identity.Email)) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if unsafeMethod(r.Method) && !a.validOrigin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if unsafeMethod(r.Method) {
			provided := r.Header.Get("X-CSRF-Token")
			if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(record.csrf)) != 1 {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Auth) handleSession(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if s, ok := a.sessionFromRequest(r); ok {
		if s.identity.Method != "cloudflare" || a.validateAssertion(r, s.identity.Email) {
			writeJSON(w, http.StatusOK, a.sessionResponse(s))
			return
		}
	}
	if email, ok := a.assertionEmail(r); ok {
		if record, token, ok := a.newSession(Identity{Method: "cloudflare", Email: email}); ok {
			a.setSessionCookie(w, token, record.expires)
			writeJSON(w, http.StatusOK, a.sessionResponse(record))
			return
		}
	}
	writeJSON(w, http.StatusOK, Session{PasswordEnabled: a.passwordHash != "", CloudflareEnabled: a.teamDomain != ""})
}

func (a *Auth) handleLogin(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !a.validOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	key := clientAddress(r)
	if !a.allowLogin(key) {
		w.Header().Set("Retry-After", "900")
		http.Error(w, "too many login attempts", http.StatusTooManyRequests)
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &body); err != nil || len(body.Password) > 1024 || body.Password == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if a.passwordHash == "" || !verifyPassword(body.Password, a.passwordHash) {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	if record, token, ok := a.newSession(Identity{Method: "password"}); ok {
		a.clearFailures(key)
		a.setSessionCookie(w, token, record.expires)
		writeJSON(w, http.StatusOK, a.sessionResponse(record))
		return
	}
	http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
}

func (a *Auth) handleLogout(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !a.validOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	record, ok := a.sessionFromRequest(r)
	if !ok || (record.identity.Method == "cloudflare" && !a.validateAssertion(r, record.identity.Email)) {
		a.clearSessionCookie(w)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	provided := r.Header.Get("X-CSRF-Token")
	if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(record.csrf)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		a.mu.Lock()
		delete(a.sessions, cookie.Value)
		a.mu.Unlock()
	}
	a.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (a *Auth) handleCloudflareLogin(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if a.teamDomain == "" {
		http.Error(w, "Cloudflare Access is not enabled", http.StatusNotFound)
		return
	}
	login := a.teamDomain + "/cdn-cgi/access/login/" + url.PathEscape(a.baseURL.Hostname()) + "?redirect_url=" + url.QueryEscape(a.baseURL.String())
	http.Redirect(w, r, login, http.StatusFound)
}

func (a *Auth) sessionResponse(s sessionRecord) Session {
	return Session{Authenticated: true, PasswordEnabled: a.passwordHash != "", CloudflareEnabled: a.teamDomain != "", CSRFToken: s.csrf, Identity: &s.identity}
}

func (a *Auth) newSession(identity Identity) (sessionRecord, string, bool) {
	token, ok := randomToken(32)
	if !ok {
		return sessionRecord{}, "", false
	}
	csrf, ok := randomToken(32)
	if !ok {
		return sessionRecord{}, "", false
	}
	now := time.Now()
	record := sessionRecord{identity: identity, csrf: csrf, expires: now.Add(sessionTTL)}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneLocked(now)
	if len(a.sessions) >= maxSessions {
		return sessionRecord{}, "", false
	}
	a.sessions[token] = record
	return record, token, true
}

func (a *Auth) sessionFromRequest(r *http.Request) (sessionRecord, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return sessionRecord{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[cookie.Value]
	if !ok {
		return sessionRecord{}, false
	}
	if !time.Now().Before(s.expires) {
		delete(a.sessions, cookie.Value)
		return sessionRecord{}, false
	}
	return s, true
}

func (a *Auth) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: path.Dir("/api/admin/v1/session"), Expires: expires, MaxAge: int(time.Until(expires).Seconds()), HttpOnly: true, Secure: a.baseURL.Scheme == "https", SameSite: http.SameSiteLaxMode})
}

func (a *Auth) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: path.Dir("/api/admin/v1/session"), MaxAge: -1, HttpOnly: true, Secure: a.baseURL.Scheme == "https", SameSite: http.SameSiteLaxMode})
}

func (a *Auth) validOrigin(r *http.Request) bool {
	values := r.Header.Values("Origin")
	return len(values) == 1 && values[0] == a.baseURL.Scheme+"://"+a.baseURL.Host
}

func (a *Auth) allowLogin(key string) bool {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, f := range a.failures {
		if now.Sub(f.started) >= 15*time.Minute {
			delete(a.failures, k)
		}
	}
	if f, ok := a.failures[key]; ok && f.count >= 10 && now.Sub(f.started) < 15*time.Minute {
		return false
	}
	if len(a.failures) >= 4096 {
		oldestKey := ""
		var oldest time.Time
		for k, f := range a.failures {
			if oldestKey == "" || f.started.Before(oldest) {
				oldestKey, oldest = k, f.started
			}
		}
		delete(a.failures, oldestKey)
	}
	f := a.failures[key]
	if f.started.IsZero() || now.Sub(f.started) >= 15*time.Minute {
		f = failureWindow{started: now}
	}
	f.count++
	a.failures[key] = f
	return f.count <= 10
}

func (a *Auth) clearFailures(key string) { a.mu.Lock(); delete(a.failures, key); a.mu.Unlock() }

func (a *Auth) pruneLocked(now time.Time) {
	for k, s := range a.sessions {
		if !now.Before(s.expires) {
			delete(a.sessions, k)
		}
	}
}

func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr == "" {
		return "unknown"
	}
	return r.RemoteAddr
}

func parseBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("admin base URL must be an origin")
	}
	u.Path = ""
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
		return nil, errors.New("admin base URL must use HTTPS except on loopback")
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	u.Host = host
	return u, nil
}

func parseTeamDomain(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || strings.ToLower(u.Scheme) != "https" || u.Host == "" || u.User != nil || u.Port() != "" || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || !strings.HasSuffix(strings.ToLower(u.Hostname()), ".cloudflareaccess.com") {
		return "", errors.New("Cloudflare team domain must be an HTTPS cloudflareaccess.com origin")
	}
	host := strings.ToLower(u.Hostname())
	team := strings.TrimSuffix(host, ".cloudflareaccess.com")
	if team == "" || strings.Contains(team, ".") {
		return "", errors.New("invalid Cloudflare team domain")
	}
	u.Scheme = "https"
	u.Host = host
	return strings.TrimSuffix(u.String(), "/"), nil
}

func isLoopbackHost(host string) bool {
	return strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
}
func validEmail(s string) bool {
	if len(s) > 254 || strings.Count(s, "@") != 1 || strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	local, domain, _ := strings.Cut(s, "@")
	return local != "" && domain != "" && !strings.HasPrefix(domain, ".") && !strings.HasSuffix(domain, ".")
}
func unsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}
func setNoStore(w http.ResponseWriter) { w.Header().Set("Cache-Control", "no-store") }

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr != nil || !strings.EqualFold(mediaType, "application/json") {
		return errors.New("content type required")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

func methodNotAllowed(w http.ResponseWriter, method string) {
	w.Header().Set("Allow", method)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func randomToken(n int) (string, bool) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", false
	}
	return base64.RawURLEncoding.EncodeToString(b), true
}
