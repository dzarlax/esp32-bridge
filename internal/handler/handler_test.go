package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
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
