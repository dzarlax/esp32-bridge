package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"esp32-bridge/internal/cache"
	"esp32-bridge/internal/fetcher"
	"esp32-bridge/internal/model"
	"esp32-bridge/internal/settings"
	"golang.org/x/image/font/sfnt"
)

type overlayTransport func(*http.Request) (*http.Response, error)

func (f overlayTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestPhotoOverlayContextAndIsolation(t *testing.T) {
	now := time.Now().UTC()
	failed := false
	client := &http.Client{Transport: overlayTransport(func(r *http.Request) (*http.Response, error) {
		if failed {
			return nil, fmt.Errorf("offline")
		}
		body := fmt.Sprintf(`{"complete":true,"sources":[{"calendar_id":"a","status":"ready","stale":false}],"items":[{"id":"1","calendar_id":"a","title":"Встреча","start":{"date_time":%q},"end":{"date_time":%q}}]}`, now.Add(-time.Minute).Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	h := New(fetcher.NewOrchestrator(nil, cache.New(), time.Second), "secret", "", "", nil, nil, client)
	h.SetCalendarPlatform("https://calendar.test", "token", []string{"a"}, "Europe/Belgrade")
	h.SetPhotoOverlaySettings(settings.Screen{Locale: "ru", Timezone: "Europe/Belgrade", ClockFormat: "24h"}, settings.Photos{}, false, true)
	request := func(path, id, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-API-Key", key)
		r.Header.Set("X-Device-ID", id)
		w := httptest.NewRecorder()
		if strings.Contains(path, "/overlays/") {
			h.PhotoOverlay(w, r)
		} else {
			h.PhotoContext(w, r)
		}
		return w
	}
	a := strings.Repeat("a", 32)
	b := strings.Repeat("b", 32)
	if got := request("/api/photo-frame/v1/context", a, "").Code; got != 401 {
		t.Fatal(got)
	}
	w := request("/api/photo-frame/v1/context", a, "secret")
	var c overlayContext
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil || len(c.Blocks) != 1 {
		t.Fatalf("%d %s %v", w.Code, w.Body.String(), err)
	}
	block := c.Blocks[0]
	image := request("/api/photo-frame/v1/overlays/"+block.RenderID, a, "secret")
	sum := sha256.Sum256(image.Body.Bytes())
	if image.Code != 200 || image.Body.Len() != 480*112*2 || hex.EncodeToString(sum[:]) != block.SHA256 || image.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("invalid image")
	}
	if request("/api/photo-frame/v1/overlays/"+block.RenderID, b, "secret").Code != 404 {
		t.Fatal("foreign image accessible")
	}
	w = request("/api/photo-frame/v1/context", a, "secret")
	var c2 overlayContext
	_ = json.Unmarshal(w.Body.Bytes(), &c2)
	if c2.Blocks[0].RenderID != block.RenderID {
		t.Fatal("unstable ID")
	}
	failed = true
	h.overlay.lastCalendar = now.Add(-time.Minute)
	w = request("/api/photo-frame/v1/context", a, "secret")
	_ = json.Unmarshal(w.Body.Bytes(), &c2)
	if len(c2.Blocks) != 1 || c2.Blocks[0].RenderID != block.RenderID {
		t.Fatal("source error discarded fresh event")
	}
	h.overlay.event.ExpiresAt = now.Add(-time.Second).Unix()
	h.overlay.lastCalendar = now
	w = request("/api/photo-frame/v1/context", a, "secret")
	_ = json.Unmarshal(w.Body.Bytes(), &c2)
	if len(c2.Blocks) != 0 {
		t.Fatal("expired event exposed")
	}
}
func TestPhotoEventSelectionAndFailures(t *testing.T) {
	now := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC)
	failed := false
	client := &http.Client{Transport: overlayTransport(func(r *http.Request) (*http.Response, error) {
		if failed {
			return nil, fmt.Errorf("offline")
		}
		start, err := time.Parse(time.RFC3339, r.URL.Query().Get("start"))
		if err != nil || !start.Equal(now) {
			t.Errorf("wrong start %q", r.URL.Query().Get("start"))
		}
		end, err := time.Parse(time.RFC3339, r.URL.Query().Get("end"))
		if err != nil || !end.Equal(now.Add(24*time.Hour)) {
			t.Errorf("wrong end %q", r.URL.Query().Get("end"))
		}
		if got := r.URL.Query()["calendar_id"]; len(got) != 1 || got[0] != "a" {
			t.Errorf("calendar filter %v", got)
		}
		body := fmt.Sprintf(`{"complete":true,"sources":[{"calendar_id":"a","status":"ready","stale":false}],"items":[{"id":"later","calendar_id":"a","title":"Later","start":{"date_time":%q},"end":{"date_time":%q}},{"id":"ongoing","calendar_id":"a","title":"Ongoing","start":{"date_time":%q},"end":{"date_time":%q}},{"id":"all","calendar_id":"a","title":"All day","start":{"date":"2026-10-25"},"end":{"date":"2026-10-26"}},{"id":"foreign","calendar_id":"foreign","title":"Foreign","start":{"date_time":"2026-10-24T20:00:00Z"},"end":{"date_time":"2026-10-25T02:00:00Z"}},{"id":"cancel","calendar_id":"a","status":"cancelled","title":"Cancelled","start":{"date_time":"2026-10-24T19:00:00Z"},"end":{"date_time":"2026-10-25T02:00:00Z"}}]}`, now.Add(time.Hour).Format(time.RFC3339), now.Add(2*time.Hour).Format(time.RFC3339), now.Add(-2*time.Hour).Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	h := New(nil, "", "", "", nil, nil, client)
	h.SetCalendarPlatform("https://calendar.test", "token", []string{"a"}, "Europe/Belgrade")
	r := httptest.NewRequest("GET", "/", nil).WithContext(context.Background())
	event, err := h.nextPhotoEvent(r, now)
	if err != nil || event == nil || event.id != "ongoing" {
		t.Fatalf("%+v %v", event, err)
	}
	failed = true
	if _, err = h.nextPhotoEvent(r, now); err == nil {
		t.Fatal("failure mistaken for empty")
	}
}
func TestOverlayFontCyrillicAndRendering(t *testing.T) {
	var buf sfnt.Buffer
	for _, r := range "ВстречаČćšžđ" {
		id, err := overlayFont.GlyphIndex(&buf, r)
		if err != nil || id == 0 {
			t.Fatalf("missing glyph %c", r)
		}
	}
	for _, locale := range []string{"ru", "en", "sr-Latn"} {
		if len(renderWeather(model.WeatherData{Temp: 18, IsDay: true}, settings.Screen{Locale: locale, City: "Beograd"})) != 236*200*2 {
			t.Fatal(locale)
		}
	}
}

type weatherTestFetcher struct{}

func (weatherTestFetcher) Name() string       { return "weather" }
func (weatherTestFetcher) TTL() time.Duration { return time.Hour }
func (weatherTestFetcher) Fetch(context.Context) (json.RawMessage, error) {
	return nil, fmt.Errorf("offline")
}
func TestPhotoWeatherUsesSourceTimestampAndExpires(t *testing.T) {
	c := cache.New()
	c.Set("weather", json.RawMessage(`{"temp":18,"wc":0,"is_day":true}`), time.Hour)
	_, sourceAt, _ := c.GetMeta("weather")
	h := New(fetcher.NewOrchestrator([]fetcher.Fetcher{weatherTestFetcher{}}, c, time.Second), "secret", "", "", nil, nil, &http.Client{})
	h.SetPhotoOverlaySettings(settings.Screen{Locale: "en", Timezone: "UTC", ClockFormat: "24h"}, settings.Photos{}, true, false)
	req := httptest.NewRequest("GET", "/api/photo-frame/v1/context", nil)
	req.Header.Set("X-API-Key", "secret")
	req.Header.Set("X-Device-ID", strings.Repeat("a", 32))
	w := httptest.NewRecorder()
	h.PhotoContext(w, req)
	var result overlayContext
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Blocks) != 1 {
		t.Fatalf("%s %v", w.Body, err)
	}
	if result.Blocks[0].ExpiresAt != sourceAt.Add(30*time.Minute).Unix() {
		t.Fatalf("expiry=%d source=%v", result.Blocks[0].ExpiresAt, sourceAt)
	}
	h.overlay.weather.ExpiresAt = time.Now().Add(-time.Second).Unix()
	h.overlay.lastWeather = time.Now().Add(-6 * time.Minute)
	c.Delete("weather")
	w = httptest.NewRecorder()
	h.PhotoContext(w, req)
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if len(result.Blocks) != 0 {
		t.Fatal("stale weather shown after source error")
	}
}
func TestPhotoEventExpiresAtStart(t *testing.T) {
	now := time.Now().UTC()
	start := now.Add(2 * time.Minute).Truncate(time.Second)
	client := &http.Client{Transport: overlayTransport(func(r *http.Request) (*http.Response, error) {
		body := fmt.Sprintf(`{"complete":true,"sources":[{"calendar_id":"a","status":"ready","stale":false}],"items":[{"id":"1","calendar_id":"a","title":"Upcoming","start":{"date_time":%q},"end":{"date_time":%q}}]}`, start.Format(time.RFC3339), start.Add(time.Hour).Format(time.RFC3339))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	h := New(nil, "secret", "", "", nil, nil, client)
	h.SetCalendarPlatform("https://calendar.test", "token", []string{"a"}, "UTC")
	h.SetPhotoOverlaySettings(settings.Screen{Locale: "en", Timezone: "UTC", ClockFormat: "24h"}, settings.Photos{}, false, true)
	req := httptest.NewRequest("GET", "/api/photo-frame/v1/context", nil)
	req.Header.Set("X-API-Key", "secret")
	req.Header.Set("X-Device-ID", strings.Repeat("a", 32))
	w := httptest.NewRecorder()
	h.PhotoContext(w, req)
	var result overlayContext
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if len(result.Blocks) != 1 || result.Blocks[0].ExpiresAt != start.Unix() {
		t.Fatalf("%s", w.Body)
	}
}
func TestWeatherConditionsAndArtDiffer(t *testing.T) {
	s := settings.Screen{Locale: "en", City: "Belgrade"}
	seen := map[string]bool{}
	for _, code := range []int{45, 61, 75, 95} {
		desc := weatherDescription(code, true, "en")
		data := renderWeather(model.WeatherData{Temp: 18, IsDay: true, WeatherCode: code}, s)
		sum := sha256.Sum256(data)
		key := hex.EncodeToString(sum[:])
		if seen[key] {
			t.Fatalf("identical artwork for %d", code)
		}
		seen[key] = true
		if desc == "" {
			t.Fatal("missing description")
		}
	}
	if weatherDescription(0, false, "en") != "Clear night" || weatherDescription(3, false, "ru") != "Облачная ночь" {
		t.Fatal("missing day/night wording")
	}
}

func TestEventLabelsChangeAtLocalMidnight(t *testing.T) {
	now := time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	for _, locale := range []string{"ru", "en", "sr-Latn"} {
		s := settings.Screen{Locale: locale, Timezone: "Europe/Belgrade", ClockFormat: "24h"}
		today := photoEvent{title: "Meeting", start: time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)}
		tomorrow := today
		tomorrow.start = tomorrow.start.Add(24 * time.Hour)
		a := renderEvent(today, s, now)
		b := renderEvent(tomorrow, s, now)
		if string(a) == string(b) {
			t.Fatalf("%s date label did not change", locale)
		}
	}
}

func TestPhotoOverlayResetsWriteDeadline(t *testing.T) {
	h := New(nil, "secret", "", "", nil, nil, nil)
	h.SetPhotoOverlaySettings(settings.Screen{}, settings.Photos{}, false, false)
	id := strings.Repeat("a", 32)
	b := h.overlay.save(id, "event", make([]byte, 480*112*2), 480, 112, 506, 450, time.Now().Add(time.Minute))
	r := httptest.NewRequest("GET", "/api/photo-frame/v1/overlays/"+b.RenderID, nil)
	r.Header.Set("X-API-Key", "secret")
	r.Header.Set("X-Device-ID", id)
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	before := time.Now()
	h.PhotoOverlay(w, r)
	if w.Code != 200 || !w.reset || w.deadline.Before(before.Add(14*time.Second)) || w.deadline.After(time.Now().Add(16*time.Second)) {
		t.Fatalf("status=%d reset=%t deadline=%v", w.Code, w.reset, w.deadline)
	}
}

func TestPhotoPartialCalendarKeepsFreshSources(t *testing.T) {
	now := time.Now().UTC()
	mode := "ready"
	client := &http.Client{Transport: overlayTransport(func(r *http.Request) (*http.Response, error) {
		body := fmt.Sprintf(`{"complete":false,"sources":[{"calendar_id":"a","status":%q,"stale":false},{"calendar_id":"b","status":"ready","stale":false,"coverage_complete":false},{"calendar_id":"c","status":"ready","stale":true,"coverage_complete":true}],"items":[{"id":"fresh","calendar_id":"a","title":"Fresh","start":{"date_time":%q},"end":{"date_time":%q}},{"id":"partial-window","calendar_id":"b","title":"Partial window","start":{"date_time":%q},"end":{"date_time":%q}},{"id":"stale","calendar_id":"c","title":"Stale","start":{"date_time":%q},"end":{"date_time":%q}}]}`, mode, now.Add(time.Hour).Format(time.RFC3339), now.Add(2*time.Hour).Format(time.RFC3339), now.Add(time.Minute).Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339), now.Add(2*time.Minute).Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	h := New(nil, "", "", "", nil, nil, client)
	h.SetCalendarPlatform("https://calendar.test", "token", []string{"a", "b", "c"}, "UTC")
	req := httptest.NewRequest("GET", "/", nil)
	event, err := h.nextPhotoEvent(req, now)
	if err != nil || event == nil || event.id != "fresh" || !event.partial {
		t.Fatalf("fresh partial event lost: %+v %v", event, err)
	}
	for _, locale := range []string{"ru", "en", "sr-Latn"} {
		screen := settings.Screen{Locale: locale, Timezone: "UTC", ClockFormat: "24h"}
		partial := renderEvent(*event, screen, now)
		full := *event
		full.partial = false
		if string(partial) == string(renderEvent(full, screen, now)) {
			t.Fatalf("missing partial-result caption for %s", locale)
		}
	}
	mode = "syncing"
	if event, err := h.nextPhotoEvent(req, now); err != nil || event != nil {
		t.Fatalf("no eligible fresh events should hide the card: event=%+v err=%v", event, err)
	}
}

func TestPhotoPartialCompatibilityAndInconsistentCompletePayload(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name, selected, body string
		wantItems            int
		wantComplete         bool
	}{
		{
			name:      "legacy missing coverage remains eligible",
			selected:  `"a"`,
			body:      fmt.Sprintf(`{"complete":false,"sources":[{"calendar_id":"a","status":"ready","stale":false}],"items":[{"id":"legacy","calendar_id":"a","title":"Legacy","start":{"date_time":%q},"end":{"date_time":%q}}]}`, now.Add(time.Hour).Format(time.RFC3339), now.Add(2*time.Hour).Format(time.RFC3339)),
			wantItems: 1,
		},
		{
			name:         "legacy missing coverage remains complete when all source metadata is ready",
			selected:     `"a"`,
			body:         fmt.Sprintf(`{"complete":true,"sources":[{"calendar_id":"a","status":"ready","stale":false}],"items":[{"id":"legacy","calendar_id":"a","title":"Legacy","start":{"date_time":%q},"end":{"date_time":%q}}]}`, now.Add(time.Hour).Format(time.RFC3339), now.Add(2*time.Hour).Format(time.RFC3339)),
			wantItems:    1,
			wantComplete: true,
		},
		{
			name:      "complete claim downgraded for coverage false",
			selected:  `"a"`,
			body:      fmt.Sprintf(`{"complete":true,"sources":[{"calendar_id":"a","status":"ready","stale":false,"coverage_complete":false}],"items":[{"id":"partial","calendar_id":"a","title":"Partial","start":{"date_time":%q},"end":{"date_time":%q}}]}`, now.Add(time.Hour).Format(time.RFC3339), now.Add(2*time.Hour).Format(time.RFC3339)),
			wantItems: 0,
		},
		{
			name:      "complete claim downgraded when a selected source is missing",
			selected:  `"a","b"`,
			body:      fmt.Sprintf(`{"complete":true,"sources":[{"calendar_id":"a","status":"ready","stale":false}],"items":[{"id":"legacy","calendar_id":"a","title":"Legacy","start":{"date_time":%q},"end":{"date_time":%q}}]}`, now.Add(time.Hour).Format(time.RFC3339), now.Add(2*time.Hour).Format(time.RFC3339)),
			wantItems: 1,
		},
		{
			name:     "complete claim downgraded for stale source",
			selected: `"a"`,
			body:     fmt.Sprintf(`{"complete":true,"sources":[{"calendar_id":"a","status":"ready","stale":true}],"items":[{"id":"stale","calendar_id":"a","title":"Stale","start":{"date_time":%q},"end":{"date_time":%q}}]}`, now.Add(time.Hour).Format(time.RFC3339), now.Add(2*time.Hour).Format(time.RFC3339)),
		},
		{
			name:     "complete claim downgraded for unknown freshness",
			selected: `"a"`,
			body:     fmt.Sprintf(`{"complete":true,"sources":[{"calendar_id":"a","status":"ready"}],"items":[{"id":"unknown","calendar_id":"a","title":"Unknown","start":{"date_time":%q},"end":{"date_time":%q}}]}`, now.Add(time.Hour).Format(time.RFC3339), now.Add(2*time.Hour).Format(time.RFC3339)),
		},
		{
			name:     "complete claim downgraded for pending source",
			selected: `"a"`,
			body:     fmt.Sprintf(`{"complete":true,"sources":[{"calendar_id":"a","status":"pending","stale":false}],"items":[{"id":"pending","calendar_id":"a","title":"Pending","start":{"date_time":%q},"end":{"date_time":%q}}]}`, now.Add(time.Hour).Format(time.RFC3339), now.Add(2*time.Hour).Format(time.RFC3339)),
		},
		{
			name:      "complete claim downgraded for safe error code",
			selected:  `"a"`,
			body:      fmt.Sprintf(`{"complete":true,"sources":[{"calendar_id":"a","status":"ready","stale":false,"error_code":"auth"}],"items":[{"id":"auth","calendar_id":"a","title":"Auth","start":{"date_time":%q},"end":{"date_time":%q}}]}`, now.Add(time.Hour).Format(time.RFC3339), now.Add(2*time.Hour).Format(time.RFC3339)),
			wantItems: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: overlayTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body)), Header: http.Header{}}, nil
			})}
			h := New(nil, "", "", "", nil, nil, client)
			ids := []string{"a"}
			if tc.selected == `"a","b"` {
				ids = []string{"a", "b"}
			}
			h.SetCalendarPlatform("https://calendar.test", "token", ids, "UTC")
			details := &CalendarDetails{}
			items, err := h.fetchCalendarPlatformRawDetailed(httptest.NewRequest(http.MethodGet, "/", nil), now, now.Add(24*time.Hour), true, details)
			if err != nil || details.Complete != tc.wantComplete || len(items) != tc.wantItems {
				t.Fatalf("complete=%t items=%#v err=%v", details.Complete, items, err)
			}
			if tc.wantItems > 0 && items[0].partial == tc.wantComplete {
				t.Fatal("partial-result caption marker was not retained")
			}
			if len(details.Sources) != len(ids) {
				t.Fatalf("missing selected source not represented as unknown: %+v", details.Sources)
			}
		})
	}
}

func TestPhotoPartialCalendarRequiresKnownCoverageAndClearsOldCard(t *testing.T) {
	now := time.Now().UTC()
	client := &http.Client{Transport: overlayTransport(func(r *http.Request) (*http.Response, error) {
		body := fmt.Sprintf(`{"complete":false,"sources":[{"calendar_id":"a","status":"ready","stale":false,"coverage_complete":false}],"items":[{"id":"partial","calendar_id":"a","title":"Partial","start":{"date_time":%q},"end":{"date_time":%q}}]}`, now.Add(time.Hour).Format(time.RFC3339), now.Add(2*time.Hour).Format(time.RFC3339))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	h := New(nil, "secret", "", "", nil, nil, client)
	h.SetCalendarPlatform("https://calendar.test", "token", []string{"a"}, "UTC")
	h.SetPhotoOverlaySettings(settings.Screen{Locale: "ru", Timezone: "UTC"}, settings.Photos{}, false, true)
	h.overlay.eventData = &photoEvent{title: "Old event", start: now.Add(time.Hour), end: now.Add(2 * time.Hour)}
	h.overlay.eventExpiry = now.Add(5 * time.Minute)
	h.overlay.lastCalendar = now.Add(-time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/api/photo-frame/v1/context", nil)
	req.Header.Set("X-API-Key", "secret")
	req.Header.Set("X-Device-ID", strings.Repeat("a", 32))
	w := httptest.NewRecorder()
	h.PhotoContext(w, req)
	var response overlayContext
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode context: %v (%s)", err, w.Body.String())
	}
	for _, block := range response.Blocks {
		if block.Kind == "event" {
			t.Fatal("partial-coverage event card was exposed")
		}
	}
	if h.overlay.eventData != nil || h.overlay.event != nil {
		t.Fatal("old event card was retained after no eligible fresh events")
	}
	sceneRequest := httptest.NewRequest(http.MethodGet, "/api/photo-frame/v3/context", nil)
	sceneRequest.Header.Set("X-API-Key", "secret")
	sceneRequest.Header.Set("X-Device-ID", strings.Repeat("a", 32))
	sceneRecorder := httptest.NewRecorder()
	h.PhotoContextV3(sceneRecorder, sceneRequest)
	var scene sceneContext
	if err := json.Unmarshal(sceneRecorder.Body.Bytes(), &scene); err != nil {
		t.Fatalf("decode scene: %v (%s)", err, sceneRecorder.Body.String())
	}
	for _, block := range scene.Blocks {
		if block.Kind == "event" {
			t.Fatal("partial-coverage event scene was exposed")
		}
	}
}
