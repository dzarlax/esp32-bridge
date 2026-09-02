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
