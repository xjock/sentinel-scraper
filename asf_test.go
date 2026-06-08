package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestQueryASFProducts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("platform") != "S1" {
			t.Errorf("expected platform=S1, got %s", q.Get("platform"))
		}
		if q.Get("processingLevel") != "SLC" {
			t.Errorf("expected processingLevel=SLC, got %s", q.Get("processingLevel"))
		}
		if q.Get("output") != "geojson" {
			t.Errorf("expected output=geojson, got %s", q.Get("output"))
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"type":"FeatureCollection","features":[{"type":"Feature","geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,1],[0,0]]]},"properties":{"sceneName":"S1A_TEST","fileName":"S1A_TEST.zip","url":"http://example.com/test.zip","fileSize":"1048576","platform":"Sentinel-1A","startTime":"2025-01-15T00:00:00Z","stopTime":"2025-01-15T00:01:00Z","processingLevel":"SLC","polarization":"VV+VH","pathNumber":1,"frameNumber":2,"orbit":123}}]}`)
	}))
	defer srv.Close()

	oldURL := asfSearchURL
	defer func() { asfSearchURL = oldURL }()
	asfSearchURL = srv.URL

	cfg := &Config{
		BBox:       []float64{0, 0, 1, 1},
		StartDate:  "2025-01-01",
		EndDate:    "2025-01-31",
		Limit:      5,
		Collection: "sentinel-1-slc",
		Satellite:  "sentinel-1-slc",
	}

	products, err := queryASFProducts(NoOpAuth{}, cfg)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(products) != 1 {
		t.Fatalf("expected 1 product, got %d", len(products))
	}
	if products[0].SceneName != "S1A_TEST" {
		t.Errorf("expected S1A_TEST, got %s", products[0].SceneName)
	}
	if products[0].FileSize != 1048576 {
		t.Errorf("expected FileSize=1048576, got %d", products[0].FileSize)
	}
	if products[0].Processing != "SLC" {
		t.Errorf("expected SLC, got %s", products[0].Processing)
	}
}

func TestQueryASFProducts_HTTPError(t *testing.T) {
	oldURL := asfSearchURL
	defer func() { asfSearchURL = oldURL }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("invalid bbox"))
	}))
	defer srv.Close()
	asfSearchURL = srv.URL

	cfg := &Config{
		BBox:       []float64{0, 0, 1, 1},
		StartDate:  "2025-01-01",
		EndDate:    "2025-01-31",
		Collection: "sentinel-1-slc",
	}

	_, err := queryASFProducts(NoOpAuth{}, cfg)
	if err == nil {
		t.Fatal("expected error for HTTP 400")
	}
}

func TestSaveKMLForASF(t *testing.T) {
	tmpDir := t.TempDir()
	p := asfResult{
		SceneName:    "S2A_T50TMK_20250101",
		StartTime:    "2025-01-01T00:00:00Z",
		StopTime:     "2025-01-01T00:01:00Z",
		Processing:   "SLC",
		Polarization: "VV+VH",
		Geometry: Geometry{
			Type:        "Polygon",
			Coordinates: [][][]float64{{{116.2, 39.8}, {116.6, 39.8}, {116.6, 40.0}, {116.2, 40.0}, {116.2, 39.8}}},
		},
	}

	path, err := SaveKMLForASF(p, tmpDir)
	if err != nil {
		t.Fatalf("SaveKMLForASF failed: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("KML file not created: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("KML file is empty")
	}

	// Skip if already exists
	path2, err := SaveKMLForASF(p, tmpDir)
	if err != nil {
		t.Fatalf("second SaveKMLForASF failed: %v", err)
	}
	if path2 != path {
		t.Error("expected same path on skip")
	}
}

func TestSaveKMLForASF_NoGeometry(t *testing.T) {
	tmpDir := t.TempDir()
	p := asfResult{SceneName: "S1A_TEST"}
	_, err := SaveKMLForASF(p, tmpDir)
	if err == nil {
		t.Fatal("expected error for missing geometry")
	}
}

func TestFormatDate(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2025-01-15T00:00:00Z", "2025-01-15"},
		{"2025-01-15", "2025-01-15"},
		{"2025", "2025"},
	}
	for _, tt := range tests {
		got := formatDate(tt.input)
		if got != tt.want {
			t.Errorf("formatDate(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDownloadASFProductOnce(t *testing.T) {
	data := []byte("FAKE_SAR_DATA")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		w.Write(data)
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	product := asfResult{
		SceneName: "S1A_TEST",
		FileName:  "S1A_TEST.zip",
		URL:       srv.URL + "/test.zip",
		FileSize:  int64(len(data)),
	}

	finalSize, err := downloadASFProductOnce(NoOpAuth{}, product, tmpDir, nil)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if finalSize != int64(len(data)) {
		t.Errorf("expected size %d, got %d", len(data), finalSize)
	}
}
