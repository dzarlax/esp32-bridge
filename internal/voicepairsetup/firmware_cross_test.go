package voicepairsetup

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"esp32-bridge/internal/config"
	"esp32-bridge/internal/management"
	"esp32-bridge/internal/settings"
	"github.com/gorilla/websocket"
)

// This test runs only through the host harness, which builds the real firmware
// coordinator against synthetic NVS and a verified libcurl-backed ESP shim.
func TestFirmwareRuntimeTLSCrossLanguage(t *testing.T) {
	binary := os.Getenv("VOICE_FIRMWARE_FIXTURE")
	if binary == "" {
		t.Skip("run display/sim/voice_pair_cross/run.sh")
	}
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Local test CA"}, DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &certKey.PublicKey, certKey)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	rawKey, err := x509.MarshalECPrivateKey(certKey)
	if err != nil {
		t.Fatal(err)
	}
	signKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signRaw, err := x509.MarshalECPrivateKey(signKey)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	origin := fmt.Sprintf("https://localhost:%d", port)
	store, err := settings.New(filepath.Join(dir, "settings"), settings.Defaults(config.Load(), origin), nil, origin, nil)
	if err != nil {
		t.Fatal(err)
	}
	device := strings.Repeat("a", 32)
	if _, err = store.Sync(settings.Device{DeviceID: device, BridgeID: store.BridgeID(), Firmware: "test", BootID: "test"}); err != nil {
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
	cfg := Config{StateDir: filepath.Join(dir, "pair"), Origin: origin, AdminBase: origin, MasterFile: write("master", bytes.Repeat([]byte{1}, 32)), MasterID: "master", IntegrityFile: write("integrity", bytes.Repeat([]byte{2}, 32)), IntegrityID: "integrity", SigningFile: write("sign", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: signRaw})), TLSCertFile: write("cert", certPEM), TLSKeyFile: write("tls", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: rawKey})), Publisher: strings.Repeat("p", 32)}
	if err = os.Mkdir(cfg.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	setup, err := New(cfg, store, admin)
	if err != nil {
		t.Fatal(err)
	}
	defer setup.Manager.Close()
	defer setup.Hub.Close()
	mux := http.NewServeMux()
	setup.Hub.Register(mux)
	mux.Handle("/api/voice/pair/v1/", TLSOnly(setup.Adapter.DeviceHandler()))
	api := &management.API{Store: store, DeviceKey: "device-key", VoiceAdmin: TLSOnly(setup.Adapter.AdminHandler()), VoiceClear: setup.Clear}
	api.Register(mux, admin)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	defer server.Close()
	go server.Serve(tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{setup.TLS}, MinVersion: tls.VersionTLS12}))
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("fixture CA")
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	cmd := exec.Command(binary, origin, cfg.TLSCertFile, store.BridgeID())
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	lines := bufio.NewScanner(stdout)
	if !lines.Scan() {
		t.Fatal("firmware emitted no QR")
	}
	qr := lines.Text()
	payload, _ := json.Marshal(map[string]string{"pairing_code": qr, "wss_origin": strings.Replace(origin, "https://", "wss://", 1)})
	req, _ := http.NewRequest("POST", origin+"/api/admin/v1/devices/"+device+"/voice/pair", bytes.NewReader(payload))
	req.Header.Set("X-Test-Admin", "yes")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("approval status %d", resp.StatusCode)
	}
	if _, err = fmt.Fprintln(stdin, "continue"); err != nil {
		t.Fatal(err)
	}
	if !lines.Scan() {
		t.Fatalf("firmware did not complete pairing: %v", cmd.Wait())
	}
	token := lines.Text()
	dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second, TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	ws, resp, err := dialer.Dial(strings.Replace(origin, "https://", "wss://", 1)+"/api/voice/v1/events", http.Header{"Authorization": []string{"Bearer " + token}})
	if err != nil {
		if resp != nil {
			t.Fatalf("WSS status %d: %v", resp.StatusCode, err)
		}
		t.Fatal(err)
	}
	disable, _ := http.NewRequest("POST", origin+"/api/admin/v1/devices/"+device+"/voice/disable", strings.NewReader("{}"))
	disable.Header.Set("X-Test-Admin", "yes")
	disable.Header.Set("Content-Type", "application/json")
	revoked, err := client.Do(disable)
	if err != nil {
		t.Fatal(err)
	}
	revoked.Body.Close()
	if revoked.StatusCode != http.StatusOK {
		t.Fatalf("disable status %d", revoked.StatusCode)
	}
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := ws.ReadMessage(); err == nil {
		t.Fatal("revoked WSS remained open")
	}
	ws.Close()
	if reconnect, _, err := dialer.Dial(strings.Replace(origin, "https://", "wss://", 1)+"/api/voice/v1/events", http.Header{"Authorization": []string{"Bearer " + token}}); err == nil {
		reconnect.Close()
		t.Fatal("revoked bearer accepted")
	}
	clear := setup.Clear(device)
	if clear == nil {
		t.Fatal("missing signed clear")
	}
	clearJSON, err := json.Marshal(clear)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fmt.Fprintln(stdin, string(clearJSON)); err != nil {
		t.Fatal(err)
	}
	if !lines.Scan() {
		t.Fatalf("firmware clear failed: %v", cmd.Wait())
	}
	if lines.Text() != "clear_ack_done" {
		t.Fatal("firmware did not acknowledge clear")
	}
	if err = cmd.Wait(); err != nil {
		t.Fatalf("firmware failed: %v", err)
	}
	if setup.Clear(device) != nil {
		t.Fatal("Bridge retained clear after firmware acknowledgement")
	}
	bad, response, err := dialer.Dial(strings.Replace(origin, "https://", "wss://", 1)+"/api/voice/v1/events", http.Header{"Authorization": []string{"Bearer invalid"}})
	if err == nil {
		bad.Close()
		t.Fatal("invalid bearer accepted")
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatal("invalid bearer did not receive 401")
	}
	// Same CA but a mismatched IP hostname must be rejected by the firmware shim.
	wrongOrigin := fmt.Sprintf("https://127.0.0.1:%d", port)
	badDevice := exec.Command(binary, wrongOrigin, cfg.TLSCertFile, store.BridgeID())
	badDevice.Stdin = strings.NewReader("continue\n")
	var output bytes.Buffer
	badDevice.Stdout = &output
	if err := badDevice.Run(); err == nil {
		t.Fatal("firmware accepted wrong TLS hostname")
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 7 || !strings.HasPrefix(output.String(), "HDV2:") {
		t.Fatalf("hostname rejection did not reach transport: %v", err)
	}
}
