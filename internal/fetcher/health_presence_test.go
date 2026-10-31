package fetcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthPresenceKeepsRealZeroAndOmitsMissingMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/dashboard" {
			w.Write([]byte(`{"cards":[{"metric":"step_count","value":0},{"metric":"heart_rate","value":null},{"metric":"sleep_total","value":7.2,"prev":null}]}`))
		} else {
			w.WriteHeader(503)
		}
	}))
	defer server.Close()
	data, err := NewHealth(server.URL, "", server.Client(), time.Minute).Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Present map[string]bool `json:"present"`
	}
	if err = json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	if !d.Present["steps"] || !d.Present["sleep"] || d.Present["hr"] || d.Present["hrv"] || d.Present["readiness"] || d.Present["sleep_prev"] {
		t.Fatalf("presence=%v", d.Present)
	}
}

func TestHealthEmptyPresenceIsAnObjectAndMissingScoreIsNotZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/dashboard" {
			w.Write([]byte(`{"cards":[]}`))
		} else {
			w.Write([]byte(`{"points":[{}]}`))
		}
	}))
	defer server.Close()
	data, err := NewHealth(server.URL, "", server.Client(), time.Minute).Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err = json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if string(payload["present"]) != "{}" {
		t.Fatalf("presence must remain explicit: %s", payload["present"])
	}
}
