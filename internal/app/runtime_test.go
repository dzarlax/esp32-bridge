package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"esp32-bridge/internal/config"
	"esp32-bridge/internal/settings"
)

func testConfig() *config.Config {
	return &config.Config{APIKey: "isolated-device", PhotoEnabled: true, PhotoSource: "fixture", PhotoIdle: 300, PhotoInterval: 60, PhotoStale: 900, PhotoCache: 32, NewsLimit: 5, NewsSinceHours: 24, FetchTimeout: time.Second}
}
func TestRuntimePreferencesPreservePhotoWorkerAndFailedPreparePreservesLiveConfig(t *testing.T) {
	cfg := testConfig()
	m, e := New(cfg, &http.Client{Timeout: time.Second})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { m.current.Load().cancel() }()
	original := m.current.Load()
	v := settings.Defaults(cfg, "")
	secrets := DefaultSecrets(cfg)
	v.Screen.Name = "Updated name"
	commit, abort, e := m.Prepare(v, secrets)
	if e != nil {
		t.Fatal(e)
	}
	if m.current.Load() != original {
		t.Fatal("prepare published before commit")
	}
	commit()
	_ = abort
	if m.current.Load().photos != original.photos {
		t.Fatal("screen change restarted photo worker")
	}
	current := m.current.Load()
	tasks := v.Services["tasks"]
	tasks.Enabled = true
	v.Services["tasks"] = tasks
	if _, _, e = m.Prepare(v, secrets); e == nil {
		t.Fatal("missing enabled credential accepted")
	}
	if m.current.Load() != current {
		t.Fatal("invalid config replaced live runtime")
	}
	tasks.Enabled = false
	v.Services["tasks"] = tasks
	v.Photos.Layout = "cover"
	commit, _, e = m.Prepare(v, secrets)
	if e != nil {
		t.Fatal(e)
	}
	commit()
	if m.current.Load().photos == original.photos {
		t.Fatal("render mode did not invalidate photo cache")
	}
	for _, tc := range []struct {
		path, key string
		status    int
	}{{"/health", "", 200}, {"/api/photo-frame/v1/manifest", "", 401}, {"/api/photo-frame/v1/manifest", cfg.APIKey, 200}, {"/api/dashboard", "", 401}, {"/api/dashboard", cfg.APIKey, 200}} {
		r := httptest.NewRequest("GET", tc.path, nil)
		r.Header.Set("X-API-Key", tc.key)
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("legacy route %s=%d wanted %d", tc.path, w.Code, tc.status)
		}
	}
}
