package voicepairsetup

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateFileValidation(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "key")
	if err := os.WriteFile(file, make([]byte, 32), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivate(file, 32); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivate(filepath.Join(dir, "link"), 32); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivate(file, 32); err == nil {
		t.Fatal("insecure mode accepted")
	}
	if err := os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivate(file, 31); err == nil {
		t.Fatal("oversized file accepted")
	}
}
func TestTLSOnly(t *testing.T) {
	called := false
	h := TLSOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(http.StatusNoContent) }))
	r := httptest.NewRequest("POST", "http://example.test/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if called || w.Code != http.StatusForbidden {
		t.Fatal("plaintext accepted")
	}
	r.TLS = &tls.ConnectionState{}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !called || w.Code != http.StatusNoContent {
		t.Fatal("TLS rejected")
	}
}
func TestMissingConfigurationFailsClosed(t *testing.T) {
	if _, err := New(Config{}, nil, nil); err == nil {
		t.Fatal("empty config accepted")
	}
}
