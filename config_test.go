package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureDefaultConfig(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	if err := ensureDefaultConfig(path); err != nil {
		t.Fatalf("ensureDefaultConfig failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read created config: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("created config is empty")
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed for default: %v", err)
	}
	if len(cfg.BBox) != 4 {
		t.Errorf("expected bbox with 4 elements, got %d", len(cfg.BBox))
	}
	if len(cfg.Bands) != 3 {
		t.Errorf("expected 3 bands, got %d", len(cfg.Bands))
	}
	if cfg.Limit != 20 {
		t.Errorf("expected limit=20, got %d", cfg.Limit)
	}
	if cfg.MaxWorkers != 4 {
		t.Errorf("expected max_workers=4, got %d", cfg.MaxWorkers)
	}
	if cfg.MaxRetries != 3 {
		t.Errorf("expected max_retries=3, got %d", cfg.MaxRetries)
	}
}

func TestEnsureDefaultConfig_Existing(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	if err := os.WriteFile(path, []byte(`{"limit":99}`), 0644); err != nil {
		t.Fatal(err)
	}

	if err := ensureDefaultConfig(path); err != nil {
		t.Fatalf("ensureDefaultConfig should not error for existing file: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Limit != 99 {
		t.Errorf("expected existing limit preserved (99), got %d", cfg.Limit)
	}
}

func TestMergeSettings(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	saved := &Settings{
		Auth: &AuthConfig{Username: "test", Password: "secret"},
	}
	if err := saveSettings(saved); err != nil {
		t.Fatalf("saveSettings failed: %v", err)
	}

	// Case 1: empty config should get auth from settings
	cfg := &Config{}
	mergeSettings(cfg)
	if cfg.Auth == nil || cfg.Auth.Username != "test" {
		t.Errorf("expected Auth from settings, got %v", cfg.Auth)
	}
}

func TestMergeSettings_PreservesExplicit(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	saved := &Settings{
		Auth: &AuthConfig{Username: "saved", Password: "secret"},
	}
	if err := saveSettings(saved); err != nil {
		t.Fatal(err)
	}

	// Config has explicit auth; settings should NOT override
	cfg := &Config{
		Auth: &AuthConfig{Username: "explicit", Password: "pass"},
	}
	mergeSettings(cfg)
	if cfg.Auth.Username != "explicit" {
		t.Errorf("explicit Auth should be preserved, got %s", cfg.Auth.Username)
	}
}

func TestMergeSettings_NoSettingsFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cfg := &Config{Limit: 5}
	mergeSettings(cfg)
	if cfg.Limit != 5 {
		t.Errorf("config should be unchanged when no settings file, got limit=%d", cfg.Limit)
	}
}

func TestResolveSatelliteType(t *testing.T) {
	cases := []struct {
		satellite string
		product   string
		want      SatelliteType
	}{
		{"sentinel-2", "", SatS2L2A},
		{"s2", "", SatS2L2A},
		{"sentinel-2-l2a", "", SatS2L2A},
		{"sentinel-1", "grd", SatS1GRD},
		{"sentinel-1", "slc", SatS1SLC},
		{"s1", "grd", SatS1GRD},
		{"s1", "slc", SatS1SLC},
		{"sentinel-1", "", SatS1GRD},
		{"sentinel-1-grd", "", SatS1GRD},
		{"sentinel-1-slc", "", SatS1SLC},
		{"hls", "", SatHLS},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s_%s", tc.satellite, tc.product), func(t *testing.T) {
			got := ResolveSatelliteType(tc.satellite, tc.product)
			if got != tc.want {
				t.Errorf("ResolveSatelliteType(%q, %q) = %q, want %q", tc.satellite, tc.product, got, tc.want)
			}
		})
	}
}

func TestLoadConfig_TwoLevelSatellite(t *testing.T) {
	tmpDir := t.TempDir()

	cases := []struct {
		name     string
		data     string
		wantSat  string
		wantProd string
	}{
		{"s2", `{"satellite":"sentinel-2","start_date":"2025-01-01","end_date":"2025-01-02"}`, "sentinel-2-l2a", ""},
		{"s1_grd", `{"satellite":"sentinel-1","product":"grd","start_date":"2025-01-01","end_date":"2025-01-02"}`, "sentinel-1-grd", "grd"},
		{"s1_slc", `{"satellite":"sentinel-1","product":"slc","start_date":"2025-01-01","end_date":"2025-01-02"}`, "sentinel-1-slc", "slc"},
		{"legacy_collection", `{"collection":"sentinel-1-slc","start_date":"2025-01-01","end_date":"2025-01-02"}`, "sentinel-1-slc", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(tmpDir, tc.name+".json")
			os.WriteFile(path, []byte(tc.data), 0644)
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("LoadConfig failed: %v", err)
			}
			if cfg.Satellite != tc.wantSat {
				t.Errorf("expected satellite=%q, got %q", tc.wantSat, cfg.Satellite)
			}
		})
	}
}

func TestLoadConfig_BBoxValidation(t *testing.T) {
	tmpDir := t.TempDir()

	cases := []struct {
		name string
		bbox []float64
		ok   bool
	}{
		{"valid 4", []float64{0, 0, 1, 1}, true},
		{"empty", []float64{}, true},
		{"two elements", []float64{0, 0}, false},
		{"six elements", []float64{0, 0, 1, 1, 2, 2}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(tmpDir, tc.name+".json")
			cfgData := Config{BBox: tc.bbox, StartDate: "2025-01-01", EndDate: "2025-01-02"}
			data, _ := json.Marshal(cfgData)
			os.WriteFile(path, data, 0644)

			_, err := LoadConfig(path)
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected error for invalid bbox length")
			}
		})
	}
}
