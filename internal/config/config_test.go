package config

import "testing"

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
