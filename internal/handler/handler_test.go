package handler

import (
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
			{"entity_id":"calendar.workday_sensor","name":"Workday Sensor"}
		]`))
	}))
	defer ha.Close()

	h := New(nil, "", ha.URL, "token", ha.Client())
	req := httptest.NewRequest(http.MethodGet, "/api/calendar", nil)

	got, err := h.fetchCalendarList(req)
	if err != nil {
		t.Fatalf("fetchCalendarList returned error: %v", err)
	}

	want := []string{"calendar.personal_gmail"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fetchCalendarList() = %#v, want %#v", got, want)
	}
}
