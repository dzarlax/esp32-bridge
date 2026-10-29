package app

import (
	"encoding/json"
	"esp32-bridge/internal/photoframe"
	"esp32-bridge/internal/settings"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const deviceA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const deviceB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestFleetIsolationAndAtomicFailure(t *testing.T) {
	cfg := testConfig()
	f, err := NewFleet(cfg, &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, m := range f.current.Load().nodes {
			m.current.Load().cancel()
		}
	}()
	defaults := settings.Defaults(cfg, "")
	a := settings.Defaults(cfg, "")
	b := settings.Defaults(cfg, "")
	a.Photos.Enabled = false
	b.Photos.IntervalSeconds = 123
	profiles := map[string]settings.Values{"": defaults, deviceA: a, deviceB: b}
	commit, _, err := f.Prepare(profiles, DefaultSecrets(cfg))
	if err != nil {
		t.Fatal(err)
	}
	commit()
	f.EnableManaged()
	request := func(id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/photo-frame/v1/manifest", nil)
		r.Header.Set("X-API-Key", cfg.APIKey)
		r.Header.Set("X-Device-ID", id)
		w := httptest.NewRecorder()
		f.ServeHTTP(w, r)
		return w
	}
	if w := request(deviceA); w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"disabled"`) {
		t.Fatalf("A manifest %d %s", w.Code, w.Body)
	}
	if w := request(deviceB); w.Code != 200 || !strings.Contains(w.Body.String(), `123`) {
		t.Fatalf("B manifest %d %s", w.Code, w.Body)
	}
	if request("cccccccccccccccccccccccccccccccc").Code != 404 {
		t.Fatal("unknown device fell back")
	}
	if request("invalid").Code != 400 {
		t.Fatal("invalid identity accepted")
	}
	if request("").Code != 200 {
		t.Fatal("legacy reads broken")
	}
	previous := f.current.Load()
	b.Services["tasks"] = settings.Service{Enabled: true}
	profiles[deviceB] = b
	if _, _, err = f.Prepare(profiles, DefaultSecrets(cfg)); err == nil {
		t.Fatal("invalid candidate accepted")
	}
	if previous != f.current.Load() {
		t.Fatal("failed prepare changed fleet")
	}
	// A-only preferences preserve both existing photo services and B settings.
	b.Services["tasks"] = settings.Service{Enabled: false}
	profiles[deviceB] = b
	a.Screen.Name = "Kitchen"
	profiles[deviceA] = a
	commit, _, err = f.Prepare(profiles, DefaultSecrets(cfg))
	if err != nil {
		t.Fatal(err)
	}
	commit()
	for _, id := range []string{deviceA, deviceB} {
		if previous.nodes[id].current.Load().photos != f.current.Load().nodes[id].current.Load().photos {
			t.Fatal("unrelated preferences restarted photos")
		}
	}
}

func TestSharedHAActionInvalidatesDifferentSelections(t *testing.T) {
	var state atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := "off"
		if state.Load() {
			value = "on"
		}
		fmt.Fprintf(w, `{"entity_id":%q,"state":%q,"attributes":{"friendly_name":"Lamp","supported_color_modes":["onoff"]}}`, strings.TrimPrefix(r.URL.Path, "/api/states/"), value)
	}))
	defer upstream.Close()
	cfg := testConfig()
	cfg.PhotoEnabled = false
	cfg.HACacheTTL = time.Hour
	f, err := NewFleet(cfg, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, m := range f.current.Load().nodes {
			m.current.Load().cancel()
		}
	}()
	a := settings.Defaults(cfg, "")
	b := settings.Defaults(cfg, "")
	a.Services["home"] = settings.Service{Enabled: true, BaseURL: upstream.URL, Lights: []string{"light.one"}}
	b.Services["home"] = settings.Service{Enabled: true, BaseURL: upstream.URL, Lights: []string{"light.one", "light.two"}}
	secrets := DefaultSecrets(cfg)
	secrets["ha_token"] = "synthetic-token"
	commit, _, err := f.Prepare(map[string]settings.Values{"": settings.Defaults(cfg, ""), deviceA: a, deviceB: b}, secrets)
	if err != nil {
		t.Fatal(err)
	}
	commit()
	first := f.current.Load().nodes[deviceB].current.Load().orch.FetchAll()["lights"]
	if !strings.Contains(string(first), `"on":false`) {
		t.Fatalf("initial state: %s", first)
	}
	state.Store(true)
	f.current.Load().nodes[deviceA].current.Load().orch.Invalidate("lights")
	second := f.current.Load().nodes[deviceB].current.Load().orch.FetchAll()["lights"]
	if !strings.Contains(string(second), `"on":true`) {
		t.Fatalf("B reused stale state: %s", second)
	}
}

func TestManagedImmichAlbumsDoNotShareFrames(t *testing.T) {
	const albumA = "123e4567-e89b-42d3-a456-426614174000"
	const albumB = "123e4567-e89b-42d3-a456-426614174001"
	const assetA = "123e4567-e89b-42d3-a456-426614174002"
	const assetB = "123e4567-e89b-42d3-a456-426614174003"
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/albums/") {
			json.NewEncoder(w).Encode(map[string]string{"id": strings.TrimPrefix(r.URL.Path, "/api/albums/")})
			return
		}
		if r.URL.Path == "/api/search/metadata" {
			body, _ := io.ReadAll(r.Body)
			id := assetA
			if strings.Contains(string(body), albumB) {
				id = assetB
			}
			json.NewEncoder(w).Encode(map[string]any{"assets": map[string]any{"items": []any{map[string]any{"id": id, "type": "IMAGE", "visibility": "timeline", "isArchived": false, "isTrashed": false, "isOffline": false, "isEdited": false, "checksum": id, "updatedAt": "2026-10-29T00:00:00Z"}}, "nextCursor": nil}})
			return
		}
		shade := color.RGBA{R: 255, A: 255}
		if strings.Contains(r.URL.Path, assetB) {
			shade = color.RGBA{B: 255, A: 255}
		}
		img := image.NewRGBA(image.Rect(0, 0, 8, 8))
		draw.Draw(img, img.Bounds(), &image.Uniform{C: shade}, image.Point{}, draw.Src)
		png.Encode(w, img)
	}))
	defer upstream.Close()
	cfg := testConfig()
	cfg.PhotoEnabled = false
	f, err := NewFleet(cfg, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, m := range f.current.Load().nodes {
			m.current.Load().cancel()
		}
	}()
	a := settings.Defaults(cfg, "")
	a.Photos.Enabled = true
	a.Photos.Source = "immich"
	a.Photos.BaseURL = upstream.URL
	a.Photos.AlbumID = albumA
	b := a
	b.Photos.AlbumID = albumB
	secrets := DefaultSecrets(cfg)
	secrets["immich_api_key"] = "synthetic-immich-key"
	commit, _, err := f.Prepare(map[string]settings.Values{"": settings.Defaults(cfg, ""), deviceA: a, deviceB: b}, secrets)
	if err != nil {
		t.Fatal(err)
	}
	commit()
	f.EnableManaged()
	var ma, mb photoframe.Manifest
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ma = f.current.Load().nodes[deviceA].current.Load().photos.Manifest()
		mb = f.current.Load().nodes[deviceB].current.Load().photos.Manifest()
		if len(ma.Frames) > 0 && len(mb.Frames) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(ma.Frames) == 0 || len(mb.Frames) == 0 {
		t.Fatalf("frames did not warm: A=%s B=%s", ma.State, mb.State)
	}
	if ma.Frames[0].ID == mb.Frames[0].ID {
		t.Fatal("different albums shared frame")
	}
	r := httptest.NewRequest("GET", "/api/photo-frame/v1/frames/"+ma.Frames[0].ID, nil)
	r.Header.Set("X-API-Key", cfg.APIKey)
	r.Header.Set("X-Device-ID", deviceB)
	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)
	if w.Code == 200 {
		t.Fatal("B downloaded A's frame")
	}
	// Disconnecting A must immediately make previously cached pixels unavailable.
	a.Photos.Enabled = false
	commit, _, err = f.Prepare(map[string]settings.Values{"": settings.Defaults(cfg, ""), deviceA: a, deviceB: b}, secrets)
	if err != nil {
		t.Fatal(err)
	}
	commit()
	r.Header.Set("X-Device-ID", deviceA)
	w = httptest.NewRecorder()
	f.ServeHTTP(w, r)
	if w.Code == 200 {
		t.Fatal("disconnected A still serves cached frame")
	}
}
