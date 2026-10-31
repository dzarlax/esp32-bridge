package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type noAuthUpstream struct{ calls atomic.Int32 }

func (s *noAuthUpstream) RoundTrip(*http.Request) (*http.Response, error) {
	s.calls.Add(1)
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`[]`))}, nil
}

func TestRequireAuthUsesOnlyOneHeaderCredential(t *testing.T) {
	h := New(nil, "fixture-native-key", "", "", nil, nil, http.DefaultClient)
	for _, tc := range []struct {
		name, path, key string
		want            int
	}{
		{"header", "/api/dashboard", "fixture-native-key", 200},
		{"missing", "/api/dashboard", "", 401},
		{"query-only", "/api/dashboard?key=fixture-native-key", "", 401},
		{"wrong-header-correct-query", "/api/dashboard?key=fixture-native-key", "wrong", 401},
		{"correct-header-and-query", "/api/dashboard?key=fixture-native-key", "fixture-native-key", 400},
		{"empty-query-value", "/api/dashboard?key=", "fixture-native-key", 400},
		{"encoded-query-name", "/api/dashboard?%6bey=fixture-native-key", "fixture-native-key", 400},
		{"unrelated-query", "/api/calendar?date=2026-10-31", "fixture-native-key", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tc.path, nil)
			if tc.key != "" {
				r.Header.Set("X-API-Key", tc.key)
			}
			w := httptest.NewRecorder()
			ok := h.requireAuth(w, r)
			if ok != (tc.want == 200) || w.Code != tc.want {
				t.Fatalf("accepted=%v HTTP=%d; want %d", ok, w.Code, tc.want)
			}
			if strings.Contains(w.Body.String(), "fixture-native-key") {
				t.Fatal("credential reflected in response")
			}
		})
	}
}

func TestNativeRoutesRejectURLCredentialsBeforeAnyUpstream(t *testing.T) {
	upstream := &noAuthUpstream{}
	h := New(nil, "fixture-native-key", "http://upstream.invalid", "fixture-ha", []string{"light.fixture"}, nil, &http.Client{Transport: upstream})
	routes := []struct {
		method, path string
		serve        http.HandlerFunc
	}{
		{"GET", "/api/dashboard", h.Dashboard},
		{"POST", "/api/ha/action", h.HAAction},
		{"GET", "/api/ha/state?entity_id=light.fixture", h.HAState},
		{"GET", "/api/calendar?date=2026-10-31", h.Calendar},
		{"GET", "/api/ota/check?v=1.0.39", h.OTACheck},
		{"GET", "/api/ota/firmware", h.OTAFirmware},
		{"GET", "/api/photo-frame/v1/manifest", h.PhotoManifest},
		{"GET", "/api/photo-frame/v1/frames/fixture", h.PhotoFrame},
		{"GET", "/api/photo-frame/v1/context", h.PhotoContext},
		{"GET", "/api/photo-frame/v1/overlays/fixture", h.PhotoOverlay},
		{"GET", "/api/photo-frame/v2/context", h.PhotoContextV2},
		{"GET", "/api/photo-frame/v2/overlays/fixture", h.PhotoOverlayV2},
		{"GET", "/api/photo-frame/v3/context", h.PhotoContextV3},
		{"GET", "/api/photo-frame/v3/overlays/fixture", h.PhotoOverlayV3},
	}
	for _, route := range routes {
		for _, tc := range []struct {
			name, key string
			want      int
		}{{"query-only", "", 401}, {"wrong-header", "wrong", 401}, {"header-plus-query", "fixture-native-key", 400}} {
			t.Run(route.path+"/"+tc.name, func(t *testing.T) {
				sep := "?"
				if strings.Contains(route.path, "?") {
					sep = "&"
				}
				r := httptest.NewRequest(route.method, route.path+sep+"key=fixture-native-key", strings.NewReader(`{"entity_id":"light.fixture","action":"turn_on"}`))
				if tc.key != "" {
					r.Header.Set("X-API-Key", tc.key)
				}
				r.Header.Set("X-Device-ID", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
				w := httptest.NewRecorder()
				route.serve(w, r)
				if w.Code != tc.want {
					t.Fatalf("HTTP %d; want %d", w.Code, tc.want)
				}
				if strings.Contains(w.Body.String(), "fixture-native-key") {
					t.Fatal("credential reflected")
				}
			})
		}
	}
	if upstream.calls.Load() != 0 {
		t.Fatalf("unauthorized requests reached upstream %d times", upstream.calls.Load())
	}
	w := httptest.NewRecorder()
	h.Health(w, httptest.NewRequest("GET", "/health", nil))
	if w.Code != 200 {
		t.Fatal("public health changed")
	}
}
