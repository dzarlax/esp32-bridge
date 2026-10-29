package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateCalendarPlatformRequiresCompleteConfiguration(t *testing.T) {
	for _, cfg := range []*Config{
		{APIKey: "bridge-key", CalendarTimeZone: "Europe/Belgrade", CalendarPlatformBaseURL: "https://calendar.example.com"},
		{APIKey: "bridge-key", CalendarTimeZone: "Europe/Belgrade", CalendarPlatformToken: "token"},
		{APIKey: "bridge-key", CalendarTimeZone: "Europe/Belgrade", CalendarPlatformIDs: []string{"google:personal"}},
	} {
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate() = nil for incomplete Calendar Platform config: %#v", cfg)
		}
	}
	complete := &Config{
		APIKey: "bridge-key", CalendarTimeZone: "Europe/Belgrade",
		CalendarPlatformBaseURL: "https://calendar.example.com", CalendarPlatformToken: "token", CalendarPlatformIDs: []string{"google:personal"},
	}
	if err := complete.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestValidateCalendarTimeZone(t *testing.T) {
	cfg := &Config{APIKey: "bridge-key", CalendarTimeZone: "not-a-zone"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() = nil for invalid time zone")
	}
}

func TestPhotoFrameConfigValidation(t *testing.T) {
	t.Setenv("API_KEY", "fixture")
	t.Setenv("PHOTO_FRAME_ENABLED", "true")
	t.Setenv("PHOTO_FRAME_SOURCE", "fixture")
	for _, tc := range []struct {
		name, value string
		valid       bool
	}{{"PHOTO_FRAME_IDLE_SECONDS", "29", false}, {"PHOTO_FRAME_IDLE_SECONDS", "30", true}, {"PHOTO_FRAME_INTERVAL_SECONDS", "14", false}, {"PHOTO_FRAME_MAX_STALE_SECONDS", "3601", false}, {"PHOTO_FRAME_CACHE_FRAMES", "15", false}, {"PHOTO_FRAME_CACHE_FRAMES", "32", true}, {"PHOTO_FRAME_SOURCE", "file", false}, {"PHOTO_FRAME_INTERVAL_SECONDS", "garbage", false}} {
		t.Run(tc.name+tc.value, func(t *testing.T) {
			t.Setenv(tc.name, tc.value)
			err := Load().Validate()
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}
func TestDisabledPhotoFrameIgnoresSourceSettings(t *testing.T) {
	t.Setenv("API_KEY", "fixture")
	t.Setenv("PHOTO_FRAME_ENABLED", "false")
	t.Setenv("PHOTO_FRAME_SOURCE", "unused")
	t.Setenv("PHOTO_FRAME_CACHE_FRAMES", "invalid")
	if err := Load().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestImmichConfiguration(t *testing.T) {
	t.Setenv("API_KEY", "bridge-test")
	t.Setenv("PHOTO_FRAME_ENABLED", "true")
	t.Setenv("PHOTO_FRAME_SOURCE", "immich")
	t.Setenv("IMMICH_BASE_URL", "https://photos.example.test")
	t.Setenv("IMMICH_ALBUM_ID", "12345678-1234-4567-8123-123456789abc")
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte("test-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IMMICH_API_KEY_FILE", path)
	if err := Load().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, value string }{
		{"IMMICH_BASE_URL", "http://photos.example.test"},
		{"IMMICH_BASE_URL", "https://user:secret@photos.example.test"},
		{"IMMICH_BASE_URL", "https://photos.example.test?key=secret"},
		{"IMMICH_ALBUM_ID", "not-an-album"},
		{"IMMICH_API_KEY_FILE", path + "missing"},
	} {
		t.Run(tc.name+tc.value, func(t *testing.T) {
			t.Setenv(tc.name, tc.value)
			if err := Load().Validate(); err == nil {
				t.Fatal("accepted invalid Immich config")
			}
		})
	}
	for _, data := range []string{"", "test\nheader", strings.Repeat("x", 4097)} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if err := Load().Validate(); err == nil {
			t.Fatal("accepted invalid secret file")
		}
	}
	if err := os.WriteFile(path, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := Load().Validate(); err == nil {
		t.Fatal("accepted public secret file")
	}
	t.Setenv("PHOTO_FRAME_ENABLED", "false")
	if err := Load().Validate(); err != nil {
		t.Fatal("disabled source requires key", err)
	}
}
