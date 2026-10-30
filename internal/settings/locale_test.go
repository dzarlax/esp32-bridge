package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLocaleIsolationAndHonestAcknowledgement(t *testing.T) {
	s, dir := newStore(t, nil)
	a, b := testID('a'), testID('b')
	register(t, s, a, "boot-a", "preferences", "locale")
	register(t, s, b, "boot-b", "preferences", "locale")
	for id, locale := range map[string]string{a: "en", b: "sr-Latn"} {
		view, _ := s.View(id, nil)
		view.Settings.Screen.Locale = locale
		if err := s.Update(id, view.Revision, Patch{Settings: view.Settings}); err != nil {
			t.Fatal(err)
		}
	}
	for id, locale := range map[string]string{a: "en", b: "sr-Latn"} {
		view, _ := s.View(id, nil)
		if view.Settings.Screen.Locale != locale {
			t.Fatal("languages leaked across devices")
		}
		d := report(s, id, "boot", "preferences", "locale")
		d.AppliedRevision = view.Revision
		d.AppliedLocale = "ru"
		if _, err := s.Sync(d); err != nil {
			t.Fatal(err)
		}
		view, _ = s.View(id, nil)
		if view.Device.AppliedRevision == view.Revision {
			t.Fatal("wrong language acknowledged")
		}
		d.AppliedLocale = locale
		if _, err := s.Sync(d); err != nil {
			t.Fatal(err)
		}
		view, _ = s.View(id, nil)
		if view.Device.AppliedRevision != view.Revision {
			t.Fatal("language not acknowledged")
		}
		d.Capabilities = []string{"preferences"}
		d.AppliedLocale = ""
		if _, err := s.Sync(d); err != nil {
			t.Fatal(err)
		}
		view, _ = s.View(id, nil)
		if view.Device.AppliedRevision == view.Revision {
			t.Fatal("downgrade falsely acknowledged language")
		}
		if s.Devices().Devices[0].ID == id && s.Devices().Devices[0].AppliedRevision == view.Revision {
			t.Fatal("device list falsely acknowledged")
		}
		// An old client omits locale during unrelated edits.
		patch := view.Settings
		patch.Screen.Locale = ""
		patch.Screen.City = "Novi Sad"
		if err := s.Update(id, view.Revision, Patch{Settings: patch}); err != nil {
			t.Fatal(err)
		}
		after, _ := s.View(id, nil)
		if after.Settings.Screen.Locale != locale {
			t.Fatal("old client reset language")
		}
		after.Settings.Screen.Locale = "ru"
		if err := s.Update(id, after.Revision, Patch{Settings: after.Settings}); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("old firmware changed locale: %v", err)
		}
	}
	restarted, err := New(dir, testValues(), nil, "https://bridge.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	va, _ := restarted.View(a, nil)
	vb, _ := restarted.View(b, nil)
	if va.Settings.Screen.Locale != "en" || vb.Settings.Screen.Locale != "sr-Latn" {
		t.Fatal("locale did not survive restart")
	}
}

func TestLocaleValidation(t *testing.T) {
	for _, locale := range []string{"ru", "en", "sr-Latn"} {
		v := testValues()
		v.Screen.Locale = locale
		if err := v.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, locale := range []string{"", "sr", "sr-Cyrl", "de", "en-US"} {
		v := testValues()
		v.Screen.Locale = locale
		if v.Validate() == nil {
			t.Fatalf("accepted %q", locale)
		}
	}
	s, _ := newStore(t, nil)
	d := report(s, testID('a'), "boot", "locale")
	d.AppliedLocale = "sr-Cyrl"
	if _, err := s.Sync(d); err == nil {
		t.Fatal("invalid heartbeat locale accepted")
	}
}

func TestLocaleV2MigrationAndRollbackExport(t *testing.T) {
	s, dir := newStore(t, nil)
	a := testID('a')
	register(t, s, a, "boot", "preferences")
	// Keep real structural state, including a pending network journal and secrets.
	state := clone(s.state)
	state.SharedRevision = 9007199254740993 // Beyond float64's exact integer range.
	p := state.Profiles[a]
	p.Network = map[string]string{"wifi_ssid": "isolated-test"}
	state.Profiles[a] = p
	original, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := ExportV2(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeStrict[diskState](v2)
	if err != nil || decoded.SharedRevision != state.SharedRevision {
		t.Fatal("rollback rounded revision")
	}
	var old map[string]any
	if err = json.Unmarshal(v2, &old); err != nil {
		t.Fatal(err)
	}
	if _, present := old["baseline"].(map[string]any)["screen"].(map[string]any)["locale"]; present {
		t.Fatal("locale leaked to rollback copy")
	}
	if err = os.WriteFile(filepath.Join(dir, "settings.json"), v2, 0600); err != nil {
		t.Fatal(err)
	}
	migrated, err := New(dir, testValues(), nil, "https://bridge.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state, migrated.state) {
		t.Fatal("migration changed non-locale state")
	}
	if migrated.state.Baseline.Screen.Locale != "ru" || migrated.state.Profiles[a].Settings.Screen.Locale != "ru" {
		t.Fatal("missing Russian defaults")
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := ExportV2(onDisk)
	if err != nil {
		t.Fatal(err)
	}
	var after map[string]any
	if err = json.Unmarshal(again, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(old, after) {
		t.Fatal("rollback lost unrelated state")
	}
	// Failed validation must leave the old input intact.
	old["profiles"].(map[string]any)[a].(map[string]any)["revision"] = float64(0)
	invalid, _ := json.Marshal(old)
	if err = os.WriteFile(filepath.Join(dir, "settings.json"), invalid, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = New(dir, testValues(), nil, "https://bridge.example.test", nil); err == nil {
		t.Fatal("invalid migration accepted")
	}
	untouched, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(untouched) != string(invalid) {
		t.Fatal("failed migration mutated file")
	}
}

func TestLegacyV1MissingLocaleMigration(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	legacy := legacyDiskState{Version: 1, BridgeID: testID('a'), Revision: 9, Settings: testValues(), Secrets: map[string]string{}}
	raw, _ := json.Marshal(legacy)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc["settings"].(map[string]any)["screen"].(map[string]any), "locale")
	raw, _ = json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(dir, testValues(), nil, "https://bridge.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.state.Version != 3 || s.state.Baseline.Screen.Locale != "ru" || s.state.Sequence != 9 || s.BridgeID() != legacy.BridgeID {
		t.Fatal("legacy migration lost state")
	}
}
