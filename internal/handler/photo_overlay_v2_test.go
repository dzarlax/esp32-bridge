package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"esp32-bridge/internal/model"
	"esp32-bridge/internal/photoframe"
	"esp32-bridge/internal/settings"
	"fmt"
	"image"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdaptiveRegionsAndPixels(t *testing.T) {
	frame := make([]byte, photoframe.FrameBytes)
	if tone, glass := analyzeRegion(frame, 24, 460, 480, 128); tone != "light" || glass {
		t.Fatalf("dark: %s %v", tone, glass)
	}
	for i := range frame {
		frame[i] = 255
	}
	if tone, glass := analyzeRegion(frame, 24, 460, 480, 128); tone != "dark" || glass {
		t.Fatalf("light: %s %v", tone, glass)
	}
	for y := 460; y < 588; y++ {
		for x := 24; x < 504; x++ {
			if (x/8+y/8)%2 == 0 {
				i := (y*1024 + x) * 2
				frame[i] = 0
				frame[i+1] = 0
			}
		}
	}
	tone, glass := analyzeRegion(frame, 24, 460, 480, 128)
	if !glass {
		t.Fatal("mixed region needs glass")
	}
	p := adaptivePatch(frame, 24, 460, 480, 128, tone, glass, nil)
	for y := 0; y < 128; y++ {
		for x := 0; x < 480; x++ {
			if !insideRound(x, y, 480, 128) {
				a := (y*480 + x) * 2
				b := ((y+460)*1024 + x + 24) * 2
				if p[a] != frame[b] || p[a+1] != frame[b+1] {
					t.Fatal("pixels outside contour changed")
				}
			}
		}
	}
	if tone2, glass2 := analyzeRegion(frame, 752, 36, 236, 200); tone2 != "dark" || glass2 {
		t.Fatal("analysis did not use actual region")
	}
}
func TestAdaptiveContextOwnershipAndCache(t *testing.T) {
	svc := photoframe.New(true, photoframe.FixtureSource{}, 300, 60, 900, 32)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	var m photoframe.Manifest
	for time.Now().Before(deadline) {
		m = svc.Manifest()
		if len(m.Frames) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(m.Frames) == 0 {
		t.Fatal("fixture unavailable")
	}
	h := New(nil, "secret", "", "", nil, nil, nil)
	h.SetPhotoFrame(svc)
	h.SetPhotoOverlaySettings(settings.Screen{Locale: "en", Timezone: "UTC", ClockFormat: "24h"}, settings.Photos{}, false, false)
	req := func(path, device string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-API-Key", "secret")
		r.Header.Set("X-Device-ID", device)
		w := httptest.NewRecorder()
		if strings.Contains(path, "/overlays/") {
			h.PhotoOverlayV2(w, r)
		} else {
			h.PhotoContextV2(w, r)
		}
		return w
	}
	device := strings.Repeat("a", 32)
	if got := req("/api/photo-frame/v2/context?frame_id="+strings.Repeat("0", 64), device).Code; got != 404 {
		t.Fatal(got)
	}
	w := req("/api/photo-frame/v2/context?frame_id="+m.Frames[0].ID, device)
	var result adaptiveContext
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.FrameID != m.Frames[0].ID || result.Version != 2 {
		t.Fatalf("context %d %s", w.Code, w.Body.String())
	}
	p := make([]byte, 4)
	block := h.overlay.saveAdaptive(device, m.Frames[0].ID, "clock", p, 2, 1, 0, 0, time.Now().Add(time.Minute))
	path := "/api/photo-frame/v2/overlays/" + block.RenderID
	w = req(path, device)
	hash := sha256.Sum256(w.Body.Bytes())
	if w.Code != 200 || hex.EncodeToString(hash[:]) != block.SHA256 {
		t.Fatal("bad pixel response")
	}
	if req(path, strings.Repeat("b", 32)).Code != 404 {
		t.Fatal("cross-device access")
	}
	wrongVersion := httptest.NewRequest("GET", "/api/photo-frame/v1/overlays/"+block.RenderID, nil)
	wrongVersion.Header.Set("X-API-Key", "secret")
	wrongVersion.Header.Set("X-Device-ID", device)
	wrongResponse := httptest.NewRecorder()
	h.PhotoOverlay(wrongResponse, wrongVersion)
	if wrongResponse.Code != 404 {
		t.Fatal("v2 ID accepted by v1")
	}
	foreign := h.overlay.saveAdaptive(device, strings.Repeat("0", 64), "clock", p, 2, 1, 0, 0, time.Now().Add(time.Minute))
	if req("/api/photo-frame/v2/overlays/"+foreign.RenderID, device).Code != 404 {
		t.Fatal("unborrowable frame accepted")
	}
	r := httptest.NewRequest("POST", "/api/photo-frame/v2/context", nil)
	r.Header.Set("X-API-Key", "secret")
	r.Header.Set("X-Device-ID", device)
	w = httptest.NewRecorder()
	h.PhotoContextV2(w, r)
	if w.Code != 405 {
		t.Fatal(w.Code)
	}
}
func TestAdaptiveExportFixtures(t *testing.T) {
	dir := os.Getenv("ADAPTIVE_FIXTURE_DIR")
	if dir == "" {
		t.Skip("fixture export not requested")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	names := []string{"bright", "dark", "mixed", "portrait"}
	if os.Getenv("ADAPTIVE_PHOTO_PATH") != "" {
		names = append(names, "real")
	}
	for _, name := range names {
		frame := make([]byte, photoframe.FrameBytes)
		if name == "real" {
			photo, err := os.Open(os.Getenv("ADAPTIVE_PHOTO_PATH"))
			if err != nil {
				t.Fatal(err)
			}
			frame, err = photoframe.Render(context.Background(), photo)
			_ = photo.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		if name != "real" {
			for y := 0; y < 600; y++ {
				for x := 0; x < 1024; x++ {
					v := uint16(0)
					switch name {
					case "bright":
						v = 0xffff
					case "mixed":
						if (x/16+y/16)%2 == 0 {
							v = 0xffff
						}
					case "portrait":
						if x > 350 && x < 674 {
							v = uint16((x+y)%32)<<11 | uint16((x+y)%64)<<5 | uint16(y%32)
						} else {
							v = 0x2104
						}
					}
					i := (y*1024 + x) * 2
					frame[i] = byte(v)
					frame[i+1] = byte(v >> 8)
				}
			}
		}
		if err := os.WriteFile(filepath.Join(dir, name+"-frame.rgb565"), frame, 0644); err != nil {
			t.Fatal(err)
		}
		meta := map[string]any{}
		for _, g := range []struct {
			kind       string
			x, y, w, h int
		}{{"weather", 752, 36, 236, 200}, {"event", 530, 450, 456, 112}, {"clock", 24, 460, 480, 128}} {
			tone, glass := analyzeTextRegions(frame, g.kind, g.x, g.y)
			for _, locale := range []string{"en", "ru", "sr-Latn"} {
				var art func(*image.RGBA, bool)
				if g.kind == "weather" {
					art = func(img *image.RGBA, d bool) {
						drawWeather(img, model.WeatherData{Temp: 23, IsDay: true, WeatherCode: 3}, settings.Screen{Locale: locale, City: "Belgrade"}, d, true)
					}
				}
				if g.kind == "event" {
					title := map[string]string{"en": "Example event", "ru": "Пример события", "sr-Latn": "Primer događaja"}[locale]
					art = func(img *image.RGBA, d bool) {
						drawEvent(img, photoEvent{title: title, start: time.Date(2026, 10, 30, 16, 0, 0, 0, time.UTC), end: time.Date(2026, 10, 30, 17, 0, 0, 0, time.UTC), partial: true}, settings.Screen{Locale: locale, Timezone: "UTC"}, time.Date(2026, 10, 30, 15, 0, 0, 0, time.UTC), d, true)
					}
				}
				patch := adaptivePatch(frame, g.x, g.y, g.w, g.h, tone, glass, art)
				if err := os.WriteFile(filepath.Join(dir, name+"-"+g.kind+"-"+locale+".rgb565"), patch, 0644); err != nil {
					t.Fatal(err)
				}
				if locale == "en" {
					if err := os.WriteFile(filepath.Join(dir, name+"-"+g.kind+".rgb565"), patch, 0644); err != nil {
						t.Fatal(err)
					}
				}
			}
			meta[g.kind] = map[string]any{"x": g.x, "y": g.y, "width": g.w, "height": g.h, "tone": tone, "backdrop": glass}
		}
		b, _ := json.MarshalIndent(meta, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, name+".json"), b, 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAdaptiveReusesCalendarSnapshot(t *testing.T) {
	now := time.Now().UTC()
	calls := 0
	client := &http.Client{Transport: overlayTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		body := fmt.Sprintf(`{"complete":true,"items":[{"id":"1","calendar_id":"a","title":"Meeting","start":{"date_time":%q},"end":{"date_time":%q}}]}`, now.Add(time.Hour).Format(time.RFC3339), now.Add(2*time.Hour).Format(time.RFC3339))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	svc := photoframe.New(true, photoframe.FixtureSource{}, 300, 60, 900, 32)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)
	var m photoframe.Manifest
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m = svc.Manifest()
		if len(m.Frames) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(m.Frames) == 0 {
		t.Fatal("no frame")
	}
	h := New(nil, "secret", "", "", nil, nil, client)
	h.SetPhotoFrame(svc)
	h.SetCalendarPlatform("https://calendar.test", "token", []string{"a"}, "UTC")
	h.SetPhotoOverlaySettings(settings.Screen{Locale: "en", Timezone: "UTC", ClockFormat: "24h"}, settings.Photos{}, false, true)
	var first adaptiveContext
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("GET", "/api/photo-frame/v2/context?frame_id="+m.Frames[0].ID, nil)
		r.Header.Set("X-API-Key", "secret")
		r.Header.Set("X-Device-ID", strings.Repeat("a", 32))
		w := httptest.NewRecorder()
		h.PhotoContextV2(w, r)
		var got adaptiveContext
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		if i == 0 {
			first = got
		} else if first.Clock.Tone != got.Clock.Tone {
			t.Fatal("tone changed")
		}
	}
	if calls != 1 {
		t.Fatalf("calendar requested %d times", calls)
	}
}

func TestAdaptiveInkRegionsAndGlassContrast(t *testing.T) {
	gray := make([]byte, photoframe.FrameBytes)
	for i := 0; i < len(gray); i += 2 {
		gray[i] = 0x10
		gray[i+1] = 0x84
	}
	for _, kind := range []string{"clock", "weather", "event"} {
		x, y := 24, 460
		if kind == "weather" {
			x, y = 752, 36
		}
		if kind == "event" {
			x, y = 530, 450
		}
		_, glass := analyzeTextRegions(gray, kind, x, y)
		if glass {
			t.Fatalf("uniform gray %s should not require glass", kind)
		}
	}

	for _, g := range []struct {
		kind       string
		x, y, w, h int
	}{{"clock", 24, 460, 480, 128}, {"weather", 752, 36, 236, 200}, {"event", 530, 450, 456, 112}} {
		for _, inverse := range []bool{false, true} {
			f := make([]byte, photoframe.FrameBytes)
			if inverse {
				for i := range f {
					f[i] = 255
				}
			}
			for _, rect := range textRegions(g.kind) {
				for y := rect.Min.Y; y < rect.Max.Y; y++ {
					for x := rect.Min.X; x < rect.Max.X; x++ {
						i := ((g.y+y)*1024 + g.x + x) * 2
						if inverse {
							f[i] = 0
							f[i+1] = 0
						} else {
							f[i] = 255
							f[i+1] = 255
						}
					}
				}
			}
			tone, glass := analyzeTextRegions(f, g.kind, g.x, g.y)
			expected := "dark"
			if inverse {
				expected = "light"
			}
			if tone != expected || glass {
				t.Fatalf("%s inverse=%v got %s glass=%v", g.kind, inverse, tone, glass)
			}
		}
		f := make([]byte, photoframe.FrameBytes)
		for y := 0; y < 600; y++ {
			for x := 0; x < 1024; x++ {
				if (x/16+y/16)%2 == 0 {
					i := (y*1024 + x) * 2
					f[i] = 255
					f[i+1] = 255
				}
			}
		}
		tone, glass := analyzeTextRegions(f, g.kind, g.x, g.y)
		if !glass {
			t.Fatal("mixed must use glass")
		}
		patch := adaptivePatch(f, g.x, g.y, g.w, g.h, tone, glass, nil)
		minL, maxL := 255, 0
		linear := func(c int) float64 {
			v := float64(c) / 255
			if v <= 0.04045 {
				return v / 12.92
			}
			return math.Pow((v+0.055)/1.055, 2.4)
		}
		for _, rect := range textRegions(g.kind) {
			for y := rect.Min.Y; y < rect.Max.Y; y++ {
				for x := rect.Min.X; x < rect.Max.X; x++ {
					i := (y*g.w + x) * 2
					v := uint16(patch[i]) | uint16(patch[i+1])<<8
					r := int(v>>11) * 255 / 31
					gg := int(v>>5&63) * 255 / 63
					b := int(v&31) * 255 / 31
					l := (r*2126 + gg*7152 + b*722) / 10000
					if l < minL {
						minL = l
					}
					if l > maxL {
						maxL = l
					}
					lum := .2126*linear(r) + .7152*linear(gg) + .0722*linear(b)
					if math.Max(1.05/(lum+.05), (lum+.05)/.05) < 4.5 {
						t.Fatalf("no contrasting glyph color at %d,%d", x, y)
					}
				}
			}
		}
		if maxL-minL < 20 {
			t.Fatalf("%s glass obscured photo detail", g.kind)
		}
	}
}

type cancelAfterRows struct {
	context.Context
	checks int
}

func (c *cancelAfterRows) Err() error {
	c.checks++
	if c.checks >= 4 {
		return context.Canceled
	}
	return nil
}
func TestAdaptiveCancellationDuringRendering(t *testing.T) {
	c := &cancelAfterRows{Context: context.Background()}
	p := adaptivePatchContext(c, make([]byte, photoframe.FrameBytes), 24, 460, 480, 128, "light", true, nil)
	if p != nil || c.checks < 4 {
		t.Fatal("render did not stop on cancellation")
	}
}

type revokingFixture struct{ revoked atomic.Bool }

func (s *revokingFixture) List(ctx context.Context) ([]photoframe.Asset, error) {
	if s.revoked.Load() {
		return nil, nil
	}
	return (photoframe.FixtureSource{}).List(ctx)
}
func (s *revokingFixture) Open(ctx context.Context, a photoframe.Asset) (io.ReadCloser, error) {
	return (photoframe.FixtureSource{}).Open(ctx, a)
}
func TestAdaptiveRevokedWhileProviderBlocked(t *testing.T) {
	source := &revokingFixture{}
	svc := photoframe.New(true, source, 300, 1, 900, 32)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	var m photoframe.Manifest
	for time.Now().Before(deadline) {
		m = svc.Manifest()
		if len(m.Frames) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(m.Frames) == 0 {
		t.Fatal("no frame")
	}
	entered := make(chan struct{})
	unblock := make(chan struct{})
	client := &http.Client{Transport: overlayTransport(func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-unblock
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"complete":true,"items":[]}`)), Header: http.Header{}}, nil
	})}
	h := New(nil, "secret", "", "", nil, nil, client)
	h.SetPhotoFrame(svc)
	h.SetCalendarPlatform("https://calendar.test", "token", []string{"a"}, "UTC")
	h.SetPhotoOverlaySettings(settings.Screen{Locale: "en", Timezone: "UTC", ClockFormat: "24h"}, settings.Photos{}, false, true)
	r := httptest.NewRequest("GET", "/api/photo-frame/v2/context?frame_id="+m.Frames[0].ID, nil)
	r.Header.Set("X-API-Key", "secret")
	r.Header.Set("X-Device-ID", strings.Repeat("a", 32))
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { h.PhotoContextV2(w, r); close(done) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not start")
	}
	source.revoked.Store(true)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, release, ok := svc.Borrow(m.Frames[0].ID)
		if !ok {
			break
		}
		release()
		time.Sleep(20 * time.Millisecond)
	}
	_, release, ok := svc.Borrow(m.Frames[0].ID)
	if ok {
		release()
		t.Fatal("frame still borrowable")
	}
	close(unblock)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("context did not finish")
	}
	if w.Code != 404 || strings.Contains(w.Body.String(), "frame_id") {
		t.Fatalf("revoked context: %d %s", w.Code, w.Body.String())
	}
}
func TestAdaptiveLocalizedTextAndPartialCaption(t *testing.T) {
	frame := make([]byte, photoframe.FrameBytes)
	now := time.Date(2026, 10, 30, 15, 0, 0, 0, time.UTC)
	e := photoEvent{title: "Meeting", start: now.Add(time.Hour), end: now.Add(2 * time.Hour), partial: true}
	renders := map[string][]byte{}
	for _, locale := range []string{"ru", "en", "sr-Latn"} {
		renders[locale] = adaptivePatch(frame, 530, 450, 456, 112, "light", false, func(img *image.RGBA, d bool) {
			drawEvent(img, e, settings.Screen{Locale: locale, Timezone: "UTC"}, now, d, true)
		})
		plain := e
		plain.partial = false
		without := adaptivePatch(frame, 530, 450, 456, 112, "light", false, func(img *image.RGBA, d bool) {
			drawEvent(img, plain, settings.Screen{Locale: locale, Timezone: "UTC"}, now, d, true)
		})
		if bytes.Equal(renders[locale], without) {
			t.Fatalf("%s missing partial caption", locale)
		}
	}
	if bytes.Equal(renders["ru"], renders["en"]) || bytes.Equal(renders["en"], renders["sr-Latn"]) {
		t.Fatal("locales did not affect rendering")
	}
}
