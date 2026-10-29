package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr string
	APIKey     string

	HealthBaseURL  string
	HealthAPIKey   string
	HealthCacheTTL time.Duration

	TodoistToken    string
	TodoistCacheTTL time.Duration

	NewsBaseURL    string
	NewsLimit      int
	NewsSinceHours int
	NewsCacheTTL   time.Duration

	HABaseURL  string
	HAToken    string
	HASensors  []string
	HALights   []string
	HAClimates []string
	HACacheTTL time.Duration

	CalendarPlatformBaseURL string
	CalendarPlatformToken   string
	CalendarPlatformIDs     []string
	CalendarTimeZone        string

	WeatherLat      string
	WeatherLon      string
	WeatherTZ       string
	WeatherCacheTTL time.Duration

	TransportBaseURL  string
	TransportStops    []string
	TransportCacheTTL time.Duration

	OTAGitHubRepo    string
	OTAGitHubToken   string
	MigrateBridgeURL string

	PhotoEnabled                                     bool
	PhotoSource                                      string
	PhotoIdle, PhotoInterval, PhotoStale, PhotoCache int
	photoErr                                         error

	FetchTimeout time.Duration
}

func Load() *Config {
	enabled, boolErr := strconv.ParseBool(envStr("PHOTO_FRAME_ENABLED", "false"))
	idle, idleErr := strconv.Atoi(envStr("PHOTO_FRAME_IDLE_SECONDS", "300"))
	interval, intervalErr := strconv.Atoi(envStr("PHOTO_FRAME_INTERVAL_SECONDS", "60"))
	stale, staleErr := strconv.Atoi(envStr("PHOTO_FRAME_MAX_STALE_SECONDS", "900"))
	cache, cacheErr := strconv.Atoi(envStr("PHOTO_FRAME_CACHE_FRAMES", "32"))
	var photoErr error
	if boolErr != nil || (enabled && (idleErr != nil || intervalErr != nil || staleErr != nil || cacheErr != nil)) {
		photoErr = fmt.Errorf("invalid photo frame configuration")
	}
	return &Config{
		PhotoEnabled: enabled, PhotoSource: envStr("PHOTO_FRAME_SOURCE", "none"), PhotoIdle: idle, PhotoInterval: interval, PhotoStale: stale, PhotoCache: cache, photoErr: photoErr,
		ListenAddr: envStr("LISTEN_ADDR", ":8090"),
		APIKey:     envStr("API_KEY", ""),

		HealthBaseURL:  envStr("HEALTH_BASE_URL", ""),
		HealthAPIKey:   envStr("HEALTH_API_KEY", ""),
		HealthCacheTTL: envDuration("HEALTH_CACHE_TTL", 300),

		TodoistToken:    envStr("TODOIST_TOKEN", ""),
		TodoistCacheTTL: envDuration("TODOIST_CACHE_TTL", 60),

		NewsBaseURL:    envStr("NEWS_BASE_URL", ""),
		NewsLimit:      envInt("NEWS_LIMIT", 5),
		NewsSinceHours: envInt("NEWS_SINCE_HOURS", 24),
		NewsCacheTTL:   envDuration("NEWS_CACHE_TTL", 900),

		HABaseURL:  envStr("HA_BASE_URL", ""),
		HAToken:    envStr("HA_TOKEN", ""),
		HASensors:  envList("HA_SENSORS"),
		HALights:   envList("HA_LIGHTS"),
		HAClimates: envList("HA_CLIMATES"),
		HACacheTTL: envDuration("HA_CACHE_TTL", 120),

		CalendarPlatformBaseURL: envStr("CALENDAR_PLATFORM_BASE_URL", ""),
		CalendarPlatformToken:   envStr("CALENDAR_PLATFORM_TOKEN", ""),
		CalendarPlatformIDs:     envList("CALENDAR_PLATFORM_IDS"),
		CalendarTimeZone:        envStr("CALENDAR_TIME_ZONE", "Europe/Belgrade"),

		WeatherLat:      envStr("WEATHER_LAT", "44.82"),
		WeatherLon:      envStr("WEATHER_LON", "20.46"),
		WeatherTZ:       envStr("WEATHER_TZ", "Europe/Belgrade"),
		WeatherCacheTTL: envDuration("WEATHER_CACHE_TTL", 1800),

		TransportBaseURL:  envStr("TRANSPORT_BASE_URL", ""),
		TransportStops:    envList("TRANSPORT_STOPS"),
		TransportCacheTTL: envDuration("TRANSPORT_CACHE_TTL", 30),

		OTAGitHubRepo:    envStr("OTA_GITHUB_REPO", ""),
		OTAGitHubToken:   envStr("OTA_GITHUB_TOKEN", ""),
		MigrateBridgeURL: envStr("OTA_MIGRATE_BRIDGE_URL", ""),

		FetchTimeout: envDuration("FETCH_TIMEOUT", 5),
	}
}

func (c *Config) Validate() error {
	if c.photoErr != nil {
		return c.photoErr
	}
	if c.PhotoEnabled && ((c.PhotoSource != "none" && c.PhotoSource != "fixture") || c.PhotoIdle < 30 || c.PhotoIdle > 3600 || c.PhotoInterval < 15 || c.PhotoInterval > 3600 || c.PhotoStale < 60 || c.PhotoStale > 3600 || c.PhotoCache < 16 || c.PhotoCache > 32) {
		return fmt.Errorf("invalid photo frame configuration")
	}
	if c.APIKey == "" {
		return fmt.Errorf("API_KEY is required")
	}
	platformFields := 0
	if c.CalendarPlatformBaseURL != "" {
		platformFields++
	}
	if c.CalendarPlatformToken != "" {
		platformFields++
	}
	if len(c.CalendarPlatformIDs) > 0 {
		platformFields++
	}
	if platformFields > 0 && platformFields < 3 {
		return fmt.Errorf("CALENDAR_PLATFORM_BASE_URL, CALENDAR_PLATFORM_TOKEN, and CALENDAR_PLATFORM_IDS must be configured together")
	}
	if _, err := time.LoadLocation(c.CalendarTimeZone); err != nil {
		return fmt.Errorf("CALENDAR_TIME_ZONE must be a valid IANA time zone: %w", err)
	}
	return nil
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, defSec int) time.Duration {
	return time.Duration(envInt(key, defSec)) * time.Second
}

func envList(key string) []string {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}
