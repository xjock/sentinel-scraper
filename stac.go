package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// satConfig holds per-satellite metadata used to drive search, download and post-processing.
type satConfig struct {
	Collection       string
	CDSECollection   string
	NeedsCloudFilter bool
	SupportsRGB      bool
	DefaultBands     []string
	BandMap          map[string]string
	KnownBands       []string
	ODataCollection  string
	ODataProductType string
}

// satelliteConfigs maps each supported satellite type to its configuration.
var satelliteConfigs = map[SatelliteType]satConfig{
	SatS2L2A: {
		Collection:       "sentinel-2-l2a",
		CDSECollection:   "sentinel-2-l2a",
		NeedsCloudFilter: true,
		SupportsRGB:      true,
		DefaultBands:     []string{"red", "green", "blue"},
		BandMap: map[string]string{
			"coastal": "B01_60m", "blue": "B02_10m", "green": "B03_10m", "red": "B04_10m",
			"rededge1": "B05_20m", "rededge2": "B06_20m", "rededge3": "B07_20m",
			"nir": "B08_10m", "nir08": "B8A_20m", "nir09": "B09_60m",
			"swir16": "B11_20m", "swir22": "B12_20m", "scl": "SCL_20m",
			"aot": "AOT_20m", "wvp": "WVP_10m", "tci": "TCI_10m",
		},
		KnownBands:       []string{"coastal", "blue", "green", "red", "rededge1", "rededge2", "rededge3", "nir", "nir08", "nir09", "swir16", "swir22", "scl"},
		ODataCollection:  "SENTINEL-2",
		ODataProductType: "S2MSI2A",
	},
	SatS1GRD: {
		Collection:       "sentinel-1-grd",
		CDSECollection:   "SENTINEL1_GRD",
		NeedsCloudFilter: false,
		SupportsRGB:      false,
		DefaultBands:     []string{"vv", "vh"},
		BandMap: map[string]string{
			"vv": "vv", "vh": "vh", "hh": "hh", "hv": "hv",
		},
		KnownBands:       []string{"vv", "vh", "hh", "hv"},
		ODataCollection:  "SENTINEL-1",
		ODataProductType: "GRD",
	},
	SatS1SLC: {
		Collection:       "sentinel-1-slc",
		CDSECollection:   "SENTINEL1_SLC",
		NeedsCloudFilter: false,
		SupportsRGB:      false,
		DefaultBands:     []string{"vv", "vh"},
		BandMap: map[string]string{
			"vv": "vv", "vh": "vh", "hh": "hh", "hv": "hv",
		},
		KnownBands:       []string{"vv", "vh", "hh", "hv"},
		ODataCollection:  "SENTINEL-1",
		ODataProductType: "SLC",
	},
}

func resolveAssetKey(band, stacURL string, sat SatelliteType) string {
	cfg := satelliteConfigs[sat]
	if strings.Contains(stacURL, "stac.dataspace.copernicus.eu") {
		if key, ok := cfg.BandMap[band]; ok {
			return key
		}
	}
	return band
}

type STACItemCollection struct {
	Type     string     `json:"type"`
	Features []STACItem `json:"features"`
}

type Geometry struct {
	Type        string        `json:"type"`
	Coordinates [][][]float64 `json:"coordinates"`
}

type STACItem struct {
	ID         string           `json:"id"`
	Type       string           `json:"type"`
	Collection string           `json:"collection"`
	BBox       []float64        `json:"bbox"`
	Geometry   Geometry         `json:"geometry"`
	Properties STACProperties   `json:"properties"`
	Assets     map[string]Asset `json:"assets"`
}

type STACProperties struct {
	Datetime   string   `json:"datetime"`
	Created    string   `json:"created"`
	CloudCover *float64 `json:"eo:cloud_cover,omitempty"`
	GranuleID  string   `json:"s2:granule_id,omitempty"`
}

type AlternateLink struct {
	Href string `json:"href"`
}

type Asset struct {
	Href      string                   `json:"href"`
	Type      string                   `json:"type"`
	Title     string                   `json:"title"`
	Roles     []string                 `json:"roles"`
	Alternate map[string]AlternateLink `json:"alternate,omitempty"`
}

type downloadTask struct {
	itemID     string
	band       string
	asset      Asset
	destDir    string
	maxRetries int
	auth       Authenticator
}

type downloadResult struct {
	path    string
	err     error
	skipped bool
	task    downloadTask
}

func SearchItems(opts SearchOptions, auth Authenticator) (*STACItemCollection, error) {
	if opts.Satellite == "" {
		opts.Satellite = SatS2L2A
	}
	if opts.Limit == 0 {
		opts.Limit = 10
	}
	if len(opts.Bbox) != 4 {
		return nil, fmt.Errorf("bbox must have 4 elements [west,south,east,north]")
	}
	bboxStr := fmt.Sprintf("%f,%f,%f,%f", opts.Bbox[0], opts.Bbox[1], opts.Bbox[2], opts.Bbox[3])
	datetime := fmt.Sprintf("%sT00:00:00Z/%sT23:59:59Z", opts.StartDate, opts.EndDate)

	stacURL := opts.STACURL
	if stacURL == "" {
		stacURL = EarthSearchURL
	}

	cfg := satelliteConfigs[opts.Satellite]
	collection := opts.Collection
	if collection == "" {
		if strings.Contains(stacURL, "stac.dataspace.copernicus.eu") {
			collection = cfg.CDSECollection
		} else {
			collection = cfg.Collection
		}
	}

	u, err := url.Parse(stacURL + "/search")
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	q := u.Query()
	q.Set("collections", collection)
	q.Set("bbox", bboxStr)
	q.Set("datetime", datetime)
	q.Set("limit", fmt.Sprintf("%d", opts.Limit))

	if cfg.NeedsCloudFilter {
		if opts.MinCloud > 0 && opts.MaxCloud > 0 {
			q.Set("query", fmt.Sprintf(`{"eo:cloud_cover":{"gte":%f,"lte":%f}}`, opts.MinCloud, opts.MaxCloud))
		} else if opts.MaxCloud > 0 {
			q.Set("query", fmt.Sprintf(`{"eo:cloud_cover":{"lte":%f}}`, opts.MaxCloud))
		} else if opts.MinCloud > 0 {
			q.Set("query", fmt.Sprintf(`{"eo:cloud_cover":{"gte":%f}}`, opts.MinCloud))
		}
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/geo+json")
	if err := auth.Apply(req); err != nil {
		return nil, fmt.Errorf("authenticate request: %w", err)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("STAC API returned %d: %s", resp.StatusCode, string(body))
	}

	var result STACItemCollection
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	return &result, nil
}

func FilterItemsByCloud(items []STACItem, minCloud, maxCloud float64, sat SatelliteType) []STACItem {
	if sat == "" {
		sat = SatS2L2A
	}
	cfg := satelliteConfigs[sat]
	if !cfg.NeedsCloudFilter {
		return items
	}
	var filtered []STACItem
	for _, item := range items {
		if item.Properties.CloudCover == nil {
			filtered = append(filtered, item)
			continue
		}
		cc := *item.Properties.CloudCover
		pass := true
		if minCloud > 0 && cc < minCloud {
			pass = false
		}
		if maxCloud > 0 && cc > maxCloud {
			pass = false
		}
		if pass {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func SaveKML(item STACItem, destDir string) (string, error) {
	if item.Geometry.Type != "Polygon" || len(item.Geometry.Coordinates) == 0 {
		return "", fmt.Errorf("no polygon geometry for %s", item.ID)
	}

	kmlPath := filepath.Join(destDir, item.ID+".kml")
	if _, err := os.Stat(kmlPath); err == nil {
		fmt.Printf("  [skip] %s already exists\n", item.ID+".kml")
		return kmlPath, nil
	}

	var fields []kmlField
	fields = append(fields, kmlField{Name: "id", Value: item.ID})
	fields = append(fields, kmlField{Name: "collection", Value: item.Collection})
	fields = append(fields, kmlField{Name: "datetime", Value: item.Properties.Datetime})
	fields = append(fields, kmlField{Name: "created", Value: item.Properties.Created})
	fields = append(fields, kmlField{Name: "granule_id", Value: item.Properties.GranuleID})
	if item.Properties.CloudCover != nil {
		fields = append(fields, kmlField{Name: "cloud_cover", Value: fmt.Sprintf("%.2f", *item.Properties.CloudCover)})
	}
	if len(item.BBox) == 4 {
		fields = append(fields, kmlField{Name: "bbox", Value: fmt.Sprintf("%.6f,%.6f,%.6f,%.6f", item.BBox[0], item.BBox[1], item.BBox[2], item.BBox[3])})
	}

	if err := writeKMLFile(kmlPath, item.ID, item.Geometry.Coordinates[0], fields); err != nil {
		return "", err
	}
	fmt.Printf("  [saved] %s\n", item.ID+".kml")
	return kmlPath, nil
}

func parseItemIDFromFilename(filename string, sat SatelliteType) string {
	if !strings.HasSuffix(filename, ".tif") {
		return ""
	}
	cfg := satelliteConfigs[sat]
	base := strings.TrimSuffix(filename, ".tif")
	for _, band := range cfg.KnownBands {
		suffix := "_" + band
		if strings.HasSuffix(base, suffix) {
			return strings.TrimSuffix(base, suffix)
		}
	}
	return ""
}

func resolveDownloadURL(asset Asset) string {
	if strings.HasPrefix(asset.Href, "s3://") {
		if alt, ok := asset.Alternate["https"]; ok && alt.Href != "" {
			return alt.Href
		}
	}
	return asset.Href
}

func scanExistingItems(destDir string, sat SatelliteType) (map[string]bool, error) {
	items := make(map[string]bool)
	entries, err := os.ReadDir(destDir)
	if err != nil {
		if os.IsNotExist(err) {
			return items, nil
		}
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		itemID := parseItemIDFromFilename(entry.Name(), sat)
		if itemID != "" {
			items[itemID] = true
		}
	}
	return items, nil
}

func fetchItem(itemID, stacURL, collection string, auth Authenticator) (STACItem, error) {
	if stacURL == "" {
		stacURL = EarthSearchURL
	}
	if collection == "" {
		collection = Collection
	}
	u, err := url.Parse(fmt.Sprintf("%s/collections/%s/items/%s", stacURL, collection, itemID))
	if err != nil {
		return STACItem{}, fmt.Errorf("parse URL: %w", err)
	}

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return STACItem{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/geo+json")
	if err := auth.Apply(req); err != nil {
		return STACItem{}, fmt.Errorf("authenticate request: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return STACItem{}, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return STACItem{}, fmt.Errorf("STAC API returned %d: %s", resp.StatusCode, string(body))
	}

	var item STACItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return STACItem{}, fmt.Errorf("decode JSON: %w", err)
	}
	if item.Geometry.Type != "Polygon" || len(item.Geometry.Coordinates) == 0 {
		return STACItem{}, fmt.Errorf("no polygon geometry in response")
	}
	return item, nil
}

func assetExists(destDir, itemID, bandName string) bool {
	filename := fmt.Sprintf("%s_%s.tif", itemID, bandName)
	_, err := os.Stat(filepath.Join(destDir, filename))
	return err == nil
}

func DownloadAsset(asset Asset, destDir string, itemID string, bandName string, auth Authenticator) (string, bool, error) {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", false, fmt.Errorf("mkdir %s: %w", destDir, err)
	}

	filename := fmt.Sprintf("%s_%s.tif", itemID, bandName)
	destPath := filepath.Join(destDir, filename)

	url := resolveDownloadURL(asset)
	client := &http.Client{Timeout: DownloadTimeout}
	label := fmt.Sprintf("%s/%s", itemID, bandName)
	ctx, cancel := context.WithTimeout(context.Background(), DownloadTimeout)
	defer cancel()
	finalSize, total, skipped, err := resumableDownload(ctx, client, url, auth, destPath, label, 0)
	if err != nil {
		return "", false, err
	}
	if skipped {
		return destPath, true, nil
	}
	if total > 0 && finalSize != total {
		os.Remove(destPath)
		return "", false, fmt.Errorf("size mismatch: got %s, expected %s", formatBytes(finalSize), formatBytes(total))
	}
	return destPath, false, nil
}

func downloadWorker(tasks <-chan downloadTask, results chan<- downloadResult) {
	for task := range tasks {
		var path string
		var skipped bool
		var err error
		for attempt := 0; attempt <= task.maxRetries; attempt++ {
			path, skipped, err = DownloadAsset(task.asset, task.destDir, task.itemID, task.band, task.auth)
			if err == nil || skipped {
				break
			}
			if attempt < task.maxRetries {
				wait := time.Duration(attempt+1) * time.Second
				fmt.Fprintf(os.Stderr, "  [retry] %s/%s in %.0fs (attempt %d/%d): %v\n", task.itemID, task.band, wait.Seconds(), attempt+1, task.maxRetries, err)
				time.Sleep(wait)
			}
		}
		results <- downloadResult{path: path, skipped: skipped, err: err, task: task}
	}
}

func PrintItemSummary(items []STACItem) {
	fmt.Println("\n=== Found Items ===")
	for _, item := range items {
		dt := item.Properties.Datetime
		if dt == "" {
			dt = item.Properties.Created
		}
		cloudStr := "N/A"
		if item.Properties.CloudCover != nil {
			cloudStr = fmt.Sprintf("%.1f%%", *item.Properties.CloudCover)
		}
		fmt.Printf("- %s | Date: %s | Cloud: %s | BBox: %v\n",
			item.ID, dt, cloudStr, item.BBox)
	}
}
