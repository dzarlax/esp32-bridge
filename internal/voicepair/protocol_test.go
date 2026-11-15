package voicepair

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"math/big"
	"strings"
	"testing"
)

func TestQR(t *testing.T) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 145)
	raw[0] = 2
	raw[1] = 1
	raw[17] = 1
	copy(raw[33:98], elliptic.Marshal(elliptic.P256(), k.X, k.Y))
	raw[98] = 7
	originHash := sha256.Sum256([]byte("https://example.com:443"))
	copy(raw[130:145], originHash[:15])
	qr := "HDV2:" + base64.RawURLEncoding.EncodeToString(raw)
	q, err := ParseQR(qr)
	if err != nil || q.Nonce[0] != 7 {
		t.Fatal("valid QR rejected")
	}
	for _, bad := range []string{qr + "=", "HDV1:" + qr[5:], qr[:198] + "B"} {
		if _, err := ParseQR(bad); err == nil {
			t.Fatal("bad QR accepted")
		}
	}
	raw[0] = 1
	if _, err := ParseQR("HDV2:" + base64.RawURLEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("old version accepted")
	}
}
func TestOrigins(t *testing.T) {
	for _, v := range []struct {
		h, w string
		ok   bool
	}{{"https://example.com:443", "wss://example.com:443", true}, {"https://[::1]:443", "wss://[::1]:443", true}, {"https://example.com:443", "wss://example.com:444", false}, {"https://example.com:443", "wss://example.com", false}, {"https://EXAMPLE.com:443", "wss://example.com:443", false}, {"https://example.com:443/", "wss://example.com:443", false}, {"https://example.com:0443", "wss://example.com:0443", false}} {
		if (ValidateOrigins(v.h, v.w) == nil) != v.ok {
			t.Fatalf("origin validation: %q %q", v.h, v.w)
		}
	}
}
func TestProofIsBoundToDeviceAndLowS(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tr := Transcript{Operation: Deliver, HTTPSOrigin: "https://example.com:443"}
	tr.Bridge[0] = 1
	tr.Device[0] = 1
	tr.Session[0] = 1
	tr.Challenge[0] = 1
	b, err := tr.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(b)
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	n := elliptic.P256().Params().N
	half := new(big.Int).Rsh(new(big.Int).Set(n), 1)
	if s.Cmp(half) > 0 {
		s.Sub(n, s)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	encoded := base64.RawURLEncoding.EncodeToString(sig)
	if !VerifyProof(&key.PublicKey, tr, encoded) {
		t.Fatal("valid proof rejected")
	}
	tr.Device[0] = 2
	if VerifyProof(&key.PublicKey, tr, encoded) {
		t.Fatal("other device accepted")
	}
	tr.Device[0] = 1
	s.Sub(n, s)
	s.FillBytes(sig[32:])
	if VerifyProof(&key.PublicKey, tr, base64.RawURLEncoding.EncodeToString(sig)) {
		t.Fatal("high-S signature accepted")
	}
	if VerifyProof(&key.PublicKey, tr, strings.Repeat("A", 85)+"B") {
		t.Fatal("noncanonical signature accepted")
	}
}
