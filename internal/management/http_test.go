package management

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"esp32-bridge/internal/adminauth"
	"esp32-bridge/internal/settings"
)

func managementDefaults() settings.Values {
	return settings.Values{
		Screen: settings.Screen{Name: "HomeDash", City: "Belgrade", Timezone: "Europe/Belgrade", ClockFormat: "24h", IdleAction: "photos", NightStart: "23:00", NightEnd: "07:00", AutoUpdateCheck: true},
		Photos: settings.Photos{Source: "none", IdleSeconds: 300, IntervalSeconds: 60, Order: "shuffle", Layout: "adaptive", CropLimitPercent: 10},
		Services: map[string]settings.Service{
			"weather": {Latitude: 44.82, Longitude: 20.46, Timezone: "Europe/Belgrade"}, "calendar": {Timezone: "Europe/Belgrade"},
			"transport": {}, "health": {}, "tasks": {}, "news": {Limit: 5, SinceHours: 24}, "home": {},
		},
		Network: settings.Network{BridgeURL: "https://panel.example"},
	}
}

func TestMultiDeviceManagementRoutesAndNativeIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := settings.New(filepath.Join(dir, "state"), managementDefaults(), map[string]string{"health_api_key": "private-health-secret"}, "https://panel.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	password := "a-long-isolated-test-password"
	hash, err := adminauth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	passwordFile := filepath.Join(dir, "password-hash")
	if err := os.WriteFile(passwordFile, []byte(hash), 0600); err != nil {
		t.Fatal(err)
	}
	auth, err := adminauth.New(adminauth.Config{BaseURL: "https://panel.example", PasswordHashFile: passwordFile}, nil)
	if err != nil {
		t.Fatal(err)
	}
	updatesCalls := 0
	mux := http.NewServeMux()
	auth.Register(mux)
	api := &API{
		Store: store, DeviceKey: "device-api-key",
		Statuses: func(id string) map[string]settings.Status { return map[string]settings.Status{"scope": {State: id}} },
		Updates: func(id, current string) map[string]any {
			updatesCalls++
			return map[string]any{"current_version": current, "device_id": id}
		},
	}
	api.Register(mux, auth.Require)
	call := func(method, path string, body any, cookies []*http.Cookie, csrf, match, apiKey, deviceID string) *httptest.ResponseRecorder {
		var requestBody bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&requestBody).Encode(body)
		}
		r := httptest.NewRequest(method, "https://panel.example"+path, &requestBody)
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		r.Header.Set("Origin", "https://panel.example")
		for _, c := range cookies {
			r.AddCookie(c)
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		if match != "" {
			r.Header.Set("If-Match", match)
		}
		if apiKey != "" {
			r.Header.Set("X-API-Key", apiKey)
		}
		if deviceID != "" {
			r.Header.Set("X-Device-ID", deviceID)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", "/api/admin/v1/devices", nil, nil, "", "", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated admin request=%d", w.Code)
	}
	for _, kind := range []string{"albums", "calendars", "entities"} {
		path := "/api/admin/v1/devices/" + strings.Repeat("a", 32) + "/" + kind
		if w := call("GET", path, nil, nil, "", "", "device-api-key", ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s accepted device key without admin session: %d", kind, w.Code)
		}
	}
	login := call("POST", "/api/admin/v1/login", map[string]string{"password": password}, nil, "", "", "", "")
	if login.Code != http.StatusOK {
		t.Fatalf("login=%d %s", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	var session adminauth.Session
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.CSRFToken == "" {
		t.Fatal("login omitted CSRF token")
	}
	if w := call("GET", "/api/admin/v1/settings", nil, cookies, "", "", "", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("unscoped settings route=%d", w.Code)
	}
	if w := call("GET", "/api/admin/v1/actions", nil, cookies, "", "", "", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("unscoped action route=%d", w.Code)
	}

	connections := call("GET", "/api/admin/v1/connections", nil, cookies, "", "", "", "")
	if connections.Code != http.StatusOK || strings.Contains(connections.Body.String(), "private-health-secret") {
		t.Fatalf("unsafe connections view: %d %s", connections.Code, connections.Body.String())
	}
	var connectionView settings.ConnectionsView
	if err := json.Unmarshal(connections.Body.Bytes(), &connectionView); err != nil {
		t.Fatal(err)
	}
	if connectionView.SecretsPresent["health_api_key"] != true {
		t.Fatal("secret presence absent")
	}
	if got := call("GET", "/api/admin/v1/devices", nil, cookies, "", "", "", ""); got.Code != http.StatusOK || strings.Contains(got.Body.String(), "device_id") {
		t.Fatalf("devices list fabricated a profile: %d %s", got.Code, got.Body.String())
	}

	deviceID := strings.Repeat("a", 32)
	probe := call("GET", "/api/device/v1/probe", nil, nil, "", "", "device-api-key", deviceID)
	if probe.Code != http.StatusOK || strings.Contains(probe.Body.String(), deviceID) == false {
		t.Fatalf("probe response=%d %s", probe.Code, probe.Body.String())
	}
	if len(store.Devices().Devices) != 0 {
		t.Fatal("probe registered a device")
	}
	device := settings.Device{DeviceID: deviceID, Firmware: "1.0.21", BootID: "boot-a", Capabilities: []string{"preferences", "photo_start", "restart", "network"}}
	if w := call("POST", "/api/device/v1/sync", device, nil, "", "", "device-api-key", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("missing native identity=%d", w.Code)
	}
	if w := call("POST", "/api/device/v1/sync", device, nil, "", "", "device-api-key", strings.Repeat("b", 32)); w.Code != http.StatusUnauthorized {
		t.Fatalf("mismatched native identity=%d", w.Code)
	}
	if w := call("POST", "/api/device/v1/sync?key=device-api-key", device, nil, "", "", "", deviceID); w.Code != http.StatusUnauthorized {
		t.Fatalf("query API key accepted=%d", w.Code)
	}
	synced := call("POST", "/api/device/v1/sync", device, nil, "", "", "device-api-key", deviceID)
	if synced.Code != http.StatusOK {
		t.Fatalf("sync=%d %s", synced.Code, synced.Body.String())
	}
	var syncResponse settings.SyncResponse
	if err := json.Unmarshal(synced.Body.Bytes(), &syncResponse); err != nil {
		t.Fatal(err)
	}
	if syncResponse.DeviceID != deviceID || syncResponse.BridgeID != store.BridgeID() || syncResponse.Preferences.Screen.Name != "Display aaaaaa" {
		t.Fatalf("bad scoped sync response: %#v", syncResponse)
	}
	if syncResponse.BacklightReportVersion != 1 {
		t.Fatal("backlight capability not advertised")
	}
	device.BacklightReport = &settings.BacklightReport{Version: 1, Phase: "unknown"}
	if w := call("POST", "/api/device/v1/sync", device, nil, "", "", "device-api-key", deviceID); w.Code != http.StatusOK {
		t.Fatalf("negotiated backlight report rejected: %d %s", w.Code, w.Body.String())
	}
	device.BacklightReport.Phase = "measured"
	if w := call("POST", "/api/device/v1/sync", device, nil, "", "", "device-api-key", deviceID); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid backlight report accepted: %d", w.Code)
	}
	device.BacklightReport = nil
	listed := call("GET", "/api/admin/v1/devices", nil, cookies, "", "", "", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), deviceID) {
		t.Fatalf("registered profile missing: %s", listed.Body.String())
	}

	settingsPath := "/api/admin/v1/devices/" + deviceID + "/settings"
	first := call("GET", settingsPath, nil, cookies, "", "", "", "")
	if first.Code != http.StatusOK || first.Header().Get("ETag") == "" {
		t.Fatalf("scoped settings=%d", first.Code)
	}
	var view settings.Envelope
	if err := json.Unmarshal(first.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.DeviceID != deviceID || view.Statuses["scope"].State != deviceID {
		t.Fatal("per-device view/status routing failed")
	}
	view.Settings.Screen.Name = "Kitchen Display"
	patch := settings.Patch{Settings: view.Settings}
	if w := call("PATCH", settingsPath, patch, cookies, "", `"2"`, "", ""); w.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF accepted=%d", w.Code)
	}
	saved := call("PATCH", settingsPath, patch, cookies, session.CSRFToken, `"2"`, "", "")
	if saved.Code != http.StatusOK {
		t.Fatalf("settings patch=%d %s", saved.Code, saved.Body.String())
	}
	var savedView settings.Envelope
	if err := json.Unmarshal(saved.Body.Bytes(), &savedView); err != nil {
		t.Fatal(err)
	}
	if savedView.Settings.Screen.Name != "Kitchen Display" || savedView.Revision <= view.Revision {
		t.Fatal("device edit not saved")
	}
	if stale := call("PATCH", settingsPath, patch, cookies, session.CSRFToken, `"2"`, "", ""); stale.Code != http.StatusConflict {
		t.Fatalf("stale device revision=%d", stale.Code)
	}
	if extraSecret := call("PATCH", settingsPath, map[string]any{"settings": savedView.Settings, "secrets": map[string]string{"health_api_key": "not-a-device-secret"}}, cookies, session.CSRFToken, `"3"`, "", ""); extraSecret.Code != http.StatusBadRequest {
		t.Fatalf("per-device shared secret accepted=%d", extraSecret.Code)
	}

	actionPath := "/api/admin/v1/devices/" + deviceID + "/actions"
	action := call("POST", actionPath, map[string]any{"action": "photo_start"}, cookies, session.CSRFToken, "", "", "")
	if action.Code != http.StatusAccepted {
		t.Fatalf("device action=%d %s", action.Code, action.Body.String())
	}
	if unknown := call("GET", "/api/admin/v1/devices/"+strings.Repeat("b", 32)+"/settings", nil, cookies, "", "", "", ""); unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown profile route=%d", unknown.Code)
	}
	updates := call("GET", "/api/admin/v1/devices/"+deviceID+"/updates", nil, cookies, "", "", "", "")
	if updates.Code != http.StatusOK || updatesCalls != 1 || !strings.Contains(updates.Body.String(), deviceID) {
		t.Fatalf("scoped updates=%d %s", updates.Code, updates.Body.String())
	}
	diagnostics := call("GET", "/api/admin/v1/devices/"+deviceID+"/diagnostics", nil, cookies, "", "", "", "")
	if diagnostics.Code != http.StatusOK || strings.Contains(diagnostics.Body.String(), "health_api_key") || strings.Contains(diagnostics.Body.String(), "private-health-secret") {
		t.Fatalf("unsafe diagnostics=%d %s", diagnostics.Code, diagnostics.Body.String())
	}

	connectionView.Connections.News.BaseURL = "https://news.example.test"
	patchConnections := settings.ConnectionPatch{Connections: connectionView.Connections}
	if stale := call("PATCH", "/api/admin/v1/connections", patchConnections, cookies, session.CSRFToken, `"99"`, "", ""); stale.Code != http.StatusConflict {
		t.Fatalf("stale shared revision=%d", stale.Code)
	}
	updatedConnections := call("PATCH", "/api/admin/v1/connections", patchConnections, cookies, session.CSRFToken, `"1"`, "", "")
	if updatedConnections.Code != http.StatusOK {
		t.Fatalf("shared connection patch=%d %s", updatedConnections.Code, updatedConnections.Body.String())
	}
	if !strings.Contains(updatedConnections.Body.String(), "news.example.test") || strings.Contains(updatedConnections.Body.String(), "private-health-secret") {
		t.Fatal("shared transaction response invalid")
	}
}

func TestNativeProbeRejectsUntrustedIdentityAndHasNoRegistrationSideEffects(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	store, err := settings.New(filepath.Join(dir, "state"), managementDefaults(), nil, "https://panel.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	api := &API{Store: store, DeviceKey: "device-api-key"}
	mux := http.NewServeMux()
	api.Register(mux, func(h http.Handler) http.Handler { return h })
	id := strings.Repeat("c", 32)
	for _, tc := range []struct {
		key, id string
		want    int
	}{{"wrong", id, http.StatusUnauthorized}, {"device-api-key", "", http.StatusUnauthorized}, {"device-api-key", "not-an-id", http.StatusUnauthorized}} {
		r := httptest.NewRequest(http.MethodGet, "http://panel.example/api/device/v1/probe", nil)
		r.Header.Set("X-API-Key", tc.key)
		r.Header.Set("X-Device-ID", tc.id)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("probe status=%d want=%d", w.Code, tc.want)
		}
	}
	if list := store.Devices(); len(list.Devices) != 0 {
		t.Fatal("probe created a device profile")
	}
}

func TestConcurrentProbeWhileSyncAndSettingsPersist(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	store, err := settings.New(filepath.Join(dir, "state"), managementDefaults(), nil, "https://panel.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("d", 32)
	device := settings.Device{DeviceID: id, Firmware: "1.0.0", BootID: "boot", Capabilities: []string{"preferences"}}
	if _, err := store.Sync(device); err != nil {
		t.Fatal(err)
	}
	api := &API{Store: store, DeviceKey: "device-api-key"}
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(3)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 80; i++ {
			r := httptest.NewRequest(http.MethodGet, "http://panel.example/api/device/v1/probe", nil)
			r.Header.Set("X-API-Key", "device-api-key")
			r.Header.Set("X-Device-ID", id)
			w := httptest.NewRecorder()
			api.probe(w, r)
			if w.Code != http.StatusOK {
				t.Errorf("probe status=%d", w.Code)
				return
			}
			var response map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response["bridge_id"] != store.BridgeID() {
				t.Errorf("probe identity response=%v err=%v", response, err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 30; i++ {
			view, err := store.View(id, nil)
			if err != nil {
				t.Errorf("view: %v", err)
				return
			}
			view.Settings.Screen.City = "City " + strconv.Itoa(i)
			if err := store.Update(id, view.Revision, settings.Patch{Settings: view.Settings}); err != nil {
				t.Errorf("update: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 50; i++ {
			device.BridgeID = store.BridgeID()
			if _, err := store.Sync(device); err != nil {
				t.Errorf("sync: %v", err)
				return
			}
		}
	}()
	close(start)
	wg.Wait()
}
