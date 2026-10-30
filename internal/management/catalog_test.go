package management

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"esp32-bridge/internal/settings"
)

func TestCalendarCatalogUsesPersistedCredentialAndCaches(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/native/v1/calendars" || r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("unexpected upstream request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"calendars":[{"id":"google:abc","name":"Home","can_read":true,"read_only":true}]}`))
	}))
	defer upstream.Close()
	d := managementDefaults()
	c := d.Services["calendar"]
	c.BaseURL = upstream.URL
	d.Services["calendar"] = c
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := settings.New(dir, d, map[string]string{"calendar_platform_token": "test-secret"}, "https://panel.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 32)
	_, err = s.Sync(settings.Device{DeviceID: id, BridgeID: s.BridgeID(), BootID: "boot", Firmware: "1"})
	if err != nil {
		t.Fatal(err)
	}
	api := &API{Store: s}
	mux := http.NewServeMux()
	api.Register(mux, func(h http.Handler) http.Handler { return h })
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/admin/v1/devices/"+id+"/calendars", nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"read_only":true`) || !strings.Contains(w.Body.String(), `"provider":"google"`) || strings.Contains(w.Body.String(), "test-secret") {
			t.Fatalf("catalog %d %s", w.Code, w.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestCalendarCatalogRejectsDuplicateRowsAndPreservesOversizeCode(t *testing.T) {
	mode := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == 1 {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"error":"catalog_too_large"}`))
			return
		}
		_, _ = w.Write([]byte(`{"calendars":[{"id":"same","name":"A","can_read":true,"read_only":true},{"id":"same","name":"B","can_read":true,"read_only":true}]}`))
	}))
	defer upstream.Close()
	d := managementDefaults()
	c := d.Services["calendar"]
	c.BaseURL = upstream.URL
	d.Services["calendar"] = c
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := settings.New(dir, d, map[string]string{"calendar_platform_token": "secret"}, "https://panel.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 32)
	if _, err = s.Sync(settings.Device{DeviceID: id, BridgeID: s.BridgeID(), BootID: "boot", Firmware: "1"}); err != nil {
		t.Fatal(err)
	}
	api := &API{Store: s}
	for _, expected := range []string{"catalog_invalid_response", "catalog_too_large"} {
		w := httptest.NewRecorder()
		api.catalog(w, httptest.NewRequest("GET", "/", nil), id, "calendars")
		if !strings.Contains(w.Body.String(), expected) {
			t.Fatalf("response %d %s", w.Code, w.Body.String())
		}
		mode++
	}
}

func catalogTestStore(t *testing.T, albumURL, calendarURL string) (*settings.Store, string) {
	t.Helper()
	d := managementDefaults()
	d.Photos.BaseURL = albumURL
	calendar := d.Services["calendar"]
	calendar.BaseURL = calendarURL
	d.Services["calendar"] = calendar
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := settings.New(dir, d, map[string]string{"immich_api_key": "album-secret", "calendar_platform_token": "calendar-secret"}, "https://panel.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 32)
	if _, err = s.Sync(settings.Device{DeviceID: id, BridgeID: s.BridgeID(), BootID: "boot", Firmware: "1"}); err != nil {
		t.Fatal(err)
	}
	return s, id
}
func catalogRequest(api *API, id, kind string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	api.catalog(w, httptest.NewRequest("GET", "/", nil), id, kind)
	return w
}

func TestAlbumAndCalendarCatalogIndependentCacheAndInvalidation(t *testing.T) {
	var albums, calendars atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/albums":
			albums.Add(1)
			if r.Header.Get("X-API-Key") != "album-secret" {
				t.Errorf("wrong album credential")
			}
			_, _ = w.Write([]byte(`[{"id":"album-1","albumName":"Family","assetCount":42}]`))
		case "/api/native/v1/calendars":
			calendars.Add(1)
			if r.Header.Get("Authorization") != "Bearer calendar-secret" {
				t.Errorf("wrong calendar credential")
			}
			_, _ = w.Write([]byte(`{"calendars":[{"id":"calendar-1","name":"Calendar","can_read":true,"read_only":true}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	s, id := catalogTestStore(t, upstream.URL, upstream.URL)
	api := &API{Store: s, catalogClient: upstream.Client()}
	a := catalogRequest(api, id, "albums")
	if a.Code != 200 || !strings.Contains(a.Body.String(), `"id":"album-1"`) || !strings.Contains(a.Body.String(), `"name":"Family"`) || !strings.Contains(a.Body.String(), `"asset_count":42`) || strings.Contains(a.Body.String(), "secret") {
		t.Fatalf("album response %d %s", a.Code, a.Body.String())
	}
	c := catalogRequest(api, id, "calendars")
	if c.Code != 200 {
		t.Fatalf("calendar response %d", c.Code)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if catalogRequest(api, id, "albums").Code != 200 {
				t.Error("album cache failed")
			}
		}()
		go func() {
			defer wg.Done()
			if catalogRequest(api, id, "calendars").Code != 200 {
				t.Error("calendar cache failed")
			}
		}()
	}
	wg.Wait()
	if albums.Load() != 1 || calendars.Load() != 1 {
		t.Fatalf("cache calls albums=%d calendars=%d", albums.Load(), calendars.Load())
	}
	view := s.ConnectionsView()
	newSecret := "new-calendar-secret"
	if err := s.UpdateConnections(view.Revision, settings.ConnectionPatch{Connections: view.Connections, Secrets: map[string]*string{"calendar_platform_token": &newSecret}}); err != nil {
		t.Fatal(err)
	}
	// The old cached response must not be returned for a changed credential.
	// Return a forbidden response with the new value to make a stale hit observable.
	// This test uses a second transport below to inspect that credential safely.
	api.catalogClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer new-calendar-secret" {
			t.Error("changed credential missing")
		}
		return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	c = catalogRequest(api, id, "calendars")
	if c.Code != 502 || !strings.Contains(c.Body.String(), "catalog_forbidden") {
		t.Fatalf("stale cache response %d %s", c.Code, c.Body.String())
	}
	// The unrelated album entry remains cached.
	if catalogRequest(api, id, "albums").Code != 200 || albums.Load() != 1 {
		t.Fatal("calendar change evicted album")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type testTimeout struct{}

func (testTimeout) Error() string   { return "timeout" }
func (testTimeout) Timeout() bool   { return true }
func (testTimeout) Temporary() bool { return true }

type timeoutBody struct{}

func (timeoutBody) Read([]byte) (int, error) { return 0, testTimeout{} }
func (timeoutBody) Close() error             { return nil }

func TestCatalogRedirectAndTimeouts(t *testing.T) {
	var redirected atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/albums" {
			http.Redirect(w, r, "/other", http.StatusFound)
			return
		}
		redirected.Add(1)
	}))
	defer upstream.Close()
	s, id := catalogTestStore(t, upstream.URL, upstream.URL)
	api := &API{Store: s, catalogClient: upstream.Client()}
	w := catalogRequest(api, id, "albums")
	if w.Code != 502 || redirected.Load() != 0 || strings.Contains(w.Body.String(), "album-secret") {
		t.Fatalf("redirect status=%d", w.Code)
	}
	for _, bodyTimeout := range []bool{false, true} {
		api.catalogClient = &http.Client{Timeout: time.Millisecond, Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			if !bodyTimeout {
				return nil, testTimeout{}
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: timeoutBody{}}, nil
		})}
		w = catalogRequest(api, id, "albums")
		if w.Code != 504 || !strings.Contains(w.Body.String(), "catalog_timeout") {
			t.Fatalf("timeout body=%v status=%d %s", bodyTimeout, w.Code, w.Body.String())
		}
	}
	api.catalogClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Header: make(http.Header), Body: timeoutBody{}}, nil
	})}
	w = catalogRequest(api, id, "albums")
	if w.Code != 504 || !strings.Contains(w.Body.String(), "catalog_timeout") {
		t.Fatalf("error-body timeout status=%d %s", w.Code, w.Body.String())
	}

}

func TestCalendarProviderPrefixes(t *testing.T) {
	for id, want := range map[string]string{"google:1": "google", "microsoft:2": "microsoft", "apple:3": "apple", "other:4": "", "google": "", "Google:1": ""} {
		if got := calendarProvider(id); got != want {
			t.Errorf("%s: got %q want %q", id, got, want)
		}
	}
}

func homeCatalogTestStore(t *testing.T, base string) (*settings.Store, string) {
	t.Helper()
	d := managementDefaults()
	home := d.Services["home"]
	home.BaseURL = base
	d.Services["home"] = home
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := settings.New(dir, d, map[string]string{"ha_token": "ha-test-secret"}, "https://panel.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("b", 32)
	if _, err = s.Sync(settings.Device{DeviceID: id, BridgeID: s.BridgeID(), BootID: "boot", Firmware: "1"}); err != nil {
		t.Fatal(err)
	}
	return s, id
}

func TestEntityCatalogFiltersAndCachesHomeAssistantStates(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/states" || r.Header.Get("Authorization") != "Bearer ha-test-secret" {
			t.Errorf("unexpected Home Assistant request")
		}
		_, _ = w.Write([]byte(`[
		 {"entity_id":"light.kitchen","state":"on","attributes":{"friendly_name":"Kitchen"}},
		 {"entity_id":"climate.den","state":"unavailable","attributes":{"friendly_name":"Den"}},
		 {"entity_id":"sensor.temperature","state":"unknown","attributes":{}},
		 {"entity_id":"switch.plug","state":"on","attributes":{"friendly_name":"Plug"}}
		]`))
	}))
	defer upstream.Close()
	s, id := homeCatalogTestStore(t, upstream.URL)
	api := &API{Store: s}
	for i := 0; i < 2; i++ {
		w := catalogRequest(api, id, "entities")
		if w.Code != 200 || strings.Contains(w.Body.String(), "switch.plug") || strings.Contains(w.Body.String(), "ha-test-secret") || !strings.Contains(w.Body.String(), `"name":"sensor.temperature"`) || !strings.Contains(w.Body.String(), `"available":false`) {
			t.Fatalf("entity catalog %d %s", w.Code, w.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("cache calls=%d", calls.Load())
	}
}

func TestEntityCatalogRejectsMalformedDuplicateAndOversizedCatalogs(t *testing.T) {
	for _, tc := range []struct {
		name string
		body func() string
	}{
		{name: "duplicate", body: func() string {
			return `[{"entity_id":"light.same","state":"on","attributes":{}},{"entity_id":"light.same","state":"off","attributes":{}}]`
		}},
		{name: "malformed id", body: func() string { return `[{"entity_id":"light.Bad ID","state":"on","attributes":{}}]` }},
		{name: "too many", body: func() string {
			var b strings.Builder
			b.WriteByte('[')
			for i := 0; i < 5001; i++ {
				if i > 0 {
					b.WriteByte(',')
				}
				fmt.Fprintf(&b, `{"entity_id":"light.item_%d","state":"on","attributes":{}}`, i)
			}
			b.WriteByte(']')
			return b.String()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, tc.body()) }))
			defer upstream.Close()
			s, id := homeCatalogTestStore(t, upstream.URL)
			w := catalogRequest(&API{Store: s}, id, "entities")
			want := "catalog_invalid_response"
			if tc.name == "too many" {
				want = "catalog_too_large"
			}
			if w.Code != 502 || !strings.Contains(w.Body.String(), want) {
				t.Fatalf("status=%d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestEntityCatalogIgnoresUnsupportedMetadataAndAllowsLongSupportedState(t *testing.T) {
	state := strings.Repeat("x", 129)
	body, err := json.Marshal([]map[string]any{
		{"entity_id": "switch.plug", "state": map[string]any{"unexpected": true}, "attributes": map[string]any{"friendly_name": 42}},
		{"entity_id": "sensor.long_state", "state": state, "attributes": map[string]any{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer upstream.Close()
	s, id := homeCatalogTestStore(t, upstream.URL)
	w := catalogRequest(&API{Store: s}, id, "entities")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"sensor.long_state"`) || !strings.Contains(w.Body.String(), `"available":true`) || strings.Contains(w.Body.String(), "switch.plug") {
		t.Fatalf("entity catalog status=%d %s", w.Code, w.Body.String())
	}
}

func TestEntityCatalogRejectsStaleInFlightResponseAfterConnectionChange(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			_, _ = io.WriteString(w, `[]`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer rotated-secret" {
			t.Errorf("changed credential was not used")
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer upstream.Close()
	s, id := homeCatalogTestStore(t, upstream.URL)
	api := &API{Store: s}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- catalogRequest(api, id, "entities") }()
	<-started
	view := s.ConnectionsView()
	newSecret := "rotated-secret"
	if err := s.UpdateConnections(view.Revision, settings.ConnectionPatch{Connections: view.Connections, Secrets: map[string]*string{"ha_token": &newSecret}}); err != nil {
		t.Fatal(err)
	}
	close(release)
	w := <-done
	if w.Code != 409 || !strings.Contains(w.Body.String(), "catalog_connection_changed") {
		t.Fatalf("stale response=%d %s", w.Code, w.Body.String())
	}
	if got := catalogRequest(api, id, "entities"); got.Code != 502 || !strings.Contains(got.Body.String(), "catalog_forbidden") {
		t.Fatalf("rotated credential not used: %d %s", got.Code, got.Body.String())
	}
}
