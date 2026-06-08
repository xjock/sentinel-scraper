package main

import (
	"archive/zip"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestQueryODataProducts(t *testing.T) {
	oldURL := cdseODataCatalogURL
	defer func() { cdseODataCatalogURL = oldURL }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("$top") != "5" {
			t.Errorf("expected $top=5, got %s", q.Get("$top"))
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"value":[{"Id":"abc","Name":"S2A_TEST","ContentLength":1048576,"OriginDate":"2025-01-15T00:00:00Z","Online":true,"GeoFootprint":{"type":"Polygon","Coordinates":[[[0,0],[1,0],[1,1],[0,1],[0,0]]]}}],"@odata.count":1}`)
	}))
	defer srv.Close()
	cdseODataCatalogURL = srv.URL

	cfg := &Config{
		BBox:       []float64{0, 0, 1, 1},
		StartDate:  "2025-01-01",
		EndDate:    "2025-01-31",
		Limit:      5,
		Collection: "sentinel-2-l2a",
	}

	auth := NoOpAuth{}
	products, err := queryODataProducts(auth, cfg)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(products) != 1 {
		t.Fatalf("expected 1 product, got %d", len(products))
	}
	if products[0].Name != "S2A_TEST" {
		t.Errorf("expected S2A_TEST, got %s", products[0].Name)
	}
	if !products[0].Online {
		t.Error("expected product to be online")
	}
}

func TestQueryODataProducts_HTTPError(t *testing.T) {
	oldURL := cdseODataCatalogURL
	defer func() { cdseODataCatalogURL = oldURL }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("invalid bbox"))
	}))
	defer srv.Close()
	cdseODataCatalogURL = srv.URL

	cfg := &Config{
		BBox:       []float64{0, 0, 1, 1},
		StartDate:  "2025-01-01",
		EndDate:    "2025-01-31",
		Limit:      1,
		Collection: "sentinel-2-l2a",
	}

	_, err := queryODataProducts(NoOpAuth{}, cfg)
	if err == nil {
		t.Fatal("expected error for HTTP 400")
	}
}

func TestSaveKMLForOData(t *testing.T) {
	tmpDir := t.TempDir()
	p := odataProduct{
		Name:       "S2A_T50TMK_20250101",
		OriginDate: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		GeoFootprint: Geometry{
			Type:        "Polygon",
			Coordinates: [][][]float64{{{116.2, 39.8}, {116.6, 39.8}, {116.6, 40.0}, {116.2, 40.0}, {116.2, 39.8}}},
		},
	}

	path, err := SaveKMLForOData(p, tmpDir)
	if err != nil {
		t.Fatalf("SaveKMLForOData failed: %v", err)
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
	path2, err := SaveKMLForOData(p, tmpDir)
	if err != nil {
		t.Fatalf("second SaveKMLForOData failed: %v", err)
	}
	if path2 != path {
		t.Error("expected same path on skip")
	}
}

func TestSaveKMLForOData_NoGeometry(t *testing.T) {
	tmpDir := t.TempDir()
	p := odataProduct{Name: "S2A_TEST"}
	_, err := SaveKMLForOData(p, tmpDir)
	if err == nil {
		t.Fatal("expected error for missing geometry")
	}
}

func TestExtractRGBJP2s(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "test.zip")

	// Create a minimal zip with the expected JP2 files
	zw := zip.NewWriter(nil)
	f, _ := os.Create(zipPath)
	zw = zip.NewWriter(f)

	for _, name := range []string{"S2A_T50TMK_20250101T030529_A039050_T50TMK/S2A_T50TMK_20250101T030529_A039050_T50TMK.SAFE/GRANULE/L2A_T50TMK_A20250101T030529/IMG_DATA/R10m/T50TMK_20250101T030529_B02_10m.jp2",
		"S2A_T50TMK_20250101T030529_A039050_T50TMK/S2A_T50TMK_20250101T030529_A039050_T50TMK.SAFE/GRANULE/L2A_T50TMK_A20250101T030529/IMG_DATA/R10m/T50TMK_20250101T030529_B03_10m.jp2",
		"S2A_T50TMK_20250101T030529_A039050_T50TMK/S2A_T50TMK_20250101T030529_A039050_T50TMK.SAFE/GRANULE/L2A_T50TMK_A20250101T030529/IMG_DATA/R10m/T50TMK_20250101T030529_B04_10m.jp2"} {
		w, _ := zw.Create(name)
		w.Write([]byte("FAKE_JP2_DATA"))
	}
	zw.Close()
	f.Close()

	outDir := filepath.Join(tmpDir, "extract")
	red, green, blue, err := extractRGBJP2s(zipPath, outDir)
	if err != nil {
		t.Fatalf("extractRGBJP2s failed: %v", err)
	}
	for _, p := range []string{red, green, blue} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected file %s to exist: %v", p, err)
		}
	}
}

func TestExtractRGBJP2s_MissingBands(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "test.zip")

	f, _ := os.Create(zipPath)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("other.txt")
	w.Write([]byte("hello"))
	zw.Close()
	f.Close()

	_, _, _, err := extractRGBJP2s(zipPath, tmpDir)
	if err == nil {
		t.Fatal("expected error for missing bands")
	}
}
