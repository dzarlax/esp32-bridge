package voicepush

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

const (
	maxBodyBytes   = 65536
	maxTextBytes   = 8192
	maxTTL         = 150 * time.Second
	maxIDBytes     = 256
	maxSubscribers = 8
	pingEvery      = 15 * time.Second
	pongWait       = 30 * time.Second
)

type Snapshot struct {
	V             int    `json:"v"`
	Type          string `json:"type"`
	LeaseID       string `json:"lease_id"`
	Revision      uint64 `json:"revision"`
	SessionID     string `json:"session_id"`
	TurnID        string `json:"turn_id"`
	Phase         string `json:"phase"`
	BackendActive bool   `json:"backend_active"`
	Playback      string `json:"playback"`
	Text          string `json:"text"`
	TextTruncated bool   `json:"text_truncated"`
	Interrupted   bool   `json:"interrupted"`
	Active        bool   `json:"active"`
	TTLMS         int64  `json:"ttl_ms"`
}

type Clear struct {
	V        int    `json:"v"`
	Type     string `json:"type"`
	LeaseID  string `json:"lease_id"`
	Revision uint64 `json:"revision"`
	Reason   string `json:"reason"`
}

// PairIdentity identifies a specific immutable issuance of a device credential.
type PairIdentity struct {
	DeviceID   string
	Generation string
}

// PairVerifier returns the current identity for a token; revoked tokens return ok=false.
// Implementations must be concurrency-safe and must not call back into the Hub.
type PairVerifier func(token string) (identity PairIdentity, ok bool)

type Hub struct {
	routing                       sync.RWMutex // serializes routing changes with authorization and writes
	verifier                      PairVerifier
	paired                        bool
	publisher, subscriber, target string
	mu                            sync.Mutex
	lease                         string
	last                          *Snapshot // live content only; erased at expiry
	revision                      uint64
	snapshotHash                  [32]byte
	lastClear                     *Clear
	eventSeq                      uint64
	active                        bool
	accepted                      time.Time
	expiryTimer                   *time.Timer
	subs                          map[*client]struct{}
	closed                        bool
}

type hubEvent struct {
	seq      uint64
	snapshot *Snapshot
	clear    *Clear
	deadline time.Time
	replay   bool
}
type client struct {
	conn     *websocket.Conn
	token    string
	identity PairIdentity
	mailbox  chan hubEvent
	// Only the WebSocket writer goroutine touches delivery state.
	deliveredLease    string
	deliveredRevision uint64
	transitionCleared bool
}

func New(publisherBearer, subscriberBearer, target string) *Hub {
	return &Hub{publisher: publisherBearer, subscriber: subscriberBearer, target: target, subs: make(map[*client]struct{})}
}

// NewPaired explicitly enables per-device subscriber authorization. The target may
// be empty to disable subscribers until SetTarget is called. Publisher authentication
// and the publisher protocol remain unchanged.
func NewPaired(publisherBearer, target string, verifier PairVerifier) *Hub {
	return &Hub{publisher: publisherBearer, target: target, verifier: verifier, paired: true, subs: make(map[*client]struct{})}
}

// NewMigrating starts in legacy mode until the pairing store reports its durable
// managed marker. After the marker is set, callers must pass managed=true on restart.
func NewMigrating(publisher, legacySubscriber, legacyTarget string, verifier PairVerifier, pairedTarget string, managed bool) *Hub {
	if managed {
		return NewPaired(publisher, pairedTarget, verifier)
	}
	h := New(publisher, legacySubscriber, legacyTarget)
	h.verifier = verifier
	return h
}

// SetTarget switches the active device, invalidates the publisher lease and retained
// events, and disconnects all subscribers. A new publisher lease is required.
func (h *Hub) SetTarget(target string) {
	h.routing.Lock()
	defer h.routing.Unlock()
	h.setTargetLocked(target)
}

func (h *Hub) setTargetLocked(target string) {
	if !h.paired || h.target == target {
		return
	}
	h.resetRoutingLocked(target)
}

func (h *Hub) resetRoutingLocked(target string) {
	h.mu.Lock()
	h.target = target
	if h.expiryTimer != nil {
		h.expiryTimer.Stop()
		h.expiryTimer = nil
	}
	h.lease, h.last, h.revision, h.snapshotHash, h.active, h.accepted = "", nil, 0, [32]byte{}, false, time.Time{}
	h.lastClear = nil
	h.eventSeq++
	clients := make([]*client, 0, len(h.subs))
	for c := range h.subs {
		clients = append(clients, c)
		delete(h.subs, c)
	}
	h.mu.Unlock()
	for _, c := range clients {
		if c.conn != nil {
			_ = c.conn.Close()
		}
	}
}

// Connected reports whether the active paired device has a registered subscriber
// whose credential is still valid. It does not alter subscriber state.
func (h *Hub) Connected(deviceID string) bool {
	h.routing.RLock()
	defer h.routing.RUnlock()
	if !h.paired || h.verifier == nil || deviceID == "" || h.target != deviceID {
		return false
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return false
	}
	clients := make([]*client, 0, len(h.subs))
	for c := range h.subs {
		clients = append(clients, c)
	}
	h.mu.Unlock()
	for _, c := range clients {
		id, ok := h.verifier(c.token)
		if ok && id == c.identity && id.DeviceID == deviceID {
			h.mu.Lock()
			_, registered := h.subs[c]
			registered = registered && !h.closed
			h.mu.Unlock()
			if registered {
				return true
			}
		}
	}
	return false
}

// Revalidate disconnects subscribers whose credentials were revoked or rotated.
// Call it after updating the verifier's backing store. Delivery also revalidates.
func (h *Hub) Revalidate() {
	h.routing.Lock()
	defer h.routing.Unlock()
	h.revalidateLocked()
}

func (h *Hub) revalidateLocked() {
	if !h.paired || h.verifier == nil {
		return
	}
	h.mu.Lock()
	clients := make([]*client, 0, len(h.subs))
	for c := range h.subs {
		clients = append(clients, c)
	}
	target := h.target
	h.mu.Unlock()
	for _, c := range clients {
		id, ok := h.verifier(c.token)
		if !ok || id != c.identity || id.DeviceID != target {
			h.mu.Lock()
			delete(h.subs, c)
			h.mu.Unlock()
			if c.conn != nil {
				_ = c.conn.Close()
			}
		}
	}
}

// RoutingUpdate serializes a pairing-store mutation and routing reconciliation
// against subscriber authorization and delivery. update must return the current
// authoritative target even on a mutation error, or an empty target if it cannot
// establish the current target. It must release store locks before returning and
// must not call Hub routing methods. Reconciliation runs even if update fails.
// Wrap the store mutation itself in update; calling this after a mutation leaves
// a window where old subscribers can receive data.
func (h *Hub) RoutingUpdate(update func() (target string, err error)) error {
	h.routing.Lock()
	defer h.routing.Unlock()
	if !h.paired {
		return errors.New("paired routing is not enabled")
	}
	target, err := update()
	h.setTargetLocked(target)
	h.revalidateLocked()
	return err
}

// RoutingUpdateMode runs the mutation and reads the durable managed marker under
// the delivery barrier. The callback must release all store locks before returning.
// err means the authoritative mode/target could not be read: routing then fails
// closed. An operation error with a successful state read must be carried separately
// by the caller; return nil here and the verified state. Managed mode is irreversible.
func (h *Hub) RoutingUpdateMode(update func() (managed bool, target string, err error)) error {
	h.routing.Lock()
	defer h.routing.Unlock()
	managed, target, err := update()
	if err != nil {
		managed = true
		target = ""
	}
	if managed && !h.paired {
		h.paired = true
		// Force invalidation even when legacy and paired target IDs happen to match.
		h.resetRoutingLocked(target)
	} else if h.paired {
		// Never return to legacy, even after revocation or an inconsistent marker.
		if !managed {
			target = ""
		}
		h.setTargetLocked(target)
	}
	if h.paired {
		h.revalidateLocked()
	}
	return err
}

func (h *Hub) enabled() bool {
	if h.paired {
		return h.publisher != "" && h.verifier != nil
	}
	return h.publisher != "" && h.subscriber != "" && h.target != "" && h.publisher != h.subscriber
}
func (h *Hub) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/voice/v1/lease", h.handleLease)
	mux.HandleFunc("POST /api/voice/v1/publish", h.handlePublish)
	mux.HandleFunc("GET /api/voice/v1/events", h.handleEvents)
}
func (h *Hub) authorize(w http.ResponseWriter, r *http.Request, expected string) bool {
	if !h.enabled() {
		http.NotFound(w, r)
		return false
	}
	const prefix = "Bearer "
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, prefix) || len(v) == len(prefix) || subtle.ConstantTimeCompare([]byte(v[len(prefix):]), []byte(expected)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}
func (h *Hub) handleLease(w http.ResponseWriter, r *http.Request) {
	h.routing.RLock()
	authorized := h.authorize(w, r, h.publisher)
	if !authorized {
		h.routing.RUnlock()
		return
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		h.routing.RUnlock()
		http.Error(w, "unavailable", 500)
		return
	}
	id := hex.EncodeToString(b)
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		h.routing.RUnlock()
		http.Error(w, "unavailable", 503)
		return
	}
	old := h.lease
	oldRevision := h.revision
	if oldRevision == 0 {
		oldRevision = 1
	}
	if h.expiryTimer != nil {
		h.expiryTimer.Stop()
		h.expiryTimer = nil
	}
	h.lease, h.last, h.revision, h.snapshotHash, h.active, h.accepted = id, nil, 0, [32]byte{}, false, time.Time{}
	if old != "" {
		h.broadcastClearLocked(Clear{1, "voice.clear", old, oldRevision, "reset"})
	}
	h.mu.Unlock()
	h.routing.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"lease_id": id})
}
func (h *Hub) handlePublish(w http.ResponseWriter, r *http.Request) {
	h.routing.RLock()
	authorized := h.authorize(w, r, h.publisher)
	h.routing.RUnlock()
	if !authorized {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil || !utf8.Valid(raw) {
		http.Error(w, "invalid snapshot", 400)
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		http.Error(w, "invalid snapshot", 400)
		return
	}
	for _, key := range []string{"v", "type", "lease_id", "revision", "session_id", "turn_id", "phase", "backend_active", "playback", "text", "text_truncated", "interrupted", "active", "ttl_ms"} {
		if _, ok := fields[key]; !ok {
			http.Error(w, "invalid snapshot", 400)
			return
		}
	}
	for _, key := range []string{"backend_active", "text_truncated", "interrupted", "active"} {
		v := strings.TrimSpace(string(fields[key]))
		if v != "true" && v != "false" {
			http.Error(w, "invalid snapshot", 400)
			return
		}
	}
	for _, key := range []string{"type", "lease_id", "session_id", "turn_id", "phase", "playback", "text"} {
		v := strings.TrimSpace(string(fields[key]))
		if len(v) == 0 || v[0] != '"' {
			http.Error(w, "invalid snapshot", 400)
			return
		}
	}
	for _, key := range []string{"v", "revision", "ttl_ms"} {
		v := strings.TrimSpace(string(fields[key]))
		if len(v) == 0 || (v[0] < '0' || v[0] > '9') {
			http.Error(w, "invalid snapshot", 400)
			return
		}
	}
	var s Snapshot
	if json.Unmarshal(raw, &s) != nil {
		http.Error(w, "invalid snapshot", 400)
		return
	}
	if validate(s) != nil {
		http.Error(w, "invalid snapshot", 400)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		http.Error(w, "unavailable", 503)
		return
	}
	if s.LeaseID != h.lease || h.lease == "" {
		http.Error(w, "invalid lease", 409)
		return
	}
	if h.revision != 0 {
		if s.Revision < h.revision {
			http.Error(w, "stale revision", 409)
			return
		}
		if s.Revision == h.revision {
			if snapshotHash(s) == h.snapshotHash {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.Error(w, "revision conflict", 409)
			return
		}
	}
	if h.expiryTimer != nil {
		h.expiryTimer.Stop()
	}
	h.last = &s
	h.revision = s.Revision
	h.snapshotHash = snapshotHash(s)
	h.lastClear = nil
	h.active = true
	h.accepted = time.Now()
	deadline := h.accepted.Add(time.Duration(s.TTLMS) * time.Millisecond)
	h.broadcastSnapshotLocked(s, deadline)
	h.expiryTimer = time.AfterFunc(time.Until(deadline), func() { h.expire(s.LeaseID, s.Revision) })
	w.WriteHeader(http.StatusNoContent)
}
func validate(s Snapshot) error {
	if s.V != 1 || s.Type != "voice.snapshot" || s.LeaseID == "" || s.SessionID == "" || s.TurnID == "" || len(s.LeaseID) > maxIDBytes || len(s.SessionID) > maxIDBytes || len(s.TurnID) > maxIDBytes || s.Revision == 0 {
		return errors.New("required")
	}
	switch s.Phase {
	case "listening", "processing", "lookup", "answer", "approval_required", "error", "closed":
	default:
		return errors.New("phase")
	}
	switch s.Playback {
	case "pending", "drained", "unknown":
	default:
		return errors.New("playback")
	}
	if !utf8.ValidString(s.Text) || len(s.Text) > maxTextBytes || s.TTLMS < 1 || s.TTLMS > int64(maxTTL/time.Millisecond) {
		return errors.New("bounds")
	}
	return nil
}
func snapshotHash(s Snapshot) [32]byte { b, _ := json.Marshal(s); return sha256.Sum256(b) }
func (h *Hub) expire(lease string, revision uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.lease != lease || h.last == nil || !h.active || h.revision != revision || time.Now().Before(h.accepted.Add(time.Duration(h.last.TTLMS)*time.Millisecond)) {
		return
	}
	h.active = false
	h.last = nil
	h.expiryTimer = nil
	// Clear repeats the expired revision; the high-water revision and digest remain.
	h.broadcastClearLocked(Clear{1, "voice.clear", lease, revision, "expired"})
}
func replayTTL(remaining time.Duration) (int64, bool) {
	if remaining < time.Millisecond {
		return 0, false
	}
	return int64(remaining / time.Millisecond), true
}
func encode(v any) []byte { b, _ := json.Marshal(v); return b }
func (h *Hub) enqueueLocked(c *client, ev hubEvent) {
	select {
	case c.mailbox <- ev:
	default:
		select {
		case <-c.mailbox:
		default:
		}
		select {
		case c.mailbox <- ev:
		default:
		}
	}
}
func (h *Hub) broadcastSnapshotLocked(s Snapshot, deadline time.Time) {
	h.eventSeq++
	cp := s
	ev := hubEvent{seq: h.eventSeq, snapshot: &cp, deadline: deadline}
	for c := range h.subs {
		h.enqueueLocked(c, ev)
	}
}
func (h *Hub) broadcastClearLocked(clear Clear) {
	h.eventSeq++
	cp := clear
	h.lastClear = &cp
	ev := hubEvent{seq: h.eventSeq, clear: &cp}
	for c := range h.subs {
		h.enqueueLocked(c, ev)
	}
}

// prepareEvent validates a dequeued value at send time, so stale buffered snapshots
// cannot survive a lease change or extend their accepted lifetime.
func (h *Hub) prepareEvent(ev hubEvent) []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ev.seq != h.eventSeq {
		return nil
	}
	if ev.clear != nil {
		return encode(*ev.clear)
	}
	if ev.snapshot == nil || h.closed || !h.active || h.last == nil || ev.snapshot.LeaseID != h.lease || ev.snapshot.Revision != h.revision || snapshotHash(*ev.snapshot) != h.snapshotHash {
		return nil
	}
	remaining := time.Until(ev.deadline)
	ttl, ok := replayTTL(remaining)
	if !ok {
		lease, rev := h.lease, h.revision
		h.active = false
		h.last = nil
		if h.expiryTimer != nil {
			h.expiryTimer.Stop()
			h.expiryTimer = nil
		}
		h.broadcastClearLocked(Clear{1, "voice.clear", lease, rev, "expired"})
		return nil
	}
	out := *ev.snapshot
	out.TTLMS = ttl
	return encode(struct {
		Snapshot
		Replay bool `json:"replay"`
	}{out, ev.replay})
}

// deliver preserves the transition barrier relative to what this subscriber
// actually received, even if intermediate lease events were coalesced. The
// writer owns c's delivery state and runs serially; the queue stays bounded.
func (h *Hub) deliver(c *client, ev hubEvent, write func([]byte, time.Time) error) error {
	h.routing.RLock()
	defer h.routing.RUnlock()
	if h.paired {
		id, ok := h.verifier(c.token)
		h.mu.Lock()
		target := h.target
		_, connected := h.subs[c]
		h.mu.Unlock()
		if !ok || !connected || id != c.identity || id.DeviceID != target {
			return errors.New("subscriber unauthorized")
		}
	}
	b := h.prepareEvent(ev)
	if b == nil {
		return nil
	}
	target := ""
	if ev.snapshot != nil {
		target = ev.snapshot.LeaseID
	} else if ev.clear != nil {
		target = ev.clear.LeaseID
	}
	if c.deliveredLease != "" && target != c.deliveredLease && !c.transitionCleared {
		clear := Clear{1, "voice.clear", c.deliveredLease, c.deliveredRevision, "reset"}
		if err := write(encode(clear), time.Now().Add(5*time.Second)); err != nil {
			return err
		}
		c.transitionCleared = true
		// A newer event can supersede ev while the barrier is in flight.
		b = h.prepareEvent(ev)
		if b == nil {
			return nil
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	if ev.snapshot != nil && ev.deadline.Before(deadline) {
		deadline = ev.deadline
	}
	if err := write(b, deadline); err != nil {
		return err
	}
	if ev.snapshot != nil {
		c.deliveredLease, c.deliveredRevision, c.transitionCleared = ev.snapshot.LeaseID, ev.snapshot.Revision, false
	} else if ev.clear != nil && ev.clear.LeaseID == c.deliveredLease && ev.clear.Revision >= c.deliveredRevision {
		c.transitionCleared = true
	}
	return nil
}
func (h *Hub) handleEvents(w http.ResponseWriter, r *http.Request) {
	var token string
	var identity PairIdentity
	h.routing.RLock()
	if h.paired {
		const prefix = "Bearer "
		auth := r.Header.Get("Authorization")
		if !h.enabled() || !strings.HasPrefix(auth, prefix) || len(auth) == len(prefix) {
			h.routing.RUnlock()
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		token = auth[len(prefix):]
		var ok bool
		identity, ok = h.verifier(token)
		if !ok || identity.DeviceID == "" || identity.DeviceID != h.target || token == h.publisher {
			h.routing.RUnlock()
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	} else if !h.authorize(w, r, h.subscriber) {
		h.routing.RUnlock()
		return
	}
	h.mu.Lock()
	full := h.closed || len(h.subs) >= maxSubscribers
	h.mu.Unlock()
	if full {
		h.routing.RUnlock()
		http.Error(w, "subscriber limit", http.StatusServiceUnavailable)
		return
	}
	conn, err := (&websocket.Upgrader{HandshakeTimeout: 5 * time.Second, CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		return origin == "" || strings.EqualFold(origin, "https://"+r.Host) || strings.EqualFold(origin, "http://"+r.Host)
	}, ReadBufferSize: 1024, WriteBufferSize: 4096}).Upgrade(w, r, nil)
	if err != nil {
		h.routing.RUnlock()
		return
	}
	c := &client{conn: conn, token: token, identity: identity, mailbox: make(chan hubEvent, 1)}
	h.mu.Lock()
	if h.closed || len(h.subs) >= maxSubscribers {
		h.mu.Unlock()
		h.routing.RUnlock()
		_ = conn.Close()
		return
	}
	h.subs[c] = struct{}{}
	if h.last != nil && h.active {
		deadline := h.accepted.Add(time.Duration(h.last.TTLMS) * time.Millisecond)
		cp := *h.last
		h.enqueueLocked(c, hubEvent{seq: h.eventSeq, snapshot: &cp, deadline: deadline, replay: true})
	} else if h.lastClear != nil {
		cp := *h.lastClear
		h.enqueueLocked(c, hubEvent{seq: h.eventSeq, clear: &cp})
	}
	h.mu.Unlock()
	h.routing.RUnlock()
	// The connection has been registered; subsequent writes acquire the routing lock.
	conn.SetReadLimit(1024)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(pongWait)) })
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, e := conn.ReadMessage(); e != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(pingEvery)
	defer ticker.Stop()
	defer func() { h.mu.Lock(); delete(h.subs, c); h.mu.Unlock(); _ = conn.Close() }()
	for {
		select {
		case ev := <-c.mailbox:
			if err := h.deliver(c, ev, func(b []byte, deadline time.Time) error {
				_ = conn.SetWriteDeadline(deadline)
				return conn.WriteMessage(websocket.TextMessage, b)
			}); err != nil {
				return
			}
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-done:
			return
		case <-r.Context().Done():
			return
		}
	}
}
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	if h.lease != "" {
		revision := h.revision
		if revision == 0 {
			revision = 1
		}
		h.broadcastClearLocked(Clear{1, "voice.clear", h.lease, revision, "closed"})
	}
	if h.expiryTimer != nil {
		h.expiryTimer.Stop()
		h.expiryTimer = nil
	}
	h.active = false
	h.last = nil
	for c := range h.subs {
		if c.conn != nil {
			_ = c.conn.Close()
		}
	}
	log.Printf("voice push hub closed")
}
