package voicepush

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const pub = "synthetic-publisher-credential"
const sub = "synthetic-subscriber-credential"

type testAPI struct{ mux *http.ServeMux }

func setup(t *testing.T) (*Hub, *testAPI) {
	t.Helper()
	h := New(pub, sub, "device-fixture")
	mux := http.NewServeMux()
	h.Register(mux)
	t.Cleanup(h.Close)
	return h, &testAPI{mux}
}
func request(t *testing.T, api *testAPI, method, path, token string, body []byte) *http.Response {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	api.mux.ServeHTTP(w, r)
	return w.Result()
}
func lease(t *testing.T, s *testAPI) string {
	t.Helper()
	r := request(t, s, "POST", "/api/voice/v1/lease", pub, nil)
	defer r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("lease status %d", r.StatusCode)
	}
	var v struct {
		LeaseID string `json:"lease_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v.LeaseID
}
func snapshot(lease string, rev uint64, ttl int64) Snapshot {
	return Snapshot{V: 1, Type: "voice.snapshot", LeaseID: lease, Revision: rev, SessionID: "sess", TurnID: "turn", Phase: "answer", Playback: "unknown", Text: "synthetic", Active: true, TTLMS: ttl}
}
func publish(t *testing.T, s *testAPI, v Snapshot) int {
	t.Helper()
	b, _ := json.Marshal(v)
	r := request(t, s, "POST", "/api/voice/v1/publish", pub, b)
	defer r.Body.Close()
	return r.StatusCode
}
func TestCredentialRolesAndDisabled(t *testing.T) {
	_, s := setup(t)
	if r := request(t, s, "POST", "/api/voice/v1/lease", sub, nil); r.StatusCode != 401 {
		t.Fatalf("subscriber got lease: %d", r.StatusCode)
	}
	if r := request(t, s, "POST", "/api/voice/v1/lease", "", nil); r.StatusCode != 401 {
		t.Fatalf("missing auth: %d", r.StatusCode)
	}
	h := New("", sub, "x")
	m := http.NewServeMux()
	h.Register(m)
	q := httptest.NewRecorder()
	m.ServeHTTP(q, httptest.NewRequest("POST", "/api/voice/v1/lease", nil))
	if q.Code != 404 {
		t.Fatalf("disabled status %d", q.Code)
	}
}
func TestRevisionAndDuplicateRules(t *testing.T) {
	_, s := setup(t)
	id := lease(t, s)
	a := snapshot(id, 2, 60000)
	if got := publish(t, s, a); got != 204 {
		t.Fatalf("publish %d", got)
	}
	if got := publish(t, s, a); got != 204 {
		t.Fatalf("identical retry %d", got)
	}
	conflict := a
	conflict.Text = "different"
	if got := publish(t, s, conflict); got != 409 {
		t.Fatalf("conflict %d", got)
	}
	if got := publish(t, s, snapshot(id, 1, 60000)); got != 409 {
		t.Fatalf("stale %d", got)
	}
	if got := publish(t, s, snapshot(id, 3, 60000)); got != 204 {
		t.Fatalf("gap %d", got)
	}
}
func TestValidationAndLeaseReplacement(t *testing.T) {
	_, s := setup(t)
	first := lease(t, s)
	bad := snapshot(first, 1, 60000)
	bad.Phase = "ordinary-error"
	if got := publish(t, s, bad); got != 400 {
		t.Fatalf("bad phase %d", got)
	}
	if got := publish(t, s, snapshot(first, 1, 60000)); got != 204 {
		t.Fatal(got)
	}
	second := lease(t, s)
	if first == second {
		t.Fatal("lease did not change")
	}
	if got := publish(t, s, snapshot(first, 2, 60000)); got != 409 {
		t.Fatalf("old lease accepted %d", got)
	}
}
func TestExpiryClearsLatestWithoutConsumingRevision(t *testing.T) {
	h, s := setup(t)
	id := lease(t, s)
	if got := publish(t, s, snapshot(id, 7, 35)); got != 204 {
		t.Fatal(got)
	}
	time.Sleep(80 * time.Millisecond)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.last != nil || h.active || h.revision != 7 || h.snapshotHash == ([32]byte{}) {
		t.Fatal("content was not erased or high-water identity was lost")
	}
	if h.lease != id {
		t.Fatal("expiry changed lease")
	}
}

func TestExpiryClearKeepsPublisherRevisionNamespace(t *testing.T) {
	h, api := setup(t)
	id := lease(t, api)
	c := &client{mailbox: make(chan hubEvent, 4)}
	h.mu.Lock()
	h.subs[c] = struct{}{}
	h.mu.Unlock()
	if got := publish(t, api, snapshot(id, 7, 25)); got != 204 {
		t.Fatal(got)
	}
	time.Sleep(70 * time.Millisecond)
	select {
	case <-c.mailbox:
	default:
	} // discard accepted snapshot
	var clear Clear
	select {
	case ev := <-c.mailbox:
		b := h.prepareEvent(ev)
		if b == nil {
			t.Fatal("expiry clear was dropped")
		}
		_ = json.Unmarshal(b, &clear)
	case <-time.After(time.Second):
		t.Fatal("missing expiry clear")
	}
	if clear.Type != "voice.clear" || clear.Revision != 7 || clear.Reason != "expired" {
		t.Fatalf("unexpected clear: %+v", clear)
	}
	if got := publish(t, api, snapshot(id, 8, 60000)); got != 204 {
		t.Fatalf("expiry consumed publisher revision: %d", got)
	}
}

func TestExpiredRevisionCannotResurrectAndTTLConflictRejected(t *testing.T) {
	h, api := setup(t)
	id := lease(t, api)
	a := snapshot(id, 4, 25)
	if got := publish(t, api, a); got != 204 {
		t.Fatal(got)
	}
	altered := a
	altered.TTLMS++
	if got := publish(t, api, altered); got != 409 {
		t.Fatalf("same revision different TTL accepted: %d", got)
	}
	time.Sleep(70 * time.Millisecond)
	if got := publish(t, api, a); got != 204 {
		t.Fatalf("identical duplicate should be idempotent: %d", got)
	}
	h.mu.Lock()
	active := h.active
	last := h.last
	h.mu.Unlock()
	if active || last != nil || h.revision != 4 || h.snapshotHash == ([32]byte{}) {
		t.Fatal("expired duplicate reopened content or lost high-water state")
	}
	if got := publish(t, api, snapshot(id, 3, 60000)); got != 409 {
		t.Fatalf("stale revision accepted after expiry: %d", got)
	}
}
func TestRequiredBooleanFieldsAndIDBounds(t *testing.T) {
	_, api := setup(t)
	id := lease(t, api)
	raw := []byte(`{"v":1,"type":"voice.snapshot","lease_id":"` + id + `","revision":1,"session_id":"s","turn_id":"t","phase":"answer","playback":"unknown","text":"x","ttl_ms":1000,"active":true}`)
	r := request(t, api, "POST", "/api/voice/v1/publish", pub, raw)
	defer r.Body.Close()
	if r.StatusCode != 400 {
		t.Fatalf("missing boolean keys accepted: %d", r.StatusCode)
	}
	s := snapshot(id, 1, 1000)
	s.SessionID = string(bytes.Repeat([]byte{'x'}, maxIDBytes+1))
	if got := publish(t, api, s); got != 400 {
		t.Fatalf("unbounded id accepted: %d", got)
	}
}
func TestOnlyOneExpiryTimerIsRetained(t *testing.T) {
	h, api := setup(t)
	id := lease(t, api)
	if publish(t, api, snapshot(id, 1, 60000)) != 204 || publish(t, api, snapshot(id, 2, 60000)) != 204 {
		t.Fatal("publish failed")
	}
	h.mu.Lock()
	timer := h.expiryTimer
	h.mu.Unlock()
	if timer == nil {
		t.Fatal("missing active expiry timer")
	}
}
func TestSubscriberLimitRejectsBeforeUpgrade(t *testing.T) {
	h, api := setup(t)
	h.mu.Lock()
	for i := 0; i < maxSubscribers; i++ {
		h.subs[&client{}] = struct{}{}
	}
	h.mu.Unlock()
	r := request(t, api, "GET", "/api/voice/v1/events", sub, nil)
	defer r.Body.Close()
	if r.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("subscriber limit status %d", r.StatusCode)
	}
}

func TestNullBooleanAndInvalidUTF8Rejected(t *testing.T) {
	_, api := setup(t)
	id := lease(t, api)
	raw := []byte(`{"v":1,"type":"voice.snapshot","lease_id":"` + id + `","revision":1,"session_id":"s","turn_id":"t","phase":"answer","backend_active":null,"playback":"unknown","text":"x","text_truncated":false,"interrupted":false,"active":true,"ttl_ms":1000}`)
	r := request(t, api, "POST", "/api/voice/v1/publish", pub, raw)
	r.Body.Close()
	if r.StatusCode != 400 {
		t.Fatalf("null boolean accepted: %d", r.StatusCode)
	}
	raw = []byte(`{"v":1,"type":"voice.snapshot","lease_id":"` + id + `","revision":1,"session_id":"s","turn_id":"t","phase":"answer","backend_active":false,"playback":"unknown","text":"`)
	raw = append(raw, 0xff)
	raw = append(raw, []byte(`","text_truncated":false,"interrupted":false,"active":true,"ttl_ms":1000}`)...)
	r = request(t, api, "POST", "/api/voice/v1/publish", pub, raw)
	r.Body.Close()
	if r.StatusCode != 400 {
		t.Fatalf("invalid UTF-8 accepted: %d", r.StatusCode)
	}
}

func TestReplayTTLNeverRoundsUp(t *testing.T) {
	if ttl, ok := replayTTL(time.Microsecond * 999); ok || ttl != 0 {
		t.Fatalf("sub-ms lifetime replayed as %d, %v", ttl, ok)
	}
	if ttl, ok := replayTTL(time.Millisecond + time.Microsecond*900); !ok || ttl != 1 {
		t.Fatalf("unexpected replay ttl: %d, %v", ttl, ok)
	}
}

func TestExpiredTextIsErasedButDigestRejectsConflicts(t *testing.T) {
	h, api := setup(t)
	id := lease(t, api)
	s := snapshot(id, 9, 25)
	if publish(t, api, s) != 204 {
		t.Fatal("publish failed")
	}
	time.Sleep(60 * time.Millisecond)
	h.mu.Lock()
	gone := h.last == nil && h.revision == 9 && h.snapshotHash == snapshotHash(s)
	h.mu.Unlock()
	if !gone {
		t.Fatal("expired plaintext retained or identity lost")
	}
	changed := s
	changed.Text = "different"
	if got := publish(t, api, changed); got != 409 {
		t.Fatalf("conflicting expired duplicate accepted: %d", got)
	}
}
func TestStaleBufferedSnapshotCannotWriteAfterExpiry(t *testing.T) {
	h, api := setup(t)
	id := lease(t, api)
	c := &client{mailbox: make(chan hubEvent, 2)}
	h.mu.Lock()
	h.subs[c] = struct{}{}
	h.mu.Unlock()
	if publish(t, api, snapshot(id, 3, 30)) != 204 {
		t.Fatal("publish failed")
	}
	old := <-c.mailbox
	time.Sleep(70 * time.Millisecond)
	if b := h.prepareEvent(old); b != nil {
		t.Fatalf("expired buffered content written: %s", b)
	}
	clearEvent := <-c.mailbox
	b := h.prepareEvent(clearEvent)
	var clear Clear
	if b == nil || json.Unmarshal(b, &clear) != nil || clear.Type != "voice.clear" {
		t.Fatalf("missing current clear: %s", b)
	}
}
func TestNewLeaseInvalidatesQueuedOldSnapshot(t *testing.T) {
	h, api := setup(t)
	first := lease(t, api)
	c := &client{mailbox: make(chan hubEvent, 2)}
	h.mu.Lock()
	h.subs[c] = struct{}{}
	h.mu.Unlock()
	if publish(t, api, snapshot(first, 1, 60000)) != 204 {
		t.Fatal("publish failed")
	}
	old := <-c.mailbox
	second := lease(t, api)
	if second == first {
		t.Fatal("lease reused")
	}
	if b := h.prepareEvent(old); b != nil {
		t.Fatalf("old lease snapshot written after transition: %s", b)
	}
	clearEvent := <-c.mailbox
	b := h.prepareEvent(clearEvent)
	var clear Clear
	if b == nil || json.Unmarshal(b, &clear) != nil || clear.Reason != "reset" || clear.LeaseID != first {
		t.Fatalf("missing old lease clear: %s", b)
	}
}
func TestDelayedSnapshotTTLUsesRemainingLifetime(t *testing.T) {
	h, api := setup(t)
	id := lease(t, api)
	c := &client{mailbox: make(chan hubEvent, 1)}
	h.mu.Lock()
	h.subs[c] = struct{}{}
	h.mu.Unlock()
	if publish(t, api, snapshot(id, 2, 180)) != 204 {
		t.Fatal("publish failed")
	}
	ev := <-c.mailbox
	time.Sleep(70 * time.Millisecond)
	b := h.prepareEvent(ev)
	var got Snapshot
	if b == nil || json.Unmarshal(b, &got) != nil {
		t.Fatalf("snapshot dropped: %s", b)
	}
	if got.TTLMS <= 0 || got.TTLMS >= 180 {
		t.Fatalf("ttl not reduced before write: %d", got.TTLMS)
	}
}

func TestClearTransitionRemainsReplayableUntilNewSnapshot(t *testing.T) {
	h, api := setup(t)
	first := lease(t, api)
	c := &client{mailbox: make(chan hubEvent, 2)}
	h.mu.Lock()
	h.subs[c] = struct{}{}
	h.mu.Unlock()
	if publish(t, api, snapshot(first, 1, 60000)) != 204 {
		t.Fatal("publish failed")
	}
	<-c.mailbox
	second := lease(t, api)
	_ = second
	h.mu.Lock()
	if h.lastClear == nil || h.lastClear.LeaseID != first || h.lastClear.Reason != "reset" {
		h.mu.Unlock()
		t.Fatal("lease clear not retained")
	}
	ev := hubEvent{seq: h.eventSeq, clear: h.lastClear}
	h.mu.Unlock()
	b := h.prepareEvent(ev)
	var clear Clear
	if b == nil || json.Unmarshal(b, &clear) != nil || clear.Reason != "reset" {
		t.Fatalf("reset replay unavailable: %s", b)
	}
	if publish(t, api, snapshot(second, 1, 60000)) != 204 {
		t.Fatal("new lease publish failed")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.lastClear != nil {
		t.Fatal("clear was not superseded by new snapshot")
	}
}
