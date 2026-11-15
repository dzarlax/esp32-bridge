package voicepair

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func signTest(t *testing.T, k *ecdsa.PrivateKey, tr Transcript) string {
	t.Helper()
	b, e := tr.Bytes()
	if e != nil {
		t.Fatal(e)
	}
	return signBytesTest(t, k, b)
}
func signBytesTest(t *testing.T, k *ecdsa.PrivateKey, b []byte) string {
	t.Helper()
	h := sha256.Sum256(b)
	r, s, e := ecdsa.Sign(rand.Reader, k, h[:])
	if e != nil {
		t.Fatal(e)
	}
	n := elliptic.P256().Params().N
	if s.Cmp(new(big.Int).Rsh(new(big.Int).Set(n), 1)) > 0 {
		s.Sub(n, s)
	}
	raw := make([]byte, 64)
	r.FillBytes(raw[:32])
	s.FillBytes(raw[32:])
	return base64.RawURLEncoding.EncodeToString(raw)
}
func TestLifecyclePersistenceAndReplay(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	signing, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	device, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	var bridge, id ID
	bridge[0] = 1
	id[0] = 2
	var integrity [32]byte
	integrity[0] = 9
	var master [32]byte
	master[0] = 3
	var nonce Secret
	nonce[0] = 4
	raw := make([]byte, 145)
	raw[0] = 2
	copy(raw[1:17], bridge[:])
	copy(raw[17:33], id[:])
	copy(raw[33:98], elliptic.Marshal(elliptic.P256(), device.X, device.Y))
	copy(raw[98:130], nonce[:])
	originHash := sha256.Sum256([]byte("https://example.com:443"))
	copy(raw[130:145], originHash[:15])
	qr := "HDV2:" + base64.RawURLEncoding.EncodeToString(raw)
	m, err := Open(dir, bridge, master, "key1", integrity, "integrity1", signing)
	if err != nil {
		t.Fatal(err)
	}
	// The same Bridge, device, key and nonce cannot authorize a QR created for another origin.
	if err = m.Approve(qr, id, bridge, "https://attacker.example:443", "wss://attacker.example:443"); err == nil {
		t.Fatal("QR origin mismatch accepted")
	}
	if err = m.Approve(qr, id, bridge, "https://example.com:443", "wss://example.com:443"); err != nil {
		t.Fatal(err)
	}
	// Expired sessions never authorize a challenge, even with a valid QR.
	savedNow := m.now
	m.now = func() time.Time { return savedNow().Add(121 * time.Second) }
	expired := Transcript{Operation: Deliver, Bridge: bridge, Device: id, Session: nonce, HTTPSOrigin: "https://example.com:443"}
	if _, err = m.Issue(expired); err == nil {
		t.Fatal("expired session accepted")
	}
	m.now = savedNow
	tr := Transcript{Operation: Deliver, Bridge: bridge, Device: id, Session: nonce, HTTPSOrigin: "https://example.com:443"}
	tr.Challenge, err = m.Issue(tr)
	if err != nil {
		t.Fatal(err)
	}
	sig := signTest(t, device, tr)
	type result struct {
		d *Delivery
		e error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); d, e := m.Prove(tr.Challenge, sig); results <- result{d, e} }()
	}
	wg.Wait()
	close(results)
	var d *Delivery
	count := 0
	for r := range results {
		if r.e == nil {
			count++
			d = r.d
		}
	}
	if count != 1 || d == nil {
		t.Fatal("concurrent proof was not one-use")
	}

	if _, _, ok := m.LookupActiveToken(d.Token); ok {
		t.Fatal("pending token authenticated")
	}
	if _, _, ok := m.LookupActiveToken(d.Token + "x"); ok {
		t.Fatal("oversized token accepted")
	}
	tr.Challenge = Secret{}
	tr.Challenge, err = m.Issue(tr)
	if err != nil {
		t.Fatal(err)
	}
	again, err := m.Prove(tr.Challenge, signTest(t, device, tr))
	if err != nil || *again != *d {
		t.Fatal("retry changed credential")
	}
	if _, err = Open(dir, bridge, master, "key1", integrity, "integrity1", signing); err == nil {
		t.Fatal("concurrent open succeeded")
	}

	file, err := os.ReadFile(filepath.Join(dir, "voicepair.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(file, []byte(d.Token)) || bytes.Contains(file, []byte(base64.RawURLEncoding.EncodeToString(nonce[:]))) || bytes.Contains(file, []byte(qr)) {
		t.Fatal("secret persisted")
	}
	st, err := os.Stat(filepath.Join(dir, "voicepair.json"))
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("unsafe permissions")
	}
	ack := Transcript{Operation: Ack, Bridge: bridge, Device: id, Session: nonce, Generation: d.Generation, HTTPSOrigin: tr.HTTPSOrigin}
	ack.Challenge, err = m.Issue(ack)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Prove(ack.Challenge, signTest(t, device, ack)); err != nil {
		t.Fatal(err)
	}
	if !m.Authenticate(id, d.Generation, d.Token) {
		t.Fatal("active token rejected")
	}
	if got, g, ok := m.LookupActiveToken(d.Token); !ok || got != id || g != d.Generation {
		t.Fatal("token lookup failed")
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if _, err = Open(dir, bridge, master, "key1", integrity, "integrity1", replacement); err == nil {
		t.Fatal("server signing key changed")
	}
	m2, err := Open(dir, bridge, master, "key1", integrity, "integrity1", signing)
	if err != nil || !m2.Authenticate(id, d.Generation, d.Token) {
		t.Fatal("restart lost active binding", err)
	}
	if _, err = m2.Issue(tr); err == nil {
		t.Fatal("session survived restart")
	}
	if err = m2.Close(); err != nil {
		t.Fatal(err)
	}
	wrong := master
	wrong[0]++
	m3, err := Open(dir, bridge, wrong, "key1", integrity, "integrity1", signing)
	if err != nil || !m3.Authenticate(id, d.Generation, d.Token) {
		t.Fatal("active verifier depended on master")
	}
	if err = m3.Close(); err != nil {
		t.Fatal(err)
	}
	missing := [32]byte{}
	inactive, err := Open(dir, bridge, missing, "key1", integrity, "integrity1", signing)
	if err != nil || !inactive.Authenticate(id, d.Generation, d.Token) {
		t.Fatal("missing master disabled active verifier", err)
	}
	if err = inactive.Approve(qr, id, bridge, tr.HTTPSOrigin, "wss://example.com:443"); err == nil {
		t.Fatal("missing master allowed issuance")
	}
	if err = inactive.Close(); err != nil {
		t.Fatal(err)
	}
	wrongIntegrity := integrity
	wrongIntegrity[0]++
	if _, err = Open(dir, bridge, master, "key1", wrongIntegrity, "integrity1", signing); err == nil {
		t.Fatal("wrong integrity key accepted")
	}
	if _, err = Open(dir, bridge, master, "other", integrity, "integrity1", signing); err == nil {
		t.Fatal("changed version accepted")
	}
	original, err := os.ReadFile(filepath.Join(dir, "voicepair.json"))
	if err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), original...)
	corrupt[len(corrupt)/2] ^= 1
	if err = os.WriteFile(filepath.Join(dir, "voicepair.json"), corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(dir, bridge, master, "key1", integrity, "integrity1", signing); err == nil {
		t.Fatal("tampered state accepted")
	}
	if err = os.WriteFile(filepath.Join(dir, "voicepair.json"), original, 0600); err != nil {
		t.Fatal(err)
	}
	m2, err = Open(dir, bridge, master, "key1", integrity, "integrity1", signing)
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	clear, err := m2.Revoke(id)
	if err != nil || m2.Authenticate(id, d.Generation, d.Token) {
		t.Fatal("revoke failed", err)
	}
	if _, _, ok := m2.LookupActiveToken(d.Token); ok {
		t.Fatal("revoked token authenticated")
	}
	if got, ok := m2.PendingClear(id); !ok || got != clear {
		t.Fatal("pending clear unavailable")
	}
	if _, ok := m2.PendingClear(ID{99}); ok {
		t.Fatal("clear returned for another device")
	}
	ca := Transcript{Operation: ClearAck, Bridge: bridge, Device: id, Generation: clear.Generation, Epoch: clear.Epoch, HTTPSOrigin: tr.HTTPSOrigin}
	if _, err = m2.Issue(ca); err == nil {
		t.Fatal("unauthenticated clear challenge accepted")
	}
	var clientNonce Secret
	clientNonce[0] = 42
	pre, err := ClearAckPreauthBytes(ca, clientNonce)
	if err != nil {
		t.Fatal(err)
	}
	preSig := signBytesTest(t, device, pre)
	for i := 0; i < 20; i++ {
		if _, err = m2.IssueClearAck(ca, Secret{byte(i + 1)}, "invalid"); err == nil {
			t.Fatal("unauthenticated challenge issued")
		}
	}
	ca.Challenge, err = m2.IssueClearAck(ca, clientNonce, preSig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m2.IssueClearAck(ca, clientNonce, preSig); err == nil {
		t.Fatal("preauth replay accepted")
	}
	if _, err = m2.Prove(ca.Challenge, signTest(t, device, ca)); err != nil {
		t.Fatal(err)
	}
	if _, ok := m2.PendingClear(id); ok {
		t.Fatal("confirmed clear returned")
	}
	if m2.Status(id).State != "revoked" {
		t.Fatal("clear not acknowledged")
	}
}

func TestUnsafePaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	var bridge ID
	bridge[0] = 1
	var master, integrity [32]byte
	master[0] = 1
	integrity[0] = 2
	if err := os.Symlink(filepath.Join(dir, "other"), filepath.Join(dir, "voicepair.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, bridge, master, "k", integrity, "i", key); err == nil {
		t.Fatal("lock symlink accepted")
	}
	if err := os.Remove(filepath.Join(dir, "voicepair.lock")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "other"), filepath.Join(dir, "voicepair.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, bridge, master, "k", integrity, "i", key); err == nil {
		t.Fatal("state symlink accepted")
	}
	if err := os.Remove(filepath.Join(dir, "voicepair.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "voicepair.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, bridge, master, "k", integrity, "i", key); err == nil {
		t.Fatal("unsafe file accepted")
	}
}

func TestDeviceIsolationAndRevocationRace(t *testing.T) {
	dir := t.TempDir()
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("chmod")
	}
	bridge := ID{1}
	a := ID{2}
	b := ID{3}
	master := [32]byte{4}
	integrity := [32]byte{5}
	signer, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ka, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	kb, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	m, err := Open(dir, bridge, master, "k", integrity, "i", signer)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if mode, e := m.ManagedMode(); e != nil || mode {
		t.Fatal("new manager entered managed mode")
	}
	origin := "https://example.com:443"
	approve := func(id ID, key *ecdsa.PrivateKey, n byte) Secret {
		var nonce Secret
		nonce[0] = n
		raw := make([]byte, 145)
		raw[0] = 2
		copy(raw[1:17], bridge[:])
		copy(raw[17:33], id[:])
		copy(raw[33:98], elliptic.Marshal(elliptic.P256(), key.X, key.Y))
		copy(raw[98:130], nonce[:])
		originHash := sha256.Sum256([]byte("https://example.com:443"))
		copy(raw[130:145], originHash[:15])
		if e := m.Approve("HDV2:"+base64.RawURLEncoding.EncodeToString(raw), id, bridge, origin, "wss://example.com:443"); e != nil {
			t.Fatal(e)
		}
		return nonce
	}
	deliver := func(id ID, key *ecdsa.PrivateKey, nonce Secret) *Delivery {
		tr := Transcript{Operation: Deliver, Bridge: bridge, Device: id, Session: nonce, HTTPSOrigin: origin}
		ch, e := m.Issue(tr)
		if e != nil {
			t.Fatal(e)
		}
		tr.Challenge = ch
		d, e := m.Prove(ch, signTest(t, key, tr))
		if e != nil {
			t.Fatal(e)
		}
		return d
	}
	ack := func(id ID, key *ecdsa.PrivateKey, nonce Secret, d *Delivery) {
		tr := Transcript{Operation: Ack, Bridge: bridge, Device: id, Session: nonce, Generation: d.Generation, HTTPSOrigin: origin}
		ch, e := m.Issue(tr)
		if e != nil {
			t.Fatal(e)
		}
		tr.Challenge = ch
		if _, e = m.Prove(ch, signTest(t, key, tr)); e != nil {
			t.Fatal(e)
		}
	}
	nc := approve(a, ka, 9)
	if m.Status(a).State != "awaiting" {
		t.Fatal("awaiting status missing")
	}
	if err = m.Cancel(a); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.PendingClear(a); ok {
		t.Fatal("cancel created clear")
	}
	if _, err = m.Issue(Transcript{Operation: Deliver, Bridge: bridge, Device: a, Session: nc, HTTPSOrigin: origin}); err == nil {
		t.Fatal("cancelled session accepted")
	}
	if mode, e := m.ManagedMode(); e != nil || mode {
		t.Fatal("cancel entered managed mode")
	}
	na := approve(a, ka, 10)
	nb := approve(b, kb, 11)
	ta := Transcript{Operation: Deliver, Bridge: bridge, Device: a, Session: na, HTTPSOrigin: origin}
	ch, err := m.Issue(ta)
	if err != nil {
		t.Fatal(err)
	}
	ta.Challenge = ch
	if _, err = m.Prove(ch, signTest(t, kb, ta)); err == nil {
		t.Fatal("other device key proved A")
	}
	da, err := m.Prove(ch, signTest(t, ka, ta))
	if err != nil || da == nil {
		t.Fatal("invalid signature consumed challenge")
	}
	db := deliver(b, kb, nb)
	if _, ok := m.SelectedTarget(); ok {
		t.Fatal("pending selected a target")
	}
	if mode, e := m.ManagedMode(); e != nil || mode {
		t.Fatal("failed or pending pairing entered managed mode")
	}
	ack(a, ka, na, da)
	if id, ok := m.SelectedTarget(); !ok || id != a {
		t.Fatal("A not selected after ACK")
	}
	if mode, e := m.ManagedMode(); e != nil || !mode {
		t.Fatal("successful ACK did not enter managed mode")
	}
	ack(b, kb, nb, db)
	if id, ok := m.SelectedTarget(); !ok || id != b {
		t.Fatal("B not selected after ACK")
	}

	if m.Authenticate(a, db.Generation, db.Token) || m.Authenticate(b, da.Generation, da.Token) {
		t.Fatal("cross-device authentication")
	}
	// A fresh pending rotation never disables the old credential before ACK.
	// Expire the initial approval in this controlled test before physical re-pair.
	m.mu.Lock()
	delete(m.sessions, na)
	m.mu.Unlock()
	newKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	nn := approve(a, newKey, 12)
	pending := deliver(a, newKey, nn)
	if id, ok := m.SelectedTarget(); !ok || id != b {
		t.Fatal("pending rotation changed target")
	}
	if err = m.Cancel(a); err != nil {
		t.Fatal(err)
	}
	if id, ok := m.SelectedTarget(); !ok || id != b {
		t.Fatal("cancel changed target")
	}
	nn = approve(a, newKey, 13)
	pending = deliver(a, newKey, nn)
	if !m.Authenticate(a, da.Generation, da.Token) {
		t.Fatal("rotation invalidated old active early")
	}
	tr := Transcript{Operation: Ack, Bridge: bridge, Device: a, Session: nn, Generation: pending.Generation, HTTPSOrigin: origin}
	tr.Challenge, err = m.Issue(tr)
	if err != nil {
		t.Fatal(err)
	}
	sig := signTest(t, newKey, tr)
	if _, err = m.Prove(tr.Challenge, signTest(t, kb, tr)); err == nil {
		t.Fatal("invalid rotation ACK succeeded")
	}
	if id, ok := m.SelectedTarget(); !ok || id != b {
		t.Fatal("failed ACK changed target")
	}
	if _, err = m.Revoke(a); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Prove(tr.Challenge, sig); err == nil {
		t.Fatal("ack revived revoked credential")
	}
	if id, ok := m.SelectedTarget(); !ok || id != b {
		t.Fatal("revoking non-target changed target")
	}
	if m.Authenticate(a, da.Generation, da.Token) || m.Authenticate(a, pending.Generation, pending.Token) || !m.Authenticate(b, db.Generation, db.Token) {
		t.Fatal("revocation isolation failure")
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = Open(dir, bridge, master, "k", integrity, "i", signer)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if id, ok := m.SelectedTarget(); !ok || id != b {
		t.Fatal("target lost across restart")
	}
	if _, err = m.Revoke(b); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.SelectedTarget(); ok {
		t.Fatal("revoking target did not clear selection")
	}
	if mode, e := m.ManagedMode(); e != nil || !mode {
		t.Fatal("revoke reset managed mode")
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = m.ManagedMode(); err == nil {
		t.Fatal("closed manager reported mode")
	}
	m, err = Open(dir, bridge, master, "k", integrity, "i", signer)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if mode, e := m.ManagedMode(); e != nil || !mode {
		t.Fatal("managed mode lost after restart")
	}

}

func TestApprovalRateLimit(t *testing.T) {
	dir := t.TempDir()
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("chmod")
	}
	bridge := ID{1}
	device := ID{2}
	master := [32]byte{3}
	integrity := [32]byte{4}
	signer, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	m, err := Open(dir, bridge, master, "k", integrity, "i", signer)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	code := func(n byte) string {
		raw := make([]byte, 145)
		raw[0] = 2
		copy(raw[1:17], bridge[:])
		copy(raw[17:33], device[:])
		copy(raw[33:98], elliptic.Marshal(elliptic.P256(), key.X, key.Y))
		raw[98] = n
		originHash := sha256.Sum256([]byte("https://example.com:443"))
		copy(raw[130:145], originHash[:15])
		return "HDV2:" + base64.RawURLEncoding.EncodeToString(raw)
	}
	if err = m.Approve(code(1), device, bridge, "https://example.com:443", "wss://example.com:443"); err != nil {
		t.Fatal(err)
	}
	if err = m.Cancel(device); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 59; i++ {
		_ = m.Approve(code(1), device, bridge, "https://example.com:443", "wss://example.com:443")
	}
	if err = m.Approve(code(2), device, bridge, "https://example.com:443", "wss://example.com:443"); err == nil {
		t.Fatal("approval rate limit did not reject")
	}
}
