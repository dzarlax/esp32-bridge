package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"esp32-bridge/internal/model"
)

func TestDeclinedEventsHiddenFromCalendarAndPhoto(t *testing.T) {
	now := time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: overlayTransport(func(r *http.Request) (*http.Response, error) {
		var w bytes.Buffer
		fmt.Fprint(&w, `{"complete":true,"sources":[{"calendar_id":"google:personal","status":"ready","stale":false}],"items":[`)
		for i, status := range []string{"declined", "accepted", "needsAction", "unknown", ""} {
			if i > 0 {
				fmt.Fprint(&w, ",")
			}
			fmt.Fprintf(&w, `{"calendar_id":"google:personal","title":"event%d","self_response_status":%q,"start":{"date_time":%q},"end":{"date_time":%q}}`, i, status, now.Add(time.Duration(i+1)*time.Hour).Format(time.RFC3339), now.Add(time.Duration(i+2)*time.Hour).Format(time.RFC3339))
		}
		fmt.Fprint(&w, `,{"calendar_id":"google:personal","title":"absent","start":{"date_time":"2026-09-02T15:00:00Z"},"end":{"date_time":"2026-09-02T16:00:00Z"}}]}`)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(&w), Header: make(http.Header)}, nil
	})}
	h := New(nil, "key", "", "", nil, nil, client)
	h.SetCalendarPlatform("https://calendar.test", "token", []string{"google:personal"}, "UTC")
	req := httptest.NewRequest(http.MethodGet, "/api/calendar?date=2026-09-02", nil)
	req.Header.Set("X-API-Key", "key")

	w := httptest.NewRecorder()
	h.Calendar(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("calendar: %d %s", w.Code, w.Body.String())
	}
	var events []model.CalendarEvent
	if err := json.Unmarshal(w.Body.Bytes(), &events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 || events[0].Summary != "event1" {
		t.Fatalf("calendar events: %#v", events)
	}
	next, err := h.nextPhotoEvent(req, now)
	if err != nil || next == nil || next.title != "event1" {
		t.Fatalf("photo event = %#v, err = %v", next, err)
	}
}
