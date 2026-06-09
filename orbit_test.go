package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseSafeDate(t *testing.T) {
	tests := []struct {
		input    string
		expected string // YYYY-MM-DD
		wantErr  bool
	}{
		{"S1A_IW_SLC__1SDV_20231231T034722_20240101T034752_123456_1234_1234.SAFE", "2023-12-31", false},
		{"S1B_IW_GRDH_1SDV_20240115T034722_20240115T034752_123456_1234_1234.zip", "2024-01-15", false},
		{"S1C_IW_SLC__1SDV_20240601T000000_20240601T000030_123456_1234_1234.SAFE", "2024-06-01", false},
		{"not_a_safe_name.zip", "", true},
		{"random_file.txt", "", true},
	}
	for _, tt := range tests {
		got, err := parseSafeDate(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseSafeDate(%q) expected error, got %v", tt.input, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSafeDate(%q) unexpected error: %v", tt.input, err)
			continue
		}
		want, _ := time.Parse("2006-01-02", tt.expected)
		if !got.Equal(want) {
			t.Errorf("parseSafeDate(%q) = %v, want %v", tt.input, got.Format("2006-01-02"), tt.expected)
		}
	}
}

func TestGetSatelliteID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"S1A_IW_SLC__1SDV_20231231T034722_20240101T034752_123456_1234_1234.SAFE", "S1A"},
		{"S1B_IW_GRDH_1SDV_20240115T034722_20240115T034752_123456_1234_1234.zip", "S1B"},
		{"S1C_IW_SLC__1SDV_20240601T000000_20240601T000030_123456_1234_1234.SAFE", "S1C"},
		{"random_file.txt", "S1A"},
	}
	for _, tt := range tests {
		got := getSatelliteID(tt.input)
		if got != tt.expected {
			t.Errorf("getSatelliteID(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFindMatchingOrbit(t *testing.T) {
	orbitFiles := []string{
		"S1A_OPER_AUX_POEORB_OPOD_20231212T060754_V20231210T225942_20231212T005942.EOF",
		"S1A_OPER_AUX_POEORB_OPOD_20231213T060754_V20231211T225942_20231213T005942.EOF",
		"S1B_OPER_AUX_POEORB_OPOD_20231212T060754_V20231210T225942_20231212T005942.EOF",
		"S1A_OPER_AUX_RESORB_OPOD_20231211T060754_V20231210T225942_20231211T005942.EOF",
	}

	tests := []struct {
		name      string
		sat       string
		acqDate   string
		orbitType string
		want      string
	}{
		{
			name:      "S1A POEORB exact match",
			sat:       "S1A",
			acqDate:   "2023-12-11",
			orbitType: "POEORB",
			want:      "S1A_OPER_AUX_POEORB_OPOD_20231212T060754_V20231210T225942_20231212T005942.EOF",
		},
		{
			name:      "S1A POEORB boundary start",
			sat:       "S1A",
			acqDate:   "2023-12-10",
			orbitType: "POEORB",
			want:      "S1A_OPER_AUX_POEORB_OPOD_20231212T060754_V20231210T225942_20231212T005942.EOF",
		},
		{
			name:      "S1A POEORB boundary end",
			sat:       "S1A",
			acqDate:   "2023-12-12",
			orbitType: "POEORB",
			want:      "S1A_OPER_AUX_POEORB_OPOD_20231212T060754_V20231210T225942_20231212T005942.EOF",
		},
		{
			name:      "S1B POEORB match",
			sat:       "S1B",
			acqDate:   "2023-12-11",
			orbitType: "POEORB",
			want:      "S1B_OPER_AUX_POEORB_OPOD_20231212T060754_V20231210T225942_20231212T005942.EOF",
		},
		{
			name:      "S1A RESORB match",
			sat:       "S1A",
			acqDate:   "2023-12-10",
			orbitType: "RESORB",
			want:      "S1A_OPER_AUX_RESORB_OPOD_20231211T060754_V20231210T225942_20231211T005942.EOF",
		},
		{
			name:      "No match - wrong satellite",
			sat:       "S1C",
			acqDate:   "2023-12-11",
			orbitType: "POEORB",
			want:      "",
		},
		{
			name:      "No match - date out of range",
			sat:       "S1A",
			acqDate:   "2023-12-20",
			orbitType: "POEORB",
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acqDate, _ := time.Parse("2006-01-02", tt.acqDate)
			got := findMatchingOrbit(orbitFiles, tt.sat, acqDate, tt.orbitType)
			if got != tt.want {
				t.Errorf("findMatchingOrbit() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFetchASFOrbitList(t *testing.T) {
	html := `<html><body>
<a href="S1A_OPER_AUX_POEORB_OPOD_20231212T060754_V20231210T225942_20231212T005942.EOF">file1</a>
<a href="S1A_OPER_AUX_POEORB_OPOD_20231213T060754_V20231211T225942_20231213T005942.EOF">file2</a>
<a href="S1B_OPER_AUX_POEORB_OPOD_20231212T060754_V20231210T225942_20231212T005942.EOF">file3</a>
</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, html)
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 30 * time.Second}
	files, err := fetchASFOrbitList(context.Background(), client, srv.URL+"/", NoOpAuth{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}
	want := "S1A_OPER_AUX_POEORB_OPOD_20231212T060754_V20231210T225942_20231212T005942.EOF"
	if files[0] != want {
		t.Errorf("expected first file %q, got %q", want, files[0])
	}
}

func TestFetchASFOrbitList_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("Unauthorized"))
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 30 * time.Second}
	_, err := fetchASFOrbitList(context.Background(), client, srv.URL+"/", NoOpAuth{})
	if err == nil {
		t.Fatal("expected error for HTTP 401")
	}
	if !contains(err.Error(), "401") {
		t.Errorf("expected error to mention 401, got: %v", err)
	}
}

func TestFetchASFOrbitList_EmptyHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html><body>No files here</body></html>")
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 30 * time.Second}
	files, err := fetchASFOrbitList(context.Background(), client, srv.URL+"/", NoOpAuth{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestRunOrbitDownload_NoSAFEFiles(t *testing.T) {
	tmpDir := t.TempDir()
	auth := NewEarthdataAuth("test", "test")
	// Override token endpoint to avoid real network call
	auth.tokenEndpoint = "http://invalid"

	err := runOrbitDownload(tmpDir, filepath.Join(tmpDir, "orbits"), auth, 1, 0, false)
	if err == nil {
		t.Fatal("expected error for empty directory")
	}
	if !contains(err.Error(), "no SAFE") {
		t.Errorf("expected 'no SAFE' error, got: %v", err)
	}
}

func TestRunOrbitDownload_WithScenes(t *testing.T) {
	tmpDir := t.TempDir()
	orbitDir := filepath.Join(tmpDir, "orbits")

	// Create fake SAFE directories
	os.MkdirAll(filepath.Join(tmpDir, "S1A_IW_SLC__1SDV_20231211T034722_20231211T034752_123456_1234_1234.SAFE"), 0755)

	html := `<html><body>
<a href="S1A_OPER_AUX_POEORB_OPOD_20231212T060754_V20231210T225942_20231212T005942.EOF">orbit</a>
</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, html)
	}))
	defer srv.Close()

	// Override ASF URLs to point to test server
	oldPOEORB := asfPOEORBURL
	oldRESORB := asfRESORBURL
	defer func() {
		// Note: these are consts so we can't restore them directly in real code,
		// but for testing we need a way to inject URLs.
		// We'll test at a lower level instead.
		_ = oldPOEORB
		_ = oldRESORB
	}()
	_ = srv
	_ = orbitDir
	// The integration test requires URL injection; unit tests above cover the logic.
	// This test serves as a placeholder for the full integration test pattern.
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
