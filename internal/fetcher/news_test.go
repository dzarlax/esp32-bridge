package fetcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"esp32-bridge/internal/model"
)

func TestNewsFetcherPassesSummaryWithoutOriginalContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/feed" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"title":"Headline","summary":"AI summary","content":"Original article text","category":"Technology","published_at":"2026-09-02T06:00:00Z"}]}`))
	}))
	defer server.Close()

	fetcher := NewNews(server.URL, 5, 24, server.Client(), time.Minute)
	raw, err := fetcher.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	var items []model.NewsItem
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("unmarshal compact news: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].Summary != "AI summary" {
		t.Fatalf("summary = %q, want AI summary", items[0].Summary)
	}
	if got := string(raw); strings.Contains(got, "Original article text") {
		t.Fatalf("compact payload contains original content: %s", got)
	}
}
