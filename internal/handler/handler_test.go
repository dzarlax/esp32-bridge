package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"esp32-bridge/internal/model"
)

func TestFetchCalendarListFiltersIgnoredCalendars(t *testing.T) {
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/calendars" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{"entity_id":"calendar.personal_gmail","name":"Personal Gmail"},
			{"entity_id":"calendar.work_microsoft","name":"Work Microsoft"},
			{"entity_id":"calendar.family","name":"Family Outlook"},
			{"entity_id":"calendar.worc_calendar_kalendar","name":"Календарь"},
			{"entity_id":"calendar.rabochii","name":"Рабочий"},
			{"entity_id":"calendar.workday_sensor","name":"Workday Sensor"}
		]`))
	}))
	defer ha.Close()

	h := New(nil, "", ha.URL, "token", nil, nil, ha.Client())
	req := httptest.NewRequest(http.MethodGet, "/api/calendar", nil)

	got, err := h.fetchCalendarList(req)
	if err != nil {
		t.Fatalf("fetchCalendarList returned error: %v", err)
	}

	want := []string{"calendar.personal_gmail", "calendar.worc_calendar_kalendar"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fetchCalendarList() = %#v, want %#v", got, want)
	}
}

func TestHAActionForConfiguredClimate(t *testing.T) {
	var gotPath string
	var gotBody map[string]interface{}
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ha.Close()

	h := New(nil, "key", ha.URL, "token", nil, []string{"climate.gostinaia"}, ha.Client())
	body := bytes.NewBufferString(`{"entity_id":"climate.gostinaia","action":"set_hvac_mode","hvac_mode":"dry"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/ha/action", body)
	req.Header.Set("X-API-Key", "key")
	res := httptest.NewRecorder()

	h.HAAction(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	if gotPath != "/api/services/climate/set_hvac_mode" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody["entity_id"] != "climate.gostinaia" || gotBody["hvac_mode"] != "dry" {
		t.Fatalf("body = %#v", gotBody)
	}
}

func TestHAActionRejectsUnconfiguredOrUnsupportedClimateCommands(t *testing.T) {
	h := New(nil, "key", "http://example.invalid", "token", nil, []string{"climate.gostinaia"}, http.DefaultClient)
	for _, body := range []string{
		`{"entity_id":"climate.other","action":"turn_on"}`,
		`{"entity_id":"climate.gostinaia","action":"set_hvac_mode","hvac_mode":"heat_cool"}`,
		`{"entity_id":"climate.gostinaia","action":"set_temperature","temperature":31}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/ha/action", bytes.NewBufferString(body))
		req.Header.Set("X-API-Key", "key")
		res := httptest.NewRecorder()
		h.HAAction(res, req)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400", body, res.Code)
		}
	}
}

func TestCalendarUsesCalendarPlatformWithStableIndices(t *testing.T) {
	calendarAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/native/v1/cached-events" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer calendar-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.URL.Query()["calendar_id"]; !reflect.DeepEqual(got, []string{"google:personal", "apple:family"}) {
			t.Fatalf("calendar_id = %#v", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"complete":true,"items":[
			{"calendar_id":"apple:family","title":"Late","start":{"date_time":"2026-09-02T09:30:00+02:00"},"end":{"date_time":"2026-09-02T10:00:00+02:00"}},
			{"calendar_id":"google:personal","title":"All day","start":{"date":"2026-09-02"},"end":{"date":"2026-09-03"}},
			{"calendar_id":"google:personal","title":"Overnight","start":{"date_time":"2026-09-01T22:00:00+02:00"},"end":{"date_time":"2026-09-02T02:30:00+02:00"}},
			{"calendar_id":"google:personal","title":"Cancelled","status":"cancelled","start":{"date_time":"2026-09-02T12:00:00+02:00"},"end":{"date_time":"2026-09-02T13:00:00+02:00"}}
		]}`))
	}))
	defer calendarAPI.Close()

	h := New(nil, "bridge-key", "", "", nil, nil, calendarAPI.Client())
	h.SetCalendarPlatform(calendarAPI.URL, "calendar-token", []string{"google:personal", "apple:family"}, "Europe/Belgrade")
	req := httptest.NewRequest(http.MethodGet, "/api/calendar?date=2026-09-02", nil)
	req.Header.Set("X-API-Key", "bridge-key")
	res := httptest.NewRecorder()

	h.Calendar(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
	var got []model.CalendarEvent
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []model.CalendarEvent{
		{Summary: "All day", AllDay: true, CalIdx: 0},
		{Summary: "Overnight", StartHour: 0, StartMin: 0, EndHour: 2, EndMin: 30, CalIdx: 0},
		{Summary: "Late", StartHour: 9, StartMin: 30, EndHour: 10, EndMin: 0, CalIdx: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calendar = %#v, want %#v", got, want)
	}
}

func TestMapCalendarPlatformEventFiltersInvalidAndNonOverlappingEvents(t *testing.T) {
	location, err := time.LoadLocation("Europe/Belgrade")
	if err != nil {
		t.Fatal(err)
	}
	dayStart := time.Date(2026, 9, 2, 0, 0, 0, 0, location)
	dayEnd := dayStart.AddDate(0, 0, 1)
	for _, item := range []calendarPlatformEvent{
		{Title: "bad", Start: calendarPlatformEventTime{Date: "2026-09-02"}, End: calendarPlatformEventTime{Date: "2026-09-02"}},
		{Title: "outside", Start: calendarPlatformEventTime{DateTime: "2026-09-03T10:00:00+02:00"}, End: calendarPlatformEventTime{DateTime: "2026-09-03T11:00:00+02:00"}},
	} {
		if _, ok := mapCalendarPlatformEvent(item, 0, dayStart, dayEnd); ok {
			t.Fatalf("event %#v must not map", item)
		}
	}
}
