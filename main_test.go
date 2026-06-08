package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func f64ptr(v float64) *float64 { return &v }

func TestLoadConfig(t *testing.T) {
	tmpDir := t.TempDir()

	valid := Config{
		BBox:       []float64{116.2, 39.8, 116.6, 40.0},
		StartDate:  "2025-01-01",
		EndDate:    "2025-01-15",
		MaxCloud:   20,
		Bands:      []string{"red", "green"},
		Limit:      10,
		MaxWorkers: 8,
	}
	data, _ := json.Marshal(valid)
	validPath := filepath.Join(tmpDir, "valid.json")
	if err := os.WriteFile(validPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(validPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Limit != 10 {
		t.Errorf("expected limit=10, got %d", cfg.Limit)
	}
	if cfg.MaxWorkers != 8 {
		t.Errorf("expected max_workers=8, got %d", cfg.MaxWorkers)
	}

	defaults := `{"bbox":[0,0,1,1],"start_date":"2025-01-01","end_date":"2025-01-02","bands":["red"]}`
	defaultPath := filepath.Join(tmpDir, "default.json")
	if err := os.WriteFile(defaultPath, []byte(defaults), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfig(defaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Limit != 20 {
		t.Errorf("expected default limit=20, got %d", cfg.Limit)
	}
	if cfg.MaxWorkers != 4 {
		t.Errorf("expected default max_workers=4, got %d", cfg.MaxWorkers)
	}

	_, err = LoadConfig(filepath.Join(tmpDir, "missing.json"))
	if err == nil {
		t.Error("expected error for missing file")
	}

	badJSONPath := filepath.Join(tmpDir, "bad.json")
	os.WriteFile(badJSONPath, []byte("not json"), 0644)
	_, err = LoadConfig(badJSONPath)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestFilterItemsByCloud(t *testing.T) {
	items := []STACItem{
		{ID: "a", Properties: STACProperties{CloudCover: f64ptr(5)}},
		{ID: "b", Properties: STACProperties{CloudCover: f64ptr(20)}},
		{ID: "c", Properties: STACProperties{CloudCover: f64ptr(21)}},
		{ID: "d", Properties: STACProperties{CloudCover: f64ptr(0)}},
		{ID: "e", Properties: STACProperties{CloudCover: nil}},
	}
	filtered := FilterItemsByCloud(items, 0, 20, SatS2L2A)
	if len(filtered) != 4 {
		t.Errorf("expected 4 items, got %d", len(filtered))
	}
	ids := make([]string, len(filtered))
	for i, it := range filtered {
		ids[i] = it.ID
	}
	want := []string{"a", "b", "d", "e"}
	for i, id := range want {
		if ids[i] != id {
			t.Errorf("expected item %s at index %d, got %s", id, i, ids[i])
		}
	}
}

func TestSearchItems(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Errorf("expected path /search, got %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("collections") != Collection {
			t.Errorf("expected collection %s, got %s", Collection, q.Get("collections"))
		}
		queryParam := q.Get("query")
		if !strings.Contains(queryParam, "eo:cloud_cover") || !strings.Contains(queryParam, "lte") {
			t.Errorf("expected server-side cloud filter, got %s", queryParam)
		}
		bbox := q.Get("bbox")
		if bbox != "116.200000,39.800000,116.600000,40.000000" {
			t.Errorf("unexpected bbox: %s", bbox)
		}
		datetime := q.Get("datetime")
		if datetime != "2025-01-01T00:00:00Z/2025-01-15T23:59:59Z" {
			t.Errorf("unexpected datetime: %s", datetime)
		}

		resp := STACItemCollection{
			Type: "FeatureCollection",
			Features: []STACItem{
				{
					ID:   "S2A_20250105",
					Type: "Feature",
					Properties: STACProperties{
						Datetime:   "2025-01-05T00:00:00Z",
						CloudCover: f64ptr(10),
					},
					Assets: map[string]Asset{
						"red": {Href: srv.URL + "/red.tif"},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/geo+json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	oldURL := EarthSearchURL
	EarthSearchURL = srv.URL
	defer func() { EarthSearchURL = oldURL }()

	opts := SearchOptions{
		Bbox:      []float64{116.2, 39.8, 116.6, 40.0},
		StartDate: "2025-01-01",
		EndDate:   "2025-01-15",
		Limit:     5,
		MaxCloud:  20,
	}
	collection, err := SearchItems(opts, NoOpAuth{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(collection.Features) != 1 {
		t.Fatalf("expected 1 feature, got %d", len(collection.Features))
	}
	if collection.Features[0].ID != "S2A_20250105" {
		t.Errorf("unexpected ID: %s", collection.Features[0].ID)
	}
}

func TestSearchItems_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("invalid bbox"))
	}))
	defer srv.Close()

	oldURL := EarthSearchURL
	EarthSearchURL = srv.URL
	defer func() { EarthSearchURL = oldURL }()

	_, err := SearchItems(SearchOptions{
		Bbox:      []float64{0, 0, 1, 1},
		StartDate: "2025-01-01",
		EndDate:   "2025-01-02",
		Limit:     1,
	}, NoOpAuth{})
	if err == nil {
		t.Fatal("expected error for HTTP 400")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected error to mention status 400, got: %v", err)
	}
}

func TestDownloadAsset_Success(t *testing.T) {
	tmpDir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/tiff")
		w.Write([]byte("FAKE_TIFF_DATA"))
	}))
	defer srv.Close()

	asset := Asset{Href: srv.URL + "/test.tif"}
	path, _, err := DownloadAsset(asset, tmpDir, "S2A_TEST", "red", NoOpAuth{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(data) != "FAKE_TIFF_DATA" {
		t.Errorf("unexpected content: %s", string(data))
	}
}

func TestDownloadAsset_HTTPError(t *testing.T) {
	tmpDir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	asset := Asset{Href: srv.URL + "/missing.tif"}
	_, _, err := DownloadAsset(asset, tmpDir, "S2A_TEST", "red", NoOpAuth{})
	if err == nil {
		t.Fatal("expected error for HTTP 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected error to mention 404, got: %v", err)
	}
}

func TestDownloadAsset_Timeout(t *testing.T) {
	oldTimeout := DownloadTimeout
	DownloadTimeout = 50 * time.Millisecond
	defer func() { DownloadTimeout = oldTimeout }()

	tmpDir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	asset := Asset{Href: srv.URL + "/slow.tif"}
	_, _, err := DownloadAsset(asset, tmpDir, "S2A_TEST", "red", NoOpAuth{})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "Client.Timeout") && !strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected timeout-related error, got: %v", err)
	}
}

func TestDownloadWorker(t *testing.T) {
	tmpDir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("TIFF"))
	}))
	defer srv.Close()

	tasks := make(chan downloadTask, 3)
	results := make(chan downloadResult, 3)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		downloadWorker(tasks, results)
	}()

	for i := 1; i <= 3; i++ {
		tasks <- downloadTask{
			itemID:  fmt.Sprintf("ITEM%d", i),
			band:    "red",
			asset:   Asset{Href: srv.URL + fmt.Sprintf("/%d.tif", i)},
			destDir: tmpDir,
			auth:    NoOpAuth{},
		}
	}
	close(tasks)
	wg.Wait()
	close(results)

	count := 0
	for res := range results {
		if res.err != nil {
			t.Errorf("unexpected error for %s: %v", res.task.itemID, res.err)
		}
		count++
	}
	if count != 3 {
		t.Errorf("expected 3 results, got %d", count)
	}
}

func TestDownloadWorker_SkipExisting(t *testing.T) {
	tmpDir := t.TempDir()
	existingFile := filepath.Join(tmpDir, "ITEM1_red.tif")
	os.WriteFile(existingFile, []byte("new"), 0644)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("new"))
	}))
	defer srv.Close()

	tasks := make(chan downloadTask, 2)
	results := make(chan downloadResult, 2)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		downloadWorker(tasks, results)
	}()

	tasks <- downloadTask{itemID: "ITEM1", band: "red", asset: Asset{Href: srv.URL + "/1.tif"}, destDir: tmpDir, auth: NoOpAuth{}}
	tasks <- downloadTask{itemID: "ITEM2", band: "red", asset: Asset{Href: srv.URL + "/2.tif"}, destDir: tmpDir, auth: NoOpAuth{}}
	close(tasks)
	wg.Wait()
	close(results)

	var skipped, downloaded int
	for res := range results {
		if res.skipped {
			skipped++
			if res.task.itemID != "ITEM1" {
				t.Errorf("expected ITEM1 to be skipped, got %s", res.task.itemID)
			}
		} else if res.err == nil {
			downloaded++
			if res.task.itemID != "ITEM2" {
				t.Errorf("expected ITEM2 to be downloaded, got %s", res.task.itemID)
			}
		}
	}
	if skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", skipped)
	}
	if downloaded != 1 {
		t.Errorf("expected 1 downloaded, got %d", downloaded)
	}
}

func TestDownloadWorker_Concurrent(t *testing.T) {
	tmpDir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	workers := 4
	tasks := make(chan downloadTask, workers*2)
	results := make(chan downloadResult, workers*2)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			downloadWorker(tasks, results)
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	total := 8
	for i := 0; i < total; i++ {
		tasks <- downloadTask{
			itemID:  fmt.Sprintf("C%d", i),
			band:    "nir",
			asset:   Asset{Href: srv.URL + fmt.Sprintf("/%d.tif", i)},
			destDir: tmpDir,
			auth:    NoOpAuth{},
		}
	}
	close(tasks)

	okCount := 0
	for res := range results {
		if res.err != nil {
			t.Errorf("unexpected error: %v", res.err)
		} else {
			okCount++
		}
	}
	if okCount != total {
		t.Errorf("expected %d successful downloads, got %d", total, okCount)
	}
}

func TestParseSatelliteType(t *testing.T) {
	tests := []struct {
		input    string
		expected SatelliteType
	}{
		{"sentinel-2-l2a", SatS2L2A},
		{"sentinel-2", SatS2L2A},
		{"sentinel-1-grd", SatS1GRD},
		{"sentinel1_grd", SatS1GRD},
		{"SENTINEL1_GRD", SatS1GRD},
		{"sentinel-1-slc", SatS1SLC},
		{"sentinel1_slc", SatS1SLC},
		{"SENTINEL1_SLC", SatS1SLC},
		{"", SatS2L2A},
		{"hls", SatHLS},
		{"HLSS30.v2.0", SatHLS},
		{"HLSL30.v2.0", SatHLS},
		{"unknown", SatS2L2A},
	}
	for _, tt := range tests {
		got := ParseSatelliteType(tt.input)
		if got != tt.expected {
			t.Errorf("ParseSatelliteType(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFilterItemsByCloud_Range(t *testing.T) {
	items := []STACItem{
		{ID: "a", Properties: STACProperties{CloudCover: f64ptr(5)}},
		{ID: "b", Properties: STACProperties{CloudCover: f64ptr(15)}},
		{ID: "c", Properties: STACProperties{CloudCover: f64ptr(25)}},
		{ID: "d", Properties: STACProperties{CloudCover: f64ptr(0)}},
		{ID: "e", Properties: STACProperties{CloudCover: nil}},
	}
	filtered := FilterItemsByCloud(items, 10, 20, SatS2L2A)
	if len(filtered) != 2 {
		t.Errorf("expected 2 items, got %d", len(filtered))
	}
	ids := make([]string, len(filtered))
	for i, it := range filtered {
		ids[i] = it.ID
	}
	want := []string{"b", "e"}
	for i, id := range want {
		if ids[i] != id {
			t.Errorf("expected item %s at index %d, got %s", id, i, ids[i])
		}
	}
}

func TestFilterItemsByCloud_S1Skip(t *testing.T) {
	items := []STACItem{
		{ID: "a", Properties: STACProperties{CloudCover: f64ptr(95)}},
		{ID: "b", Properties: STACProperties{CloudCover: f64ptr(5)}},
	}
	filtered := FilterItemsByCloud(items, 0, 10, SatS1GRD)
	if len(filtered) != 2 {
		t.Errorf("expected 2 items (S1 skips cloud filter), got %d", len(filtered))
	}
}

func TestSearchItems_CloudRange(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		queryParam := q.Get("query")
		if !strings.Contains(queryParam, `"gte":10`) || !strings.Contains(queryParam, `"lte":20`) {
			t.Errorf("expected cloud range query, got %s", queryParam)
		}
		resp := STACItemCollection{
			Type: "FeatureCollection",
			Features: []STACItem{
				{ID: "S2A_20250105", Type: "Feature", Properties: STACProperties{Datetime: "2025-01-05T00:00:00Z", CloudCover: f64ptr(15)}},
			},
		}
		w.Header().Set("Content-Type", "application/geo+json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	oldURL := EarthSearchURL
	EarthSearchURL = srv.URL
	defer func() { EarthSearchURL = oldURL }()

	opts := SearchOptions{
		Bbox:      []float64{116.2, 39.8, 116.6, 40.0},
		StartDate: "2025-01-01",
		EndDate:   "2025-01-15",
		Limit:     5,
		MinCloud:  10,
		MaxCloud:  20,
	}
	collection, err := SearchItems(opts, NoOpAuth{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(collection.Features) != 1 {
		t.Fatalf("expected 1 feature, got %d", len(collection.Features))
	}
}

func TestSearchItems_S1NoCloudFilter(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		queryParam := q.Get("query")
		if strings.Contains(queryParam, "eo:cloud_cover") {
			t.Errorf("S1 should not send cloud filter, got %s", queryParam)
		}
		resp := STACItemCollection{
			Type:     "FeatureCollection",
			Features: []STACItem{{ID: "S1A_20250105", Type: "Feature", Properties: STACProperties{Datetime: "2025-01-05T00:00:00Z"}}},
		}
		w.Header().Set("Content-Type", "application/geo+json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	oldURL := EarthSearchURL
	EarthSearchURL = srv.URL
	defer func() { EarthSearchURL = oldURL }()

	opts := SearchOptions{
		Bbox:      []float64{116.2, 39.8, 116.6, 40.0},
		StartDate: "2025-01-01",
		EndDate:   "2025-01-15",
		Limit:     5,
		MaxCloud:  20,
		Satellite: SatS1GRD,
	}
	collection, err := SearchItems(opts, NoOpAuth{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(collection.Features) != 1 {
		t.Fatalf("expected 1 feature, got %d", len(collection.Features))
	}
}

func TestResolveAssetKey_S1(t *testing.T) {
	key := resolveAssetKey("vv", "https://stac.dataspace.copernicus.eu/v1", SatS1GRD)
	if key != "vv" {
		t.Errorf("expected vv, got %s", key)
	}
	key = resolveAssetKey("vh", "https://earth-search.aws.element84.com/v1", SatS1GRD)
	if key != "vh" {
		t.Errorf("expected vh, got %s", key)
	}
}

func TestBuildRGB_S1Skip(t *testing.T) {
	err := BuildRGB("", "S1A_TEST", SatS1GRD)
	if err != nil {
		t.Errorf("S1 should skip RGB without error, got %v", err)
	}
}
