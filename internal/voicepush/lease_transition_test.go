package voicepush

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLeaseInFlightBeforeTargetChangeIsInvalidated(t *testing.T) {
	h := NewPaired(strings.Repeat("p", 32), "a", func(string) (PairIdentity, bool) { return PairIdentity{}, false })
	defer h.Close()
	h.mu.Lock()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest("POST", "/api/voice/v1/lease", nil)
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("p", 32))
		w := httptest.NewRecorder()
		h.handleLease(w, req)
		done <- w
	}()
	deadline := time.Now().Add(time.Second)
	for h.routing.TryLock() {
		h.routing.Unlock()
		if time.Now().After(deadline) {
			h.mu.Unlock()
			t.Fatal("lease did not enter routing barrier")
		}
		runtime.Gosched()
	}
	switched := make(chan struct{})
	go func() { h.SetTarget("b"); close(switched) }()
	h.mu.Unlock()
	response := <-done
	<-switched
	var lease map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &lease); err != nil || lease["lease_id"] == "" {
		t.Fatal("lease response")
	}
	h.mu.Lock()
	current := h.lease
	h.mu.Unlock()
	if current != "" {
		t.Fatal("pre-transition lease survived target change")
	}
}

type heldBody struct {
	started chan struct{}
	release chan struct{}
	raw     *bytes.Reader
}

func (b *heldBody) Read(p []byte) (int, error) {
	select {
	case <-b.started:
	default:
		close(b.started)
	}
	<-b.release
	return b.raw.Read(p)
}
func (*heldBody) Close() error { return nil }

func TestInFlightPublishBeforeTargetChangeIsRejected(t *testing.T) {
	h := NewPaired(strings.Repeat("p", 32), "a", func(string) (PairIdentity, bool) { return PairIdentity{}, false })
	defer h.Close()
	h.mu.Lock()
	h.lease = "old"
	h.mu.Unlock()
	body := &heldBody{started: make(chan struct{}), release: make(chan struct{}), raw: bytes.NewReader([]byte(`{"v":1,"type":"voice.snapshot","lease_id":"old","revision":1,"session_id":"s","turn_id":"t","phase":"answer","backend_active":true,"playback":"pending","text":"private","text_truncated":false,"interrupted":false,"active":true,"ttl_ms":1000}`))}
	req := httptest.NewRequest("POST", "/api/voice/v1/publish", io.NopCloser(strings.NewReader("")))
	req.Body = body
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("p", 32))
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { w := httptest.NewRecorder(); h.handlePublish(w, req); result <- w }()
	<-body.started
	h.SetTarget("b")
	close(body.release)
	w := <-result
	if w.Code != http.StatusConflict {
		t.Fatalf("pre-transition publish status %d", w.Code)
	}
	h.mu.Lock()
	last := h.last
	h.mu.Unlock()
	if last != nil {
		t.Fatal("old snapshot survived")
	}
}
