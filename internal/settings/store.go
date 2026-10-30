package settings

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

const MaxDevices = 8

var (
	ErrConflict                = errors.New("revision conflict")
	ErrUnavailable             = errors.New("device unavailable or action unsupported")
	ErrBusy                    = errors.New("a device command is pending")
	ErrDeviceNotFound          = errors.New("device not found")
	ErrConfirmationRequired    = errors.New("disconnect confirmation required")
	ErrTooManyDevices          = errors.New("device limit reached")
	ErrSharedURLMismatch       = errors.New("shared connection URLs are read-only in device settings")
	ErrSharedRevisionExhausted = errors.New("shared connection revision exhausted")
	ErrDeviceRevisionExhausted = errors.New("device revision sequence exhausted")
)

// Prepare stages complete runtime candidates keyed by "" for the legacy
// baseline and by stable device ID for each registered display.
type Prepare func(map[string]Values, map[string]string) (commit func(), abort func(), err error)

type profileState struct {
	Revision   uint64            `json:"revision"`
	Settings   Values            `json:"settings"`
	Network    map[string]string `json:"network,omitempty"`
	Command    *Command          `json:"command,omitempty"`
	OTAHistory []OTAAttempt      `json:"ota_history,omitempty"`
}

type archivedPending struct {
	Network map[string]string `json:"network,omitempty"`
	Command *Command          `json:"command,omitempty"`
}

type diskState struct {
	Version           int                     `json:"version"`
	BridgeID          string                  `json:"bridge_id"`
	Sequence          uint64                  `json:"sequence"`
	SharedRevision    uint64                  `json:"shared_revision"`
	Connections       Connections             `json:"connections"`
	Baseline          Values                  `json:"baseline"`
	SecretOverrides   map[string]string       `json:"secret_overrides"`
	Profiles          map[string]profileState `json:"profiles"`
	LegacyUnaddressed *archivedPending        `json:"legacy_unaddressed_pending,omitempty"`
}

type legacyDiskState struct {
	Version  int               `json:"version"`
	BridgeID string            `json:"bridge_id,omitempty"`
	Revision uint64            `json:"revision"`
	Settings Values            `json:"settings"`
	Secrets  map[string]string `json:"secrets"`
	Network  map[string]string `json:"network,omitempty"`
	Command  *Command          `json:"command,omitempty"`
}

type Store struct {
	mu       sync.Mutex
	path     string
	state    diskState
	defaults map[string]string
	devices  map[string]Device // Heartbeats and device-reported data are volatile.
	prepare  Prepare
	baseURL  string
}

func New(dir string, defaults Values, secrets map[string]string, baseURL string, prepare Prepare) (*Store, error) {
	if dir == "" {
		return nil, errors.New("settings state directory required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, errors.New("cannot create settings state directory")
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode().Perm()&0077 != 0 || dirInfo.Mode().Perm()&0700 != 0700 {
		return nil, errors.New("settings state directory must be private")
	}
	if defaults.Screen.Locale == "" {
		defaults.Screen.Locale = "ru"
	}
	if err := defaults.Validate(); err != nil {
		return nil, errors.New("invalid baseline settings")
	}
	if err := validateStoredState(secrets, nil, nil); err != nil {
		return nil, errors.New("invalid default credentials")
	}
	s := &Store{
		path: filepath.Join(dir, "settings.json"), defaults: clone(secrets),
		devices: make(map[string]Device), prepare: prepare,
		baseURL: strings.TrimSuffix(baseURL, "/"),
	}

	info, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		id, idErr := newBridgeID()
		if idErr != nil {
			return nil, errors.New("cannot create Bridge identity")
		}
		s.state = diskState{
			Version: 3, BridgeID: id, Sequence: 1, SharedRevision: 1,
			Connections: connectionsFromValues(defaults), Baseline: clone(defaults),
			SecretOverrides: map[string]string{}, Profiles: map[string]profileState{},
		}
		s.state.Baseline = withConnections(s.state.Baseline, s.state.Connections)
		if err := s.persist(s.state); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err != nil {
		return nil, errors.New("cannot inspect settings state")
	}
	if !info.Mode().IsRegular() || (info.Mode().Perm() != 0400 && info.Mode().Perm() != 0600) || info.Size() > 4<<20 {
		return nil, errors.New("settings state file must be private and bounded")
	}
	f, err := os.Open(s.path)
	if err != nil {
		return nil, errors.New("cannot read settings state")
	}
	defer f.Close()
	openedInfo, err := f.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || (openedInfo.Mode().Perm() != 0400 && openedInfo.Mode().Perm() != 0600) || openedInfo.Size() > 4<<20 || !os.SameFile(info, openedInfo) {
		return nil, errors.New("settings state file changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return nil, errors.New("invalid settings state")
	}
	var version struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &version); err != nil {
		return nil, errors.New("invalid settings state")
	}
	switch version.Version {
	case 1:
		legacy, err := decodeStrict[legacyDiskState](data)
		if err != nil || legacy.Revision == 0 {
			return nil, errors.New("invalid legacy settings state")
		}
		if legacy.Settings.Screen.Locale == "" {
			legacy.Settings.Screen.Locale = "ru"
		}
		if err := legacy.Settings.Validate(); err != nil {
			return nil, errors.New("invalid legacy settings state")
		}
		if err := validateStoredState(legacy.Secrets, legacy.Network, legacy.Command); err != nil {
			return nil, errors.New("invalid legacy settings state")
		}
		bridgeID := legacy.BridgeID
		if bridgeID == "" {
			bridgeID, err = newBridgeID()
			if err != nil {
				return nil, errors.New("cannot create Bridge identity")
			}
		}
		if !validBridgeID(bridgeID) {
			return nil, errors.New("invalid stored Bridge identity")
		}
		s.state = diskState{
			Version: 3, BridgeID: bridgeID, Sequence: legacy.Revision, SharedRevision: 1,
			Connections: connectionsFromValues(legacy.Settings), Baseline: withConnections(clone(legacy.Settings), connectionsFromValues(legacy.Settings)),
			SecretOverrides: clone(legacy.Secrets), Profiles: map[string]profileState{},
			LegacyUnaddressed: &archivedPending{Network: clone(legacy.Network), Command: clone(legacy.Command)},
		}
		if s.state.Sequence == 0 {
			s.state.Sequence = 1
		}
		if s.state.SecretOverrides == nil {
			s.state.SecretOverrides = map[string]string{}
		}
	case 2, 3:
		state, err := decodeStrict[diskState](data)
		if err != nil {
			return nil, errors.New("invalid settings state")
		}
		if version.Version == 2 {
			state.Version = 3
			if state.Baseline.Screen.Locale == "" {
				state.Baseline.Screen.Locale = "ru"
			}
			for id, p := range state.Profiles {
				if p.Settings.Screen.Locale == "" {
					p.Settings.Screen.Locale = "ru"
				}
				state.Profiles[id] = p
			}
		}
		s.state = state
	default:
		return nil, errors.New("unsupported settings state version")
	}
	if err := s.validateState(s.state); err != nil {
		return nil, err
	}
	if version.Version != 3 {
		if err := s.persist(s.state); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func decodeStrict[T any](data []byte) (T, error) {
	var result T
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&result); err != nil {
		return result, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return result, errors.New("trailing data")
	}
	return result, nil
}

func newBridgeID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func (s *Store) BridgeID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.BridgeID
}

func (s *Store) validateState(st diskState) error {
	if st.Version != 3 || !validBridgeID(st.BridgeID) || st.Sequence == 0 || st.SharedRevision == 0 || st.Profiles == nil || st.SecretOverrides == nil || len(st.Profiles) > MaxDevices {
		return errors.New("invalid settings state")
	}
	if err := validateConnections(st.Connections); err != nil {
		return errors.New("invalid shared connections")
	}
	if err := withConnections(st.Baseline, st.Connections).Validate(); err != nil {
		return errors.New("invalid baseline settings")
	}
	if err := validateStoredState(st.SecretOverrides, nil, nil); err != nil {
		return errors.New("invalid shared credentials")
	}
	for id, profile := range st.Profiles {
		if !validBridgeID(id) || profile.Revision == 0 || profile.Revision > st.Sequence {
			return errors.New("invalid device profile")
		}
		if err := profile.Settings.Validate(); err != nil {
			return errors.New("invalid device settings")
		}
		if !sameSharedURLs(profile.Settings, st.Connections) {
			return ErrSharedURLMismatch
		}
		if len(profile.OTAHistory) > 100 {
			return errors.New("invalid OTA history")
		}
		seenHistory := map[string]bool{}
		for _, h := range profile.OTAHistory {
			if !validCommandID(h.ID) || seenHistory[h.ID] || !cleanText(h.Version, 32) || !cleanText(h.PreviousVersion, 32) || !cleanText(h.InstalledVersion, 32) || h.At.IsZero() || !slices.Contains([]string{"queued", "accepted", "succeeded", "failed", "unknown"}, h.State) || (h.Stage != "" && !slices.Contains([]string{"checking", "downloading", "verifying", "writing", "rebooting"}, h.Stage)) || (h.Error != "" && !slices.Contains([]string{"ota_check_failed", "no_update", "download_failed", "validation_failed", "flash_failed", "device_error", "unknown"}, h.Error)) {
				return errors.New("invalid OTA history")
			}
			seenHistory[h.ID] = true
		}
		if err := validateStoredState(nil, profile.Network, profile.Command); err != nil {
			return errors.New("invalid device state")
		}
	}
	if p := st.LegacyUnaddressed; p != nil {
		if err := validateStoredState(nil, p.Network, p.Command); err != nil {
			return errors.New("invalid archived legacy state")
		}
	}
	return nil
}

func clone[T any](v T) T {
	b, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(b, &out)
	return out
}

func (s *Store) persist(state diskState) error {
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return errors.New("cannot encode settings")
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".settings-*")
	if err != nil {
		return errors.New("cannot write settings")
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return errors.New("cannot flush settings")
	}
	if err = os.Rename(name, s.path); err != nil {
		return errors.New("cannot commit settings")
	}
	if dir, openErr := os.Open(filepath.Dir(s.path)); openErr == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func connectionsFromValues(v Values) Connections {
	return Connections{
		Immich:    ConnectionEndpoint{BaseURL: v.Photos.BaseURL},
		Health:    ConnectionEndpoint{BaseURL: v.Services["health"].BaseURL},
		News:      ConnectionEndpoint{BaseURL: v.Services["news"].BaseURL},
		Home:      ConnectionEndpoint{BaseURL: v.Services["home"].BaseURL},
		Calendar:  ConnectionEndpoint{BaseURL: v.Services["calendar"].BaseURL},
		Transport: ConnectionEndpoint{BaseURL: v.Services["transport"].BaseURL},
	}
}

func withConnections(v Values, c Connections) Values {
	v = clone(v)
	v.Photos.BaseURL = c.Immich.BaseURL
	for name, endpoint := range map[string]ConnectionEndpoint{
		"health": c.Health, "news": c.News, "home": c.Home,
		"calendar": c.Calendar, "transport": c.Transport,
	} {
		svc := v.Services[name]
		svc.BaseURL = endpoint.BaseURL
		v.Services[name] = svc
	}
	return v
}

func sameSharedURLs(v Values, c Connections) bool {
	return v.Photos.BaseURL == c.Immich.BaseURL &&
		v.Services["health"].BaseURL == c.Health.BaseURL &&
		v.Services["news"].BaseURL == c.News.BaseURL &&
		v.Services["home"].BaseURL == c.Home.BaseURL &&
		v.Services["calendar"].BaseURL == c.Calendar.BaseURL &&
		v.Services["transport"].BaseURL == c.Transport.BaseURL
}

func validateConnections(c Connections) error {
	for _, endpoint := range []ConnectionEndpoint{c.Immich, c.Health, c.News, c.Home, c.Calendar, c.Transport} {
		if !validURL(endpoint.BaseURL) {
			return errors.New("invalid connection URL")
		}
	}
	return nil
}

func (s *Store) projectedValues(st diskState) map[string]Values {
	values := make(map[string]Values, len(st.Profiles)+1)
	values[""] = withConnections(st.Baseline, st.Connections)
	for id, profile := range st.Profiles {
		values[id] = withConnections(profile.Settings, st.Connections)
	}
	return values
}

func (s *Store) effectiveSecrets(st diskState) map[string]string {
	secrets := clone(s.defaults)
	if secrets == nil {
		secrets = make(map[string]string)
	}
	for name, value := range st.SecretOverrides {
		secrets[name] = value
	}
	return secrets
}

// Snapshot returns the legacy baseline under key "" and all persisted device
// profiles, each projected with the currently shared connection URLs.
func (s *Store) Snapshot() (map[string]Values, map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.projectedValues(s.state), s.effectiveSecrets(s.state)
}

func (s *Store) sequenceNext(st *diskState) (uint64, error) {
	if st.Sequence == ^uint64(0) {
		return 0, ErrDeviceRevisionExhausted
	}
	st.Sequence++
	return st.Sequence, nil
}

func (s *Store) candidate(st diskState) (func(), func(), error) {
	if s.prepare == nil {
		return func() {}, func() {}, nil
	}
	commit, abort, err := s.prepare(s.projectedValues(st), s.effectiveSecrets(st))
	if commit == nil {
		commit = func() {}
	}
	if abort == nil {
		abort = func() {}
	}
	return commit, abort, err
}

func (s *Store) Update(id string, revision uint64, p Patch) error {
	if !validBridgeID(id) {
		return ErrDeviceNotFound
	}
	if err := validateSecrets(p.NetworkCredentials, true); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.state.Profiles[id]
	if !ok {
		return ErrDeviceNotFound
	}
	if profile.Revision != revision {
		return ErrConflict
	}
	// Older clients omit the field; preserve the current device language.
	if p.Settings.Screen.Locale == "" {
		p.Settings.Screen.Locale = profile.Settings.Screen.Locale
	}
	if err := p.Settings.Validate(); err != nil {
		return err
	}
	if p.Settings.Screen.Locale != profile.Settings.Screen.Locale && !slices.Contains(s.devices[id].Capabilities, "locale") {
		return ErrUnavailable
	}
	oldHome, newHome := profile.Settings.Services["home"], p.Settings.Services["home"]
	if !slices.Equal(oldHome.Favorites, newHome.Favorites) && !slices.Contains(s.devices[id].Capabilities, "home_favorites") {
		return ErrUnavailable
	}
	for _, f := range newHome.Favorites {
		allowed := newHome.Lights
		if f.Kind == "climate" {
			allowed = newHome.Climates
		}
		if !slices.Contains(allowed, f.EntityID) && !slices.Contains(oldHome.Favorites, f) {
			return errors.New("favorite not configured")
		}
	}
	if (!equalIntPtr(p.Settings.Screen.BacklightPercent, profile.Settings.Screen.BacklightPercent) || !equalIntPtr(p.Settings.Screen.NightBacklightPercent, profile.Settings.Screen.NightBacklightPercent) || p.Settings.Screen.NightAction != profile.Settings.Screen.NightAction) && !slices.Contains(s.devices[id].Capabilities, "backlight_brightness") {
		return ErrUnavailable
	}
	if !sameSharedURLs(p.Settings, s.state.Connections) {
		return ErrSharedURLMismatch
	}
	if p.Settings.Photos.Source == "fixture" && profile.Settings.Photos.Source != "fixture" {
		return errors.New("fixture source is not available")
	}
	bridgeURLChanged := p.Settings.Network.BridgeURL != profile.Settings.Network.BridgeURL
	if (bridgeURLChanged || len(p.NetworkCredentials) > 0) && (!s.online(s.devices[id]) || !slices.Contains(s.devices[id].Capabilities, "network")) {
		return ErrUnavailable
	}
	next := clone(s.state)
	nextProfile := next.Profiles[id]
	nextProfile.Settings = clone(p.Settings)
	if bridgeURLChanged || len(p.NetworkCredentials) > 0 {
		if nextProfile.Network == nil {
			nextProfile.Network = map[string]string{}
		}
		nextProfile.Network["bridge_url"] = p.Settings.Network.BridgeURL
		for key, value := range p.NetworkCredentials {
			if value == nil {
				nextProfile.Network[key] = ""
			} else if *value != "" {
				nextProfile.Network[key] = *value
			}
		}
	}
	revision, err := s.sequenceNext(&next)
	if err != nil {
		return err
	}
	nextProfile.Revision = revision
	next.Profiles[id] = nextProfile
	if err := s.validateState(next); err != nil {
		return err
	}
	commit, abort, err := s.candidate(next)
	if err != nil {
		abort()
		return err
	}
	if err := s.persist(next); err != nil {
		abort()
		return err
	}
	s.state = next
	commit()
	return nil
}

func (s *Store) online(d Device) bool {
	if d.LastSeen == nil {
		return false
	}
	age := time.Since(*d.LastSeen)
	return age >= 0 && age < 45*time.Second
}

func safeError(v string) string {
	if v == "" {
		return ""
	}
	switch v {
	case "apply_failed", "network_failed", "nvs_write_failed", "ota_failed", "expired", "unsupported", "interrupted":
		return v
	default:
		return "device_error"
	}
}

func secretPresence(secrets map[string]string) map[string]bool {
	present := make(map[string]bool, len(secretNames))
	for name := range secretNames {
		present[name] = secrets[name] != ""
	}
	return present
}

func (s *Store) ConnectionsView() ConnectionsView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ConnectionsView{Revision: s.state.SharedRevision, Connections: clone(s.state.Connections), SecretsPresent: secretPresence(s.effectiveSecrets(s.state))}
}

func (s *Store) Devices() DevicesList {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.state.Profiles))
	for id := range s.state.Profiles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := DevicesList{Devices: make([]DeviceSummary, 0, len(ids)), MaxDevices: MaxDevices}
	for _, id := range ids {
		profile := s.state.Profiles[id]
		device := clone(s.devices[id])
		device.Connected = s.online(device)
		device.AppliedRevision = appliedRevision(device, profile)
		result.Devices = append(result.Devices, DeviceSummary{
			ID: id, Name: profile.Settings.Screen.Name, Connected: device.Connected,
			LastSeen: clone(device.LastSeen), AppliedRevision: device.AppliedRevision,
			DesiredRevision: profile.Revision, Firmware: device.Firmware, IP: device.IP, SSID: device.SSID,
		})
	}
	return result
}

// Legacy firmware only renders Russian. Never project a matching revision as
// applied when the reported renderer does not match the desired language.
func equalIntPtr(a, b *int) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func appliedRevision(d Device, p profileState) uint64 {
	if len(p.Settings.Services["home"].Favorites) > 0 && !slices.Contains(d.Capabilities, "home_favorites") {
		return 0
	}
	if (p.Settings.Screen.BacklightPercent != nil || p.Settings.Screen.NightBacklightPercent != nil || p.Settings.Screen.NightAction == "dim") && !slices.Contains(d.Capabilities, "backlight_brightness") {
		return 0
	}
	matches := d.AppliedLocale == p.Settings.Screen.Locale && slices.Contains(d.Capabilities, "locale")
	if !slices.Contains(d.Capabilities, "locale") {
		matches = p.Settings.Screen.Locale == "ru"
	}
	if !matches {
		return 0
	}
	return d.AppliedRevision
}

func (s *Store) HasDevice(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.state.Profiles[id]
	return ok
}

func (s *Store) View(id string, statuses map[string]Status) (Envelope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.state.Profiles[id]
	if !ok {
		return Envelope{}, ErrDeviceNotFound
	}
	settings := withConnections(profile.Settings, s.state.Connections)
	device := clone(s.devices[id])
	device.DeviceID = ""
	device.BridgeID = ""
	device.Connected = s.online(device)
	device.AppliedRevision = appliedRevision(device, profile)
	if device.Capabilities == nil {
		device.Capabilities = []string{}
	}
	visible := map[string]bool{}
	for name, service := range settings.Services {
		visible[name] = service.Enabled
	}
	return Envelope{
		DeviceID: id, Revision: profile.Revision, Settings: settings,
		SecretsPresent: secretPresence(s.effectiveSecrets(s.state)),
		Device:         DeviceView{Device: device, Command: clone(profile.Command)},
		Statuses:       clone(statuses), Capabilities: map[string]any{"brightness": slices.Contains(device.Capabilities, "backlight_brightness"), "google_photos": false, "locales": []string{"ru", "en", "sr-Latn"}, "timezones": []string{"Europe/Belgrade", "UTC"}},
	}, nil
}

func (s *Store) UpdateConnections(revision uint64, p ConnectionPatch) error {
	if err := validateConnections(p.Connections); err != nil {
		return err
	}
	if err := validateSecrets(p.Secrets, false); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if revision != s.state.SharedRevision {
		return ErrConflict
	}
	if s.state.SharedRevision == ^uint64(0) {
		return ErrSharedRevisionExhausted
	}
	next := clone(s.state)
	oldSecrets := s.effectiveSecrets(s.state)
	nextSecrets := s.effectiveSecrets(s.state)
	for key, value := range p.Secrets {
		if value == nil {
			next.SecretOverrides[key] = ""
			nextSecrets[key] = ""
		} else if *value != "" {
			next.SecretOverrides[key] = *value
			nextSecrets[key] = *value
		}
	}
	if p.Connections == s.state.Connections && equalStringMap(next.SecretOverrides, s.state.SecretOverrides) {
		return nil
	}
	removed := removedConnections(s.state.Connections, p.Connections, oldSecrets, nextSecrets)
	if removalInUse(s.state, removed) && !p.ConfirmDisconnect {
		return ErrConfirmationRequired
	}
	if p.ConfirmDisconnect {
		disableRemovedSelections(&next, removed)
	}
	next.Connections = clone(p.Connections)
	next.Baseline = withConnections(next.Baseline, next.Connections)
	if _, err := s.sequenceNext(&next); err != nil {
		return err
	}
	for id, profile := range next.Profiles {
		profile.Settings = withConnections(profile.Settings, next.Connections)
		if p.ConfirmDisconnect {
			disableRemovedSelectionsFromValues(&profile.Settings, removed)
		}
		revision, err := s.sequenceNext(&next)
		if err != nil {
			return err
		}
		profile.Revision = revision
		next.Profiles[id] = profile
	}
	next.SharedRevision++
	if err := s.validateState(next); err != nil {
		return err
	}
	commit, abort, err := s.candidate(next)
	if err != nil {
		abort()
		return err
	}
	if err := s.persist(next); err != nil {
		abort()
		return err
	}
	s.state = next
	commit()
	return nil
}

func equalStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

type disconnectSet struct{ immich, health, tasks, home, calendar, transport, news bool }

func removedConnections(old, next Connections, oldSecrets, nextSecrets map[string]string) disconnectSet {
	return disconnectSet{
		immich:    (old.Immich.BaseURL != "" && next.Immich.BaseURL == "") || (oldSecrets["immich_api_key"] != "" && nextSecrets["immich_api_key"] == ""),
		health:    (old.Health.BaseURL != "" && next.Health.BaseURL == "") || (oldSecrets["health_api_key"] != "" && nextSecrets["health_api_key"] == ""),
		tasks:     oldSecrets["todoist_token"] != "" && nextSecrets["todoist_token"] == "",
		home:      (old.Home.BaseURL != "" && next.Home.BaseURL == "") || (oldSecrets["ha_token"] != "" && nextSecrets["ha_token"] == ""),
		calendar:  (old.Calendar.BaseURL != "" && next.Calendar.BaseURL == "") || (oldSecrets["calendar_platform_token"] != "" && nextSecrets["calendar_platform_token"] == ""),
		transport: old.Transport.BaseURL != "" && next.Transport.BaseURL == "",
		news:      old.News.BaseURL != "" && next.News.BaseURL == "",
	}
}

func valuesUse(v Values, removed disconnectSet) bool {
	return (removed.immich && v.Photos.Enabled && v.Photos.Source == "immich") ||
		(removed.health && v.Services["health"].Enabled) || (removed.tasks && v.Services["tasks"].Enabled) ||
		(removed.home && v.Services["home"].Enabled) || (removed.calendar && v.Services["calendar"].Enabled) ||
		(removed.transport && v.Services["transport"].Enabled) || (removed.news && v.Services["news"].Enabled)
}

func removalInUse(st diskState, removed disconnectSet) bool {
	if valuesUse(st.Baseline, removed) {
		return true
	}
	for _, profile := range st.Profiles {
		if valuesUse(profile.Settings, removed) {
			return true
		}
	}
	return false
}

func disableRemovedSelectionsFromValues(v *Values, removed disconnectSet) {
	if removed.immich && v.Photos.Source == "immich" {
		v.Photos.Enabled = false
		v.Photos.Source = "none"
		v.Photos.AlbumID = ""
	}
	for name, disconnect := range map[string]bool{"health": removed.health, "tasks": removed.tasks, "home": removed.home, "calendar": removed.calendar, "transport": removed.transport, "news": removed.news} {
		if !disconnect {
			continue
		}
		service := v.Services[name]
		service.Enabled = false
		if name == "calendar" || name == "transport" {
			service.IDs = []string{}
		}
		if name == "home" {
			service.Sensors = []string{}
			service.Lights = []string{}
			service.Climates = []string{}
		}
		v.Services[name] = service
	}
}

func disableRemovedSelections(st *diskState, removed disconnectSet) {
	disableRemovedSelectionsFromValues(&st.Baseline, removed)
	for id, profile := range st.Profiles {
		disableRemovedSelectionsFromValues(&profile.Settings, removed)
		st.Profiles[id] = profile
	}
}

func (s *Store) Sync(d Device) (SyncResponse, error) {
	if d.BacklightPercent != nil && (*d.BacklightPercent < 0 || *d.BacklightPercent > 100) {
		return SyncResponse{}, errors.New("invalid backlight")
	}
	if d.BacklightError != "" && !slices.Contains([]string{"unsupported", "unavailable", "read_failed", "write_failed", "uncalibrated", "unknown"}, d.BacklightError) {
		return SyncResponse{}, errors.New("invalid backlight error")
	}
	if d.OTAAttemptID != "" && !validCommandID(d.OTAAttemptID) {
		return SyncResponse{}, errors.New("invalid OTA attempt")
	}
	if d.OTAStage != "" && !slices.Contains([]string{"checking", "downloading", "verifying", "writing", "rebooting"}, d.OTAStage) {
		return SyncResponse{}, errors.New("invalid OTA stage")
	}
	if d.OTAError != "" && !slices.Contains([]string{"ota_check_failed", "no_update", "download_failed", "validation_failed", "flash_failed", "device_error", "unknown"}, d.OTAError) {
		return SyncResponse{}, errors.New("invalid OTA error")
	}
	if d.AppliedLocale != "" && !validLocale(d.AppliedLocale) {
		return SyncResponse{}, errors.New("invalid applied locale")
	}
	if !validBridgeID(d.DeviceID) || d.Firmware == "" || d.BootID == "" || len(d.Firmware) > 32 || len(d.BootID) > 64 || len(d.BridgeID) > 32 || len(d.SSID) > 32 || len(d.IP) > 64 || len(d.Capabilities) > 16 || len(d.Model) > 80 || len(d.AvailableVersion) > 32 || d.Width < 0 || d.Width > 10000 || d.Height < 0 || d.Height > 10000 || d.OTAProgress < 0 || d.OTAProgress > 100 {
		return SyncResponse{}, errors.New("invalid device report")
	}
	if !cleanText(d.Firmware, 32) || !cleanText(d.BootID, 64) || (d.BridgeID != "" && !validBridgeID(d.BridgeID)) || !cleanText(d.SSID, 32) || !cleanText(d.IP, 64) || !cleanText(d.Model, 80) || !cleanText(d.AvailableVersion, 32) || !validIDs(d.Capabilities, 16) {
		return SyncResponse{}, errors.New("invalid device report")
	}
	if d.CommandResult != nil {
		if !validCommandID(d.CommandResult.ID) || !cleanText(d.CommandResult.Error, 128) {
			return SyncResponse{}, errors.New("invalid device report")
		}
		switch d.CommandResult.State {
		case "accepted", "succeeded", "failed":
		default:
			return SyncResponse{}, errors.New("invalid device report")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := clone(s.state)
	profile, registered := next.Profiles[d.DeviceID]
	newProfile := !registered
	if newProfile {
		if len(next.Profiles) >= MaxDevices {
			return SyncResponse{}, ErrTooManyDevices
		}
		revision, err := s.sequenceNext(&next)
		if err != nil {
			return SyncResponse{}, err
		}
		profile = profileState{Revision: revision, Settings: clone(next.Baseline), Network: map[string]string{}}
		profile.Settings = withConnections(profile.Settings, next.Connections)
		profile.Settings.Screen.Name = "Display " + d.DeviceID[len(d.DeviceID)-6:]
		next.Profiles[d.DeviceID] = profile
	}
	scopeMatches := d.BridgeID == next.BridgeID
	if scopeMatches && d.AppliedRevision > profile.Revision {
		return SyncResponse{}, errors.New("invalid applied revision")
	}
	if !scopeMatches {
		d.AppliedRevision = 0
		d.CommandResult = nil
	}
	changed := newProfile
	if scopeMatches && profile.Command != nil && (profile.Command.State == "queued" || profile.Command.State == "accepted") {
		cmd := profile.Command
		resultMatches := d.CommandResult != nil && d.CommandResult.ID == cmd.ID
		finalResult := resultMatches && (d.CommandResult.State == "succeeded" || d.CommandResult.State == "failed")
		switch {
		case finalResult && d.BootID != cmd.BootID:
			cmd.State, cmd.Error, changed = d.CommandResult.State, safeError(d.CommandResult.Error), true
		case d.BootID != cmd.BootID:
			cmd.State, cmd.Error, changed = "interrupted", "interrupted", true
		case time.Now().After(cmd.ExpiresAt):
			cmd.State, cmd.Error, changed = "expired", "expired", true
		case resultMatches:
			transition := (cmd.State == "queued" && d.CommandResult.State == "accepted") || ((cmd.State == "accepted" || cmd.State == "queued") && finalResult)
			if transition {
				cmd.State, cmd.Error, changed = d.CommandResult.State, safeError(d.CommandResult.Error), true
			}
		}
	}
	if scopeMatches && profile.Command != nil && profile.Command.Action == "ota_install" && profile.Command.State == "succeeded" && profile.Command.TargetVersion != "" && profile.Command.TargetVersion != d.Firmware {
		profile.Command.State = "unknown"
		profile.Command.Error = ""
		changed = true
	}
	if scopeMatches {
		now := time.Now().UTC()
		for i := range profile.OTAHistory {
			h := &profile.OTAHistory[i]
			prior := *h
			if profile.Command != nil && profile.Command.ID == h.ID {
				switch profile.Command.State {
				case "accepted":
					if h.State == "queued" {
						h.State = "accepted"
					}
				case "expired", "interrupted":
					if h.State != "succeeded" && h.State != "failed" {
						h.State = "unknown"
					}
				}
			}
			if d.CommandResult != nil && d.CommandResult.ID == h.ID && h.State != "succeeded" && h.State != "failed" {
				switch d.CommandResult.State {
				case "accepted":
					if h.State == "queued" {
						h.State = "accepted"
					}
				case "succeeded":
					if h.Version == "" || h.Version == d.Firmware {
						h.State = "succeeded"
						h.InstalledVersion = d.Firmware
						h.Error = ""
					} else {
						h.State = "unknown"
					}
				case "failed":
					h.State = "failed"
					h.Error = "device_error"
				}
			}
			if d.OTAAttemptID == h.ID && h.State != "succeeded" && (h.State != "failed" || prior.State != "failed") {
				if d.OTAStage != "" {
					h.Stage = d.OTAStage
				}
				if d.OTAError != "" && h.State == "failed" {
					h.Error = d.OTAError
				}
			}
			if *h != prior {
				h.UpdatedAt = &now
				changed = true
			}
		}
	}
	if scopeMatches && d.AppliedRevision == profile.Revision && slices.Contains(d.Capabilities, "network") && len(profile.Network) > 0 {
		profile.Network = nil
		changed = true
	}
	if changed {
		next.Profiles[d.DeviceID] = profile
		if err := s.validateState(next); err != nil {
			return SyncResponse{}, err
		}
		if newProfile {
			commit, abort, err := s.candidate(next)
			if err != nil {
				abort()
				return SyncResponse{}, err
			}
			if err := s.persist(next); err != nil {
				abort()
				return SyncResponse{}, err
			}
			s.state = next
			commit()
		} else {
			if err := s.persist(next); err != nil {
				return SyncResponse{}, err
			}
			s.state = next
		}
	}
	now := time.Now().UTC()
	d.LastSeen = &now
	d.Error = safeError(d.Error)
	d.CommandResult = nil
	s.devices[d.DeviceID] = clone(d)
	profile = s.state.Profiles[d.DeviceID]
	values := withConnections(profile.Settings, s.state.Connections)
	visible := make(map[string]bool, len(values.Services))
	for name, service := range values.Services {
		visible[name] = service.Enabled
	}
	tz := "UTC0"
	if values.Screen.Timezone == "Europe/Belgrade" {
		tz = "CET-1CEST,M3.5.0,M10.5.0/3"
	}
	response := SyncResponse{
		DeviceID: d.DeviceID, BridgeID: s.state.BridgeID, Revision: profile.Revision,
		Preferences: Preferences{Screen: values.Screen, Home: HomePreferences{Favorites: append([]Favorite{}, values.Services["home"].Favorites...)}, Photos: values.Photos, Visible: visible, POSIXTimezone: tz, SettingsURL: s.baseURL + "/settings/?device_id=" + d.DeviceID},
	}
	if (!scopeMatches || d.AppliedRevision != profile.Revision) && slices.Contains(d.Capabilities, "network") && len(profile.Network) > 0 {
		response.Preferences.Network = clone(profile.Network)
	}
	if scopeMatches && profile.Command != nil && profile.Command.State == "queued" {
		response.Command = clone(profile.Command)
	}
	return response, nil
}

func (s *Store) Enqueue(id, action string, confirm bool) (*Command, error) {
	return s.EnqueueOptions(id, action, confirm, nil, "")
}
func (s *Store) EnqueueOptions(id, action string, confirm bool, percent *int, targetVersion string) (*Command, error) {
	if action == "backlight_preview" {
		if percent == nil || *percent < 10 || *percent > 100 {
			return nil, errors.New("invalid percent")
		}
	} else if percent != nil {
		return nil, errors.New("unexpected percent")
	}
	if !cleanText(targetVersion, 32) || (action != "ota_install" && targetVersion != "") {
		return nil, errors.New("invalid target version")
	}
	if !validBridgeID(id) {
		return nil, ErrDeviceNotFound
	}
	switch action {
	case "photo_start", "ota_check", "backlight_preview", "backlight_restore":
	case "restart", "ota_install", "reset":
		if !confirm {
			return nil, errors.New("confirmation required")
		}
	default:
		return nil, errors.New("unsupported action")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.state.Profiles[id]
	if !ok {
		return nil, ErrDeviceNotFound
	}
	profile = clone(profile)
	device := s.devices[id]
	if !s.online(device) || !slices.Contains(device.Capabilities, action) {
		return nil, ErrUnavailable
	}
	if c := profile.Command; c != nil && (c.State == "queued" || c.State == "accepted") && time.Now().Before(c.ExpiresAt) {
		return nil, ErrBusy
	}
	if c := profile.Command; c != nil && c.Action == "ota_install" && (c.State == "queued" || c.State == "accepted") && time.Now().After(c.ExpiresAt) {
		for i := range profile.OTAHistory {
			if profile.OTAHistory[i].ID == c.ID {
				now := time.Now().UTC()
				profile.OTAHistory[i].State = "unknown"
				profile.OTAHistory[i].UpdatedAt = &now
			}
		}
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, errors.New("cannot create command")
	}
	now := time.Now().UTC()
	timeout := 2 * time.Minute
	if action == "ota_install" {
		timeout = 10 * time.Minute
	}
	profile.Command = &Command{ID: hex.EncodeToString(raw[:]), Action: action, State: "queued", CreatedAt: now, ExpiresAt: now.Add(timeout), BootID: device.BootID, Percent: percent, TargetVersion: targetVersion}
	if action == "ota_install" {
		profile.OTAHistory = append(profile.OTAHistory, OTAAttempt{ID: profile.Command.ID, Version: targetVersion, PreviousVersion: device.Firmware, State: "queued", At: now})
		if len(profile.OTAHistory) > 100 {
			profile.OTAHistory = profile.OTAHistory[len(profile.OTAHistory)-100:]
		}
	}
	next := clone(s.state)
	next.Profiles[id] = profile
	if err := s.persist(next); err != nil {
		return nil, err
	}
	s.state = next
	return clone(profile.Command), nil
}

// OTAHistory returns a newest-first copy of the persisted attempt history.
func (s *Store) OTAHistory(id string) ([]OTAAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.state.Profiles[id]
	if !ok {
		return nil, ErrDeviceNotFound
	}
	if c := p.Command; c != nil && c.Action == "ota_install" && (c.State == "queued" || c.State == "accepted") && time.Now().After(c.ExpiresAt) {
		next := clone(s.state)
		p = next.Profiles[id]
		for i := range p.OTAHistory {
			if p.OTAHistory[i].ID == c.ID {
				now := time.Now().UTC()
				p.OTAHistory[i].State = "unknown"
				p.OTAHistory[i].UpdatedAt = &now
			}
		}
		p.Command.State = "expired"
		p.Command.Error = "expired"
		next.Profiles[id] = p
		if err := s.persist(next); err != nil {
			return nil, err
		}
		s.state = next
	}
	result := clone(p.OTAHistory)
	slices.Reverse(result)
	if result == nil {
		result = []OTAAttempt{}
	}
	return result, nil
}
