package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"esp32-bridge/internal/model"
)

func TestDisabledCalendarContract(t *testing.T) {
	now := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name, sources           string
		ids                     []string
		complete, wantComplete  bool
		wantCalendar, wantPhoto string
		wantCount               int
	}{
		{"mixed", `[{"calendar_id":"a","status":"disabled","stale":false},{"calendar_id":"b","status":"ready","stale":false}]`, []string{"a", "b"}, true, true, "visible", "visible", 1},
		{"disabled only", `[{"calendar_id":"a","status":"disabled","stale":false}]`, []string{"a"}, true, true, "", "", 0},
		{"disabled and stale", `[{"calendar_id":"a","status":"disabled","stale":false},{"calendar_id":"b","status":"ready","stale":true}]`, []string{"a", "b"}, false, false, "visible", "", 1},
		{"disabled and error", `[{"calendar_id":"a","status":"disabled","stale":false},{"calendar_id":"b","status":"error","stale":false,"error_code":"auth"}]`, []string{"a", "b"}, false, false, "visible", "", 1},
		{"missing", `[{"calendar_id":"a","status":"disabled","stale":false}]`, []string{"a", "b"}, true, false, "visible", "", 1},
		{"unknown", `[{"calendar_id":"a","status":"disabled","stale":false},{"calendar_id":"b","status":"mystery","stale":false}]`, []string{"a", "b"}, true, false, "visible", "", 1},
		{"disabled stale", `[{"calendar_id":"a","status":"disabled","stale":true}]`, []string{"a"}, true, false, "", "", 0},
		{"disabled error", `[{"calendar_id":"a","status":"disabled","stale":false,"error_code":"auth"}]`, []string{"a"}, true, false, "", "", 0},
		{"disabled freshness missing", `[{"calendar_id":"a","status":"disabled"}]`, []string{"a"}, true, false, "", "", 0},
		{"reenabled pending", `[{"calendar_id":"a","status":"pending","stale":true}]`, []string{"a"}, false, false, "hidden", "", 1},
		{"reenabled ready", `[{"calendar_id":"a","status":"ready","stale":false}]`, []string{"a"}, true, true, "hidden", "hidden", 1},
		{"legacy", `[{"calendar_id":"a","status":"ready","stale":false}]`, []string{"a"}, true, true, "hidden", "hidden", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"complete":%t,"sources":%s,"items":[{"calendar_id":"a","title":"hidden","start":{"date_time":"2026-11-04T10:00:00Z"},"end":{"date_time":"2026-11-04T11:00:00Z"}},{"calendar_id":"b","title":"visible","start":{"date_time":"2026-11-04T12:00:00Z"},"end":{"date_time":"2026-11-04T13:00:00Z"}}]}`, tc.complete, tc.sources)
			client := &http.Client{Transport: overlayTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
			})}
			h := New(nil, "key", "", "", nil, nil, client)
			h.SetCalendarPlatform("https://calendar.test", "token", tc.ids, "UTC")
			req := httptest.NewRequest("GET", "/api/calendar?date=2026-11-04", nil)
			req.Header.Set("X-API-Key", "key")
			w := httptest.NewRecorder()
			h.Calendar(w, req)
			var events []model.CalendarEvent
			if err := json.Unmarshal(w.Body.Bytes(), &events); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || len(events) != tc.wantCount {
				t.Fatalf("calendar status=%d events=%+v", w.Code, events)
			}
			if len(events) > 0 && events[0].Summary != tc.wantCalendar {
				t.Fatalf("calendar=%+v", events)
			}
			if (w.Header().Get(calendarStateHeader) == "complete") != tc.wantComplete {
				t.Fatalf("state=%s", w.Header().Get(calendarStateHeader))
			}
			details, err := h.CalendarStatus(context.Background())
			if err != nil || details.Complete != tc.wantComplete {
				t.Fatalf("details=%+v err=%v", details, err)
			}
			if strings.Contains(tc.sources, `"status":"disabled"`) && details.Sources[0].Reason != "disabled" {
				t.Fatalf("disabled reason: %+v", details.Sources[0])
			}
			event, err := h.nextPhotoEvent(req, now)
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			if event != nil {
				got = event.title
			}
			if got != tc.wantPhoto {
				t.Fatalf("photo=%q want %q", got, tc.wantPhoto)
			}
		})
	}
}
