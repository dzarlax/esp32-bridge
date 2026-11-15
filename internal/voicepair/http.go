package voicepair

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
)

// HTTPConfig is an explicit opt-in; Admin must enforce authentication, CSRF and
// Origin. RoutingUpdate holds the routing lock while mutation and reconciliation run.
type HTTPConfig struct {
	Enabled          bool
	TrustedTransport bool
	HTTPSOrigin      string
	Manager          *Manager
	Admin            func(http.Handler) http.Handler
	DeviceExists     func(ID) bool
	RoutingUpdate    func(func() error) error
	Connected        func(ID) bool
}
type HTTPAdapter struct {
	config HTTPConfig
	admin  http.Handler
}

func NewHTTPAdapter(c HTTPConfig) (*HTTPAdapter, error) {
	if !c.Enabled || !c.TrustedTransport || c.Manager == nil || c.Admin == nil || c.DeviceExists == nil || c.RoutingUpdate == nil {
		return nil, ErrRejected
	}
	if _, err := canonicalAuthority(c.HTTPSOrigin, "https"); err != nil {
		return nil, ErrRejected
	}
	a := &HTTPAdapter{config: c}
	a.admin = c.Admin(http.HandlerFunc(a.adminRequest))
	if a.admin == nil {
		return nil, ErrRejected
	}
	return a, nil
}

// AdminHandler returns the middleware-protected admin sub-handler.
func (a *HTTPAdapter) AdminHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/admin/v1/devices/") {
			http.NotFound(w, r)
			return
		}
		a.ServeHTTP(w, r)
	})
}

// DeviceHandler returns the device-only endpoint handler.
func (a *HTTPAdapter) DeviceHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.TLS == nil {
			reject(w)
			return
		}
		if r.URL.RawQuery != "" || r.URL.ForceQuery || (r.URL.Path != "/api/voice/pair/v1/challenge" && r.URL.Path != "/api/voice/pair/v1/proof") {
			reject(w)
			return
		}
		a.deviceRequest(w, r)
	})
}
func (a *HTTPAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.TLS == nil {
		reject(w)
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		reject(w)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/v1/devices/") {
		a.admin.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/api/voice/pair/v1/challenge" || r.URL.Path == "/api/voice/pair/v1/proof" {
		a.deviceRequest(w, r)
		return
	}
	http.NotFound(w, r)
}
func reject(w http.ResponseWriter) { http.Error(w, "request rejected", http.StatusBadRequest) }
func sendJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func object(r *http.Request, allowed map[string]bool) (map[string]json.RawMessage, error) {
	typ, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || typ != "application/json" || len(params) != 0 {
		return nil, ErrRejected
	}
	if r.ContentLength > 2048 {
		return nil, ErrRejected
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2049))
	if err != nil || len(body) > 2048 {
		return nil, ErrRejected
	}
	d := json.NewDecoder(strings.NewReader(string(body)))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, ErrRejected
	}
	out := map[string]json.RawMessage{}
	for d.More() {
		v, e := d.Token()
		key, ok := v.(string)
		if e != nil || !ok || !allowed[key] {
			return nil, ErrRejected
		}
		if _, exists := out[key]; exists {
			return nil, ErrRejected
		}
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return nil, ErrRejected
		}
		out[key] = raw
	}
	if v, e := d.Token(); e != nil || v != json.Delim('}') {
		return nil, ErrRejected
	}
	if _, e := d.Token(); e != io.EOF {
		return nil, ErrRejected
	}
	return out, nil
}
func stringField(m map[string]json.RawMessage, k string, required bool) (string, error) {
	raw, ok := m[k]
	if !ok {
		if required {
			return "", ErrRejected
		}
		return "", nil
	}
	var s string
	if json.Unmarshal(raw, &s) != nil || string(raw) == "null" {
		return "", ErrRejected
	}
	return s, nil
}
func secretField(s string) (Secret, error) {
	var out Secret
	b, e := decode64(s, 32)
	if e != nil {
		return out, ErrRejected
	}
	copy(out[:], b)
	return out, nil
}
func generationField(s string) (Generation, error) {
	var out Generation
	if len(s) != 32 || strings.ToLower(s) != s {
		return out, ErrRejected
	}
	b, e := hex.DecodeString(s)
	if e != nil {
		return out, ErrRejected
	}
	copy(out[:], b)
	return out, nil
}
func (a *HTTPAdapter) adminRequest(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/api/admin/v1/devices/")
	parts := strings.Split(tail, "/")
	if len(parts) < 2 || len(parts) > 3 || parts[1] != "voice" {
		http.NotFound(w, r)
		return
	}
	id, err := DecodeID(parts[0])
	if err != nil || !a.config.DeviceExists(id) {
		reject(w)
		return
	}
	if len(parts) == 2 && r.Method == http.MethodGet {
		a.sendStatus(w, id)
		return
	}
	if len(parts) != 3 || r.Method != http.MethodPost {
		reject(w)
		return
	}
	switch parts[2] {
	case "pair":
		o, e := object(r, map[string]bool{"pairing_code": true, "wss_origin": true})
		if e != nil {
			reject(w)
			return
		}
		qr, e := stringField(o, "pairing_code", true)
		if e != nil {
			reject(w)
			return
		}
		wss, e := stringField(o, "wss_origin", true)
		if e != nil || a.config.Manager.Approve(qr, id, a.config.Manager.bridge, a.config.HTTPSOrigin, wss) != nil {
			reject(w)
			return
		}
		a.sendStatus(w, id)
	case "cancel":
		if _, e := object(r, map[string]bool{}); e != nil {
			reject(w)
			return
		}
		if a.config.Manager.Cancel(id) != nil {
			reject(w)
			return
		}
		a.sendStatus(w, id)
	case "disable":
		if _, e := object(r, map[string]bool{}); e != nil {
			reject(w)
			return
		}
		if a.routingMutation(func() error { _, err := a.config.Manager.Revoke(id); return err }) != nil {
			reject(w)
			return
		}
		a.sendStatus(w, id)
	default:
		reject(w)
	}
}
func (a *HTTPAdapter) deviceRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		reject(w)
		return
	}
	proof := strings.HasSuffix(r.URL.Path, "/proof")
	allowed := map[string]bool{"bridge_id": true, "device_id": true, "session": true, "op": true, "generation": true, "revocation_epoch": true}
	if !proof {
		allowed["client_nonce"] = true
		allowed["pre_signature"] = true
	}
	if proof {
		allowed["challenge"] = true
		allowed["signature"] = true
	}
	o, err := object(r, allowed)
	if err != nil {
		reject(w)
		return
	}
	bs, e := stringField(o, "bridge_id", true)
	if e != nil {
		reject(w)
		return
	}
	ds, e := stringField(o, "device_id", true)
	if e != nil {
		reject(w)
		return
	}
	op, e := stringField(o, "op", true)
	if e != nil {
		reject(w)
		return
	}
	bridge, e := DecodeID(bs)
	if e != nil {
		reject(w)
		return
	}
	device, e := DecodeID(ds)
	if e != nil {
		reject(w)
		return
	}
	t := Transcript{Bridge: bridge, Device: device, HTTPSOrigin: a.config.HTTPSOrigin}
	switch op {
	case "deliver":
		t.Operation = Deliver
	case "ack":
		t.Operation = Ack
	case "clear_ack":
		t.Operation = ClearAck
	default:
		reject(w)
		return
	}
	if t.Operation != ClearAck {
		s, e := stringField(o, "session", true)
		if e != nil {
			reject(w)
			return
		}
		t.Session, e = secretField(s)
		if e != nil {
			reject(w)
			return
		}
	} else if _, ok := o["session"]; ok {
		reject(w)
		return
	}
	if t.Operation != Deliver {
		s, e := stringField(o, "generation", true)
		if e != nil {
			reject(w)
			return
		}
		t.Generation, e = generationField(s)
		if e != nil {
			reject(w)
			return
		}
	} else if _, ok := o["generation"]; ok {
		reject(w)
		return
	}
	if t.Operation == ClearAck {
		raw, ok := o["revocation_epoch"]
		if !ok || json.Unmarshal(raw, &t.Epoch) != nil || t.Epoch == 0 {
			reject(w)
			return
		}
	} else if _, ok := o["revocation_epoch"]; ok {
		reject(w)
		return
	}
	if !proof {
		var ch Secret
		if t.Operation == ClearAck {
			ns, err := stringField(o, "client_nonce", true)
			if err != nil {
				reject(w)
				return
			}
			nonce, err := secretField(ns)
			if err != nil {
				reject(w)
				return
			}
			sig, err := stringField(o, "pre_signature", true)
			if err != nil {
				reject(w)
				return
			}
			ch, e = a.config.Manager.IssueClearAck(t, nonce, sig)
		} else {
			if _, ok := o["client_nonce"]; ok {
				reject(w)
				return
			}
			if _, ok := o["pre_signature"]; ok {
				reject(w)
				return
			}
			ch, e = a.config.Manager.Issue(t)
		}
		if e != nil {
			reject(w)
			return
		}
		sendJSON(w, map[string]any{"challenge": base64.RawURLEncoding.EncodeToString(ch[:]), "expires_in": 30})
		return
	}
	cs, e := stringField(o, "challenge", true)
	if e != nil {
		reject(w)
		return
	}
	t.Challenge, e = secretField(cs)
	if e != nil {
		reject(w)
		return
	}
	sig, e := stringField(o, "signature", true)
	if e != nil {
		reject(w)
		return
	}
	var d *Delivery
	if t.Operation == Ack {
		e = a.routingMutation(func() error { var err error; d, err = a.config.Manager.ProveBound(t, sig); return err })
	} else {
		d, e = a.config.Manager.ProveBound(t, sig)
	}
	if e != nil {
		reject(w)
		return
	}
	if d == nil {
		sendJSON(w, map[string]string{"status": "ok"})
		return
	}
	sendJSON(w, map[string]string{"generation": hex.EncodeToString(d.Generation[:]), "wss_base": d.WSSBase, "token": d.Token, "server_signing_key": base64.RawURLEncoding.EncodeToString(d.ServerSigningKey[:])})
}

func (a *HTTPAdapter) sendStatus(w http.ResponseWriter, id ID) {
	s := a.config.Manager.Status(id)
	status := "error"
	switch s.State {
	case "unpaired", "revoked":
		status = "unconfigured"
	case "awaiting", "pending_ack":
		status = "pending"
	case "active":
		status = "applied"
		if a.config.Connected != nil && a.config.Connected(id) {
			status = "connected"
		}
	case "pending_clear":
		status = "revoked_cleanup_pending"
	}
	sendJSON(w, map[string]string{"device_id": hex.EncodeToString(id[:]), "wss_origin": s.WSSOrigin, "status": status})
}

// routingMutation requires the callback to invoke the mutation exactly once and
// return only after synchronously reconciling routing and closing stale sockets.
func (a *HTTPAdapter) routingMutation(mutate func() error) error {
	called := false
	invalid := false
	var operationErr error
	err := a.config.RoutingUpdate(func() error {
		if called {
			invalid = true
			return ErrRejected
		}
		called = true
		operationErr = mutate()
		return operationErr
	})
	if !called || invalid || operationErr != nil || err != nil {
		return ErrRejected
	}
	return nil
}
