package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"esp32-bridge/internal/model"
	"esp32-bridge/internal/photoframe"
	"esp32-bridge/internal/settings"
)

func TestSceneIndependentContract(t *testing.T) {
	h := New(nil, "secret", "", "", nil, nil, nil)
	h.SetPhotoOverlaySettings(settings.Screen{Locale: "sr-Latn", Timezone: "UTC", ClockFormat: "12h"}, settings.Photos{}, true, true)
	now := time.Now()
	s := h.overlay
	s.weatherData = &model.WeatherData{Temp: 23, IsDay: true, WeatherCode: 3}
	s.weatherExpiry = now.Add(time.Minute)
	s.lastWeather = now
	s.eventData = &photoEvent{title: "Meeting", start: now.Add(time.Hour), end: now.Add(2 * time.Hour)}
	s.eventExpiry = now.Add(time.Minute)
	s.lastCalendar = now
	device := strings.Repeat("a", 32)
	req := func(path, dev string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-API-Key", "secret")
		r.Header.Set("X-Device-ID", dev)
		w := httptest.NewRecorder()
		if strings.Contains(path, "/overlays/") {
			h.PhotoOverlayV3(w, r)
		} else {
			h.PhotoContextV3(w, r)
		}
		return w
	}
	w := req("/api/photo-frame/v3/context", device)
	var c sceneContext
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &c) != nil || c.Version != 3 || c.Clock.Tone != "light" || !c.Clock.Backdrop || len(c.Blocks) != 2 || strings.Contains(w.Body.String(), "frame_id") {
		t.Fatalf("bad context %d %s", w.Code, w.Body.String())
	}
	for _, b := range c.Blocks {
		switch b.Kind {
		case "weather":
			if b.X != 752 || b.Y != 36 || b.Width > 236 || b.Height > 200 {
				t.Fatal(b)
			}
		case "event":
			if b.X+b.Width != 986 || b.Y+b.Height != 576 || b.X < 530 || b.Width > 456 || b.Height > 112 {
				t.Fatal(b)
			}
		default:
			t.Fatal("unexpected block", b.Kind)
		}
		p := req("/api/photo-frame/v3/overlays/"+b.RenderID, device)
		sum := sha256.Sum256(p.Body.Bytes())
		if p.Code != 200 || b.PixelFormat != scenePixelFormat || b.ByteCount != b.Width*b.Height*4 || len(p.Body.Bytes()) != b.ByteCount || hex.EncodeToString(sum[:]) != b.SHA256 {
			t.Fatal("bad image", b)
		}
		transparent, partial := false, false
		for i := 0; i < len(p.Body.Bytes()); i += 4 {
			a := p.Body.Bytes()[i+3]
			if a == 0 {
				transparent = true
				if p.Body.Bytes()[i] != 0 || p.Body.Bytes()[i+1] != 0 || p.Body.Bytes()[i+2] != 0 {
					t.Fatal("transparent RGB")
				}
			}
			if a > 0 && a < 255 {
				partial = true
			}
		}
		if !transparent || !partial {
			t.Fatal("expected transparency and antialiasing")
		}
		if req("/api/photo-frame/v3/overlays/"+b.RenderID, strings.Repeat("b", 32)).Code != 404 {
			t.Fatal("device scope")
		}
		r := httptest.NewRequest("GET", "/api/photo-frame/v2/overlays/"+b.RenderID, nil)
		r.Header.Set("X-API-Key", "secret")
		r.Header.Set("X-Device-ID", device)
		old := httptest.NewRecorder()
		h.PhotoOverlayV2(old, r)
		if old.Code != 404 {
			t.Fatal("version scope")
		}
	}
	legacy := s.saveAdaptive(device, strings.Repeat("0", 64), "event", []byte{1, 2}, 1, 1, 0, 0, now.Add(time.Minute))
	if req("/api/photo-frame/v3/overlays/"+legacy.RenderID, device).Code != 404 {
		t.Fatal("v2 ID accepted by v3")
	}
	service := photoframe.New(true, photoframe.FixtureSource{}, 300, 60, 900, 32)
	h.SetPhotoFrame(service)
	manifestReq := httptest.NewRequest("GET", "/api/photo-frame/v1/manifest", nil)
	manifestReq.Header.Set("X-API-Key", "secret")
	manifestResp := httptest.NewRecorder()
	h.PhotoManifest(manifestResp, manifestReq)
	var manifest photoframe.Manifest
	if manifestResp.Code != 200 || json.Unmarshal(manifestResp.Body.Bytes(), &manifest) != nil || manifest.OverlayContextVersion != 2 || manifest.SceneContextVersion != 3 {
		t.Fatal("manifest negotiation", manifestResp.Body.String())
	}
	again := req("/api/photo-frame/v3/context", device)
	var c2 sceneContext
	_ = json.Unmarshal(again.Body.Bytes(), &c2)
	for i, b := range c.Blocks {
		if b.RenderID != c2.Blocks[i].RenderID {
			t.Fatal("unstable id")
		}
	}
	a := s.saveScene(device, "event", []byte{1, 2, 3, 4}, 1, 1, 0, 0, now)
	b := s.saveScene(device, "event", []byte{1, 2, 3, 4}, 1, 1, 1, 0, now)
	if a.RenderID == b.RenderID {
		t.Fatal("geometry not scoped")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "/api/photo-frame/v3/context", nil).WithContext(canceled)
	r.Header.Set("X-API-Key", "secret")
	r.Header.Set("X-Device-ID", device)
	resp := httptest.NewRecorder()
	h.PhotoContextV3(resp, r)
	if resp.Body.Len() != 0 {
		t.Fatal("canceled context returned scene")
	}
}

func TestSceneStraightAlpha(t *testing.T) {
	p := scenePixels(4, 4, func(img *image.RGBA, _ bool) {
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				i := img.PixOffset(x, y)
				img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 64, 32, 16, 128
			}
		}
	})
	for i := 0; i < len(p); i += 4 {
		if p[i] != 32 || p[i+1] != 64 || p[i+2] != 128 || p[i+3] != 128 {
			t.Fatal(p[i : i+4])
		}
		for _, bg := range []int{0, 127, 255} { // straight-alpha blend reconstructs premultiplied source.
			got := (int(p[i+2])*128 + bg*127 + 127) / 255
			want := 64 + (bg*127+127)/255
			if got-want > 1 || want-got > 1 {
				t.Fatalf("blend %d %d", got, want)
			}
		}
	}
}

func TestSceneExportFixtures(t *testing.T) {
	dir := os.Getenv("SCENE_FIXTURE_DIR")
	if dir == "" {
		t.Skip("fixture export not requested")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"en", "ru", "sr-Latn"} {
		for _, format := range []string{"24h", "12h"} {
			screen := settings.Screen{Locale: locale, City: "Belgrade", Timezone: "UTC", ClockFormat: format}
			now := time.Date(2026, 10, 30, 15, 0, 0, 0, time.UTC)
			e := photoEvent{title: map[string]string{"en": "Example event", "ru": "Пример события", "sr-Latn": "Primer događaja"}[locale], start: now.Add(time.Hour), end: now.Add(2 * time.Hour), partial: true}
			ww, wh, wa := contentArt(236, 200, func(i *image.RGBA) {
				drawWeather(i, model.WeatherData{Temp: 23, IsDay: true, WeatherCode: 3}, screen, false, true)
			})
			ew, eh, ea := contentArt(456, 112, func(i *image.RGBA) { drawEvent(i, e, screen, now, false, true) })
			blocks := []struct {
				kind       string
				x, y, w, h int
				art        func(*image.RGBA, bool)
			}{{"weather", 752, 36, ww, wh, wa}, {"event", 986 - ew, 576 - eh, ew, eh, ea}}
			meta := map[string]any{}
			for _, b := range blocks {
				p := scenePixels(b.w, b.h, b.art)
				filename := b.kind + "-" + locale + "-" + format + ".bgra"
				if err := os.WriteFile(filepath.Join(dir, filename), p, 0644); err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256(p)
				meta[b.kind] = map[string]any{"kind": b.kind, "x": b.x, "y": b.y, "width": b.w, "height": b.h, "pixel_format": scenePixelFormat, "byte_count": len(p), "sha256": hex.EncodeToString(sum[:]), "file": filename}
			}
			data, _ := json.MarshalIndent(meta, "", "  ")
			if err := os.WriteFile(filepath.Join(dir, locale+"-"+format+".json"), data, 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if path := os.Getenv("SCENE_PHOTO_PATH"); path != "" {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		frame, err := photoframe.Render(context.Background(), f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "real-frame.rgb565"), frame, 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSceneSurvivesPhotoRevocation(t *testing.T) {
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
	h := New(nil, "secret", "", "", nil, nil, nil)
	h.SetPhotoFrame(svc)
	h.SetPhotoOverlaySettings(settings.Screen{}, settings.Photos{}, false, false)
	device := strings.Repeat("a", 32)
	block := h.overlay.saveScene(device, "event", []byte{1, 2, 3, 4}, 1, 1, 0, 0, time.Now().Add(time.Minute))
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
	r := httptest.NewRequest("GET", "/api/photo-frame/v3/overlays/"+block.RenderID, nil)
	r.Header.Set("X-API-Key", "secret")
	r.Header.Set("X-Device-ID", device)
	w := httptest.NewRecorder()
	h.PhotoOverlayV3(w, r)
	if w.Code != 200 || w.Body.Len() != 4 {
		t.Fatal("card revoked with photo")
	}
}
