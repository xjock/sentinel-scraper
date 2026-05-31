//go:build windows

package bundle

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed assets_windows.tar.gz
var windowsAssets []byte

func ensureExtracted() (string, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		localAppData = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
	}

	baseDir := filepath.Join(localAppData, "sentinel-scraper", "bundle")

	hash := sha256.Sum256(windowsAssets)
	hashStr := hex.EncodeToString(hash[:8])

	bundleDir := filepath.Join(baseDir, hashStr)
	marker := filepath.Join(bundleDir, ".extracted")

	if _, err := os.Stat(marker); err == nil {
		return bundleDir, nil
	}

	_ = cleanOldBundles(baseDir, hashStr)

	if err := os.MkdirAll(bundleDir, 0755); err != nil {
		return "", err
	}

	if err := extractTarGz(bytes.NewReader(windowsAssets), bundleDir); err != nil {
		return "", fmt.Errorf("extract bundle: %w", err)
	}

	if err := os.WriteFile(marker, []byte(hashStr), 0644); err != nil {
		return "", err
	}

	return bundleDir, nil
}

func toolPath(name string) (string, error) {
	dir, err := ensureExtracted()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".exe"), nil
}

func projDataPath() (string, error) {
	dir, err := ensureExtracted()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "share", "proj"), nil
}

func cleanOldBundles(baseDir, keep string) error {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != keep {
			_ = os.RemoveAll(filepath.Join(baseDir, entry.Name()))
		}
	}
	return nil
}
