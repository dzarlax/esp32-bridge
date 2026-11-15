package voicepairsetup

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Tests a real old Bridge image with voice explicitly disabled, or the restored
// pairing binary using the same most-recent state. No state backup is restored.
func TestTraefikBridgeRollback(t *testing.T) {
	origin := os.Getenv("VOICE_PROXY_TEST_ORIGIN")
	mode := os.Getenv("VOICE_PROXY_TEST_MODE")
	if origin == "" || mode == "" {
		t.Skip("isolated rollback fixture not enabled")
	}
	ca, e := os.ReadFile(os.Getenv("VOICE_PROXY_TEST_CA"))
	if e != nil {
		t.Fatal(e)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		t.Fatal("CA")
	}
	tc := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Timeout: 3 * time.Second, Jar: jar, Transport: &http.Transport{TLSClientConfig: tc}}
	ready := false
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		r, e := c.Get(origin + "/api/admin/v1/session")
		if e == nil {
			r.Body.Close()
			if r.StatusCode == 200 {
				ready = true
				break
			}
		}
	}
	if !ready {
		t.Fatal("rollback fixture not ready")
	}
	call := func(method, path string, payload any, headers map[string]string) (int, map[string]any) {
		var body io.Reader
		if payload != nil {
			raw, _ := json.Marshal(payload)
			body = bytes.NewReader(raw)
		}
		r, _ := http.NewRequest(method, origin+path, body)
		if payload != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		resp, e := c.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	const device = "cccccccccccccccccccccccccccccccc"
	deviceHeaders := map[string]string{"X-API-Key": "synthetic-device-api-key-only", "X-Device-ID": device}
	code, probe := call("GET", "/api/device/v1/probe", nil, deviceHeaders)
	if code != 200 {
		t.Fatalf("probe %d", code)
	}
	code, _ = call("POST", "/api/device/v1/sync", map[string]any{"device_id": device, "bridge_id": probe["bridge_id"], "firmware": "fixture", "boot_id": "fixture"}, deviceHeaders)
	if code != 200 {
		t.Fatalf("sync %d", code)
	}
	code, login := call("POST", "/api/admin/v1/login", map[string]string{"password": "synthetic-test-password-only"}, map[string]string{"Origin": origin})
	if code != 200 {
		t.Fatalf("admin login %d", code)
	}
	csrf, ok := login["csrf_token"].(string)
	if !ok || csrf == "" {
		t.Fatal("csrf")
	}
	code, _ = call("GET", "/api/admin/v1/devices", nil, nil)
	if code != 200 {
		t.Fatalf("admin devices %d", code)
	}
	dialer := websocket.Dialer{TLSClientConfig: tc, HandshakeTimeout: 3 * time.Second}
	wsURL := "wss://" + strings.TrimPrefix(origin, "https://") + "/api/voice/v1/events"
	legacy := "synthetic-legacy-subscriber-only-12345"
	if mode == "preack" {
		conn, _, e := dialer.Dial(wsURL, http.Header{"Authorization": []string{"Bearer " + legacy}})
		if e != nil {
			t.Fatal("legacy before ACK", e)
		}
		conn.Close()
		fmt.Println("PASS before ACK: legacy WSS and ordinary admin/sync")
		return
	}
	tokens := []string{legacy}
	if path := os.Getenv("VOICE_PROXY_TEST_TOKEN_FILE"); path != "" {
		raw, e := os.ReadFile(path)
		if e == nil {
			tokens = append(tokens, string(raw))
		} else if !os.IsNotExist(e) {
			t.Fatal(e)
		}
	}
	for _, token := range tokens {
		conn, resp, e := dialer.Dial(wsURL, http.Header{"Authorization": []string{"Bearer " + token}})
		if e == nil {
			conn.Close()
			t.Fatal("credential reactivated")
		}
		if resp == nil || (resp.StatusCode != 401 && resp.StatusCode != 404) {
			t.Fatal("unexpected subscriber denial")
		}
	}
	if mode == "rollback" {
		code, _ = call("POST", "/api/voice/v1/lease", nil, map[string]string{"Authorization": "Bearer synthetic-publisher-token-only-12345"})
		if code != 401 && code != 404 {
			t.Fatalf("publisher reactivated %d", code)
		}
	}
	fmt.Printf("PASS %s: ordinary admin/sync works, legacy and available revoked bearer remain rejected\n", mode)
}
