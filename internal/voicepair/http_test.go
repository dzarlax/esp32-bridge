package voicepair

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestHTTPFlow(t *testing.T) {
	dir := t.TempDir()
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("chmod")
	}
	bridge := ID{1}
	device := ID{2}
	other := ID{3}
	master := [32]byte{4}
	integrity := [32]byte{5}
	signer, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	m, err := Open(dir, bridge, master, "k", integrity, "i", signer)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	callbacks := 0
	failRouting := false
	connected := true
	connectedCalls := 0
	config := HTTPConfig{Enabled: true, TrustedTransport: true, HTTPSOrigin: "https://example.com:443", Manager: m, DeviceExists: func(id ID) bool { return id == device || id == other }, Connected: func(id ID) bool { connectedCalls++; return connected }, RoutingUpdate: func(mutate func() error) error {
		callbacks++
		before, selected := m.SelectedTarget()
		if callbacks == 1 && selected {
			t.Fatal("ACK mutated before routing transaction")
		}
		if callbacks == 2 && (!selected || before != device) {
			t.Fatal("revoke mutated before routing transaction")
		}
		if err := mutate(); err != nil {
			return err
		}
		after, selected := m.SelectedTarget()
		if callbacks == 1 && (!selected || after != device) {
			t.Fatal("ACK missing inside routing transaction")
		}
		if callbacks == 2 && selected {
			t.Fatal("revoke missing inside routing transaction")
		}
		if failRouting {
			return ErrRejected
		}
		return nil
	}, Admin: func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("auth") != "yes" || (r.Method != "GET" && r.Header.Get("csrf") != "yes") {
				http.Error(w, "denied", 403)
				return
			}
			next.ServeHTTP(w, r)
		})
	}}
	disabled := config
	disabled.TrustedTransport = false
	if _, err = NewHTTPAdapter(disabled); err == nil {
		t.Fatal("untrusted adapter enabled")
	}
	h, err := NewHTTPAdapter(config)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string, auth, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://example.com"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if auth {
			r.Header.Set("auth", "yes")
		}
		if csrf {
			r.Header.Set("csrf", "yes")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/api/voice/pair/v1/challenge", "/api/voice/pair/v1/proof", "/api/admin/v1/devices/" + hex.EncodeToString(device[:]) + "/voice/pair"} {
		r := httptest.NewRequest("POST", "http://example.com"+path, strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("auth", "yes")
		r.Header.Set("csrf", "yes")
		for _, handler := range []http.Handler{h, h.DeviceHandler(), h.AdminHandler()} {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code == 200 {
				t.Fatal("plaintext request accepted")
			}
		}
	}
	id := hex.EncodeToString(device[:])
	admin := "/api/admin/v1/devices/" + id + "/voice"
	if w := call("GET", admin, "", false, false); w.Code != 403 {
		t.Fatal("auth bypass")
	}
	if w := call("POST", admin+"/cancel", "", true, false); w.Code != 403 {
		t.Fatal("csrf bypass")
	}
	var nonce Secret
	nonce[0] = 6
	raw := make([]byte, 145)
	raw[0] = 2
	copy(raw[1:17], bridge[:])
	copy(raw[17:33], device[:])
	copy(raw[33:98], elliptic.Marshal(elliptic.P256(), key.X, key.Y))
	copy(raw[98:130], nonce[:])
	originHash := sha256.Sum256([]byte("https://example.com:443"))
	copy(raw[130:145], originHash[:15])
	qr := "HDV2:" + base64.RawURLEncoding.EncodeToString(raw)
	payload, _ := json.Marshal(map[string]string{"pairing_code": qr, "wss_origin": "wss://example.com:443"})
	if w := call("POST", admin+"/pair", string(payload), true, true); w.Code != 200 || strings.Contains(w.Body.String(), qr) || !strings.Contains(w.Body.String(), `"device_id":"`+id+`"`) || !strings.Contains(w.Body.String(), `"wss_origin":"wss://example.com:443"`) || !strings.Contains(w.Body.String(), `"status":"pending"`) {
		t.Fatal("approval failed or echoed secret", w.Code)
	}
	if connectedCalls != 0 {
		t.Fatal("connected queried before active")
	}
	if w := call("POST", admin+"/cancel", `{"unexpected":1}`, true, true); w.Code != 400 {
		t.Fatal("cancel accepted nonempty JSON")
	}
	if w := call("GET", admin, "", true, false); w.Code != 200 || strings.Contains(w.Body.String(), base64.RawURLEncoding.EncodeToString(nonce[:])) {
		t.Fatal("status leaked secret")
	}
	if w := call("POST", "/api/voice/pair/v1/challenge", `{"op":"deliver","op":"deliver"}`, false, false); w.Code != 400 {
		t.Fatal("duplicate accepted")
	}
	if w := call("POST", "/api/voice/pair/v1/challenge?x=1", `{}`, false, false); w.Code != 400 {
		t.Fatal("query accepted")
	}
	session := base64.RawURLEncoding.EncodeToString(nonce[:])
	base := map[string]any{"bridge_id": hex.EncodeToString(bridge[:]), "device_id": id, "op": "deliver", "session": session}
	request := func(path string, v map[string]any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(v)
		return call("POST", "/api/voice/pair/v1/"+path, string(b), false, false)
	}
	badExtra := map[string]any{}
	for k, v := range base {
		badExtra[k] = v
	}
	badExtra["client_nonce"] = session
	badExtra["pre_signature"] = "invalid"
	if w := request("challenge", badExtra); w.Code == 200 {
		t.Fatal("preauth fields accepted on deliver")
	}
	expiredResponse := request("challenge", base)
	var expiredIssued struct {
		Challenge string `json:"challenge"`
	}
	if json.Unmarshal(expiredResponse.Body.Bytes(), &expiredIssued) != nil {
		t.Fatal("expired setup")
	}
	expiredChallenge, _ := secretField(expiredIssued.Challenge)
	expiredTr := Transcript{Operation: Deliver, Bridge: bridge, Device: device, Session: nonce, Challenge: expiredChallenge, HTTPSOrigin: config.HTTPSOrigin}
	expiredReq := map[string]any{}
	for k, v := range base {
		expiredReq[k] = v
	}
	expiredReq["challenge"] = expiredIssued.Challenge
	expiredReq["signature"] = signTest(t, key, expiredTr)
	oldNow := m.now
	m.now = func() time.Time { return oldNow().Add(31 * time.Second) }
	if w := request("proof", expiredReq); w.Code != 400 {
		t.Fatal("expired challenge accepted")
	}
	m.now = oldNow
	issue := request("challenge", base)
	if issue.Code != 200 {
		t.Fatal("challenge rejected", issue.Code)
	}
	var issued struct {
		Challenge string `json:"challenge"`
	}
	if json.Unmarshal(issue.Body.Bytes(), &issued) != nil {
		t.Fatal("bad challenge")
	}
	ch, _ := secretField(issued.Challenge)
	tr := Transcript{Operation: Deliver, Bridge: bridge, Device: device, Session: nonce, Challenge: ch, HTTPSOrigin: config.HTTPSOrigin}
	base["challenge"] = issued.Challenge
	base["signature"] = signTest(t, key, tr)
	wrong := map[string]any{}
	for k, v := range base {
		wrong[k] = v
	}
	wrong["device_id"] = hex.EncodeToString(other[:])
	if w := request("proof", wrong); w.Code != 400 {
		t.Fatal("mismatched context accepted")
	}
	response := request("proof", base)
	if response.Code != 200 {
		t.Fatal("proof rejected", response.Code)
	}
	var d struct {
		Generation string `json:"generation"`
		Token      string `json:"token"`
	}
	if json.Unmarshal(response.Body.Bytes(), &d) != nil || d.Token == "" {
		t.Fatal("delivery missing")
	}
	if w := request("proof", base); w.Code != 400 || bytes.Contains(w.Body.Bytes(), []byte(d.Token)) {
		t.Fatal("replay accepted or token leaked")
	}
	gen, _ := generationField(d.Generation)
	ack := map[string]any{"bridge_id": hex.EncodeToString(bridge[:]), "device_id": id, "op": "ack", "session": session, "generation": d.Generation}
	ai := request("challenge", ack)
	if json.Unmarshal(ai.Body.Bytes(), &issued) != nil || ai.Code != 200 {
		t.Fatal("ack challenge")
	}
	ch, _ = secretField(issued.Challenge)
	atr := Transcript{Operation: Ack, Bridge: bridge, Device: device, Session: nonce, Challenge: ch, Generation: gen, HTTPSOrigin: config.HTTPSOrigin}
	ack["challenge"] = issued.Challenge
	ack["signature"] = signTest(t, key, atr)
	if w := request("proof", ack); w.Code != 200 || callbacks != 1 || !m.Authenticate(device, gen, d.Token) {
		t.Fatal("ack not synchronized")
	}
	if w := call("GET", admin, "", true, false); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"connected"`) || connectedCalls == 0 {
		t.Fatal("connected status missing")
	}
	connected = false
	if w := call("GET", admin, "", true, false); !strings.Contains(w.Body.String(), `"status":"applied"`) {
		t.Fatal("disconnected status missing")
	}
	failRouting = true
	if w := call("POST", admin+"/disable", "{}", true, true); w.Code == 200 {
		t.Fatal("success before reconciliation")
	}
	failRouting = false
	if w := call("POST", admin+"/disable", "{}", true, true); w.Code != 200 || callbacks != 3 || m.Authenticate(device, gen, d.Token) || !strings.Contains(w.Body.String(), `"device_id":"`+id+`"`) || !strings.Contains(w.Body.String(), `"wss_origin":`) || !strings.Contains(w.Body.String(), `"status":"revoked_cleanup_pending"`) {
		t.Fatal("disable not synchronized")
	}
	clear, ok := m.PendingClear(device)
	if !ok {
		t.Fatal("missing clear")
	}
	ca := Transcript{Operation: ClearAck, Bridge: bridge, Device: device, Generation: gen, Epoch: clear.Epoch, HTTPSOrigin: config.HTTPSOrigin}
	caReq := map[string]any{"bridge_id": hex.EncodeToString(bridge[:]), "device_id": id, "op": "clear_ack", "generation": d.Generation, "revocation_epoch": clear.Epoch}
	if w := request("challenge", caReq); w.Code == 200 {
		t.Fatal("unauthenticated clear challenge accepted")
	}
	var clientNonce Secret
	clientNonce[0] = 44
	pre, err := ClearAckPreauthBytes(ca, clientNonce)
	if err != nil {
		t.Fatal(err)
	}
	caReq["client_nonce"] = base64.RawURLEncoding.EncodeToString(clientNonce[:])
	caReq["pre_signature"] = signBytesTest(t, key, pre)
	issueClear := request("challenge", caReq)
	if issueClear.Code != 200 {
		t.Fatal("clear preauth rejected")
	}
	if w := request("challenge", caReq); w.Code == 200 {
		t.Fatal("preauth replay accepted")
	}
	if json.Unmarshal(issueClear.Body.Bytes(), &issued) != nil {
		t.Fatal("clear response")
	}
	ca.Challenge, _ = secretField(issued.Challenge)
	caReq["challenge"] = issued.Challenge
	caReq["signature"] = "invalid"
	if w := request("proof", caReq); w.Code == 200 {
		t.Fatal("preauth fields accepted on proof")
	}
	delete(caReq, "challenge")
	delete(caReq, "signature")
	delete(caReq, "client_nonce")
	delete(caReq, "pre_signature")
	caReq["challenge"] = issued.Challenge
	caReq["signature"] = signTest(t, key, ca)
	if w := request("proof", caReq); w.Code != 200 {
		t.Fatal("clear acknowledgement failed")
	}
	if w := call("POST", admin+"/cancel", "{}", true, true); w.Code != 200 || !strings.Contains(w.Body.String(), `"device_id":"`+id+`"`) || !strings.Contains(w.Body.String(), `"status":"unconfigured"`) {
		t.Fatal("cancel status shape")
	}
}
