package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"esp32-bridge/internal/cache"
	"esp32-bridge/internal/config"
	"esp32-bridge/internal/fetcher"
	"esp32-bridge/internal/handler"
	"esp32-bridge/internal/photoframe"
	"esp32-bridge/internal/settings"
)

type runtime struct {
	handler   *handler.Handler
	mux       *http.ServeMux
	orch      *fetcher.Orchestrator
	photos    *photoframe.Service
	photoHash [32]byte
	cancel    context.CancelFunc
	start     func()
	values    settings.Values
}
type Manager struct {
	current  atomic.Pointer[runtime]
	defaults config.Config
	client   *http.Client
	started  time.Time
	shared   *cache.Cache
	managed  bool
}

func New(c *config.Config, client *http.Client) (*Manager, error) {
	m := &Manager{defaults: *c, client: client, started: time.Now(), shared: cache.New()}
	values := settings.Defaults(c, "")
	r, e := m.build(*c, values)
	if e != nil {
		return nil, e
	}
	m.current.Store(r)
	r.start()
	return m, nil
}
func DefaultSecrets(c *config.Config) map[string]string {
	values := map[string]string{"health_api_key": c.HealthAPIKey, "todoist_token": c.TodoistToken, "ha_token": c.HAToken, "calendar_platform_token": c.CalendarPlatformToken, "immich_api_key": ""}
	if c.ImmichAPIKeyFile != "" {
		if k, e := c.ReadImmichKey(); e == nil {
			values["immich_api_key"] = k
		}
	}
	return values
}
func (m *Manager) configFor(v settings.Values, secrets map[string]string) (config.Config, error) {
	c := m.defaults
	c.HealthAPIKey = secrets["health_api_key"]
	c.TodoistToken = secrets["todoist_token"]
	c.HAToken = secrets["ha_token"]
	c.CalendarPlatformToken = secrets["calendar_platform_token"]
	c.RuntimeImmichKey = secrets["immich_api_key"]
	for name, s := range v.Services {
		if s.Enabled {
			required := map[string]string{"health": "health_api_key", "tasks": "todoist_token", "home": "ha_token", "calendar": "calendar_platform_token"}[name]
			if required != "" && secrets[required] == "" {
				return c, errors.New(name + " credential required")
			}
		}
		switch name {
		case "health":
			c.HealthBaseURL = s.BaseURL
			if !s.Enabled {
				c.HealthBaseURL = ""
			}
		case "tasks":
			if !s.Enabled {
				c.TodoistToken = ""
			}
		case "news":
			c.NewsBaseURL = s.BaseURL
			c.NewsLimit = s.Limit
			c.NewsSinceHours = s.SinceHours
			if !s.Enabled {
				c.NewsBaseURL = ""
			}
		case "home":
			c.HABaseURL = s.BaseURL
			c.HASensors = s.Sensors
			c.HALights = s.Lights
			c.HAClimates = s.Climates
			if !s.Enabled {
				c.HABaseURL = ""
			}
		case "calendar":
			c.CalendarPlatformBaseURL = s.BaseURL
			c.CalendarPlatformIDs = s.IDs
			c.CalendarTimeZone = s.Timezone
			if !s.Enabled {
				c.CalendarPlatformBaseURL = ""
				c.CalendarPlatformToken = ""
				c.CalendarPlatformIDs = nil
			}
		case "transport":
			c.TransportBaseURL = s.BaseURL
			c.TransportStops = s.IDs
			if !s.Enabled {
				c.TransportBaseURL = ""
			}
		case "weather":
			c.WeatherLat = strconv.FormatFloat(s.Latitude, 'f', -1, 64)
			c.WeatherLon = strconv.FormatFloat(s.Longitude, 'f', -1, 64)
			c.WeatherTZ = s.Timezone
			if !s.Enabled {
				c.WeatherLat = ""
			}
		}
	}
	c.PhotoEnabled = v.Photos.Enabled
	c.PhotoSource = v.Photos.Source
	c.ImmichBaseURL = v.Photos.BaseURL
	c.ImmichAlbumID = v.Photos.AlbumID
	c.PhotoIdle = v.Photos.IdleSeconds
	c.PhotoInterval = v.Photos.IntervalSeconds
	if c.PhotoEnabled && c.PhotoSource == "immich" && c.RuntimeImmichKey == "" {
		return c, errors.New("Immich credential required")
	}
	if e := c.Validate(); e != nil {
		return c, errors.New("invalid service configuration")
	}
	return c, nil
}
func (m *Manager) Prepare(v settings.Values, secrets map[string]string) (func(), func(), error) {
	c, e := m.configFor(v, secrets)
	if e != nil {
		return nil, nil, e
	}
	r, e := m.build(c, v)
	if e != nil {
		return nil, nil, e
	}
	commit := func() {
		old := m.current.Swap(r)
		r.start()
		if old != nil && old.photos != r.photos {
			old.cancel()
		}
	}
	abort := func() {
		if old := m.current.Load(); old == nil || old.photos != r.photos {
			r.cancel()
		}
	}
	return commit, abort, nil
}
func (m *Manager) build(c config.Config, v settings.Values) (*runtime, error) {
	var fs []fetcher.Fetcher
	if c.HealthBaseURL != "" {
		fs = append(fs, fetcher.NewHealth(c.HealthBaseURL, c.HealthAPIKey, m.client, c.HealthCacheTTL))
	}
	if c.TodoistToken != "" {
		fs = append(fs, fetcher.NewTodoist(c.TodoistToken, m.client, c.TodoistCacheTTL))
	}
	if c.NewsBaseURL != "" {
		fs = append(fs, fetcher.NewNews(c.NewsBaseURL, c.NewsLimit, c.NewsSinceHours, m.client, c.NewsCacheTTL))
	}
	if c.HABaseURL != "" {
		if len(c.HASensors) > 0 {
			fs = append(fs, fetcher.NewHA(c.HABaseURL, c.HAToken, c.HASensors, m.client, c.HACacheTTL))
		}
		if len(c.HALights) > 0 {
			fs = append(fs, fetcher.NewLights(c.HABaseURL, c.HAToken, c.HALights, m.client, c.HACacheTTL))
		}
		if len(c.HAClimates) > 0 {
			fs = append(fs, fetcher.NewClimate(c.HABaseURL, c.HAToken, c.HAClimates, m.client, c.HACacheTTL))
		}
	}
	if c.WeatherLat != "" {
		fs = append(fs, fetcher.NewWeather(c.WeatherLat, c.WeatherLon, c.WeatherTZ, m.client, c.WeatherCacheTTL))
	}
	if c.TransportBaseURL != "" && len(c.TransportStops) > 0 {
		fs = append(fs, fetcher.NewTransport(c.TransportBaseURL, c.TransportStops, m.client, c.TransportCacheTTL))
	}
	for i, f := range fs {
		var parameters any
		switch f.Name() {
		case "health":
			parameters = []any{c.HealthBaseURL, c.HealthAPIKey}
		case "tasks":
			parameters = c.TodoistToken
		case "news":
			parameters = []any{c.NewsBaseURL, c.NewsLimit, c.NewsSinceHours}
		case "sensors":
			parameters = []any{c.HABaseURL, c.HAToken, c.HASensors}
		case "lights":
			parameters = []any{c.HABaseURL, c.HAToken, c.HALights}
		case "climate":
			parameters = []any{c.HABaseURL, c.HAToken, c.HAClimates}
		case "weather":
			parameters = []any{c.WeatherLat, c.WeatherLon, c.WeatherTZ}
		case "transport":
			parameters = []any{c.TransportBaseURL, c.TransportStops}
		default:
			parameters = c
		}
		data, _ := json.Marshal(parameters)
		prefix := ""
		if f.Name() == "lights" || f.Name() == "climate" || f.Name() == "sensors" {
			identity, _ := json.Marshal([]string{c.HABaseURL, c.HAToken})
			prefix = fmt.Sprintf("%s:%x:", f.Name(), sha256.Sum256(identity))
		}
		fs[i] = keyedFetcher{Fetcher: f, key: fmt.Sprintf("%s%s:%x", prefix, f.Name(), sha256.Sum256(data)), prefix: prefix}
	}
	if m.shared == nil {
		m.shared = cache.New()
	}
	orch := fetcher.NewOrchestrator(fs, m.shared, c.FetchTimeout)
	h := handler.New(orch, c.APIKey, c.HABaseURL, c.HAToken, c.HALights, c.HAClimates, m.client)
	h.SetCalendarPlatform(c.CalendarPlatformBaseURL, c.CalendarPlatformToken, c.CalendarPlatformIDs, c.CalendarTimeZone)
	h.SetOTA(c.OTAGitHubRepo, c.OTAGitHubToken, c.MigrateBridgeURL)
	key := ""
	if c.PhotoEnabled && c.PhotoSource == "immich" {
		var e error
		key, e = c.ReadImmichKey()
		if e != nil {
			return nil, e
		}
	}
	if m.managed {
		c.PhotoCache = 16
	}
	hashData, _ := json.Marshal(struct {
		Photo settings.Photos
		Key   string
	}{v.Photos, key})
	photoHash := sha256.Sum256(hashData)
	r := &runtime{handler: h, orch: orch, mux: http.NewServeMux(), photoHash: photoHash, values: v, start: func() {}}
	if old := m.current.Load(); old != nil && old.photoHash == photoHash {
		r.photos = old.photos
		r.cancel = old.cancel
	} else {
		var src photoframe.Source
		if c.PhotoEnabled && c.PhotoSource == "fixture" {
			src = photoframe.FixtureSource{}
		}
		if c.PhotoEnabled && c.PhotoSource == "immich" {
			var e error
			src, e = photoframe.NewImmich(c.ImmichBaseURL, c.ImmichAlbumID, key, m.client)
			if e != nil {
				return nil, errors.New("invalid Immich connection")
			}
		}
		r.photos = photoframe.New(c.PhotoEnabled, src, c.PhotoIdle, c.PhotoInterval, c.PhotoStale, c.PhotoCache)
		r.photos.Configure(photoframe.RenderOptions{Layout: v.Photos.Layout, CropLimitPercent: v.Photos.CropLimitPercent}, v.Photos.Order)
		ctx, cancel := context.WithCancel(context.Background())
		r.cancel = cancel
		r.start = func() { go r.photos.Run(ctx) }
	}
	h.SetPhotoFrame(r.photos)
	r.mux.HandleFunc("/api/photo-frame/v1/manifest", h.PhotoManifest)
	r.mux.HandleFunc("/api/photo-frame/v1/frames/", h.PhotoFrame)
	r.mux.HandleFunc("/api/dashboard", h.Dashboard)
	r.mux.HandleFunc("/api/ha/action", h.HAAction)
	r.mux.HandleFunc("/api/ha/state", h.HAState)
	r.mux.HandleFunc("/api/calendar", h.Calendar)
	r.mux.HandleFunc("/api/ota/check", h.OTACheck)
	r.mux.HandleFunc("/api/ota/firmware", h.OTAFirmware)
	return r, nil
}
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "uptime": int(time.Since(m.started).Seconds())})
		return
	}
	m.current.Load().mux.ServeHTTP(w, r)
}
func (m *Manager) Statuses() map[string]settings.Status {
	r := m.current.Load()
	raw := r.orch.Statuses()
	out := map[string]settings.Status{}
	for name, s := range r.values.Services {
		state := settings.Status{State: "unknown"}
		if !s.Enabled {
			state.State = "disabled"
			out[name] = state
			continue
		}
		keys := []string{name}
		if name == "health" {
			keys = []string{"health"}
		}
		if name == "tasks" {
			keys = []string{"tasks"}
		}
		if name == "home" {
			keys = []string{"sensors", "lights", "climate"}
		}
		for _, k := range keys {
			if f, ok := raw[k]; ok {
				if !f.LastSuccess.IsZero() {
					t := f.LastSuccess
					state.LastSuccess = &t
				}
				if f.State == "error" {
					state.State = "error"
					state.Error = "source_unavailable"
					break
				}
				state.State = f.State
			}
		}
		out[name] = state
	}
	p := r.photos.Manifest()
	state := "unknown"
	if p.State == "ready" {
		state = "ready"
	}
	if p.State == "unavailable" {
		state = "error"
	}
	if p.State == "disabled" {
		state = "disabled"
	}
	out["photos"] = settings.Status{State: state}
	return out
}
func (m *Manager) Updates(current string) map[string]any {
	return m.current.Load().handler.FirmwareStatus(current)
}

type keyedFetcher struct {
	fetcher.Fetcher
	key    string
	prefix string
}

func (f keyedFetcher) CacheKey() string { return f.key }

func (f keyedFetcher) InvalidationPrefix() string { return f.prefix }
