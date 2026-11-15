package voicepush

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestPairedIsolationSwitchAndRevoke(t *testing.T) {
	var mu sync.RWMutex
	tokens := map[string]PairIdentity{"a": {"A", "generation-a"}, "b": {"B", "generation-b"}}
	verify := func(token string) (PairIdentity, bool) {
		mu.RLock()
		defer mu.RUnlock()
		id, ok := tokens[token]
		return id, ok
	}
	h := NewPaired("publisher", "A", verify)
	defer h.Close()
	req := httptest.NewRequest(http.MethodGet, "/api/voice/v1/events", nil)
	req.Header.Set("Authorization", "Bearer b")
	w := httptest.NewRecorder()
	h.handleEvents(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("other device: %d", w.Code)
	}
	c := &client{token: "a", identity: PairIdentity{"A", "generation-a"}, mailbox: make(chan hubEvent, 1)}
	h.mu.Lock()
	h.subs[c] = struct{}{}
	h.lease = "lease"
	s := Snapshot{LeaseID: "lease", Revision: 1, Text: "private", TTLMS: 10000}
	h.last = &s
	h.active = true
	h.revision = 1
	h.snapshotHash = snapshotHash(s)
	h.accepted = time.Now()
	h.broadcastSnapshotLocked(s, time.Now().Add(time.Second))
	ev := <-c.mailbox
	h.mu.Unlock()
	h.SetTarget("B")
	if err := h.deliver(c, ev, func([]byte, time.Time) error { t.Fatal("old event leaked"); return nil }); err == nil {
		t.Fatal("old subscriber accepted")
	}
	h.mu.Lock()
	if h.last != nil || h.lastClear != nil || h.lease != "" || len(h.subs) != 0 {
		t.Fatal("retained old target state")
	}
	h.mu.Unlock()
	c2 := &client{token: "b", identity: PairIdentity{"B", "generation-b"}, mailbox: make(chan hubEvent, 1)}
	h.mu.Lock()
	h.subs[c2] = struct{}{}
	h.mu.Unlock()
	mu.Lock()
	tokens["b"] = PairIdentity{"B", "generation-b2"}
	mu.Unlock()
	h.Revalidate()
	h.mu.Lock()
	_, exists := h.subs[c2]
	h.mu.Unlock()
	if exists {
		t.Fatal("rotated credential remains connected")
	}
}

func TestPairedConcurrentRouting(t *testing.T) {
	var mu sync.RWMutex
	valid := true
	verify := func(string) (PairIdentity, bool) {
		mu.RLock()
		defer mu.RUnlock()
		return PairIdentity{"A", "generation-a"}, valid
	}
	h := NewPaired("publisher", "A", verify)
	defer h.Close()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				switch i {
				case 0:
					h.SetTarget("B")
					h.SetTarget("A")
				case 1:
					h.Revalidate()
				case 2:
					mu.Lock()
					valid = !valid
					mu.Unlock()
				case 3:
					h.mu.Lock()
					h.broadcastClearLocked(Clear{V: 1})
					h.mu.Unlock()
				}
			}
		}(i)
	}
	wg.Wait()
}

// The routing barrier must finish an already-started write before a switch or
// revocation returns, and reject any later delivery for the old subscriber.
func TestPairedRoutingWaitsForInflightWrite(t *testing.T) {
	for _, action := range []string{"switch", "revoke"} {
		t.Run(action, func(t *testing.T) {
			var mu sync.RWMutex
			valid := true
			verify := func(string) (PairIdentity, bool) {
				mu.RLock()
				defer mu.RUnlock()
				return PairIdentity{"A", "generation-a"}, valid
			}
			h := NewPaired("publisher", "A", verify)
			defer h.Close()
			c := &client{token: "token", identity: PairIdentity{"A", "generation-a"}, mailbox: make(chan hubEvent, 1)}
			h.mu.Lock()
			h.subs[c] = struct{}{}
			h.lease = "lease"
			s := Snapshot{LeaseID: "lease", Revision: 1, Text: "private", TTLMS: 10000}
			h.last = &s
			h.active = true
			h.revision = 1
			h.snapshotHash = snapshotHash(s)
			h.accepted = time.Now()
			h.broadcastSnapshotLocked(s, time.Now().Add(5*time.Second))
			ev := <-c.mailbox
			h.mu.Unlock()
			started, release, delivered := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				delivered <- h.deliver(c, ev, func(_ []byte, deadline time.Time) error {
					if time.Until(deadline) > 5*time.Second {
						t.Error("write deadline exceeds five seconds")
					}
					close(started)
					<-release
					return nil
				})
			}()
			<-started
			if action == "revoke" {
				mu.Lock()
				valid = false
				mu.Unlock()
			}
			finished := make(chan struct{})
			go func() {
				if action == "switch" {
					h.SetTarget("B")
				} else {
					h.Revalidate()
				}
				close(finished)
			}()
			select {
			case <-finished:
				t.Fatal("routing change returned during write")
			case <-time.After(30 * time.Millisecond):
			}
			close(release)
			if err := <-delivered; err != nil {
				t.Fatal(err)
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("routing change did not finish")
			}
			if err := h.deliver(c, ev, func([]byte, time.Time) error { t.Fatal("delivery after routing change"); return nil }); err == nil {
				t.Fatal("old subscriber accepted")
			}
		})
	}
}

func TestConnectedPairedStatus(t *testing.T) {
	var mu sync.RWMutex
	valid := true
	verify := func(string) (PairIdentity, bool) {
		mu.RLock()
		defer mu.RUnlock()
		return PairIdentity{"A", "generation-a"}, valid
	}
	h := NewPaired("publisher", "A", verify)
	defer h.Close()
	if h.Connected("A") || h.Connected("") {
		t.Fatal("empty hub reported connected")
	}
	c := &client{token: "token", identity: PairIdentity{"A", "generation-a"}, mailbox: make(chan hubEvent, 1)}
	h.mu.Lock()
	h.subs[c] = struct{}{}
	h.mu.Unlock()
	if !h.Connected("A") || h.Connected("B") {
		t.Fatal("wrong paired status")
	}
	mu.Lock()
	valid = false
	mu.Unlock()
	if h.Connected("A") {
		t.Fatal("revoked credential reported connected")
	}
	h.Revalidate()
	mu.Lock()
	valid = true
	mu.Unlock()
	if h.Connected("A") {
		t.Fatal("disconnected subscriber reported connected")
	}
	h.mu.Lock()
	h.subs[c] = struct{}{}
	h.mu.Unlock()
	h.SetTarget("B")
	if h.Connected("A") || h.Connected("B") {
		t.Fatal("old subscriber survived target switch")
	}
	legacy := New("publisher", "subscriber", "A")
	defer legacy.Close()
	legacy.mu.Lock()
	legacy.subs[c] = struct{}{}
	legacy.mu.Unlock()
	if legacy.Connected("A") {
		t.Fatal("legacy subscriber reported paired")
	}
}

func TestRoutingUpdateBlocksDeliveryThroughMutation(t *testing.T) {
	for _, action := range []string{"switch", "revoke"} {
		t.Run(action, func(t *testing.T) {
			var mu sync.RWMutex
			valid := true
			verify := func(string) (PairIdentity, bool) {
				mu.RLock()
				defer mu.RUnlock()
				return PairIdentity{"A", "generation-a"}, valid
			}
			h := NewPaired("publisher", "A", verify)
			defer h.Close()
			c := &client{token: "token", identity: PairIdentity{"A", "generation-a"}, mailbox: make(chan hubEvent, 1)}
			h.mu.Lock()
			h.subs[c] = struct{}{}
			h.lease = "lease"
			s := Snapshot{LeaseID: "lease", Revision: 1, Text: "private", TTLMS: 10000}
			h.last = &s
			h.active = true
			h.revision = 1
			h.snapshotHash = snapshotHash(s)
			h.accepted = time.Now()
			h.broadcastSnapshotLocked(s, time.Now().Add(5*time.Second))
			ev := <-c.mailbox
			h.mu.Unlock()
			mutated, release, completed := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				completed <- h.RoutingUpdate(func() (string, error) {
					if action == "revoke" {
						mu.Lock()
						valid = false
						mu.Unlock()
					}
					close(mutated)
					<-release
					if action == "switch" {
						return "B", nil
					}
					return "A", nil
				})
			}()
			<-mutated
			delivered := make(chan error, 1)
			go func() {
				delivered <- h.deliver(c, ev, func([]byte, time.Time) error { t.Error("old delivery during transaction"); return nil })
			}()
			select {
			case <-delivered:
				t.Fatal("delivery escaped transaction")
			case <-time.After(30 * time.Millisecond):
			}
			close(release)
			if err := <-completed; err != nil {
				t.Fatal(err)
			}
			if err := <-delivered; err == nil {
				t.Fatal("old subscriber remained authorized")
			}
		})
	}
}

func TestMigratingModeIsOneWayAndAtomic(t *testing.T) {
	verify := func(token string) (PairIdentity, bool) { return PairIdentity{"A", "gen"}, token == "paired" }
	h := NewMigrating("pub", "legacy", "A", verify, "", false)
	defer h.Close()
	legacy := &client{mailbox: make(chan hubEvent, 1)}
	h.mu.Lock()
	h.subs[legacy] = struct{}{}
	h.lease = "old"
	h.last = &Snapshot{LeaseID: "old", Revision: 1, TTLMS: 1000}
	h.active = true
	h.mu.Unlock()
	if err := h.RoutingUpdateMode(func() (bool, string, error) { return false, "", nil }); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	_, registered := h.subs[legacy]
	retained := h.last != nil
	h.mu.Unlock()
	if h.paired || !registered || !retained {
		t.Fatal("pending or failed mutation changed legacy")
	}
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		finished <- h.RoutingUpdateMode(func() (bool, string, error) { close(started); <-release; return true, "A", nil })
	}()
	<-started
	delivered := make(chan error, 1)
	go func() {
		delivered <- h.deliver(legacy, hubEvent{}, func([]byte, time.Time) error { t.Error("legacy write after migration"); return nil })
	}()
	select {
	case <-delivered:
		t.Fatal("delivery not blocked")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := <-delivered; err == nil {
		t.Fatal("legacy still authorized")
	}
	h.mu.Lock()
	_, registered = h.subs[legacy]
	retained = h.last != nil || h.lease != ""
	h.mu.Unlock()
	if registered || retained {
		t.Fatal("legacy state retained")
	}
	req := httptest.NewRequest(http.MethodGet, "/api/voice/v1/events", nil)
	req.Header.Set("Authorization", "Bearer legacy")
	w := httptest.NewRecorder()
	h.handleEvents(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("legacy token status %d", w.Code)
	}
	if err := h.RoutingUpdateMode(func() (bool, string, error) { return true, "", nil }); err != nil {
		t.Fatal(err)
	}
	if err := h.RoutingUpdateMode(func() (bool, string, error) { return false, "", nil }); err != nil {
		t.Fatal(err)
	}
	if !h.paired || h.target != "" {
		t.Fatal("reverted to legacy")
	}
	restarted := NewMigrating("pub", "legacy", "A", verify, "", true)
	defer restarted.Close()
	w = httptest.NewRecorder()
	restarted.handleEvents(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatal("restart restored legacy")
	}
}

func TestMigratingUnknownModeFailsClosed(t *testing.T) {
	h := NewMigrating("pub", "legacy", "A", func(string) (PairIdentity, bool) { return PairIdentity{}, false }, "", false)
	defer h.Close()
	if err := h.RoutingUpdateMode(func() (bool, string, error) { return false, "", errors.New("store unavailable") }); err == nil {
		t.Fatal("expected error")
	}
	if !h.paired || h.target != "" {
		t.Fatal("unknown mode did not fail closed")
	}
}
