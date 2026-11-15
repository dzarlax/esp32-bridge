package voicepairsetup

// Opt-in system test for an isolated real Traefik + Bridge process. The test
// network, certificates, password and bearer values are synthetic fixtures.
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
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"esp32-bridge/internal/voicepair"
	"github.com/gorilla/websocket"
)

func TestTraefikBridgeSystem(t *testing.T) {
	origin := os.Getenv("VOICE_PROXY_TEST_ORIGIN")
	if origin == "" {
		t.Skip("isolated Traefik system fixture not enabled")
	}
	ca, err := os.ReadFile(os.Getenv("VOICE_PROXY_TEST_CA"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		t.Fatal("invalid fixture CA")
	}
	jar, _ := cookiejar.New(nil)
	tc := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tc}, Jar: jar, Timeout: 5 * time.Second}
	ready := false
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		resp, err := client.Get(origin + "/api/admin/v1/session")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
	}
	if !ready {
		t.Fatal("isolated proxy and Bridge did not become ready")
	}
	const device = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const api = "synthetic-device-api-key-only"
	const publisher = "synthetic-publisher-token-only-12345"
	const password = "synthetic-test-password-only"
	call := func(method, path string, payload any, headers map[string]string) (int, map[string]any) {
		var body io.Reader
		if payload != nil {
			raw, e := json.Marshal(payload)
			if e != nil {
				t.Fatal(e)
			}
			body = bytes.NewReader(raw)
		}
		req, e := http.NewRequest(method, origin+path, body)
		if e != nil {
			t.Fatal(e)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		var result map[string]any
		_ = json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&result)
		return resp.StatusCode, result
	}
	deviceHeaders := map[string]string{"X-API-Key": api, "X-Device-ID": device}
	for _, path := range []string{"/qa-badname", "/qa-badca"} {
		code, _ := call("GET", path, nil, nil)
		if code != http.StatusInternalServerError && code != http.StatusBadGateway {
			t.Fatalf("TLS verification did not reject %s: %d", path, code)
		}
	}
	code, probe := call("GET", "/api/device/v1/probe", nil, deviceHeaders)
	if code != 200 {
		t.Fatalf("probe %d", code)
	}
	bridgeID, ok := probe["bridge_id"].(string)
	if !ok {
		t.Fatal("missing bridge id")
	}
	code, _ = call("POST", "/api/device/v1/sync", map[string]any{"device_id": device, "bridge_id": bridgeID, "firmware": "fixture", "boot_id": "fixture"}, deviceHeaders)
	if code != 200 {
		t.Fatalf("sync %d", code)
	}
	adminPath := "/api/admin/v1/devices/" + device + "/voice"
	code, _ = call("GET", adminPath, nil, nil)
	if code != 401 {
		t.Fatalf("unauthenticated admin %d", code)
	}
	loginHeaders := map[string]string{"Origin": origin}
	code, login := call("POST", "/api/admin/v1/login", map[string]string{"password": password}, loginHeaders)
	if code != 200 {
		t.Fatalf("login %d", code)
	}
	csrf, ok := login["csrf_token"].(string)
	if !ok || csrf == "" {
		t.Fatal("missing csrf")
	}
	adminHeaders := map[string]string{"Origin": origin, "X-CSRF-Token": csrf}
	code, _ = call("POST", adminPath+"/cancel", map[string]any{}, loginHeaders)
	if code != 403 {
		t.Fatalf("missing CSRF accepted %d", code)
	}
	devkey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bridge, _ := voicepair.DecodeID(bridgeID)
	id, _ := voicepair.DecodeID(device)
	var session voicepair.Secret
	if _, err = rand.Read(session[:]); err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 145)
	raw[0] = 2
	copy(raw[1:17], bridge[:])
	copy(raw[17:33], id[:])
	copy(raw[33:98], elliptic.Marshal(elliptic.P256(), devkey.X, devkey.Y))
	copy(raw[98:130], session[:])
	oh := sha256.Sum256([]byte(origin))
	copy(raw[130:], oh[:15])
	qr := "HDV2:" + base64.RawURLEncoding.EncodeToString(raw)
	code, _ = call("POST", adminPath+"/pair", map[string]string{"pairing_code": qr, "wss_origin": "wss://" + strings.TrimPrefix(origin, "https://")}, adminHeaders)
	if code != 200 {
		t.Fatalf("pair approval %d", code)
	}
	base := map[string]any{"bridge_id": bridgeID, "device_id": device, "session": base64.RawURLEncoding.EncodeToString(session[:]), "op": "deliver"}
	prove := func(op voicepair.Operation, generation voicepair.Generation) map[string]any {
		code, c := call("POST", "/api/voice/pair/v1/challenge", base, nil)
		if code != 200 {
			t.Fatalf("challenge %d", code)
		}
		b, e := base64.RawURLEncoding.DecodeString(c["challenge"].(string))
		if e != nil {
			t.Fatal(e)
		}
		var challenge voicepair.Secret
		copy(challenge[:], b)
		tr := voicepair.Transcript{Operation: op, Bridge: bridge, Device: id, Session: session, Challenge: challenge, Generation: generation, HTTPSOrigin: origin}
		data, e := tr.Bytes()
		if e != nil {
			t.Fatal(e)
		}
		hash := sha256.Sum256(data)
		r, s, e := ecdsa.Sign(rand.Reader, devkey, hash[:])
		if e != nil {
			t.Fatal(e)
		}
		n := elliptic.P256().Params().N
		if s.Cmp(new(big.Int).Rsh(new(big.Int).Set(n), 1)) > 0 {
			s.Sub(n, s)
		}
		sig := make([]byte, 64)
		r.FillBytes(sig[:32])
		s.FillBytes(sig[32:])
		base["challenge"] = c["challenge"]
		base["signature"] = base64.RawURLEncoding.EncodeToString(sig)
		code, out := call("POST", "/api/voice/pair/v1/proof", base, nil)
		delete(base, "challenge")
		delete(base, "signature")
		if code != 200 {
			t.Fatalf("proof %d", code)
		}
		return out
	}
	delivered := prove(voicepair.Deliver, voicepair.Generation{})
	generationBytes, e := hex.DecodeString(delivered["generation"].(string))
	if e != nil {
		t.Fatal(e)
	}
	var generation voicepair.Generation
	copy(generation[:], generationBytes)
	base["op"] = "ack"
	base["generation"] = delivered["generation"]
	legacyDialer := websocket.Dialer{TLSClientConfig: tc, HandshakeTimeout: 5 * time.Second}
	legacyHeader := http.Header{"Authorization": []string{"Bearer synthetic-legacy-subscriber-only-12345"}}
	if os.Getenv("VOICE_PROXY_TEST_MIGRATION") == "true" {
		conn, _, err := legacyDialer.Dial("wss://"+strings.TrimPrefix(origin, "https://")+"/api/voice/v1/events", legacyHeader)
		if err != nil {
			t.Fatal("legacy unavailable before ACK", err)
		}
		conn.Close()
	}
	prove(voicepair.Ack, generation)
	if os.Getenv("VOICE_PROXY_TEST_MIGRATION") == "true" {
		if conn, _, err := legacyDialer.Dial("wss://"+strings.TrimPrefix(origin, "https://")+"/api/voice/v1/events", legacyHeader); err == nil {
			conn.Close()
			t.Fatal("legacy accepted after ACK")
		}
	}
	token := delivered["token"].(string)
	if path := os.Getenv("VOICE_PROXY_TEST_TOKEN_FILE"); path != "" {
		if err := os.WriteFile(path, []byte(token), 0600); err != nil {
			t.Fatal(err)
		}
	}
	dialer := websocket.Dialer{TLSClientConfig: tc, HandshakeTimeout: 5 * time.Second}
	ws, _, err := dialer.Dial("wss://"+strings.TrimPrefix(origin, "https://")+"/api/voice/v1/events", http.Header{"Authorization": []string{"Bearer " + token}})
	if err != nil {
		t.Fatal("WSS", err)
	}
	defer ws.Close()
	code, status := call("GET", adminPath, nil, adminHeaders)
	if code != 200 || status["status"] != "connected" {
		t.Fatalf("connected status %d %v", code, status["status"])
	}
	code, lease := call("POST", "/api/voice/v1/lease", nil, map[string]string{"Authorization": "Bearer " + publisher})
	if code != 200 {
		t.Fatalf("lease %d", code)
	}
	snap := map[string]any{"v": 1, "type": "voice.snapshot", "lease_id": lease["lease_id"], "revision": 1, "session_id": "synthetic", "turn_id": "synthetic", "phase": "answer", "backend_active": true, "playback": "pending", "text": "synthetic test", "text_truncated": false, "interrupted": false, "active": true, "ttl_ms": 30000}
	code, _ = call("POST", "/api/voice/v1/publish", snap, map[string]string{"Authorization": "Bearer " + publisher})
	if code != 204 {
		t.Fatalf("publish %d", code)
	}
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, message, e := ws.ReadMessage()
	if e != nil || !bytes.Contains(message, []byte("synthetic test")) {
		t.Fatal("snapshot not delivered", e)
	}
	code, _ = call("POST", adminPath+"/disable", map[string]any{}, adminHeaders)
	if code != 200 {
		t.Fatalf("disable %d", code)
	}
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, e = ws.ReadMessage(); e != nil {
			break
		}
	}
	if c, _, e := dialer.Dial("wss://"+strings.TrimPrefix(origin, "https://")+"/api/voice/v1/events", http.Header{"Authorization": []string{"Bearer " + token}}); e == nil {
		c.Close()
		t.Fatal("revoked token accepted")
	}
	code, sync := call("POST", "/api/device/v1/sync", map[string]any{"device_id": device, "bridge_id": bridgeID, "firmware": "fixture", "boot_id": "fixture"}, deviceHeaders)
	if code != 200 || sync["voice_clear"] == nil {
		t.Fatal("signed clear missing")
	}
	clear := sync["voice_clear"].(map[string]any)
	epoch, e := strconv.ParseUint(clear["revocation_epoch"].(string), 10, 64)
	if e != nil {
		t.Fatal(e)
	}
	tr := voicepair.Transcript{Operation: voicepair.ClearAck, Bridge: bridge, Device: id, Generation: generation, Epoch: epoch, HTTPSOrigin: origin}
	var nonce voicepair.Secret
	if _, e = rand.Read(nonce[:]); e != nil {
		t.Fatal(e)
	}
	sign := func(data []byte) string {
		h := sha256.Sum256(data)
		r, s, err := ecdsa.Sign(rand.Reader, devkey, h[:])
		if err != nil {
			t.Fatal(err)
		}
		n := elliptic.P256().Params().N
		if s.Cmp(new(big.Int).Rsh(new(big.Int).Set(n), 1)) > 0 {
			s.Sub(n, s)
		}
		b := make([]byte, 64)
		r.FillBytes(b[:32])
		s.FillBytes(b[32:])
		return base64.RawURLEncoding.EncodeToString(b)
	}
	pre, e := voicepair.ClearAckPreauthBytes(tr, nonce)
	if e != nil {
		t.Fatal(e)
	}
	ack := map[string]any{"bridge_id": bridgeID, "device_id": device, "op": "clear_ack", "generation": delivered["generation"], "revocation_epoch": epoch, "client_nonce": base64.RawURLEncoding.EncodeToString(nonce[:]), "pre_signature": sign(pre)}
	code, ch := call("POST", "/api/voice/pair/v1/challenge", ack, nil)
	if code != 200 {
		t.Fatalf("clear challenge %d", code)
	}
	chBytes, e := base64.RawURLEncoding.DecodeString(ch["challenge"].(string))
	if e != nil {
		t.Fatal(e)
	}
	copy(tr.Challenge[:], chBytes)
	transcript, e := tr.Bytes()
	if e != nil {
		t.Fatal(e)
	}
	delete(ack, "client_nonce")
	delete(ack, "pre_signature")
	ack["challenge"] = ch["challenge"]
	ack["signature"] = sign(transcript)
	code, _ = call("POST", "/api/voice/pair/v1/proof", ack, nil)
	if code != 200 {
		t.Fatalf("clear ACK %d", code)
	}
	code, sync = call("POST", "/api/device/v1/sync", map[string]any{"device_id": device, "bridge_id": bridgeID, "firmware": "fixture", "boot_id": "fixture"}, deviceHeaders)
	if code != 200 || sync["voice_clear"] != nil {
		t.Fatal("clear still pending after ACK")
	}
	fmt.Println("PASS real Bridge via Traefik: admin auth+CSRF, signed pairing, WSS publish/delivery, revoke and signed clear ACK")
}
