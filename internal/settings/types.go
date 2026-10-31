package settings

import (
	"esp32-bridge/internal/config"
	"strconv"
	"strings"
	"time"
)

type Screen struct {
	Locale                string `json:"locale"`
	Name                  string `json:"name"`
	City                  string `json:"city"`
	Timezone              string `json:"timezone"`
	ClockFormat           string `json:"clock_format"`
	IdleAction            string `json:"idle_action"`
	NightEnabled          bool   `json:"night_enabled"`
	NightStart            string `json:"night_start"`
	NightEnd              string `json:"night_end"`
	AutoUpdateCheck       bool   `json:"auto_update_check"`
	AutoInstall           bool   `json:"auto_install"`
	BacklightPercent      *int   `json:"backlight_percent,omitempty"`
	NightAction           string `json:"night_action,omitempty"`
	NightBacklightPercent *int   `json:"night_backlight_percent,omitempty"`
}
type Photos struct {
	ShowClock        *bool  `json:"show_clock,omitempty"`
	ShowWeather      *bool  `json:"show_weather,omitempty"`
	ShowEvent        *bool  `json:"show_event,omitempty"`
	Enabled          bool   `json:"enabled"`
	Source           string `json:"source"`
	BaseURL          string `json:"base_url"`
	AlbumID          string `json:"album_id"`
	IdleSeconds      int    `json:"idle_seconds"`
	IntervalSeconds  int    `json:"interval_seconds"`
	Order            string `json:"order"`
	Layout           string `json:"layout"`
	CropLimitPercent int    `json:"crop_limit_percent"`
}
type Favorite struct {
	EntityID string `json:"entity_id"`
	Kind     string `json:"kind"`
	Label    string `json:"label,omitempty"`
}
type Service struct {
	Favorites  []Favorite `json:"favorites,omitempty"`
	Enabled    bool       `json:"enabled"`
	BaseURL    string     `json:"base_url"`
	IDs        []string   `json:"ids"`
	Timezone   string     `json:"timezone"`
	Latitude   float64    `json:"latitude"`
	Longitude  float64    `json:"longitude"`
	Limit      int        `json:"limit"`
	SinceHours int        `json:"since_hours"`
	Sensors    []string   `json:"sensors"`
	Lights     []string   `json:"lights"`
	Climates   []string   `json:"climates"`
}
type Network struct {
	BridgeURL string `json:"bridge_url"`
}

type ConnectionEndpoint struct {
	BaseURL string `json:"base_url"`
}

// Connections are shared by all managed displays. Credential values are never
// part of this projection; they are write-only through ConnectionPatch.Secrets.
type Connections struct {
	Immich    ConnectionEndpoint `json:"immich"`
	Health    ConnectionEndpoint `json:"health"`
	Tasks     struct{}           `json:"tasks"`
	News      ConnectionEndpoint `json:"news"`
	Home      ConnectionEndpoint `json:"home"`
	Calendar  ConnectionEndpoint `json:"calendar"`
	Transport ConnectionEndpoint `json:"transport"`
}

type Values struct {
	Screen   Screen             `json:"screen"`
	Photos   Photos             `json:"photos"`
	Services map[string]Service `json:"services"`
	Network  Network            `json:"network"`
}
type Command struct {
	ID            string    `json:"id"`
	Action        string    `json:"action"`
	State         string    `json:"state"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	Error         string    `json:"error,omitempty"`
	BootID        string    `json:"boot_id,omitempty"`
	Percent       *int      `json:"percent,omitempty"`
	TargetVersion string    `json:"target_version,omitempty"`
}
type OTAAttempt struct {
	ID               string     `json:"id"`
	Version          string     `json:"version"`
	PreviousVersion  string     `json:"previous_version,omitempty"`
	InstalledVersion string     `json:"installed_version,omitempty"`
	State            string     `json:"state"`
	At               time.Time  `json:"at"`
	UpdatedAt        *time.Time `json:"updated_at,omitempty"`
	Stage            string     `json:"stage,omitempty"`
	Error            string     `json:"error,omitempty"`
}
type Result struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}
type Device struct {
	BacklightReport  *BacklightReport `json:"backlight_report,omitempty"`
	DeviceID         string           `json:"device_id,omitempty"`
	Connected        bool             `json:"connected"`
	BridgeID         string           `json:"bridge_id,omitempty"`
	LastSeen         *time.Time       `json:"last_seen"`
	AppliedRevision  uint64           `json:"applied_revision"`
	AppliedLocale    string           `json:"applied_locale,omitempty"`
	Firmware         string           `json:"firmware"`
	BootID           string           `json:"boot_id"`
	IP               string           `json:"ip"`
	SSID             string           `json:"ssid"`
	UptimeSeconds    uint64           `json:"uptime_seconds"`
	TimeSynced       bool             `json:"time_synced"`
	Capabilities     []string         `json:"capabilities"`
	Error            string           `json:"error"`
	CommandResult    *Result          `json:"command_result,omitempty"`
	AvailableVersion string           `json:"available_version,omitempty"`
	OTAProgress      int              `json:"ota_progress,omitempty"`
	OTAAttemptID     string           `json:"ota_attempt_id,omitempty"`
	OTAStage         string           `json:"ota_stage,omitempty"`
	OTAError         string           `json:"ota_error,omitempty"`
	BacklightPercent *int             `json:"backlight_percent,omitempty"`
	BacklightError   string           `json:"backlight_error,omitempty"`
	Model            string           `json:"model,omitempty"`
	Width            int              `json:"width,omitempty"`
	Height           int              `json:"height,omitempty"`
}
type Status struct {
	State       string     `json:"state"`
	LastSuccess *time.Time `json:"last_success,omitempty"`
	Error       string     `json:"error,omitempty"`
}
type Envelope struct {
	DeviceID       string            `json:"device_id"`
	Revision       uint64            `json:"revision"`
	Settings       Values            `json:"settings"`
	SecretsPresent map[string]bool   `json:"secrets_present"`
	Device         DeviceView        `json:"device"`
	Statuses       map[string]Status `json:"statuses"`
	Capabilities   map[string]any    `json:"capabilities"`
}
type DeviceView struct {
	Device
	Command *Command `json:"command"`
}
type Patch struct {
	Settings           Values             `json:"settings"`
	NetworkCredentials map[string]*string `json:"network_credentials,omitempty"`
}
type ConnectionPatch struct {
	Connections       Connections        `json:"connections"`
	Secrets           map[string]*string `json:"secrets,omitempty"`
	ConfirmDisconnect bool               `json:"confirm_disconnect,omitempty"`
}
type ConnectionsView struct {
	Revision       uint64          `json:"revision"`
	Connections    Connections     `json:"connections"`
	SecretsPresent map[string]bool `json:"secrets_present"`
}
type DeviceSummary struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Connected       bool       `json:"connected"`
	LastSeen        *time.Time `json:"last_seen"`
	AppliedRevision uint64     `json:"applied_revision"`
	DesiredRevision uint64     `json:"desired_revision"`
	Firmware        string     `json:"firmware"`
	IP              string     `json:"ip"`
	SSID            string     `json:"ssid"`
}
type DevicesList struct {
	Devices    []DeviceSummary `json:"devices"`
	MaxDevices int             `json:"max_devices"`
}
type SyncResponse struct {
	BacklightControlVersion int         `json:"backlight_control_version"`
	BacklightReportVersion  int         `json:"backlight_report_version"`
	DeviceID                string      `json:"device_id"`
	BridgeID                string      `json:"bridge_id"`
	Revision                uint64      `json:"revision"`
	Preferences             Preferences `json:"preferences"`
	Command                 *Command    `json:"command"`
}
type HomePreferences struct {
	Favorites []Favorite `json:"favorites"`
}
type Preferences struct {
	Home          HomePreferences   `json:"home"`
	Screen        Screen            `json:"screen"`
	Photos        Photos            `json:"photos"`
	Visible       map[string]bool   `json:"visible"`
	POSIXTimezone string            `json:"posix_timezone"`
	SettingsURL   string            `json:"settings_url"`
	Network       map[string]string `json:"network,omitempty"`
}

func Defaults(c *config.Config, baseURL string) Values {
	lat, _ := strconv.ParseFloat(c.WeatherLat, 64)
	lon, _ := strconv.ParseFloat(c.WeatherLon, 64)
	idle, interval := c.PhotoIdle, c.PhotoInterval
	if idle < 30 {
		idle = 300
	}
	if interval < 15 {
		interval = 60
	}
	source := c.PhotoSource
	if source == "" {
		source = "none"
	}
	return Values{
		Screen:  Screen{Locale: "ru", Name: "HomeDash", City: "Belgrade", Timezone: "Europe/Belgrade", ClockFormat: "24h", IdleAction: "photos", NightStart: "23:00", NightEnd: "07:00", AutoUpdateCheck: true},
		Photos:  Photos{Enabled: c.PhotoEnabled, Source: source, BaseURL: c.ImmichBaseURL, AlbumID: c.ImmichAlbumID, IdleSeconds: idle, IntervalSeconds: interval, Order: "shuffle", Layout: "adaptive", CropLimitPercent: 10},
		Network: Network{BridgeURL: strings.TrimSuffix(baseURL, "/")},
		Services: map[string]Service{
			"weather":   {Enabled: c.WeatherLat != "", Latitude: lat, Longitude: lon, Timezone: c.WeatherTZ},
			"calendar":  {Enabled: c.CalendarPlatformBaseURL != "", BaseURL: c.CalendarPlatformBaseURL, IDs: append([]string{}, c.CalendarPlatformIDs...), Timezone: c.CalendarTimeZone},
			"transport": {Enabled: c.TransportBaseURL != "", BaseURL: c.TransportBaseURL, IDs: append([]string{}, c.TransportStops...)},
			"health":    {Enabled: c.HealthBaseURL != "", BaseURL: c.HealthBaseURL},
			"tasks":     {Enabled: c.TodoistToken != ""},
			"news":      {Enabled: c.NewsBaseURL != "", BaseURL: c.NewsBaseURL, Limit: c.NewsLimit, SinceHours: c.NewsSinceHours},
			"home":      {Enabled: c.HABaseURL != "", BaseURL: c.HABaseURL, Sensors: append([]string{}, c.HASensors...), Lights: append([]string{}, c.HALights...), Climates: append([]string{}, c.HAClimates...)},
		},
	}
}
