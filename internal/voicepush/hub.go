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

type Hub struct {
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
	conn    *websocket.Conn
	mailbox chan hubEvent
	// Only the WebSocket writer goroutine touches delivery state.
	deliveredLease    string
	deliveredRevision uint64
	transitionCleared bool
}

func New(publisherBearer, subscriberBearer, target string) *Hub {
	return &Hub{publisher: publisherBearer, subscriber: subscriberBearer, target: target, subs: make(map[*client]struct{})}
}
func (h *Hub) enabled() bool {
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
	if !h.authorize(w, r, h.publisher) {
		return
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		http.Error(w, "unavailable", 500)
		return
	}
	id := hex.EncodeToString(b)
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"lease_id": id})
}
func (h *Hub) handlePublish(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, h.publisher) {
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
	if !h.authorize(w, r, h.subscriber) {
		return
	}
	h.mu.Lock()
	full := h.closed || len(h.subs) >= maxSubscribers
	h.mu.Unlock()
	if full {
		http.Error(w, "subscriber limit", http.StatusServiceUnavailable)
		return
	}
	conn, err := (&websocket.Upgrader{CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		return origin == "" || strings.EqualFold(origin, "https://"+r.Host) || strings.EqualFold(origin, "http://"+r.Host)
	}, ReadBufferSize: 1024, WriteBufferSize: 4096}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &client{conn: conn, mailbox: make(chan hubEvent, 1)}
	h.mu.Lock()
	if h.closed || len(h.subs) >= maxSubscribers {
		h.mu.Unlock()
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
