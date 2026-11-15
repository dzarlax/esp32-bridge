package voicepair

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

var ErrRejected = errors.New("pairing request rejected")

type binding struct {
	WSS        string
	Origin     string
	PublicKey  [65]byte
	Generation Generation
	TokenHash  [32]byte
	Revoked    bool
	Epoch      uint64
	Clear      *ClearCommand
	Confirmed  bool
}
type candidate struct {
	PublicKey     [65]byte
	Generation    Generation
	TokenHash     [32]byte
	SessionDigest Secret
	KeyID         string
	WSS           string
}
type diskState struct {
	ManagedMode bool
	Target      ID
	Version     int
	SigningHash [32]byte
	KeyID       string
	Bridge      ID
	Bindings    map[ID]binding
	Candidates  map[ID]candidate
	Tombstones  map[Secret]time.Time
}
type session struct {
	promoted    bool
	qr          QR
	origin, wss string
	expires     time.Time
}
type challenge struct {
	transcript Transcript
	pub        [65]byte
	expires    time.Time
}
type Manager struct {
	clearNonces      map[Secret]time.Time
	lock             *os.File
	approvals        []time.Time
	mu               sync.Mutex
	path             string
	bridge           ID
	master           [32]byte
	integrity        [32]byte
	integrityID      string
	pendingAvailable bool
	keyID            string
	poisoned         bool
	signing          *ecdsa.PrivateKey
	state            diskState
	sessions         map[Secret]session
	challenges       map[Secret]challenge
	recent           map[ID][]time.Time
	now              func() time.Time
}
type Delivery struct {
	Generation       Generation
	WSSBase          string
	Token            string
	ServerSigningKey [65]byte
}
type Status struct {
	WSSOrigin  string
	State      string
	Generation Generation
	Epoch      uint64
}
type ClearCommand struct {
	Bridge     ID
	Device     ID
	KeyHash    [32]byte
	Generation Generation
	Epoch      uint64
	Nonce      Secret
	Signature  string
}

// Open requires a dedicated master key and separate persistent P-256 signing key.
// Sessions and challenges are intentionally lost on restart.
func Open(dir string, bridge ID, master [32]byte, keyID string, integrity [32]byte, integrityID string, signing *ecdsa.PrivateKey) (*Manager, error) {
	if integrity == ([32]byte{}) || integrityID == "" || integrity == master || keyID == "" || bridge == (ID{}) || signing == nil || signing.Curve != elliptic.P256() || signing.D == nil || signing.X == nil || signing.Y == nil || !signing.Curve.IsOnCurve(signing.X, signing.Y) {
		return nil, ErrRejected
	}
	if signing.D.Sign() <= 0 || signing.D.Cmp(elliptic.P256().Params().N) >= 0 {
		return nil, ErrRejected
	}
	var scalar [32]byte
	signing.D.FillBytes(scalar[:])
	if scalar == master || scalar == integrity {
		return nil, ErrRejected
	}
	x, y := elliptic.P256().ScalarBaseMult(signing.D.Bytes())
	if x.Cmp(signing.X) != 0 || y.Cmp(signing.Y) != 0 {
		return nil, ErrRejected
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode().Perm() != 0700 {
		return nil, ErrRejected
	}
	lockPath := filepath.Join(dir, "voicepair.lock")
	fd, err := unix.Open(lockPath, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, ErrRejected
	}
	lock := os.NewFile(uintptr(fd), lockPath)
	acquired := false
	defer func() {
		if !acquired {
			lock.Close()
		}
	}()
	li, err := os.Lstat(lockPath)
	if err != nil || !li.Mode().IsRegular() || li.Mode().Perm() != 0600 {
		return nil, ErrRejected
	}
	fi, err := lock.Stat()
	if err != nil || !os.SameFile(li, fi) || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0600 {
		return nil, ErrRejected
	}
	if unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		return nil, ErrRejected
	}
	m := &Manager{clearNonces: map[Secret]time.Time{}, lock: lock, path: filepath.Join(dir, "voicepair.json"), bridge: bridge, master: master, keyID: keyID, integrity: integrity, integrityID: integrityID, pendingAvailable: master != ([32]byte{}), signing: signing, sessions: map[Secret]session{}, challenges: map[Secret]challenge{}, recent: map[ID][]time.Time{}, now: time.Now}
	signingHash := sha256.Sum256(elliptic.Marshal(elliptic.P256(), signing.X, signing.Y))
	m.state = diskState{Version: 3, SigningHash: signingHash, KeyID: keyID, Bridge: bridge, Bindings: map[ID]binding{}, Candidates: map[ID]candidate{}, Tombstones: map[Secret]time.Time{}}
	info, err := os.Lstat(m.path)
	if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 8<<20) {
		return nil, ErrRejected
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, ErrRejected
	}
	var b []byte
	if err == nil {
		f, e := os.Open(m.path)
		if e != nil {
			return nil, ErrRejected
		}
		opened, e := f.Stat()
		if e != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Mode().Perm() != 0600 || opened.Size() > 8<<20 {
			f.Close()
			return nil, ErrRejected
		}
		b, e = io.ReadAll(io.LimitReader(f, (8<<20)+1))
		f.Close()
		if e != nil || len(b) > 8<<20 {
			return nil, ErrRejected
		}
	}
	if err == nil {
		var envelope struct {
			KeyID string
			State json.RawMessage
			MAC   []byte
		}
		if json.Unmarshal(b, &envelope) != nil || envelope.KeyID != integrityID {
			return nil, ErrRejected
		}
		h := hmac.New(sha256.New, integrity[:])
		h.Write([]byte("HomeDash voice state v1\x00"))
		h.Write(envelope.State)
		if !hmac.Equal(h.Sum(nil), envelope.MAC) {
			return nil, ErrRejected
		}
		if json.Unmarshal(envelope.State, &m.state) != nil || m.state.Version != 3 || m.state.SigningHash != signingHash || m.state.KeyID != keyID || m.state.Bridge != bridge || m.state.Bindings == nil || m.state.Candidates == nil || m.state.Tombstones == nil || len(m.state.Bindings) > 4096 || len(m.state.Candidates) > 4096 || len(m.state.Tombstones) > 4096 {
			return nil, ErrRejected
		}
	} else if !os.IsNotExist(err) {
		return nil, ErrRejected
	}
	if m.state.Target != (ID{}) {
		b, ok := m.state.Bindings[m.state.Target]
		if !ok || b.Revoked {
			return nil, ErrRejected
		}
	}
	for device, p := range m.state.Candidates {
		if !m.pendingAvailable {
			continue
		}
		token := m.token(device, p)
		if p.KeyID != keyID || sha256.Sum256(token[:]) != p.TokenHash {
			m.pendingAvailable = false
		}
	}
	acquired = true
	return m, nil
}
func (m *Manager) save(s diskState) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return ErrRejected
	}
	h := hmac.New(sha256.New, m.integrity[:])
	h.Write([]byte("HomeDash voice state v1\x00"))
	h.Write(raw)
	b, err := json.Marshal(struct {
		KeyID string
		State json.RawMessage
		MAC   []byte
	}{m.integrityID, raw, h.Sum(nil)})
	if err != nil {
		return ErrRejected
	}
	dir := filepath.Dir(m.path)
	f, err := os.CreateTemp(dir, ".voicepair-")
	if err != nil {
		return ErrRejected
	}
	name := f.Name()
	defer os.Remove(name)
	if f.Chmod(0600) != nil {
		f.Close()
		return ErrRejected
	}
	if _, err = f.Write(b); err != nil {
		f.Close()
		return ErrRejected
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if syncErr != nil || closeErr != nil {
		return ErrRejected
	}
	if os.Rename(name, m.path) != nil {
		return ErrRejected
	}
	d, err := os.Open(dir)
	if err != nil {
		return ErrRejected
	}
	err = d.Sync()
	d.Close()
	if err != nil {
		return ErrRejected
	}
	return nil
}
func cloneState(s diskState) diskState {
	n := diskState{ManagedMode: s.ManagedMode, Target: s.Target, Version: s.Version, SigningHash: s.SigningHash, KeyID: s.KeyID, Bridge: s.Bridge, Bindings: map[ID]binding{}, Candidates: map[ID]candidate{}, Tombstones: map[Secret]time.Time{}}
	for k, v := range s.Bindings {
		n.Bindings[k] = v
	}
	for k, v := range s.Candidates {
		n.Candidates[k] = v
	}
	for k, v := range s.Tombstones {
		n.Tombstones[k] = v
	}
	return n
}
func random(b []byte) error {
	_, err := io.ReadFull(rand.Reader, b)
	if err != nil {
		return ErrRejected
	}
	return nil
}
func (m *Manager) mac(label string, parts ...[]byte) Secret {
	h := hmac.New(sha256.New, m.master[:])
	h.Write([]byte(label))
	h.Write([]byte{0})
	for _, p := range parts {
		h.Write(p)
	}
	var result Secret
	copy(result[:], h.Sum(nil))
	return result
}
func (m *Manager) token(device ID, p candidate) Secret {
	return m.mac("HomeDash voice token v1", m.bridge[:], device[:], p.Generation[:], p.SessionDigest[:])
}
func (m *Manager) persist(s diskState) error {
	if m.poisoned {
		return ErrRejected
	}
	if err := m.save(s); err != nil {
		m.poisoned = true
		return err
	}
	m.state = s
	return nil
}
func (m *Manager) Approve(qrText string, selected, selectedBridge ID, httpsOrigin, wss string) error {
	if !m.pendingAvailable {
		return ErrRejected
	}
	q, err := ParseQR(qrText)
	if err != nil || q.Device != selected || q.Bridge != selectedBridge || q.Bridge != m.bridge || ValidateOrigins(httpsOrigin, wss) != nil {
		return ErrRejected
	}
	expected := sha256.Sum256([]byte(httpsOrigin))
	if subtle.ConstantTimeCompare(q.OriginHash[:], expected[:15]) != 1 {
		return ErrRejected
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	m.approvals = append(m.approvals, now)
	if len(m.approvals) > 60 {
		m.approvals = m.approvals[len(m.approvals)-60:]
		if now.Sub(m.approvals[0]) < time.Minute {
			return ErrRejected
		}
	}
	if m.poisoned {
		return ErrRejected
	}
	for k, v := range m.sessions {
		if !now.Before(v.expires) {
			delete(m.sessions, k)
		}
	}
	s := cloneState(m.state)
	for n, t := range s.Tombstones {
		if !now.Before(t) {
			delete(s.Tombstones, n)
		}
	}
	digest := m.mac("HomeDash voice nonce tombstone v1", q.Nonce[:])
	if _, ok := s.Tombstones[digest]; ok || len(s.Bindings) >= 4096 || len(s.Tombstones) >= 4096 || len(m.sessions) >= 128 {
		return ErrRejected
	}
	for _, v := range m.sessions {
		if v.qr.Device == q.Device && now.Before(v.expires) {
			return ErrRejected
		}
	}
	// A candidate belongs to a lost or expired session; discard only the candidate.
	if b, ok := s.Bindings[q.Device]; ok && b.Clear != nil && !b.Confirmed {
		return ErrRejected
	}
	delete(s.Candidates, q.Device)
	s.Tombstones[digest] = now.Add(24 * time.Hour)
	if err := m.persist(s); err != nil {
		return err
	}
	m.sessions[q.Nonce] = session{qr: q, origin: httpsOrigin, wss: wss, expires: now.Add(120 * time.Second)}
	return nil
}
func (m *Manager) Issue(t Transcript) (Secret, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.Operation == ClearAck {
		return Secret{}, ErrRejected
	}
	return m.issueLocked(t)
}

// IssueClearAck verifies possession before allocating challenge/rate-limit slots.
func (m *Manager) IssueClearAck(t Transcript, nonce Secret, signature string) (Secret, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if m.poisoned || t.Bridge != m.bridge {
		return Secret{}, ErrRejected
	}
	b, ok := m.state.Bindings[t.Device]
	if !ok || b.Clear == nil || b.Generation != t.Generation || b.Epoch != t.Epoch || b.Origin != t.HTTPSOrigin {
		return Secret{}, ErrRejected
	}
	msg, err := ClearAckPreauthBytes(t, nonce)
	if err != nil {
		return Secret{}, ErrRejected
	}
	pub, err := ParsePublicKey(b.PublicKey[:])
	if err != nil || !verifySignature(pub, msg, signature) {
		return Secret{}, ErrRejected
	}
	for n, expiry := range m.clearNonces {
		if !now.Before(expiry) {
			delete(m.clearNonces, n)
		}
	}
	if _, exists := m.clearNonces[nonce]; exists || len(m.clearNonces) >= 4096 {
		return Secret{}, ErrRejected
	}
	challenge, err := m.issueLocked(t)
	if err != nil {
		return Secret{}, ErrRejected
	}
	m.clearNonces[nonce] = now.Add(30 * time.Second)
	return challenge, nil
}
func (m *Manager) issueLocked(t Transcript) (Secret, error) {
	now := m.now()
	var z Secret
	if m.poisoned || (!m.pendingAvailable && t.Operation != ClearAck) {
		return z, ErrRejected
	}
	for k, v := range m.challenges {
		if !now.Before(v.expires) {
			delete(m.challenges, k)
		}
	}
	if len(m.challenges) >= 1024 {
		return z, ErrRejected
	}

	count := 0
	for _, v := range m.challenges {
		if v.transcript.Device == t.Device {
			count++
		}
	}
	if count >= 8 || t.Bridge != m.bridge {
		return z, ErrRejected
	}
	var pub [65]byte
	if t.Operation == ClearAck {
		b, ok := m.state.Bindings[t.Device]
		if !ok || b.Clear == nil || b.Generation != t.Generation || b.Epoch != t.Epoch || b.Origin != t.HTTPSOrigin || t.Session != z {
			return z, ErrRejected
		}
		pub = b.PublicKey
	} else {
		s, ok := m.sessions[t.Session]
		if !ok || !now.Before(s.expires) || s.qr.Device != t.Device || s.origin != t.HTTPSOrigin {
			return z, ErrRejected
		}
		pub = s.qr.PublicKey
		if t.Operation == Ack {
			c, ok := m.state.Candidates[t.Device]
			b := m.state.Bindings[t.Device]
			if (!ok || c.Generation != t.Generation) && (b.Revoked || b.Generation != t.Generation || b.PublicKey != pub) {
				return z, ErrRejected
			}
		} else if t.Operation != Deliver || s.promoted {
			return z, ErrRejected
		}
	}
	// Only authenticated session/binding identities can occupy rate-limit entries.
	if _, ok := m.recent[t.Device]; !ok && len(m.recent) >= 4096 {
		return z, ErrRejected
	}
	r := m.recent[t.Device][:0]
	for _, v := range m.recent[t.Device] {
		if now.Sub(v) < time.Minute {
			r = append(r, v)
		}
	}
	if len(r) >= 60 {
		return z, ErrRejected
	}
	m.recent[t.Device] = r
	t.Challenge[0] = 1 // Validate context; replaced with fresh randomness below.
	if _, err := t.Bytes(); err != nil {
		return z, ErrRejected
	}
	if t.Operation == ClearAck { // origin must be supplied by trusted caller and checked at integration
		if _, err := canonicalAuthority(t.HTTPSOrigin, "https"); err != nil {
			return z, ErrRejected
		}
	}
	if random(z[:]) != nil {
		return Secret{}, ErrRejected
	}
	if _, exists := m.challenges[z]; exists {
		return Secret{}, ErrRejected
	}
	t.Challenge = z
	m.challenges[z] = challenge{transcript: t, pub: pub, expires: now.Add(30 * time.Second)}
	m.recent[t.Device] = append(r, now)
	return z, nil
}

// Prove consumes a challenge after signature verification. Replays cannot succeed.
// A valid deliver returns the same pending credential until ACK.
func (m *Manager) Prove(ch Secret, sig string) (*Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.proveLocked(ch, sig, nil)
}

// ProveBound additionally requires the request context to equal the issued challenge.
func (m *Manager) ProveBound(t Transcript, sig string) (*Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.proveLocked(t.Challenge, sig, &t)
}
func (m *Manager) proveLocked(ch Secret, sig string, supplied *Transcript) (*Delivery, error) {
	if m.poisoned {
		return nil, ErrRejected
	}
	c, ok := m.challenges[ch]
	if !ok {
		return nil, ErrRejected
	}
	if !m.now().Before(c.expires) {
		delete(m.challenges, ch)
		return nil, ErrRejected
	}
	t := c.transcript
	if supplied != nil && *supplied != t {
		return nil, ErrRejected
	}
	pub, err := ParsePublicKey(c.pub[:])
	if err != nil || !VerifyProof(pub, t, sig) {
		return nil, ErrRejected
	}
	delete(m.challenges, ch)
	if t.Operation != ClearAck {
		s, ok := m.sessions[t.Session]
		if !ok || !m.now().Before(s.expires) || s.qr.PublicKey != c.pub || (t.Operation == Deliver && s.promoted) {
			return nil, ErrRejected
		}
	}
	s := cloneState(m.state)
	switch t.Operation {
	case Deliver:
		p, ok := s.Candidates[t.Device]
		if !ok {
			if random(p.Generation[:]) != nil || p.Generation == (Generation{}) {
				return nil, ErrRejected
			}
			p.PublicKey = c.pub
			p.WSS = m.sessions[t.Session].wss
			p.SessionDigest = m.mac("HomeDash voice session digest v1", t.Session[:])
			p.KeyID = m.keyID
			tok := m.token(t.Device, p)
			p.TokenHash = sha256.Sum256(tok[:])
			s.Candidates[t.Device] = p
			if m.persist(s) != nil {
				return nil, ErrRejected
			}
		}
		if p.PublicKey != c.pub {
			return nil, ErrRejected
		}
		if p.KeyID != m.keyID {
			return nil, ErrRejected
		}
		token := m.token(t.Device, p)
		if sha256.Sum256(token[:]) != p.TokenHash {
			return nil, ErrRejected
		}
		var pk [65]byte
		copy(pk[:], elliptic.Marshal(elliptic.P256(), m.signing.X, m.signing.Y))
		return &Delivery{Generation: p.Generation, WSSBase: p.WSS, Token: base64.RawURLEncoding.EncodeToString(token[:]), ServerSigningKey: pk}, nil
	case Ack:
		p, ok := s.Candidates[t.Device]
		if !ok {
			b := s.Bindings[t.Device]
			if b.Generation == t.Generation && b.PublicKey == c.pub && !b.Revoked {
				return nil, nil
			}
			return nil, ErrRejected
		}
		if p.Generation != t.Generation || p.PublicKey != c.pub {
			return nil, ErrRejected
		}
		old := s.Bindings[t.Device]
		if old.Clear != nil && !old.Confirmed {
			return nil, ErrRejected
		}
		s.Target = t.Device
		s.ManagedMode = true
		s.Bindings[t.Device] = binding{PublicKey: p.PublicKey, Generation: p.Generation, TokenHash: p.TokenHash, Epoch: old.Epoch, Origin: t.HTTPSOrigin, WSS: p.WSS}
		delete(s.Candidates, t.Device)
		if m.persist(s) != nil {
			return nil, ErrRejected
		}
		ss := m.sessions[t.Session]
		ss.promoted = true
		m.sessions[t.Session] = ss
		return nil, nil
	case ClearAck:
		b, ok := s.Bindings[t.Device]
		if !ok || b.Clear == nil || b.PublicKey != c.pub || b.Generation != t.Generation || b.Epoch != t.Epoch || b.Origin != t.HTTPSOrigin {
			return nil, ErrRejected
		}
		if !b.Confirmed {
			b.Confirmed = true
			s.Bindings[t.Device] = b
			if m.persist(s) != nil {
				return nil, ErrRejected
			}
		}
		return nil, nil
	}
	return nil, ErrRejected
}
func (m *Manager) Status(device ID) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.poisoned {
		return Status{State: "unavailable"}
	}
	if c, ok := m.state.Candidates[device]; ok {
		return Status{State: "pending_ack", Generation: c.Generation, WSSOrigin: c.WSS}
	}
	for _, sess := range m.sessions {
		if !sess.promoted && sess.qr.Device == device && m.now().Before(sess.expires) {
			return Status{State: "awaiting", WSSOrigin: sess.wss}
		}
	}
	b, ok := m.state.Bindings[device]
	if !ok {
		return Status{State: "unpaired"}
	}
	if b.Revoked {
		if b.Confirmed {
			return Status{State: "revoked", Generation: b.Generation, Epoch: b.Epoch, WSSOrigin: b.WSS}
		}
		return Status{State: "pending_clear", Generation: b.Generation, Epoch: b.Epoch, WSSOrigin: b.WSS}
	}
	return Status{State: "active", Generation: b.Generation, Epoch: b.Epoch, WSSOrigin: b.WSS}
}
func (m *Manager) Authenticate(device ID, generation Generation, encodedToken string) bool {
	token, err := decode64(encodedToken, 32)
	if err != nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.state.Bindings[device]
	hash := sha256.Sum256(token)
	return !m.poisoned && ok && !b.Revoked && b.Generation == generation && subtle.ConstantTimeCompare(b.TokenHash[:], hash[:]) == 1
}

// Revoke invalidates the active grant before returning a signed clear command.
// Callers must serialize this with connection authentication and close sockets.
func (m *Manager) Revoke(device ID) (ClearCommand, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var zero ClearCommand
	if m.poisoned {
		return zero, ErrRejected
	}
	b, ok := m.state.Bindings[device]
	if !ok {
		return zero, ErrRejected
	}
	if b.Revoked && b.Clear != nil {
		return *b.Clear, nil
	}
	if b.Epoch == ^uint64(0) {
		return zero, ErrRejected
	}
	c := ClearCommand{Bridge: m.bridge, Device: device, KeyHash: sha256.Sum256(b.PublicKey[:]), Generation: b.Generation, Epoch: b.Epoch + 1}
	if random(c.Nonce[:]) != nil {
		return zero, ErrRejected
	}
	msg := append([]byte("HomeDash voice server clear v1"), 0, 1, 1)
	msg = append(msg, c.Bridge[:]...)
	msg = append(msg, c.Device[:]...)
	msg = append(msg, c.KeyHash[:]...)
	msg = append(msg, c.Generation[:]...)
	msg = binary.BigEndian.AppendUint64(msg, c.Epoch)
	msg = append(msg, c.Nonce[:]...)
	digest := sha256.Sum256(msg)
	r, s, err := ecdsa.Sign(rand.Reader, m.signing, digest[:])
	if err != nil {
		return zero, ErrRejected
	}
	n := elliptic.P256().Params().N
	half := new(big.Int).Rsh(new(big.Int).Set(n), 1)
	if s.Cmp(half) > 0 {
		s.Sub(n, s)
	}
	raw := make([]byte, 64)
	r.FillBytes(raw[:32])
	s.FillBytes(raw[32:])
	c.Signature = base64.RawURLEncoding.EncodeToString(raw)
	st := cloneState(m.state)
	b.Revoked = true
	b.Epoch = c.Epoch
	b.Clear = &c
	b.Confirmed = false
	if st.Target == device {
		st.Target = ID{}
	}
	st.Bindings[device] = b
	delete(st.Candidates, device)
	if m.persist(st) != nil {
		return zero, ErrRejected
	}
	for k, v := range m.sessions {
		if v.qr.Device == device {
			delete(m.sessions, k)
		}
	}
	for k, v := range m.challenges {
		if v.transcript.Device == device {
			delete(m.challenges, k)
		}
	}
	return c, nil
}

// Close releases exclusive ownership and permanently disables this manager.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lock == nil {
		return nil
	}
	m.poisoned = true
	err := m.lock.Close()
	m.lock = nil
	if err != nil {
		return ErrRejected
	}
	return nil
}

// LookupActiveToken resolves only a currently active credential; pending and
// revoked credentials never authenticate. State capacity bounds the scan.
func (m *Manager) LookupActiveToken(encoded string) (ID, Generation, bool) {
	var id ID
	var gen Generation
	token, err := decode64(encoded, 32)
	if err != nil {
		return id, gen, false
	}
	hash := sha256.Sum256(token)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.poisoned {
		return id, gen, false
	}
	matches := 0
	for device, b := range m.state.Bindings {
		equal := subtle.ConstantTimeCompare(hash[:], b.TokenHash[:])
		if equal == 1 && !b.Revoked {
			id = device
			gen = b.Generation
			matches++
		}
	}
	if matches != 1 {
		return ID{}, Generation{}, false
	}
	return id, gen, true
}

// Cancel ends a pending physical pairing while preserving the active grant.
func (m *Manager) Cancel(device ID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.poisoned {
		return ErrRejected
	}
	st := cloneState(m.state)
	if _, ok := st.Candidates[device]; ok {
		delete(st.Candidates, device)
		if m.persist(st) != nil {
			return ErrRejected
		}
	}
	for k, v := range m.sessions {
		if v.qr.Device == device {
			delete(m.sessions, k)
		}
	}
	for k, v := range m.challenges {
		if v.transcript.Device == device && v.transcript.Operation != ClearAck {
			delete(m.challenges, k)
		}
	}
	return nil
}

// PendingClear returns the signed, nonsecret clear command for exactly device.
// Its command nonce is public replay-binding data, not a pairing session secret.
// The caller must deliver it only to the addressed device as untrusted data.
func (m *Manager) PendingClear(device ID) (ClearCommand, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.poisoned {
		return ClearCommand{}, false
	}
	b, ok := m.state.Bindings[device]
	if !ok || !b.Revoked || b.Confirmed || b.Clear == nil || b.Clear.Device != device {
		return ClearCommand{}, false
	}
	return *b.Clear, true
}

// SelectedTarget returns the one durably selected active voice recipient.
func (m *Manager) SelectedTarget() (ID, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.poisoned || m.state.Target == (ID{}) {
		return ID{}, false
	}
	b, ok := m.state.Bindings[m.state.Target]
	if !ok || b.Revoked {
		return ID{}, false
	}
	return m.state.Target, true
}

// ManagedMode reports whether any pairing has been successfully promoted.
// It never reverts automatically; callers must treat errors as fail-closed.
func (m *Manager) ManagedMode() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.poisoned {
		return false, ErrRejected
	}
	return m.state.ManagedMode, nil
}
