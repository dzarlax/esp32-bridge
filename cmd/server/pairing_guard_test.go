package main

import "testing"

func TestDisabledPairingGuard(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"existing legacy install", nil, false},
		{"explicit migration disabled", map[string]string{"VOICE_PAIRING_LEGACY_MIGRATION": "false"}, false},
		{"state remains", map[string]string{"VOICE_PAIRING_STATE_DIR": "/pairing"}, true},
		{"partial key remains", map[string]string{"VOICE_PAIRING_MASTER_FILE": "/key"}, true},
		{"enabled migration", map[string]string{"VOICE_PAIRING_LEGACY_MIGRATION": "1"}, true},
		{"invalid migration setting", map[string]string{"VOICE_PAIRING_LEGACY_MIGRATION": "invalid"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pairingConfigurationPresent(func(k string) string { return tt.env[k] }); got != tt.want {
				t.Fatalf("guard %v, want %v", got, tt.want)
			}
		})
	}
}
