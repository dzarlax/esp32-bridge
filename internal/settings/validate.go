package settings

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"esp32-bridge/internal/photoframe"
)

var serviceNames = []string{"weather", "calendar", "transport", "health", "tasks", "news", "home"}
var secretNames = map[string]bool{"health_api_key": true, "todoist_token": true, "ha_token": true, "calendar_platform_token": true, "immich_api_key": true}

func cleanText(v string, n int) bool {
	if !utf8.ValidString(v) || len(v) > n {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validURL(v string) bool {
	if v == "" {
		return true
	}
	u, e := url.Parse(v)
	return e == nil && cleanText(v, 512) && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}
func validIDs(ids []string, max int) bool { return validIDsWithLength(ids, max, 128) }
func validIDsWithLength(ids []string, max, maxLength int) bool {
	if len(ids) > max {
		return false
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" || !cleanText(id, maxLength) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func validLocale(locale string) bool { return locale == "ru" || locale == "en" || locale == "sr-Latn" }

func (v Values) Validate() error {
	if !validLocale(v.Screen.Locale) {
		return errors.New("unsupported display locale")
	}
	if strings.TrimSpace(v.Screen.Name) == "" || !cleanText(v.Screen.Name, 64) || !cleanText(v.Screen.City, 64) {
		return errors.New("invalid display name or city")
	}
	if v.Screen.Timezone != "Europe/Belgrade" && v.Screen.Timezone != "UTC" {
		return errors.New("unsupported display timezone")
	}
	if v.Screen.ClockFormat != "24h" && v.Screen.ClockFormat != "12h" {
		return errors.New("invalid clock format")
	}
	if v.Screen.IdleAction != "photos" && v.Screen.IdleAction != "off" {
		return errors.New("invalid idle action")
	}
	for _, clock := range []string{v.Screen.NightStart, v.Screen.NightEnd} {
		if _, e := time.Parse("15:04", clock); e != nil || len(clock) != 5 {
			return errors.New("invalid night schedule")
		}
	}
	if v.Screen.BacklightPercent != nil && (*v.Screen.BacklightPercent < 10 || *v.Screen.BacklightPercent > 100) || v.Screen.NightBacklightPercent != nil && (*v.Screen.NightBacklightPercent < 10 || *v.Screen.NightBacklightPercent > 100) || (v.Screen.NightAction != "" && v.Screen.NightAction != "off" && v.Screen.NightAction != "dim") || (v.Screen.NightAction == "dim" && (v.Screen.BacklightPercent == nil || v.Screen.NightBacklightPercent == nil)) {
		return errors.New("invalid brightness settings")
	}
	p := v.Photos
	if p.IdleSeconds < 30 || p.IdleSeconds > 3600 || p.IntervalSeconds < 15 || p.IntervalSeconds > 3600 || p.CropLimitPercent < 0 || p.CropLimitPercent > 40 {
		return errors.New("photo timing or crop limit out of range")
	}
	if p.Source != "none" && p.Source != "fixture" && p.Source != "immich" {
		return errors.New("unsupported photo source")
	}
	if p.Order != "shuffle" && p.Order != "sequential" {
		return errors.New("invalid photo order")
	}
	if p.Layout != "adaptive" && p.Layout != "contain" && p.Layout != "cover" {
		return errors.New("invalid photo layout")
	}
	if !validURL(p.BaseURL) || !cleanText(p.AlbumID, 64) {
		return errors.New("invalid photo endpoint")
	}
	if p.Enabled && p.Source == "immich" {
		if e := photoframe.ValidateImmichEndpoint(p.BaseURL, p.AlbumID); e != nil {
			return errors.New("invalid Immich endpoint or album")
		}
	}
	if !validURL(v.Network.BridgeURL) || len(v.Network.BridgeURL) > 127 {
		return errors.New("invalid Bridge URL")
	}
	if len(v.Services) != len(serviceNames) {
		return errors.New("invalid service set")
	}
	for _, name := range serviceNames {
		s, ok := v.Services[name]
		idLength := 128
		if name == "calendar" {
			idLength = 512
		}
		if !ok || !validURL(s.BaseURL) || !validIDsWithLength(s.IDs, 32, idLength) || !validIDs(s.Sensors, 10) || !validIDs(s.Lights, 8) || !validIDs(s.Climates, 1) {
			return fmt.Errorf("invalid %s settings", name)
		}
		if name != "home" && len(s.Favorites) != 0 {
			return errors.New("favorites only allowed for home")
		}
		if len(s.Favorites) > 4 {
			return errors.New("too many favorites")
		}
		seen := map[string]bool{}
		for _, f := range s.Favorites {
			if f.EntityID == "" || !cleanText(f.EntityID, 47) || !cleanText(f.Label, 32) || (f.Kind != "light" && f.Kind != "climate") || !strings.HasPrefix(f.EntityID, f.Kind+".") || seen[f.EntityID] {
				return errors.New("invalid favorite")
			}
			seen[f.EntityID] = true
		}
		if len(s.Timezone) > 64 || !cleanText(s.Timezone, 64) {
			return fmt.Errorf("invalid %s timezone", name)
		}
		if s.Timezone != "" {
			if _, e := time.LoadLocation(s.Timezone); e != nil {
				return fmt.Errorf("invalid %s timezone", name)
			}
		}
		if math.IsNaN(s.Latitude) || math.IsNaN(s.Longitude) || math.IsInf(s.Latitude, 0) || math.IsInf(s.Longitude, 0) || s.Latitude < -90 || s.Latitude > 90 || s.Longitude < -180 || s.Longitude > 180 {
			return errors.New("invalid coordinates")
		}
		if name == "news" && (s.Limit < 1 || s.Limit > 20 || s.SinceHours < 1 || s.SinceHours > 168) {
			return errors.New("invalid news limits")
		}
		if s.Enabled && name != "weather" && name != "tasks" && s.BaseURL == "" {
			return fmt.Errorf("%s endpoint required", name)
		}
		if name == "calendar" && s.Enabled && len(s.IDs) == 0 {
			return errors.New("calendar selection required")
		}
	}
	return nil
}

func validateStoredState(secrets, network map[string]string, command *Command) error {
	secretValues := make(map[string]*string, len(secrets))
	for key, value := range secrets {
		v := value
		secretValues[key] = &v
	}
	if err := validateSecrets(secretValues, false); err != nil {
		return errors.New("invalid stored credential")
	}
	if len(network) > 4 {
		return errors.New("invalid stored network credentials")
	}
	for key, value := range network {
		switch key {
		case "bridge_url":
			if len(value) > 127 || !validURL(value) {
				return errors.New("invalid stored network settings")
			}
		case "wifi_ssid", "wifi_password", "bridge_key":
			values := map[string]*string{key: &value}
			if err := validateSecrets(values, true); err != nil {
				return errors.New("invalid stored network credentials")
			}
		default:
			return errors.New("unknown stored network field")
		}
	}
	if command != nil && !validCommand(command) {
		return errors.New("invalid stored device command")
	}
	return nil
}

func validCommand(c *Command) bool {
	if c == nil || !validCommandID(c.ID) {
		return false
	}
	switch c.Action {
	case "photo_start", "restart", "ota_check", "ota_install", "reset", "backlight_preview", "backlight_adjust", "backlight_begin", "backlight_restore":
	default:
		return false
	}
	switch c.State {
	case "queued", "accepted", "succeeded", "failed", "expired", "interrupted", "unknown":
	default:
		return false
	}
	if c.CreatedAt.IsZero() || c.ExpiresAt.IsZero() || c.CreatedAt.After(time.Now().Add(time.Minute)) || !c.ExpiresAt.After(c.CreatedAt) || !cleanText(c.BootID, 64) || c.BootID == "" {
		return false
	}
	maxAge := 2 * time.Minute
	if c.Action == "ota_install" {
		maxAge = 10 * time.Minute
	}
	if c.ExpiresAt.Sub(c.CreatedAt) > maxAge+time.Second {
		return false
	}
	if c.Action == "backlight_preview" || c.Action == "backlight_adjust" || c.Action == "backlight_begin" {
		if c.Percent == nil || *c.Percent < 10 || *c.Percent > 100 {
			return false
		}
	} else if c.Percent != nil {
		return false
	}
	if !cleanText(c.TargetVersion, 32) || (c.Action != "ota_install" && c.TargetVersion != "") {
		return false
	}
	return c.Error == "" || safeError(c.Error) == c.Error
}
func validCommandID(id string) bool {
	if len(id) != 32 || strings.ToLower(id) != id {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func validBridgeID(id string) bool {
	if len(id) != 32 || strings.ToLower(id) != id {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func validateSecrets(p map[string]*string, network bool) error {
	allowed := secretNames
	if network {
		allowed = map[string]bool{"wifi_ssid": true, "wifi_password": true, "bridge_key": true}
	}
	for k, v := range p {
		if !allowed[k] {
			return errors.New("unknown credential field")
		}
		if v == nil {
			continue
		}
		limit := 8192
		if network {
			limit = 64
			if k == "wifi_ssid" {
				limit = 32
			}
			if k == "bridge_key" {
				limit = 63
			}
		}
		if !cleanText(*v, limit) {
			return errors.New("invalid credential value")
		}
	}
	return nil
}
