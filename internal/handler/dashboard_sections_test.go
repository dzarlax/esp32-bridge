package handler

import (
	"context"
	"encoding/json"
	"errors"
	"esp32-bridge/internal/cache"
	"esp32-bridge/internal/fetcher"
	"net/http/httptest"
	"testing"
	"time"
)

type sectionFixture struct{ fail bool }

func (f *sectionFixture) Name() string       { return "tasks" }
func (f *sectionFixture) TTL() time.Duration { return time.Minute }
func (f *sectionFixture) Fetch(context.Context) (json.RawMessage, error) {
	if f.fail {
		return nil, errors.New("fixture failure")
	}
	return json.RawMessage(`[]`), nil
}
func TestDashboardMetadataKeepsCachedAgeAndSeparatesEmptyFailureDisabled(t *testing.T) {
	f := &sectionFixture{}
	orch := fetcher.NewOrchestrator([]fetcher.Fetcher{f}, cache.New(), time.Second)
	h := New(orch, "key", "", "", nil, nil, nil)
	call := func() map[string]json.RawMessage {
		r := httptest.NewRequest("GET", "/api/dashboard", nil)
		r.Header.Set("X-API-Key", "key")
		w := httptest.NewRecorder()
		h.Dashboard(w, r)
		var d map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	first := call()
	if string(first["tasks"]) != "[]" {
		t.Fatalf("not empty: %s", first["tasks"])
	}
	var meta map[string]struct {
		State string `json:"state"`
		Age   *int   `json:"age_seconds"`
	}
	json.Unmarshal(first["sections"], &meta)
	if meta["tasks"].State != "ready" || meta["tasks"].Age == nil || meta["health"].State != "disabled" {
		t.Fatalf("meta=%v", meta)
	}
	time.Sleep(1100 * time.Millisecond)
	cached := call()
	json.Unmarshal(cached["sections"], &meta)
	if meta["tasks"].Age == nil || *meta["tasks"].Age < 1 {
		t.Fatal("cache hit reset acquisition age")
	}
	f.fail = true
	orch.Invalidate("tasks")
	failed := call()
	json.Unmarshal(failed["sections"], &meta)
	if string(failed["tasks"]) != "null" || meta["tasks"].State != "error" || meta["tasks"].Age != nil {
		t.Fatalf("failure=%s", failed["sections"])
	}
}
