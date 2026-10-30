package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHAStateAndActionObservation(t *testing.T) {
	state := `{"entity_id":"light.a","state":"on"}`
	calls := 0
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing upstream auth")
		}
		if r.Method == http.MethodPost {
			calls++
			w.WriteHeader(200)
			return
		}
		_, _ = w.Write([]byte(state))
	}))
	defer ha.Close()
	h := New(nil, "key", ha.URL, "token", []string{"light.a"}, nil, ha.Client())
	invoke := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("X-API-Key", "key")
		w := httptest.NewRecorder()
		if method == http.MethodPost {
			h.HAAction(w, req)
		} else {
			h.HAState(w, req)
		}
		return w
	}
	w := invoke("POST", "/api/ha/action", `{"entity_id":"light.a","action":"turn_on"}`)
	var got struct {
		OK           bool        `json:"ok"`
		Observation  observation `json:"observation"`
		ActionResult struct {
			State string `json:"state"`
		} `json:"action_result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || !got.OK || got.ActionResult.State != "accepted" || !got.Observation.Available || got.Observation.State != "on" {
		t.Fatalf("response %d %s (%v)", w.Code, w.Body, err)
	}
	state = `{"entity_id":"light.a","state":"unavailable"}`
	w = invoke("GET", "/api/ha/state?entity_id=light.a", "")
	var read struct {
		Observation observation `json:"observation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &read); err != nil || read.Observation.Available {
		t.Fatalf("unavailable: %s", w.Body)
	}
	for _, path := range []string{"/api/ha/state", "/api/ha/state?entity_id=light.other", "/api/ha/state?entity_id=light.a&entity_id=light.a"} {
		if w = invoke("GET", path, ""); w.Code != 400 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	state = `{"entity_id":"light.other","state":"on"}`
	if w = invoke("GET", "/api/ha/state?entity_id=light.a", ""); w.Code != 502 || !bytes.Contains(w.Body.Bytes(), []byte("invalid_response")) {
		t.Fatalf("wrong entity: %d %s", w.Code, w.Body)
	}
	if calls != 1 {
		t.Fatalf("service calls %d", calls)
	}
}

func TestHAActionDoesNotLeakUpstreamError(t *testing.T) {
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("secret upstream detail"))
	}))
	defer ha.Close()
	h := New(nil, "key", ha.URL, "token", []string{"light.a"}, nil, ha.Client())
	req := httptest.NewRequest("POST", "/api/ha/action", strings.NewReader(`{"entity_id":"light.a","action":"turn_on"}`))
	req.Header.Set("X-API-Key", "key")
	w := httptest.NewRecorder()
	h.HAAction(w, req)
	if w.Code != 500 || strings.Contains(w.Body.String(), "secret") || !strings.Contains(w.Body.String(), `"state":"unknown"`) {
		t.Fatalf("response %d %s", w.Code, w.Body)
	}
}

func TestHAStateErrorsAreSafe(t *testing.T) {
	status := 401
	payload := "private detail"
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); _, _ = w.Write([]byte(payload)) }))
	defer ha.Close()
	h := New(nil, "key", ha.URL, "token", []string{"light.a"}, nil, ha.Client())
	for _, tc := range []struct {
		status        int
		payload, code string
	}{{401, "secret", "unauthorized"}, {500, "secret", "upstream_unavailable"}, {200, "invalid", "invalid_response"}} {
		status, payload = tc.status, tc.payload
		req := httptest.NewRequest("GET", "/api/ha/state?entity_id=light.a", nil)
		req.Header.Set("X-API-Key", "key")
		w := httptest.NewRecorder()
		h.HAState(w, req)
		if w.Code != 502 || !strings.Contains(w.Body.String(), tc.code) || strings.Contains(w.Body.String(), "secret") || w.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("response %d %s", w.Code, w.Body)
		}
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHAActionCancelledAfterDispatchIsUnknown(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })}
	h := New(nil, "key", "http://ha.invalid", "token", []string{"light.a"}, nil, client)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("POST", "/api/ha/action", strings.NewReader(`{"entity_id":"light.a","action":"turn_on"}`)).WithContext(ctx)
	req.Header.Set("X-API-Key", "key")
	w := httptest.NewRecorder()
	h.HAAction(w, req)
	if w.Code != 502 || !strings.Contains(w.Body.String(), `"state":"unknown"`) || !strings.Contains(w.Body.String(), `"timeout"`) {
		t.Fatalf("response %d %s", w.Code, w.Body)
	}
}

func TestAcceptedActionWithoutValidObservation(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		body := "invalid"
		if r.Method == http.MethodPost {
			body = "[]"
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	h := New(nil, "key", "http://ha.invalid", "token", []string{"light.a"}, nil, client)
	req := httptest.NewRequest("POST", "/api/ha/action", strings.NewReader(`{"entity_id":"light.a","action":"turn_on"}`))
	req.Header.Set("X-API-Key", "key")
	w := httptest.NewRecorder()
	h.HAAction(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"accepted"`) || strings.Contains(w.Body.String(), `"observation"`) {
		t.Fatalf("response %d %s", w.Code, w.Body)
	}
}
