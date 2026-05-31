package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	cdseODataCatalogURL  = "https://catalogue.dataspace.copernicus.eu/odata/v1/Products"
	cdseODataDownloadURL = "https://zipper.dataspace.copernicus.eu/odata/v1/Products"
)

// OData types for CDSE OData Catalog API.
type odataProduct struct {
	ID            string    `json:"Id"`
	Name          string    `json:"Name"`
	ContentLength int64     `json:"ContentLength"`
	OriginDate    time.Time `json:"OriginDate"`
	Online        bool      `json:"Online"`
	GeoFootprint  Geometry  `json:"GeoFootprint"`
}

type odataCatalogResponse struct {
	Value []odataProduct `json:"value"`
	Count int            `json:"@odata.count"`
}

func SaveKMLForOData(product odataProduct, destDir string) (string, error) {
	if product.GeoFootprint.Type != "Polygon" || len(product.GeoFootprint.Coordinates) == 0 {
		return "", fmt.Errorf("no polygon geometry for %s", product.Name)
	}

	kmlPath := filepath.Join(destDir, product.Name+".kml")
	if _, err := os.Stat(kmlPath); err == nil {
		fmt.Printf("  [skip] %s already exists\n", product.Name+".kml")
		return kmlPath, nil
	}

	var fields []kmlField
	fields = append(fields, kmlField{Name: "id", Value: product.Name})
	if !product.OriginDate.IsZero() {
		fields = append(fields, kmlField{Name: "datetime", Value: product.OriginDate.Format(time.RFC3339)})
	}
	if err := writeKMLFile(kmlPath, product.Name, product.GeoFootprint.Coordinates[0], fields); err != nil {
		return "", err
	}
	fmt.Printf("  [saved] %s\n", product.Name+".kml")
	return kmlPath, nil
}

// ---------- CDSE OData Flow ----------

func queryODataProducts(auth Authenticator, cfg *Config) ([]odataProduct, error) {
	if len(cfg.BBox) != 4 {
		return nil, fmt.Errorf("bbox must have 4 elements [west,south,east,north]")
	}
	west, south, east, north := cfg.BBox[0], cfg.BBox[1], cfg.BBox[2], cfg.BBox[3]

	polygon := fmt.Sprintf(
		"POLYGON((%f %f,%f %f,%f %f,%f %f,%f %f))",
		west, south, east, south, east, north, west, north, west, south,
	)

	sat := ParseSatelliteType(cfg.Collection)
	if cfg.Satellite != "" {
		sat = SatelliteType(cfg.Satellite)
	}
	sc := satelliteConfigs[sat]

	filters := []string{
		fmt.Sprintf("Collection/Name eq '%s'", sc.ODataCollection),
		fmt.Sprintf("Attributes/OData.CSC.StringAttribute/any(att:att/Name eq 'productType' and att/OData.CSC.StringAttribute/Value eq '%s')", sc.ODataProductType),
		fmt.Sprintf("ContentDate/Start gt %sT00:00:00.000Z", cfg.StartDate),
		fmt.Sprintf("ContentDate/Start lt %sT23:59:59.000Z", cfg.EndDate),
	}
	if sc.NeedsCloudFilter {
		if cfg.MinCloud > 0 || cfg.MaxCloud > 0 {
			cloudFilters := []string{"att/Name eq 'cloudCover'"}
			if cfg.MinCloud > 0 {
				cloudFilters = append(cloudFilters, fmt.Sprintf("att/OData.CSC.DoubleAttribute/Value ge %.1f", cfg.MinCloud))
			}
			if cfg.MaxCloud > 0 {
				cloudFilters = append(cloudFilters, fmt.Sprintf("att/OData.CSC.DoubleAttribute/Value lt %.1f", cfg.MaxCloud))
			}
			filters = append(filters, fmt.Sprintf(
				"Attributes/OData.CSC.DoubleAttribute/any(%s)",
				strings.Join(cloudFilters, " and "),
			))
		}
	}
	filters = append(filters, fmt.Sprintf(
		"OData.CSC.Intersects(area=geography'SRID=4326;%s')", polygon,
	))

	q := url.Values{}
	q.Set("$filter", strings.Join(filters, " and "))
	q.Set("$orderby", "ContentDate/Start desc")
	q.Set("$top", fmt.Sprintf("%d", cfg.Limit))
	q.Set("$count", "true")
	q.Set("$select", "Id,Name,ContentLength,OriginDate,Online,GeoFootprint")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", cdseODataCatalogURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
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
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalog returned %d: %s", resp.StatusCode, string(body))
	}

	var result odataCatalogResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result.Value, nil
}

func downloadODataProductOnce(auth Authenticator, product odataProduct, destDir string) (int64, error) {
	downloadURL := fmt.Sprintf("%s(%s)/$value", cdseODataDownloadURL, product.ID)
	tmpPath := filepath.Join(destDir, product.Name+".zip.tmp")
	client := &http.Client{Timeout: 30 * time.Minute}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	finalSize, total, _, err := resumableDownload(ctx, client, downloadURL, auth, tmpPath, product.Name, product.ContentLength)
	if err != nil {
		return 0, err
	}
	if total > 0 && finalSize != total {
		os.Remove(tmpPath)
		return 0, fmt.Errorf("size mismatch: got %s, expected %s", formatBytes(finalSize), formatBytes(total))
	}
	return finalSize, nil
}

func downloadODataProduct(auth Authenticator, product odataProduct, destDir string, maxRetries int) error {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	outputPath := filepath.Join(destDir, product.Name+".zip")
	if _, err := os.Stat(outputPath); err == nil {
		fmt.Printf("  [skip] %s already exists\n", product.Name+".zip")
		return nil
	}

	var finalSize int64
	var err error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		finalSize, err = downloadODataProductOnce(auth, product, destDir)
		if err == nil {
			break
		}
		if attempt < maxRetries {
			wait := time.Duration(attempt+1) * time.Second
			fmt.Fprintf(os.Stderr, "  [retry] %s in %.0fs (attempt %d/%d): %v\n", product.Name, wait.Seconds(), attempt+1, maxRetries, err)
			time.Sleep(wait)
		}
	}
	if err != nil {
		return err
	}

	tmpPath := filepath.Join(destDir, product.Name+".zip.tmp")
	if err := os.Rename(tmpPath, outputPath); err != nil {
		return fmt.Errorf("rename file: %w", err)
	}

	fmt.Printf("  [saved] %s (%s)\n", outputPath, formatBytes(finalSize))
	return nil
}

func runODataFlow(cfg *Config, auth Authenticator, destDir string) error {
	fmt.Println("\n=== CDSE OData Search ===")
	products, err := queryODataProducts(auth, cfg)
	if err != nil {
		return fmt.Errorf("OData search failed: %w", err)
	}

	if len(products) == 0 {
		fmt.Println("No products found.")
		return nil
	}

	fmt.Printf("\nFound %d products\n\n", len(products))
	for i, p := range products {
		sizeMB := float64(p.ContentLength) / 1024 / 1024
		online := "online"
		if !p.Online {
			online = "OFFLINE (LTA)"
		}
		fmt.Printf("[%d] %s | %s | %.1f MB | %s\n",
			i+1, p.Name, p.OriginDate.Format("2006-01-02"), sizeMB, online)
	}

	fmt.Println("\n=== Saving KML ===")
	for _, p := range products {
		if _, err := SaveKMLForOData(p, destDir); err != nil {
			fmt.Fprintf(os.Stderr, "  [kml skip] %s: %v\n", p.Name, err)
		}
	}

	type odataTask struct{ product odataProduct }
	type odataResult struct {
		product odataProduct
		err     error
		offline bool
	}

	tasks := make(chan odataTask, cfg.MaxWorkers*2)
	results := make(chan odataResult, cfg.MaxWorkers*2)

	var wg sync.WaitGroup
	for i := 0; i < cfg.MaxWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range tasks {
				if !t.product.Online {
					results <- odataResult{product: t.product, offline: true}
					continue
				}
				err := downloadODataProduct(auth, t.product, destDir, cfg.MaxRetries)
				results <- odataResult{product: t.product, err: err}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	fmt.Println("\n=== Downloading Products ===")
	for _, p := range products {
		tasks <- odataTask{product: p}
	}
	close(tasks)

	failed := 0
	skipped := 0
	for r := range results {
		switch {
		case r.offline:
			fmt.Printf("  [skip] %s is offline in LTA\n", r.product.Name)
			skipped++
		case r.err != nil:
			fmt.Fprintf(os.Stderr, "  [error] %s: %v\n", r.product.Name, r.err)
			failed++
		}
	}

	sat := ParseSatelliteType(cfg.Collection)
	if cfg.Satellite != "" {
		sat = SatelliteType(cfg.Satellite)
	}
	sc := satelliteConfigs[sat]
	if sc.SupportsRGB {
		fmt.Println("\n=== Processing RGB ===")
		for _, p := range products {
			zipPath := filepath.Join(destDir, p.Name+".zip")
			if _, err := os.Stat(zipPath); err != nil {
				continue
			}
			if err := processODataProduct(zipPath, destDir, p.Name, sat); err != nil {
				fmt.Fprintf(os.Stderr, "  [rgb skip] %s: %v\n", p.Name, err)
			}
		}
	}

	fmt.Println("\nDone.")
	if failed > 0 {
		return fmt.Errorf("%d downloads failed", failed)
	}
	if skipped > 0 {
		fmt.Printf("%d products were offline (LTA), skipped.\n", skipped)
	}
	return nil
}

// extractRGBJP2s 从 Sentinel-2 SAFE zip 包里只解压 R10m 的 B02/B03/B04 三个 jp2，
// 返回 red、green、blue 三个本地路径（顺序：R=B04, G=B03, B=B02）。
func extractRGBJP2s(zipPath, outDir string) (string, string, string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", "", "", fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	var redEntry, greenEntry, blueEntry *zip.File
	for _, f := range r.File {
		name := f.Name
		if !strings.Contains(name, "/IMG_DATA/R10m/") {
			continue
		}
		base := path.Base(name)
		switch {
		case strings.HasSuffix(base, "_B04_10m.jp2"):
			redEntry = f
		case strings.HasSuffix(base, "_B03_10m.jp2"):
			greenEntry = f
		case strings.HasSuffix(base, "_B02_10m.jp2"):
			blueEntry = f
		}
	}
	if redEntry == nil || greenEntry == nil || blueEntry == nil {
		return "", "", "", fmt.Errorf("missing R10m B02/B03/B04 in zip")
	}

	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", "", "", fmt.Errorf("mkdir: %w", err)
	}

	extract := func(f *zip.File) (string, error) {
		dst := filepath.Join(outDir, path.Base(f.Name))
		src, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("open entry %s: %w", f.Name, err)
		}
		defer src.Close()
		w, err := os.Create(dst)
		if err != nil {
			return "", fmt.Errorf("create %s: %w", dst, err)
		}
		if _, err := io.Copy(w, src); err != nil {
			w.Close()
			return "", fmt.Errorf("write %s: %w", dst, err)
		}
		if err := w.Close(); err != nil {
			return "", fmt.Errorf("close %s: %w", dst, err)
		}
		return dst, nil
	}

	redPath, err := extract(redEntry)
	if err != nil {
		return "", "", "", err
	}
	greenPath, err := extract(greenEntry)
	if err != nil {
		return "", "", "", err
	}
	bluePath, err := extract(blueEntry)
	if err != nil {
		return "", "", "", err
	}
	return redPath, greenPath, bluePath, nil
}

// processODataProduct 把整景 zip 解压出 R/G/B 波段，合成拉伸成 byte 格式 tif，
// 再跑 gdal_trace_outline → gdal_rasterize → gdalwarp → gdal_merge_simple 合成 RGBA，
// 最终目录下保留原始 .zip 和 *_rgba.tif。rgba 失败时回退保留 *_byte.tif。
func processODataProduct(zipPath, destDir, productName string, sat SatelliteType) error {
	sc := satelliteConfigs[sat]
	if !sc.SupportsRGB {
		return nil
	}

	bytePath := filepath.Join(destDir, productName+"_byte.tif")
	rgbaPath := filepath.Join(destDir, productName+"_rgba.tif")

	if _, err := os.Stat(rgbaPath); err == nil {
		fmt.Printf("  [skip] %s already exists\n", filepath.Base(rgbaPath))
		return nil
	}

	workDir := filepath.Join(destDir, productName+"_extract")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("mkdir workDir: %w", err)
	}

	if _, err := os.Stat(bytePath); err != nil {
		fmt.Printf("  [extract] %s -> R10m B02/B03/B04\n", productName)
		redPath, greenPath, bluePath, err := extractRGBJP2s(zipPath, workDir)
		if err != nil {
			return fmt.Errorf("extract: %w", err)
		}
		if err := buildRGBByte(redPath, greenPath, bluePath, bytePath, workDir); err != nil {
			return err
		}
		fmt.Printf("  [byte] %s\n", bytePath)
	} else {
		fmt.Printf("  [reuse] %s, retrying rgba\n", filepath.Base(bytePath))
	}

	if err := buildRGBA(bytePath, rgbaPath, workDir); err != nil {
		fmt.Fprintf(os.Stderr, "  [rgba skip] %s: %v\n", productName, err)
		return nil
	}

	os.Remove(bytePath)
	os.RemoveAll(workDir)
	fmt.Printf("  [rgba] %s\n", filepath.Base(rgbaPath))
	return nil
}
