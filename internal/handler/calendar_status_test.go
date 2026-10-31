package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCalendarReasonDistinguishesCacheAgeWithoutHidingPartial(t *testing.T) {
	for _, tc := range []struct {
		complete        bool
		sources, reason string
	}{
		{false, `[{"calendar_id":"google:one","status":"ready","stale":true}]`, "cache_stale"},
		{false, `[{"calendar_id":"google:one","status":"error","stale":true}]`, "incomplete"},
		{false, `[]`, "incomplete"},
		{false, `[{"calendar_id":"google:one","status":"degraded","stale":false}]`, "incomplete"},
		{false, `[{"calendar_id":"google:one","status":"ready","stale":false}]`, "incomplete"},
		{true, `[{"calendar_id":"google:one","status":"ready","stale":false}]`, "complete"},
	} {
		t.Run(tc.reason+tc.sources, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Error("missing auth")
				}
				if r.URL.Query().Get("calendar_id") != "google:one" {
					t.Error("wrong selection")
				}
				start, _ := time.Parse(time.RFC3339, r.URL.Query().Get("start"))
				end, _ := time.Parse(time.RFC3339, r.URL.Query().Get("end"))
				if start.Format("2006-01-02") == "2026-10-25" && end.Sub(start) != 25*time.Hour {
					t.Error("DST range must span 25 hours")
				}
				if !end.After(start) {
					t.Error("invalid range")
				}
				fmt.Fprintf(w, `{"complete":%v,"sources":%s,"items":[]}`, tc.complete, tc.sources)
			}))
			defer upstream.Close()
			h := New(nil, "key", "", "", nil, nil, upstream.Client())
			h.SetCalendarPlatform(upstream.URL, "fixture-token", []string{"google:one"}, "Europe/Belgrade")
			detail, err := h.CalendarStatus(context.Background())
			if err != nil || detail.Reason != tc.reason || detail.Complete != tc.complete {
				t.Fatalf("detail=%+v err=%v", detail, err)
			}
			if len(detail.Sources) > 0 && detail.Sources[0].State == "degraded" && detail.Sources[0].Reason != "source_incomplete" {
				t.Fatal("degraded source mislabeled")
			}
			r := httptest.NewRequest("GET", "/api/calendar?date=2026-10-25", nil)
			r.Header.Set("X-API-Key", "key")
			w := httptest.NewRecorder()
			h.Calendar(w, r)
			if w.Code != 200 || w.Header().Get("X-HomeDash-Calendar-Reason") != tc.reason {
				t.Fatalf("response %d %v", w.Code, w.Header())
			}
			expected := "partial"
			if tc.complete {
				expected = "complete"
			}
			if w.Header().Get(calendarStateHeader) != expected {
				t.Fatal("partial was hidden")
			}
		})
	}
}
