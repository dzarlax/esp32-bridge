package voicepush

import (
	"encoding/json"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type observedFrame struct {
	Type     string `json:"type"`
	LeaseID  string `json:"lease_id"`
	Revision uint64 `json:"revision"`
	Reason   string `json:"reason"`
}

func recordFrames(t *testing.T, out *[]observedFrame) func([]byte, time.Time) error {
	return func(b []byte, _ time.Time) error {
		t.Helper()
		var f observedFrame
		if err := json.Unmarshal(b, &f); err != nil {
			t.Fatal(err)
		}
		*out = append(*out, f)
		return nil
	}
}
func attach(h *Hub) *client {
	c := &client{mailbox: make(chan hubEvent, 1)}
	h.mu.Lock()
	h.subs[c] = struct{}{}
	h.mu.Unlock()
	return c
}
func requireFrame(t *testing.T, f observedFrame, typ, lease string) {
	t.Helper()
	if f.Type != typ || f.LeaseID != lease {
		t.Fatalf("got %#v, want %s lease %s", f, typ, lease)
	}
}

func TestResetSurvivesCoalescing(t *testing.T) {
	h, api := setup(t)
	a := lease(t, api)
	c := attach(h)
	var out []observedFrame
	write := recordFrames(t, &out)
	if publish(t, api, snapshot(a, 5, 60000)) != 204 {
		t.Fatal("publish A")
	}
	if err := h.deliver(c, <-c.mailbox, write); err != nil {
		t.Fatal(err)
	}
	b := lease(t, api)
	if publish(t, api, snapshot(b, 1, 60000)) != 204 {
		t.Fatal("publish B")
	}
	if err := h.deliver(c, <-c.mailbox, write); err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("missing ordered barrier: %#v", out)
	}
	requireFrame(t, out[0], "voice.snapshot", a)
	requireFrame(t, out[1], "voice.clear", a)
	requireFrame(t, out[2], "voice.snapshot", b)
	if out[1].Reason != "reset" || out[1].Revision != 5 {
		t.Fatalf("incorrect reset: %#v", out[1])
	}
}
func TestTransitionDuringInflightClearSkipsIntermediateLease(t *testing.T) {
	h, api := setup(t)
	a := lease(t, api)
	c := attach(h)
	var out []observedFrame
	write := recordFrames(t, &out)
	_ = publish(t, api, snapshot(a, 3, 60000))
	_ = h.deliver(c, <-c.mailbox, write)
	b := lease(t, api)
	_ = publish(t, api, snapshot(b, 1, 60000))
	ev := <-c.mailbox
	var last string
	changing := func(raw []byte, deadline time.Time) error {
		if err := write(raw, deadline); err != nil {
			return err
		}
		if len(out) == 2 {
			last = lease(t, api)
			_ = publish(t, api, snapshot(last, 1, 60000))
		}
		return nil
	}
	if err := h.deliver(c, ev, changing); err != nil {
		t.Fatal(err)
	}
	if err := h.deliver(c, <-c.mailbox, write); err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("intermediate snapshot escaped or clear lost: %#v", out)
	}
	requireFrame(t, out[1], "voice.clear", a)
	requireFrame(t, out[2], "voice.snapshot", last)
}
func TestLeaseChangesWhilePriorSnapshotInflight(t *testing.T) {
	h, api := setup(t)
	a := lease(t, api)
	c := attach(h)
	var out []observedFrame
	write := recordFrames(t, &out)
	_ = publish(t, api, snapshot(a, 4, 60000))
	ev := <-c.mailbox
	var newest string
	inflight := func(raw []byte, d time.Time) error {
		if err := write(raw, d); err != nil {
			return err
		}
		_ = lease(t, api)
		newest = lease(t, api)
		_ = publish(t, api, snapshot(newest, 1, 60000))
		return nil
	}
	if err := h.deliver(c, ev, inflight); err != nil {
		t.Fatal(err)
	}
	if err := h.deliver(c, <-c.mailbox, write); err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("invalid sequence: %#v", out)
	}
	requireFrame(t, out[0], "voice.snapshot", a)
	requireFrame(t, out[1], "voice.clear", a)
	requireFrame(t, out[2], "voice.snapshot", newest)
}
func TestOtherLeaseExpiryClearsLastDeliveredLease(t *testing.T) {
	h, api := setup(t)
	a := lease(t, api)
	c := attach(h)
	var out []observedFrame
	write := recordFrames(t, &out)
	_ = publish(t, api, snapshot(a, 2, 60000))
	_ = h.deliver(c, <-c.mailbox, write)
	b := lease(t, api)
	_ = publish(t, api, snapshot(b, 1, 10))
	time.Sleep(40 * time.Millisecond)
	if err := h.deliver(c, <-c.mailbox, write); err != nil {
		t.Fatal(err)
	}
	if len(out) < 2 {
		t.Fatalf("missing reset for last delivered lease: %#v", out)
	}
	requireFrame(t, out[1], "voice.clear", a)
}

func TestWebSocketLeaseTransitionsRemainOrdered(t *testing.T) {
	_, api := setup(t)
	srv := httptest.NewServer(api.mux)
	defer srv.Close()
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/voice/v1/events", http.Header{"Authorization": []string{"Bearer " + sub}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(4 * time.Second))
	a := lease(t, api)
	_ = publish(t, api, snapshot(a, 3, 60000))
	var f observedFrame
	if err = ws.ReadJSON(&f); err != nil {
		t.Fatal(err)
	}
	requireFrame(t, f, "voice.snapshot", a)
	var newest string
	for i := 0; i < 6; i++ {
		newest = lease(t, api)
		_ = publish(t, api, snapshot(newest, 1, 60000))
	}
	current := a
	cleared := false
	for {
		if err = ws.ReadJSON(&f); err != nil {
			t.Fatal(err)
		}
		switch f.Type {
		case "voice.clear":
			if f.LeaseID == current && f.Revision > 0 {
				cleared = true
			}
		case "voice.snapshot":
			if f.LeaseID != current {
				if !cleared {
					t.Fatalf("lease %s delivered before reset of %s", f.LeaseID, current)
				}
				current = f.LeaseID
				cleared = false
			}
			if current == newest {
				return
			}
		}
	}
}

func TestQueuedClearInflightBeforeNextSnapshot(t *testing.T) {
	h, api := setup(t)
	a := lease(t, api)
	c := attach(h)
	var out []observedFrame
	write := recordFrames(t, &out)
	_ = publish(t, api, snapshot(a, 5, 60000))
	_ = h.deliver(c, <-c.mailbox, write)
	b := lease(t, api)
	clear := <-c.mailbox
	inflight := func(raw []byte, d time.Time) error {
		if err := write(raw, d); err != nil {
			return err
		}
		_ = publish(t, api, snapshot(b, 1, 60000))
		return nil
	}
	if err := h.deliver(c, clear, inflight); err != nil {
		t.Fatal(err)
	}
	if err := h.deliver(c, <-c.mailbox, write); err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("unexpected extra or missing transition: %#v", out)
	}
	requireFrame(t, out[1], "voice.clear", a)
	requireFrame(t, out[2], "voice.snapshot", b)
}
