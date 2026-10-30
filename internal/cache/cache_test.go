package cache

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentFetchAndInvalidation(t *testing.T) {
	c := New()
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, e := c.Fetch("same", time.Hour, func() (json.RawMessage, error) {
				if calls.Add(1) == 1 {
					close(entered)
				}
				<-release
				return json.RawMessage(`1`), nil
			})
			if e != nil {
				t.Error(e)
			}
		}()
	}
	<-entered
	time.Sleep(10 * time.Millisecond)
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("upstream called %d times", calls.Load())
	}
	oldStarted := make(chan struct{})
	oldRelease := make(chan struct{})
	done := make(chan struct{})
	c.Delete("same")
	go func() {
		defer close(done)
		c.Fetch("same", time.Hour, func() (json.RawMessage, error) { close(oldStarted); <-oldRelease; return json.RawMessage(`2`), nil })
	}()
	<-oldStarted
	c.Delete("same")
	c.Fetch("same", time.Hour, func() (json.RawMessage, error) { return json.RawMessage(`3`), nil })
	close(oldRelease)
	<-done
	data, _ := c.Get("same")
	if string(data) != "3" {
		t.Fatalf("stale result restored: %s", data)
	}
}
func TestPanicReleasesWaitersAndFailureCanRetry(t *testing.T) {
	c := New()
	_, _, e := c.Fetch("panic", time.Hour, func() (json.RawMessage, error) { panic("secret") })
	if e == nil {
		t.Fatal("panic accepted")
	}
	_, _, e = c.Fetch("panic", time.Hour, func() (json.RawMessage, error) { return nil, errors.New("failure") })
	if e == nil {
		t.Fatal("failure accepted")
	}
	data, _, e := c.Fetch("panic", time.Hour, func() (json.RawMessage, error) { return json.RawMessage(`true`), nil })
	if e != nil || string(data) != "true" {
		t.Fatal("retry failed")
	}
}

func TestFetchContextCancelsCoalescedWait(t *testing.T) {
	c := New()
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = c.Fetch("key", time.Minute, func() (json.RawMessage, error) { close(started); <-release; return json.RawMessage(`true`), nil })
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := c.FetchContext(ctx, "key", time.Minute, func() (json.RawMessage, error) { t.Error("unexpected fetch"); return nil, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	close(release)
	<-done
}

func TestFetchRecentCoalescesAndKeepsOriginalTime(t *testing.T) {
	c := New()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	fetch := func() (json.RawMessage, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return json.RawMessage(`{"value":1}`), nil
	}
	type result struct {
		at  time.Time
		err error
	}
	results := make(chan result, 2)
	go func() {
		_, at, err := c.FetchRecentContext(context.Background(), "weather", time.Second, 5*time.Minute, fetch)
		results <- result{at, err}
	}()
	<-started
	go func() {
		_, at, err := c.FetchRecentContext(context.Background(), "weather", time.Second, 5*time.Minute, fetch)
		results <- result{at, err}
	}()
	close(release)
	a, b := <-results, <-results
	if a.err != nil || b.err != nil || calls.Load() != 1 || !a.at.Equal(b.at) {
		t.Fatalf("calls=%d results=%+v %+v", calls.Load(), a, b)
	}
	_, at, err := c.FetchRecentContext(context.Background(), "weather", time.Second, 5*time.Minute, fetch)
	if err != nil || !at.Equal(a.at) || calls.Load() != 1 {
		t.Fatalf("cached time changed: %v %v", at, err)
	}
}
