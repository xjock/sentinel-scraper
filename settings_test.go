package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSettingsPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	p := settingsPath()
	expected := filepath.Join(tmpDir, ".sentinel-scraper", "settings.json")
	if p != expected {
		t.Errorf("expected %s, got %s", expected, p)
	}
}

func TestLoadSettings_NotExist(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	s, err := loadSettings()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s != nil {
		t.Error("expected nil for non-existent settings")
	}
}

func TestLoadSettings_Valid(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	data := []byte(`{"auth":{"username":"u","password":"p"},"cdse_auth":{"username":"c","password":"d"}}`)
	if err := os.MkdirAll(filepath.Dir(settingsPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath(), data, 0600); err != nil {
		t.Fatal(err)
	}

	s, err := loadSettings()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s == nil {
		t.Fatal("expected settings, got nil")
	}
	if s.Auth == nil || s.Auth.Username != "u" {
		t.Errorf("expected auth username=u, got %v", s.Auth)
	}
	if s.CDSEAuth == nil || s.CDSEAuth.Username != "c" {
		t.Errorf("expected cdse_auth username=c, got %v", s.CDSEAuth)
	}
}

func TestLoadSettings_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	if err := os.MkdirAll(filepath.Dir(settingsPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath(), []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := loadSettings()
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSaveSettings(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	s := &Settings{
		Auth:          &AuthConfig{Username: "legacy@example.com", Password: "secret"},
		CDSEAuth:      &AuthConfig{Username: "cdse@example.com", Password: "cdsepass"},
		EarthdataAuth: &AuthConfig{Username: "earth", Password: "earthpass"},
	}
	if err := saveSettings(s); err != nil {
		t.Fatalf("saveSettings failed: %v", err)
	}

	data, err := os.ReadFile(settingsPath())
	if err != nil {
		t.Fatalf("failed to read saved settings: %v", err)
	}
	if len(data) == 0 {
		t.Error("saved settings file is empty")
	}

	info, err := os.Stat(settingsPath())
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Errorf("expected permissions 0600, got %04o", info.Mode().Perm())
	}

	loaded, err := loadSettings()
	if err != nil {
		t.Fatalf("loadSettings failed: %v", err)
	}
	if loaded.CDSEAuth == nil || loaded.CDSEAuth.Username != "cdse@example.com" {
		t.Errorf("expected cdse_auth username=cdse@example.com, got %v", loaded.CDSEAuth)
	}
	if loaded.EarthdataAuth == nil || loaded.EarthdataAuth.Username != "earth" {
		t.Errorf("expected earthdata_auth username=earth, got %v", loaded.EarthdataAuth)
	}
}

func TestHasSavedAuth(t *testing.T) {
	if hasSavedAuth(nil) {
		t.Error("expected false for nil settings")
	}
	if hasSavedAuth(&Settings{}) {
		t.Error("expected false for empty auth")
	}
	if hasSavedAuth(&Settings{CDSEAuth: &AuthConfig{Username: "u"}}) {
		t.Error("expected false for CDSE auth missing password")
	}
	if !hasSavedAuth(&Settings{CDSEAuth: &AuthConfig{Username: "u", Password: "p"}}) {
		t.Error("expected true for complete CDSE auth")
	}
	if !hasSavedAuth(&Settings{EarthdataAuth: &AuthConfig{Username: "u", Password: "p"}}) {
		t.Error("expected true for complete Earthdata auth")
	}
}

func TestNeedsSetup(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	if !needsSetup() {
		t.Error("expected needsSetup=true when settings file missing")
	}

	if err := os.MkdirAll(filepath.Dir(settingsPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath(), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	if needsSetup() {
		t.Error("expected needsSetup=false when settings file exists")
	}
}
