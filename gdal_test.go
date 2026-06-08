package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveWithRetry(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "remove_me.txt")

	if err := os.WriteFile(testFile, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	removeWithRetry(testFile)

	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("expected file to be removed")
	}
}

func TestRemoveWithRetry_NonExistent(t *testing.T) {
	// Should not panic on non-existent file
	removeWithRetry("/nonexistent/path/file.txt")
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		{1024 * 1024 * 1024 * 1024, "1.0 TB"},
	}

	for _, tt := range tests {
		got := formatBytes(tt.bytes)
		if got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}
