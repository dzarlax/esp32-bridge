package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func testValues() Values {
	return Values{
		Screen: Screen{Name: "HomeDash", City: "Belgrade", Timezone: "Europe/Belgrade", ClockFormat: "24h", IdleAction: "photos", NightStart: "23:00", NightEnd: "07:00", AutoUpdateCheck: true},
		Photos: Photos{Source: "none", IdleSeconds: 300, IntervalSeconds: 60, Order: "shuffle", Layout: "adaptive", CropLimitPercent: 10},
		Services: map[string]Service{
			"weather":  {Enabled: true, Latitude: 44.82, Longitude: 20.46, Timezone: "Europe/Belgrade"},
			"calendar": {Timezone: "Europe/Belgrade"}, "transport": {}, "health": {}, "tasks": {},
			"news": {Limit: 5, SinceHours: 24}, "home": {},
		},
		Network: Network{BridgeURL: "https://bridge.example.test"},
	}
}

func newStore(t *testing.T, prepare Prepare) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := New(dir, testValues(), map[string]string{"health_api_key": "default-health"}, "https://bridge.example.test", prepare)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func testID(ch byte) string { return strings.Repeat(string(ch), 32) }

func report(s *Store, id, boot string, capabilities ...string) Device {
	return Device{DeviceID: id, BridgeID: s.BridgeID(), Firmware: "1.0.0", BootID: boot, Capabilities: capabilities}
}

func register(t *testing.T, s *Store, id, boot string, capabilities ...string) SyncResponse {
	t.Helper()
	response, err := s.Sync(report(s, id, boot, capabilities...))
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestRegistrationCreatesDistinctPersistedProfilesAndIdentity(t *testing.T) {
	s, dir := newStore(t, nil)
	if got := s.Devices(); len(got.Devices) != 0 || got.MaxDevices != MaxDevices {
		t.Fatalf("unexpected synthetic profiles: %#v", got)
	}
	a, b := testID('a'), testID('b')
	ra := register(t, s, a, "boot-a", "preferences")
	rb := register(t, s, b, "boot-b", "preferences")
	if ra.DeviceID != a || rb.DeviceID != b || ra.BridgeID != s.BridgeID() || ra.BridgeID == "" {
		t.Fatalf("bad scoped sync response: %#v %#v", ra, rb)
	}
	if ra.Preferences.Screen.Name != "Display aaaaaa" || rb.Preferences.Screen.Name != "Display bbbbbb" {
		t.Fatalf("default names not distinguishable: %q %q", ra.Preferences.Screen.Name, rb.Preferences.Screen.Name)
	}
	if list := s.Devices(); len(list.Devices) != 2 || list.Devices[0].ID != a || list.Devices[1].ID != b {
		t.Fatalf("device list=%#v", list)
	}
	bridgeID := s.BridgeID()
	restarted, err := New(dir, testValues(), map[string]string{"health_api_key": "default-health"}, "https://bridge.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.BridgeID() != bridgeID || len(restarted.Devices().Devices) != 2 {
		t.Fatal("restart lost Bridge identity or registered profiles")
	}
	if restarted.Devices().Devices[0].Connected {
		t.Fatal("heartbeat should be volatile across restart")
	}
	profiles, _ := restarted.Snapshot()
	if len(profiles) != 3 || profiles[""].Screen.Name != "HomeDash" {
		t.Fatalf("snapshot profile map=%#v", profiles)
	}
}

func TestPerDeviceSettingsRevisionsCommandsAndAcksAreIsolated(t *testing.T) {
	s, dir := newStore(t, nil)
	a, b := testID('a'), testID('b')
	ra := register(t, s, a, "boot-a", "preferences", "photo_start", "restart", "network")
	rb := register(t, s, b, "boot-b", "preferences", "photo_start", "network")
	va, err := s.View(a, map[string]Status{"weather": {State: "ok"}})
	if err != nil {
		t.Fatal(err)
	}
	vb, err := s.View(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	va.Settings.Screen.Name = "Kitchen Display"
	va.Settings.Photos.AlbumID = "album-a"
	if err := s.Update(a, va.Revision, Patch{Settings: va.Settings}); err != nil {
		t.Fatal(err)
	}
	va2, _ := s.View(a, nil)
	vb2, _ := s.View(b, nil)
	if va2.Revision <= va.Revision || vb2.Revision != vb.Revision || vb2.Settings.Screen.Name != rb.Preferences.Screen.Name || vb2.Settings.Photos.AlbumID != "" {
		t.Fatalf("device A edit leaked: A=%#v B=%#v", va2, vb2)
	}
	if err := s.Update(a, va.Revision, Patch{Settings: va.Settings}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale device revision accepted: %v", err)
	}
	va2, _ = s.View(a, map[string]Status{"weather": {State: "ok"}})
	if va2.Statuses["weather"].State != "ok" {
		t.Fatal("status projection lost")
	}

	if _, err := s.Sync(report(s, a, "boot-a", "preferences", "photo_start", "restart", "network")); err != nil {
		t.Fatal(err)
	}
	command, err := s.Enqueue(a, "photo_start", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enqueue(b, "photo_start", false); err != nil {
		t.Fatalf("independent command slot blocked: %v", err)
	}
	responseA, err := s.Sync(report(s, a, "boot-a", "preferences", "photo_start", "restart", "network"))
	if err != nil {
		t.Fatal(err)
	}
	responseB, err := s.Sync(report(s, b, "boot-b", "preferences", "photo_start", "network"))
	if err != nil {
		t.Fatal(err)
	}
	if responseA.Command == nil || responseA.Command.ID != command.ID || responseB.Command == nil || responseB.Command.ID == command.ID {
		t.Fatalf("commands crossed profiles: A=%#v B=%#v", responseA.Command, responseB.Command)
	}
	reportA := report(s, a, "boot-a", "preferences", "photo_start", "restart", "network")
	reportA.CommandResult = &Result{ID: command.ID, State: "succeeded"}
	if _, err := s.Sync(reportA); err != nil {
		t.Fatal(err)
	}
	viewA, _ := s.View(a, nil)
	viewB, _ := s.View(b, nil)
	if viewA.Device.Command.State != "succeeded" || viewB.Device.Command == nil || viewB.Device.Command.State != "queued" {
		t.Fatalf("result changed wrong command: A=%#v B=%#v", viewA.Device.Command, viewB.Device.Command)
	}

	restarted, err := New(dir, testValues(), map[string]string{"health_api_key": "default-health"}, "https://bridge.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := restarted.View(b, nil); got.Device.Command == nil || got.Device.Command.State != "queued" {
		t.Fatal("B command did not persist")
	}
	if got, _ := restarted.View(a, nil); got.Device.Command == nil || got.Device.Command.State != "succeeded" {
		t.Fatal("A result did not persist")
	}
	if response, err := restarted.Sync(report(restarted, b, "boot-b", "preferences", "photo_start", "network")); err != nil || response.Command == nil {
		t.Fatalf("restart did not recover B command: %#v %v", response, err)
	}
	_ = ra
}

func TestConcurrentDeviceEditsAllocateIndependentRevisions(t *testing.T) {
	s, _ := newStore(t, nil)
	a, b := testID('a'), testID('b')
	register(t, s, a, "boot-a", "preferences")
	register(t, s, b, "boot-b", "preferences")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for id, city := range map[string]string{a: "A City", b: "B City"} {
		wg.Add(1)
		go func(id, city string) {
			defer wg.Done()
			view, err := s.View(id, nil)
			if err != nil {
				errs <- err
				return
			}
			view.Settings.Screen.City = city
			errs <- s.Update(id, view.Revision, Patch{Settings: view.Settings})
		}(id, city)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	viewA, _ := s.View(a, nil)
	viewB, _ := s.View(b, nil)
	if viewA.Settings.Screen.City != "A City" || viewB.Settings.Screen.City != "B City" || viewA.Revision == viewB.Revision {
		t.Fatalf("concurrent edits cross-contaminated: A=%#v B=%#v", viewA, viewB)
	}
}

func TestBridgeScopeAndPendingNetworkArePerDeviceAndPersisted(t *testing.T) {
	s, dir := newStore(t, nil)
	a, b := testID('a'), testID('b')
	register(t, s, a, "boot-a", "preferences", "network")
	register(t, s, b, "boot-b", "preferences", "network")
	va, _ := s.View(a, nil)
	va.Settings.Network.BridgeURL = "https://new-a.example.test"
	ssid := "network-a"
	if err := s.Update(a, va.Revision, Patch{Settings: va.Settings, NetworkCredentials: map[string]*string{"wifi_ssid": &ssid}}); err != nil {
		t.Fatal(err)
	}
	va, _ = s.View(a, nil)
	vb, _ := s.View(b, nil)
	foreign := report(s, a, "boot-a", "preferences", "network")
	foreign.BridgeID = testID('f')
	foreign.AppliedRevision = va.Revision
	response, err := s.Sync(foreign)
	if err != nil {
		t.Fatal(err)
	}
	if response.Preferences.Network["wifi_ssid"] != ssid || response.BridgeID != s.BridgeID() {
		t.Fatal("scope-mismatch bootstrap omitted scoped candidate network")
	}
	if profile := s.state.Profiles[a]; profile.Network["wifi_ssid"] != ssid {
		t.Fatal("foreign scope cleared A's candidate network")
	}
	if _, _ = s.View(b, nil); vb.Revision != s.state.Profiles[b].Revision {
		t.Fatal("A's network edit changed B revision")
	}

	matching := report(s, a, "boot-a", "preferences", "network")
	matching.AppliedRevision = va.Revision
	if _, err := s.Sync(matching); err != nil {
		t.Fatal(err)
	}
	if len(s.state.Profiles[a].Network) != 0 {
		t.Fatal("matching revision did not clear A network transaction")
	}
	if len(s.state.Profiles[b].Network) != 0 {
		t.Fatal("A network state leaked to B")
	}

	va.Settings.Network.BridgeURL = "https://persisted-a.example.test"
	va.Revision = s.state.Profiles[a].Revision
	password := "wifi-secret-a"
	if err := s.Update(a, va.Revision, Patch{Settings: va.Settings, NetworkCredentials: map[string]*string{"wifi_password": &password}}); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(dir, testValues(), map[string]string{"health_api_key": "default-health"}, "https://bridge.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := restarted.View(a, nil); got.Settings.Network.BridgeURL != "https://persisted-a.example.test" {
		t.Fatal("A network preference did not persist")
	}
	if got, _ := restarted.View(b, nil); got.Settings.Network.BridgeURL != "https://bridge.example.test" {
		t.Fatal("A network preference changed B")
	}
}

func TestNetworkProfileChangesRequireOnlineNetworkCapability(t *testing.T) {
	s, _ := newStore(t, nil)
	id := testID('1')
	register(t, s, id, "boot", "preferences")
	view, _ := s.View(id, nil)
	view.Settings.Network.BridgeURL = "https://candidate.example.test"
	if err := s.Update(id, view.Revision, Patch{Settings: view.Settings}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("network URL change without capability: %v", err)
	}
	if _, err := s.Sync(report(s, id, "boot", "preferences", "network")); err != nil {
		t.Fatal(err)
	}
	view, _ = s.View(id, nil)
	view.Settings.Network.BridgeURL = "https://candidate.example.test"
	past := time.Now().Add(-time.Minute)
	s.mu.Lock()
	device := s.devices[id]
	device.LastSeen = &past
	s.devices[id] = device
	s.mu.Unlock()
	if err := s.Update(id, view.Revision, Patch{Settings: view.Settings}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("offline network change: %v", err)
	}
}

func TestSharedConnectionChangesConfirmDisconnectAndBumpAllProfiles(t *testing.T) {
	values := testValues()
	values.Services["health"] = Service{Enabled: false, BaseURL: "https://health.example.test"}
	commits, aborts := 0, 0
	var prepared map[string]Values
	prepare := func(candidates map[string]Values, _ map[string]string) (func(), func(), error) {
		prepared = clone(candidates)
		return func() { commits++ }, func() { aborts++ }, nil
	}
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := New(dir, values, map[string]string{"health_api_key": "health-secret"}, "https://bridge.example.test", prepare)
	if err != nil {
		t.Fatal(err)
	}
	a, b := testID('a'), testID('b')
	register(t, s, a, "boot-a", "preferences")
	register(t, s, b, "boot-b", "preferences")
	viewA, _ := s.View(a, nil)
	viewA.Settings.Services["health"] = Service{Enabled: true, BaseURL: "https://health.example.test"}
	if err := s.Update(a, viewA.Revision, Patch{Settings: viewA.Settings}); err != nil {
		t.Fatal(err)
	}
	beforeA, _ := s.View(a, nil)
	beforeB, _ := s.View(b, nil)
	connections := s.ConnectionsView()
	empty := ""
	if err := s.UpdateConnections(connections.Revision, ConnectionPatch{Connections: connections.Connections, Secrets: map[string]*string{"health_api_key": &empty}}); err != nil {
		t.Fatal(err)
	}
	if view := s.ConnectionsView(); view.Revision != connections.Revision || !view.SecretsPresent["health_api_key"] {
		t.Fatal("empty shared secret did not preserve value/revision")
	}
	connections.Connections.Health.BaseURL = ""
	remove := map[string]*string{"health_api_key": nil}
	patch := ConnectionPatch{Connections: connections.Connections, Secrets: remove}
	if err := s.UpdateConnections(connections.Revision, patch); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("disconnect without confirmation: %v", err)
	}
	patch.ConfirmDisconnect = true
	if err := s.UpdateConnections(connections.Revision, patch); err != nil {
		t.Fatal(err)
	}
	afterA, _ := s.View(a, nil)
	afterB, _ := s.View(b, nil)
	if afterA.Revision <= beforeA.Revision || afterB.Revision <= beforeB.Revision || s.ConnectionsView().Revision != connections.Revision+1 {
		t.Fatalf("shared update did not version all scopes: A=%d B=%d shared=%d", afterA.Revision, afterB.Revision, s.ConnectionsView().Revision)
	}
	if afterA.Settings.Services["health"].Enabled || afterB.Settings.Services["health"].Enabled || prepared[""].Services["health"].Enabled {
		t.Fatal("confirmed disconnect left health selections enabled")
	}
	if commits != 4 || aborts != 0 {
		t.Fatalf("prepare/commit unexpected: commits=%d aborts=%d", commits, aborts)
	}
	if s.ConnectionsView().SecretsPresent["health_api_key"] {
		t.Fatal("removed shared credential still present")
	}
	if got := s.Devices(); len(got.Devices) != 2 {
		t.Fatal("shared edit changed registration count")
	}
	if err := s.UpdateConnections(connections.Revision, patch); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale shared revision accepted: %v", err)
	}
}

func TestSharedTransactionFailureAbortsAndPreservesState(t *testing.T) {
	commits, aborts := 0, 0
	s, _ := newStore(t, func(map[string]Values, map[string]string) (func(), func(), error) {
		return func() { commits++ }, func() { aborts++ }, nil
	})
	connections := s.ConnectionsView()
	beforeValues, beforeSecrets := s.Snapshot()
	backup := s.path + ".backup"
	if err := os.Rename(s.path, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(s.path, 0700); err != nil {
		t.Fatal(err)
	}
	connections.Connections.News.BaseURL = "https://news2.example.test"
	err := s.UpdateConnections(connections.Revision, ConnectionPatch{Connections: connections.Connections})
	if err == nil {
		t.Fatal("expected persistence error")
	}
	if commits != 0 || aborts != 1 {
		t.Fatalf("prepare commit/abort=%d/%d", commits, aborts)
	}
	afterValues, afterSecrets := s.Snapshot()
	if !reflect.DeepEqual(beforeValues, afterValues) || !reflect.DeepEqual(beforeSecrets, afterSecrets) || s.ConnectionsView().Revision != connections.Revision {
		t.Fatal("failed transaction mutated in-memory shared state")
	}
	if err := os.Remove(s.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, s.path); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyV1MigrationArchivesUnaddressedPendingState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	legacyID := testID('c')
	cmd := &Command{ID: strings.Repeat("1", 32), Action: "restart", State: "queued", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Minute), BootID: "old-boot"}
	legacy := legacyDiskState{Version: 1, BridgeID: legacyID, Revision: 9, Settings: testValues(), Secrets: map[string]string{"health_api_key": "migrated-secret"}, Network: map[string]string{"wifi_ssid": "legacy-wifi"}, Command: cmd}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(dir, testValues(), nil, "https://bridge.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.BridgeID() != legacyID || s.state.LegacyUnaddressed == nil || s.state.LegacyUnaddressed.Command.ID != cmd.ID {
		t.Fatal("legacy Bridge identity or pending archive was lost")
	}
	response := register(t, s, testID('a'), "new-boot", "preferences", "restart", "network")
	if response.Command != nil || response.Preferences.Network != nil {
		t.Fatalf("legacy unaddressed state was delivered: %#v", response)
	}
	if secrets := s.effectiveSecrets(s.state); secrets["health_api_key"] != "migrated-secret" {
		t.Fatal("legacy credentials were not migrated")
	}
	if st, err := os.Stat(filepath.Join(dir, "settings.json")); err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("migrated state not privately persisted")
	}
}

func TestDeviceCapAndSyncScopeValidation(t *testing.T) {
	s, _ := newStore(t, nil)
	for i := 1; i <= MaxDevices; i++ {
		register(t, s, testID(byte('0'+i)), "boot", "preferences")
	}
	if _, err := s.Sync(report(s, testID('9'), "boot", "preferences")); !errors.Is(err, ErrTooManyDevices) {
		t.Fatalf("device cap error=%v", err)
	}
	if s.HasDevice(testID('9')) {
		t.Fatal("rejected device was partially registered")
	}
	bad := report(s, testID('1'), "boot", "preferences")
	bad.AppliedRevision = ^uint64(0)
	if _, err := s.Sync(bad); err == nil {
		t.Fatal("same Bridge accepted an impossible revision")
	}
	bad.BridgeID = testID('f')
	if _, err := s.Sync(bad); err != nil {
		t.Fatalf("different Bridge scope should bootstrap: %v", err)
	}
}

func TestStateRejectsSymlinksPermissionsUnknownFieldsAndInvalidIdentity(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if _, err := New(link, testValues(), nil, "", nil); err == nil {
		t.Fatal("accepted symlink state directory")
	}
	s, stateDir := newStore(t, nil)
	if err := os.Chmod(s.path, 0640); err != nil {
		t.Fatal(err)
	}
	if _, err := New(stateDir, testValues(), nil, "", nil); err == nil {
		t.Fatal("accepted group-readable state")
	}
	if err := os.Chmod(s.path, 0600); err != nil {
		t.Fatal(err)
	}
	state := clone(s.state)
	state.BridgeID = "bad"
	b, _ := json.Marshal(state)
	if err := os.WriteFile(s.path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(stateDir, testValues(), nil, "", nil); err == nil {
		t.Fatal("accepted invalid persisted Bridge ID")
	}
}

func TestConcurrentBridgeIDReadsDuringSyncAndProfileReplacement(t *testing.T) {
	s, _ := newStore(t, nil)
	id := testID('1')
	register(t, s, id, "boot-a", "preferences")
	bridgeID := s.BridgeID()
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(3)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 100; i++ {
			if got := s.BridgeID(); got != bridgeID {
				t.Errorf("Bridge identity changed: %q", got)
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 50; i++ {
			d := report(s, id, "boot-a", "preferences")
			if _, err := s.Sync(d); err != nil {
				t.Errorf("sync: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 20; i++ {
			view, err := s.View(id, nil)
			if err != nil {
				t.Errorf("view: %v", err)
				return
			}
			view.Settings.Screen.City = "City " + strconv.Itoa(i)
			if err := s.Update(id, view.Revision, Patch{Settings: view.Settings}); err != nil {
				t.Errorf("update: %v", err)
				return
			}
		}
	}()
	close(start)
	wg.Wait()
}
