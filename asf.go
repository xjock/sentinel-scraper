package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var asfSearchURL = "https://api.daac.asf.alaska.edu/services/search/param"

// asfResult represents a single product from ASF Search API response.
type asfResult struct {
	SceneName    string   `json:"sceneName"`
	FileName     string   `json:"fileName"`
	URL          string   `json:"url"`
	FileSize     int64    `json:"fileSize,string"`
	Platform     string   `json:"platform"`
	StartTime    string   `json:"startTime"`
	StopTime     string   `json:"stopTime"`
	Path         int      `json:"pathNumber"`
	Frame        int      `json:"frameNumber"`
	Orbit        int      `json:"orbit"`
	Polarization string   `json:"polarization"`
	Processing   string   `json:"processingLevel"`
	Sensor       string   `json:"sensor"`
	Geometry     Geometry `json:"geometry"`
}

// asfSearchResponse wraps the GeoJSON FeatureCollection from ASF.
type asfSearchResponse struct {
	Type     string       `json:"type"`
	Features []asfFeature `json:"features"`
}

type asfFeature struct {
	Type       string          `json:"type"`
	Properties asfResult       `json:"properties"`
	Geometry   json.RawMessage `json:"geometry"`
}

func queryASFProducts(auth Authenticator, cfg *Config) ([]asfResult, error) {
	if len(cfg.BBox) != 4 {
		return nil, fmt.Errorf("bbox must have 4 elements [west,south,east,north]")
	}
	west, south, east, north := cfg.BBox[0], cfg.BBox[1], cfg.BBox[2], cfg.BBox[3]

	// Build WKT POLYGON for ASF intersectsWith
	polygon := fmt.Sprintf(
		"POLYGON((%f %f,%f %f,%f %f,%f %f,%f %f))",
		west, south, east, south, east, north, west, north, west, south,
	)

	q := url.Values{}
	q.Set("intersectsWith", polygon)
	q.Set("start", cfg.StartDate)
	q.Set("end", cfg.EndDate)
	q.Set("output", "geojson")

	// Map satellite type to ASF platform and processing level
	sat := SatelliteType(cfg.Satellite)
	if sat == "" {
		sat = ParseSatelliteType(cfg.Collection)
	}
	// ASF only serves S1; default to GRD if not specified
	if sat != SatS1GRD && sat != SatS1SLC {
		sat = SatS1GRD
	}
	sc := satelliteConfigs[sat]

	q.Set("platform", "S1")
	if sc.ASFProductType != "" {
		q.Set("processingLevel", sc.ASFProductType)
	}
	if cfg.Limit > 0 {
		q.Set("maxResults", fmt.Sprintf("%d", cfg.Limit))
	}

	searchURL := asfSearchURL + "?" + q.Encode()
	fmt.Printf("  ASF search URL: %s\n", searchURL)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", asfSearchURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if err := auth.Apply(req); err != nil {
		return nil, fmt.Errorf("authenticate: %w", err)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ASF returned %d: %s", resp.StatusCode, string(body))
	}

	var result asfSearchResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	products := make([]asfResult, 0, len(result.Features))
	for _, f := range result.Features {
		p := f.Properties
		// ASF GeoJSON has geometry at Feature level, not in properties
		if len(f.Geometry) > 0 {
			var geom Geometry
			if err := json.Unmarshal(f.Geometry, &geom); err == nil {
				p.Geometry = geom
			}
		}
		products = append(products, p)
	}

	// Client-side filter: ensure returned products match the requested processing level.
	filtered := make([]asfResult, 0, len(products))
	expectedPL := strings.ToLower(sc.ASFProductType)
	for _, p := range products {
		actualPL := strings.ToLower(p.Processing)
		// ASF uses values like "GRD_HD", "SLC", "METADATA". Allow prefix match for SLC/GRD.
		if strings.HasPrefix(actualPL, expectedPL) {
			filtered = append(filtered, p)
		} else {
			fmt.Printf("  [filter skip] %s processingLevel=%s (expected %s)\n", p.SceneName, p.Processing, sc.ASFProductType)
		}
	}

	// Apply limit
	if cfg.Limit > 0 && len(filtered) > cfg.Limit {
		filtered = filtered[:cfg.Limit]
	}

	fmt.Printf("  ASF returned %d products, %d after filter (expected processingLevel=%s)\n", len(products), len(filtered), sc.ASFProductType)
	return filtered, nil
}

func SaveKMLForASF(product asfResult, destDir string) (string, error) {
	if product.Geometry.Type != "Polygon" || len(product.Geometry.Coordinates) == 0 {
		return "", fmt.Errorf("no polygon geometry for %s", product.SceneName)
	}

	kmlPath := filepath.Join(destDir, product.SceneName+".kml")
	if _, err := os.Stat(kmlPath); err == nil {
		fmt.Printf("  [skip] %s already exists\n", product.SceneName+".kml")
		return kmlPath, nil
	}

	var fields []kmlField
	fields = append(fields, kmlField{Name: "scene", Value: product.SceneName})
	fields = append(fields, kmlField{Name: "platform", Value: product.Platform})
	fields = append(fields, kmlField{Name: "start", Value: product.StartTime})
	fields = append(fields, kmlField{Name: "stop", Value: product.StopTime})
	fields = append(fields, kmlField{Name: "processing", Value: product.Processing})
	fields = append(fields, kmlField{Name: "polarization", Value: product.Polarization})

	if err := writeKMLFile(kmlPath, product.SceneName, product.Geometry.Coordinates[0], fields); err != nil {
		return "", err
	}
	fmt.Printf("  [saved] %s\n", product.SceneName+".kml")
	return kmlPath, nil
}

// ursLogin performs a browser-style login to NASA Earthdata URS and returns
// a cookie jar containing the session cookies needed for ASF downloads.
func ursLogin(username, password string) (http.CookieJar, error) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return nil
		},
	}

	// 1. Get URS login page to extract CSRF token
	resp, err := client.Get("https://urs.earthdata.nasa.gov/home")
	if err != nil {
		return nil, fmt.Errorf("URS login page: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	token := ""
	if idx := strings.Index(string(body), `name="authenticity_token" value="`); idx != -1 {
		start := idx + len(`name="authenticity_token" value="`)
		if end := strings.Index(string(body[start:]), `"`); end != -1 {
			token = string(body[start : start+end])
		}
	}
	if token == "" {
		return nil, fmt.Errorf("could not extract URS authenticity token")
	}

	// 2. Submit login form
	data := url.Values{}
	data.Set("authenticity_token", token)
	data.Set("username", username)
	data.Set("password", password)
	data.Set("commit", "Log in")

	resp, err = client.Post("https://urs.earthdata.nasa.gov/login", "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("URS login submit: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		return nil, fmt.Errorf("URS login returned %d", resp.StatusCode)
	}

	return jar, nil
}

func downloadASFProductOnce(auth Authenticator, product asfResult, destDir string, ursJar http.CookieJar) (int64, error) {
	tmpPath := filepath.Join(destDir, product.FileName+".tmp")

	// ASF downloads redirect through Earthdata URS OAuth.
	// We need the URS session cookie jar plus CheckRedirect to preserve auth headers.
	client := &http.Client{
		Timeout: 30 * time.Minute,
		Jar:     ursJar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 0 {
				if h := via[0].Header.Get("Authorization"); h != "" {
					req.Header.Set("Authorization", h)
				}
			}
			return nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	finalSize, total, _, err := resumableDownload(ctx, client, product.URL, auth, tmpPath, product.SceneName, product.FileSize)
	if err != nil {
		return 0, err
	}
	if total > 0 && finalSize != total {
		os.Remove(tmpPath)
		return 0, fmt.Errorf("size mismatch: got %s, expected %s", formatBytes(finalSize), formatBytes(total))
	}
	return finalSize, nil
}

func downloadASFProduct(auth Authenticator, product asfResult, destDir string, maxRetries int, ursJar http.CookieJar) error {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	outputPath := filepath.Join(destDir, product.FileName)
	if _, err := os.Stat(outputPath); err == nil {
		fmt.Printf("  [skip] %s already exists\n", product.FileName)
		return nil
	}

	var finalSize int64
	var err error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		finalSize, err = downloadASFProductOnce(auth, product, destDir, ursJar)
		if err == nil {
			break
		}
		if attempt < maxRetries {
			wait := time.Duration(attempt+1) * time.Second
			fmt.Fprintf(os.Stderr, "  [retry] %s in %.0fs (attempt %d/%d): %v\n", product.FileName, wait.Seconds(), attempt+1, maxRetries, err)
			time.Sleep(wait)
		}
	}
	if err != nil {
		return err
	}

	tmpPath := filepath.Join(destDir, product.FileName+".tmp")
	if err := os.Rename(tmpPath, outputPath); err != nil {
		return fmt.Errorf("rename file: %w", err)
	}

	fmt.Printf("  [saved] %s (%s)\n", outputPath, formatBytes(finalSize))
	return nil
}

func runASFFlow(cfg *Config, auth Authenticator, destDir string) error {
	fmt.Println("\n=== ASF Search ===")
	products, err := queryASFProducts(auth, cfg)
	if err != nil {
		return fmt.Errorf("ASF search failed: %w", err)
	}

	if len(products) == 0 {
		fmt.Println("No products found.")
		return nil
	}

	fmt.Printf("\nFound %d products\n\n", len(products))
	for i, p := range products {
		sizeMB := float64(p.FileSize) / 1024 / 1024
		fmt.Printf("[%d] %s | %s | %.1f MB | %s | %s\n",
			i+1, p.SceneName, formatDate(p.StartTime), sizeMB, p.Processing, p.Polarization)
	}

	fmt.Println("\n=== Saving KML ===")
	for _, p := range products {
		if _, err := SaveKMLForASF(p, destDir); err != nil {
			fmt.Fprintf(os.Stderr, "  [kml skip] %s: %v\n", p.SceneName, err)
		}
	}

	// ASF downloads require an Earthdata URS session cookie.
	var ursJar http.CookieJar
	if ea, ok := auth.(*EarthdataAuth); ok {
		fmt.Println("\n=== Earthdata Login ===")
		jar, err := ursLogin(ea.Username, ea.Password)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [warn] URS login failed: %v (downloads may fail)\n", err)
		} else {
			fmt.Println("  [ok] URS session established")
			ursJar = jar
		}
	}

	type asfTask struct{ product asfResult }
	type asfResultCh struct {
		product asfResult
		err     error
	}

	tasks := make(chan asfTask, cfg.MaxWorkers*2)
	results := make(chan asfResultCh, cfg.MaxWorkers*2)

	var wg sync.WaitGroup
	for i := 0; i < cfg.MaxWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range tasks {
				err := downloadASFProduct(auth, t.product, destDir, cfg.MaxRetries, ursJar)
				results <- asfResultCh{product: t.product, err: err}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	fmt.Println("\n=== Downloading Products ===")
	for _, p := range products {
		tasks <- asfTask{product: p}
	}
	close(tasks)

	failed := 0
	skipped := 0
	for r := range results {
		if r.err != nil {
			if strings.Contains(r.err.Error(), "already exists") {
				skipped++
			} else {
				fmt.Fprintf(os.Stderr, "  [error] %s: %v\n", r.product.SceneName, r.err)
				failed++
			}
		}
	}

	fmt.Println("\nDone.")
	if failed > 0 {
		return fmt.Errorf("%d downloads failed", failed)
	}
	if skipped > 0 {
		fmt.Printf("%d products already existed, skipped.\n", skipped)
	}
	return nil
}

func formatDate(t string) string {
	if len(t) >= 10 {
		return t[:10]
	}
	return t
}
