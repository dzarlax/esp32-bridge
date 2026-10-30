package main

import (
	"esp32-bridge/internal/config"
	"esp32-bridge/internal/settings"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateNonOverwritingExport(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if _, err := settings.New(dir, settings.Defaults(&config.Config{NewsLimit: 5, NewsSinceHours: 24}, "https://bridge.test"), nil, "https://bridge.test", nil); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "settings.json")
	output := filepath.Join(dir, "rollback-v2.json")
	before, _ := os.ReadFile(input)
	if err := run([]string{input, output}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(output)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("output permissions: %v", err)
	}
	exported, _ := os.ReadFile(output)
	if err := run([]string{input, output}); err == nil {
		t.Fatal("overwrote existing export")
	}
	after, _ := os.ReadFile(output)
	if string(after) != string(exported) {
		t.Fatal("changed existing export")
	}
	original, _ := os.ReadFile(input)
	if string(original) != string(before) {
		t.Fatal("mutated source")
	}
}
