package voicepairsetup

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"esp32-bridge/internal/config"
	"esp32-bridge/internal/management"
	"esp32-bridge/internal/settings"
	"esp32-bridge/internal/voicepair"
	"github.com/gorilla/websocket"
)

func TestComposedTLSFlow(t *testing.T) {
	t.Run("revoke", func(t *testing.T) { composedTLSFlow(t, false) })
	t.Run("poisoned", func(t *testing.T) { composedTLSFlow(t, true) })
}

func composedTLSFlow(t *testing.T, poison bool) {
	dir := t.TempDir()
	state := filepath.Join(dir, "pair")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	signKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	sk, _ := x509.MarshalECPrivateKey(signKey)
	tlsKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tk, _ := x509.MarshalECPrivateKey(tlsKey)
	certDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}, &x509.Certificate{SerialNumber: big.NewInt(1)}, &tlsKey.PublicKey, tlsKey)
	if err != nil {
		t.Fatal(err)
	}
	origin := "https://example.test:443"
	values := settings.Defaults(config.Load(), origin)
	store, err := settings.New(filepath.Join(dir, "settings"), values, nil, origin, nil)
	if err != nil {
		t.Fatal(err)
	}
	device := strings.Repeat("a", 32)
	if _, err = store.Sync(settings.Device{DeviceID: device, BridgeID: store.BridgeID(), Firmware: "1", BootID: "boot"}); err != nil {
		t.Fatal(err)
	}
	other := strings.Repeat("b", 32)
	if _, err = store.Sync(settings.Device{DeviceID: other, BridgeID: store.BridgeID(), Firmware: "1", BootID: "boot"}); err != nil {
		t.Fatal(err)
	}
	admin := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Test-Admin") != "yes" {
				http.Error(w, "denied", 401)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	cfg := Config{StateDir: state, Origin: origin, AdminBase: "https://example.test", MasterFile: write("master", bytes.Repeat([]byte{1}, 32)), MasterID: "one", IntegrityFile: write("integrity", bytes.Repeat([]byte{2}, 32)), IntegrityID: "two", SigningFile: write("sign", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: sk})), TLSCertFile: write("cert", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})), TLSKeyFile: write("tls", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: tk})), Publisher: strings.Repeat("p", 32)}
	cfg.LegacyMigration = true
	cfg.LegacySubscriber = strings.Repeat("s", 32)
	cfg.LegacyTarget = device
	s, err := New(cfg, store, admin)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Manager.Close()
	defer s.Hub.Close()
	mux := http.NewServeMux()
	s.Hub.Register(mux)
	mux.Handle("/api/voice/pair/v1/", TLSOnly(s.Adapter.DeviceHandler()))
	api := &management.API{Store: store, DeviceKey: "device-key", VoiceAdmin: TLSOnly(s.Adapter.AdminHandler()), VoiceClear: s.Clear}
	api.Register(mux, admin)
	srv := httptest.NewUnstartedServer(mux)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{s.TLS}}
	srv.StartTLS()
	defer srv.Close()
	client := srv.Client()
	client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // Synthetic self-signed test certificate.
	call := func(method, path string, body any, auth bool) (int, map[string]any) {
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		if auth {
			req.Header.Set("X-Test-Admin", "yes")
		}
		resp, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	adminPath := "/api/admin/v1/devices/" + device + "/voice"
	dialer := websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	legacy := http.Header{"Authorization": []string{"Bearer " + cfg.LegacySubscriber}}
	legacyWS, _, err := dialer.Dial("wss"+strings.TrimPrefix(srv.URL, "https")+"/api/voice/v1/events", legacy)
	if err != nil {
		t.Fatal("legacy unavailable before ACK", err)
	}
	defer legacyWS.Close()
	if code, _ := call("GET", adminPath, nil, false); code != 401 {
		t.Fatal("admin bypass")
	}
	devkey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	bridge, _ := voicepair.DecodeID(store.BridgeID())
	id, _ := voicepair.DecodeID(device)
	var session voicepair.Secret
	session[0] = 9
	raw := make([]byte, 145)
	raw[0] = 2
	copy(raw[1:17], bridge[:])
	copy(raw[17:33], id[:])
	copy(raw[33:98], elliptic.Marshal(elliptic.P256(), devkey.X, devkey.Y))
	copy(raw[98:130], session[:])
	oh := sha256.Sum256([]byte(origin))
	copy(raw[130:], oh[:15])
	qr := "HDV2:" + base64.RawURLEncoding.EncodeToString(raw)
	if code, _ := call("POST", adminPath+"/pair", map[string]string{"pairing_code": qr, "wss_origin": "wss://example.test:443"}, true); code != 200 {
		t.Fatalf("pair %d", code)
	}
	if code, _ := call("POST", adminPath+"/cancel", map[string]any{}, true); code != 200 {
		t.Fatal("cancel")
	}
	if check, _, err := dialer.Dial("wss"+strings.TrimPrefix(srv.URL, "https")+"/api/voice/v1/events", legacy); err != nil {
		t.Fatal("cancel lost legacy", err)
	} else {
		check.Close()
	}
	session[0]++
	copy(raw[98:130], session[:])
	qr = "HDV2:" + base64.RawURLEncoding.EncodeToString(raw)
	if code, _ := call("POST", adminPath+"/pair", map[string]string{"pairing_code": qr, "wss_origin": "wss://example.test:443"}, true); code != 200 {
		t.Fatal("second pair")
	}
	base := map[string]any{"bridge_id": store.BridgeID(), "device_id": device, "session": base64.RawURLEncoding.EncodeToString(session[:]), "op": "deliver"}
	if code, out := call("POST", "/api/voice/pair/v1/challenge", base, false); code != 200 {
		t.Fatal("initial challenge")
	} else {
		base["challenge"] = out["challenge"]
		base["signature"] = base64.RawURLEncoding.EncodeToString(make([]byte, 64))
		if status, _ := call("POST", "/api/voice/pair/v1/proof", base, false); status == 200 {
			t.Fatal("invalid first signature")
		}
		delete(base, "challenge")
		delete(base, "signature")
	}
	if check, _, err := dialer.Dial("wss"+strings.TrimPrefix(srv.URL, "https")+"/api/voice/v1/events", legacy); err != nil {
		t.Fatal("failed proof lost legacy", err)
	} else {
		check.Close()
	}
	prove := func(op voicepair.Operation, gen voicepair.Generation) map[string]any {
		code, out := call("POST", "/api/voice/pair/v1/challenge", base, false)
		if code != 200 {
			t.Fatalf("challenge %d", code)
		}
		challenge, _ := base64.RawURLEncoding.DecodeString(out["challenge"].(string))
		var ch voicepair.Secret
		copy(ch[:], challenge)
		tr := voicepair.Transcript{Operation: op, Bridge: bridge, Device: id, Session: session, Challenge: ch, Generation: gen, HTTPSOrigin: origin}
		msg, e := tr.Bytes()
		if e != nil {
			t.Fatal(e)
		}
		hash := sha256.Sum256(msg)
		r, ss, _ := ecdsa.Sign(rand.Reader, devkey, hash[:])
		n := elliptic.P256().Params().N
		if ss.Cmp(new(big.Int).Rsh(new(big.Int).Set(n), 1)) > 0 {
			ss.Sub(n, ss)
		}
		sig := make([]byte, 64)
		r.FillBytes(sig[:32])
		ss.FillBytes(sig[32:])
		base["challenge"] = out["challenge"]
		base["signature"] = base64.RawURLEncoding.EncodeToString(sig)
		code, out = call("POST", "/api/voice/pair/v1/proof", base, false)
		delete(base, "challenge")
		delete(base, "signature")
		if code != 200 {
			t.Fatalf("proof %d", code)
		}
		return out
	}
	delivered := prove(voicepair.Deliver, voicepair.Generation{})
	token := delivered["token"].(string)
	genBytes, _ := hex.DecodeString(delivered["generation"].(string))
	var gen voicepair.Generation
	copy(gen[:], genBytes)
	base["op"] = "ack"
	base["generation"] = delivered["generation"]
	if code, out := call("POST", "/api/voice/pair/v1/challenge", base, false); code != 200 {
		t.Fatal("initial ACK challenge")
	} else {
		base["challenge"] = out["challenge"]
		base["signature"] = base64.RawURLEncoding.EncodeToString(make([]byte, 64))
		if status, _ := call("POST", "/api/voice/pair/v1/proof", base, false); status == 200 {
			t.Fatal("invalid first ACK accepted")
		}
		delete(base, "challenge")
		delete(base, "signature")
	}
	if check, _, err := dialer.Dial("wss"+strings.TrimPrefix(srv.URL, "https")+"/api/voice/v1/events", legacy); err != nil {
		t.Fatal("failed ACK lost legacy", err)
	} else {
		check.Close()
	}
	prove(voicepair.Ack, gen)
	_ = legacyWS.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := legacyWS.ReadMessage(); err == nil {
		t.Fatal("legacy socket survived ACK")
	}
	if conn, _, err := dialer.Dial("wss"+strings.TrimPrefix(srv.URL, "https")+"/api/voice/v1/events", legacy); err == nil {
		conn.Close()
		t.Fatal("legacy bearer accepted")
	}
	header := http.Header{"Authorization": []string{"Bearer " + token}}
	ws, _, err := dialer.Dial("wss"+strings.TrimPrefix(srv.URL, "https")+"/api/voice/v1/events", header)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	// The client can finish the handshake before the server registers the socket.
	deadline := time.Now().Add(2 * time.Second)
	for {
		code, out := call("GET", adminPath, nil, true)
		if code != http.StatusOK {
			t.Fatalf("connected status request: %d", code)
		}
		if out["status"] == "connected" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket was not registered before the deadline")
		}
		time.Sleep(time.Millisecond)
	}
	if code, _ := call("POST", "/api/admin/v1/devices/"+other+"/voice/disable", map[string]any{}, true); code == 200 {
		t.Fatal("unpaired device revoke accepted")
	}
	if code, out := call("GET", adminPath, nil, true); code != 200 || out["status"] != "connected" {
		t.Fatal("failed mutation disrupted healthy target")
	}
	// Start a replacement pairing while the original credential remains live.
	devkey, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	session[0]++
	device = other
	id, _ = voicepair.DecodeID(device)
	copy(raw[17:33], id[:])
	adminPath = "/api/admin/v1/devices/" + device + "/voice"
	copy(raw[33:98], elliptic.Marshal(elliptic.P256(), devkey.X, devkey.Y))
	copy(raw[98:130], session[:])
	qr = "HDV2:" + base64.RawURLEncoding.EncodeToString(raw)
	if code, _ := call("POST", adminPath+"/pair", map[string]string{"pairing_code": qr, "wss_origin": "wss://example.test:443"}, true); code != 200 {
		t.Fatalf("rotation pair %d", code)
	}
	base["op"] = "deliver"
	delete(base, "generation")
	base["device_id"] = device
	base["session"] = base64.RawURLEncoding.EncodeToString(session[:])
	replacement := prove(voicepair.Deliver, voicepair.Generation{})
	genBytes, _ = hex.DecodeString(replacement["generation"].(string))
	copy(gen[:], genBytes)
	base["op"] = "ack"
	base["generation"] = replacement["generation"]
	code, challenge := call("POST", "/api/voice/pair/v1/challenge", base, false)
	if code != 200 {
		t.Fatal("invalid signature setup")
	}
	base["challenge"] = challenge["challenge"]
	base["signature"] = base64.RawURLEncoding.EncodeToString(make([]byte, 64))
	if code, _ = call("POST", "/api/voice/pair/v1/proof", base, false); code == 200 {
		t.Fatal("invalid signature accepted")
	}
	delete(base, "challenge")
	delete(base, "signature")
	if !s.Hub.Connected(strings.Repeat("a", 32)) {
		t.Fatal("invalid signature changed target")
	}
	leaseReq, _ := http.NewRequest("POST", srv.URL+"/api/voice/v1/lease", nil)
	leaseReq.Header.Set("Authorization", "Bearer "+cfg.Publisher)
	leaseResp, err := client.Do(leaseReq)
	if err != nil {
		t.Fatal(err)
	}
	var lease map[string]string
	_ = json.NewDecoder(leaseResp.Body).Decode(&lease)
	leaseResp.Body.Close()
	if leaseResp.StatusCode != 200 {
		t.Fatal("lease")
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	started := make(chan bool, 1)
	go func() {
		defer close(done)
		for revision := uint64(1); ; revision++ {
			select {
			case <-stop:
				return
			default:
			}
			payload, _ := json.Marshal(map[string]any{"v": 1, "type": "voice.snapshot", "lease_id": lease["lease_id"], "revision": revision, "session_id": "session", "turn_id": "turn", "phase": "answer", "backend_active": true, "playback": "pending", "text": "synthetic", "text_truncated": false, "interrupted": false, "active": true, "ttl_ms": 30000})
			req, _ := http.NewRequest("POST", srv.URL+"/api/voice/v1/publish", bytes.NewReader(payload))
			req.Header.Set("Authorization", "Bearer "+cfg.Publisher)
			resp, err := client.Do(req)
			if err != nil {
				if revision == 1 {
					started <- false
				}
				return
			}
			if revision == 1 {
				started <- resp.StatusCode == http.StatusNoContent
			}
			resp.Body.Close()
		}
	}()
	if !<-started {
		close(stop)
		<-done
		t.Fatal("publisher did not start")
	}
	prove(voicepair.Ack, gen)
	close(stop)
	<-done
	_ = ws.SetReadDeadline(time.Now().Add(time.Second))
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			break
		}
	}
	if conn, _, err := dialer.Dial("wss"+strings.TrimPrefix(srv.URL, "https")+"/api/voice/v1/events", header); err == nil {
		conn.Close()
		t.Fatal("rotated token accepted")
	}
	header.Set("Authorization", "Bearer "+replacement["token"].(string))
	ws, _, err = dialer.Dial("wss"+strings.TrimPrefix(srv.URL, "https")+"/api/voice/v1/events", header)
	if err != nil {
		t.Fatal("replacement socket rejected", err)
	}
	defer ws.Close()
	if poison {
		if err := s.Manager.Close(); err != nil {
			t.Fatal(err)
		}
		if code, _ := call("POST", adminPath+"/disable", map[string]any{}, true); code == 200 {
			t.Fatal("poisoned manager accepted mutation")
		}
		_ = ws.SetReadDeadline(time.Now().Add(time.Second))
		if _, _, err := ws.ReadMessage(); err == nil {
			t.Fatal("poisoned manager retained socket")
		}
		return
	}
	if code, _ := call("POST", adminPath+"/disable", map[string]any{}, true); code != 200 {
		t.Fatal("disable")
	}
	_ = ws.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := ws.ReadMessage(); err == nil {
		t.Fatal("revoked socket remains open")
	}
	if _, _, err := dialer.Dial("wss"+strings.TrimPrefix(srv.URL, "https")+"/api/voice/v1/events", header); err == nil {
		t.Fatal("revoked token accepted")
	}
	if clear := s.Clear(device); clear == nil {
		t.Fatal("missing signed clear")
	}
	if err := s.Manager.Close(); err != nil {
		t.Fatal(err)
	}
	s.Hub.Close()
	restarted, err := New(cfg, store, admin)
	if err != nil {
		t.Fatal("restart", err)
	}
	defer restarted.Manager.Close()
	defer restarted.Hub.Close()
	restartMux := http.NewServeMux()
	restarted.Hub.Register(restartMux)
	req := httptest.NewRequest("GET", "/api/voice/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.LegacySubscriber)
	w := httptest.NewRecorder()
	restartMux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("legacy restored after revoke/restart: %d", w.Code)
	}
}
