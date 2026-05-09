package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"sentinel2-go/internal/bundle"
)

var (
	EarthSearchURL  = "https://earth-search.aws.element84.com/v1"
	Collection      = "sentinel-2-l2a"
	DownloadTimeout = 10 * time.Minute
	version         = "dev"
)

func main() {
	configPath := flag.String("config", "config.json", "Path to configuration JSON file")
	destDir := flag.String("dest", "./sentinel2_data", "Destination directory for downloaded files")
	setupAuth := flag.Bool("setup-auth", false, "Interactive authentication setup wizard (CLI)")
	setupFlag := flag.Bool("setup", false, "Open web-based setup wizard")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Println(version)
		return
	}

	if _, err := bundle.EnsureExtracted(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to extract bundled assets: %v\n", err)
		os.Exit(1)
	}

	if *setupAuth {
		setupAuthWizard()
		return
	}

	if *setupFlag || needsSetup() {
		_, err := runSetupWizard()
		if err != nil {
			fmt.Fprintf(os.Stderr, "配置失败：%v\n", err)
			os.Exit(1)
		}
		fmt.Println("配置已保存。")
		if *setupFlag {
			return
		}
		// First-run: continue with the saved settings
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}
	mergeSettings(cfg)

	settings, _ := loadSettings()
	if settings != nil && settings.Source == "cdse_odata" {
		if cfg.Auth == nil || cfg.Auth.Username == "" {
			fmt.Fprintln(os.Stderr, "CDSE OData requires authentication. Run with -setup-auth to configure.")
			os.Exit(1)
		}
		auth := NewCDSEAuth(cfg.Auth.Username, cfg.Auth.Password)
		runODataFlow(cfg, auth, *destDir)
		return
	}

	var auth Authenticator = NoOpAuth{}
	if cfg.Auth != nil && cfg.Auth.Username != "" && cfg.Auth.Password != "" {
		auth = NewCDSEAuth(cfg.Auth.Username, cfg.Auth.Password)
	}

	opts := SearchOptions{
		Bbox:       cfg.BBox,
		StartDate:  cfg.StartDate,
		EndDate:    cfg.EndDate,
		Limit:      cfg.Limit,
		MaxCloud:   cfg.MaxCloud,
		STACURL:    cfg.STACURL,
		Collection: cfg.Collection,
	}

	fmt.Printf("Searching Sentinel-2 L2A data...\n")
	fmt.Printf("  Config:    %s\n", *configPath)
	fmt.Printf("  Dest:      %s\n", *destDir)
	fmt.Printf("  STAC URL:  %s\n", opts.STACURL)
	fmt.Printf("  Collection: %s\n", opts.Collection)
	fmt.Printf("  Auth:      %s\n", func() string {
		if cfg.Auth != nil {
			return "OAuth2 (CDSE)"
		}
		return "none"
	}())
	fmt.Printf("  BBox:      %v (west, south, east, north)\n", opts.Bbox)
	fmt.Printf("  Date:      %s to %s\n", opts.StartDate, opts.EndDate)
	fmt.Printf("  Cloud:     <= %.0f%%\n", opts.MaxCloud)
	fmt.Printf("  Bands:     %v\n", cfg.Bands)
	fmt.Printf("  Workers:   %d\n", cfg.MaxWorkers)
	fmt.Printf("  Retries:   %d\n\n", cfg.MaxRetries)

	stacCollection, err := SearchItems(opts, auth)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Search failed: %v\n", err)
		os.Exit(1)
	}

	if len(stacCollection.Features) == 0 {
		fmt.Println("No items found.")
		return
	}

	items := FilterItemsByCloud(stacCollection.Features, opts.MaxCloud)
	PrintItemSummary(items)

	// 为已有数据补生成 KML
	existingItems := scanExistingItems(*destDir)
	if len(existingItems) > 0 {
		fmt.Println("\n=== Checking existing KML ===")
		for itemID := range existingItems {
			kmlPath := filepath.Join(*destDir, itemID+".kml")
			if _, err := os.Stat(kmlPath); err == nil {
				continue
			}
			fmt.Printf("  [kml fetch] %s\n", itemID)
			item, err := fetchItem(itemID, cfg.STACURL, cfg.Collection, auth)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  [kml fail] %s: %v\n", itemID, err)
				continue
			}
			if _, err := SaveKML(item, *destDir); err != nil {
				fmt.Fprintf(os.Stderr, "  [kml fail] %s: %v\n", itemID, err)
			}
		}
	}

	tasks := make(chan downloadTask, cfg.MaxWorkers*2)
	results := make(chan downloadResult, cfg.MaxWorkers*2)

	var wg sync.WaitGroup
	for i := 0; i < cfg.MaxWorkers; i++ {
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

	fmt.Println("\n=== Downloading Bands ===")
	total := 0
	for _, item := range items {
		fmt.Printf("\nItem: %s\n", item.ID)
		if _, err := SaveKML(item, *destDir); err != nil {
			fmt.Fprintf(os.Stderr, "  [kml skip] %s: %v\n", item.ID, err)
		}
		for _, band := range cfg.Bands {
			assetKey := resolveAssetKey(band, cfg.STACURL)
			asset, ok := item.Assets[assetKey]
			if !ok {
				fmt.Printf("  [warn] band '%s' not available (tried '%s')\n", band, assetKey)
				continue
			}
			tasks <- downloadTask{itemID: item.ID, band: band, asset: asset, destDir: *destDir, maxRetries: cfg.MaxRetries, auth: auth}
			total++
		}
	}
	close(tasks)

	failed := 0
	skipped := 0
	for res := range results {
		if res.skipped {
			fmt.Printf("  [skip] %s_%s.tif already exists\n", res.task.itemID, res.task.band)
			skipped++
		} else if res.err != nil {
			fmt.Fprintf(os.Stderr, "  [error] %s/%s: %v\n", res.task.itemID, res.task.band, res.err)
			failed++
		} else {
			fmt.Printf("  [saved] %s\n", filepath.Base(res.path))
		}
	}

	fmt.Println("\n=== Building RGB ===")
	for _, item := range items {
		if err := BuildRGB(*destDir, item.ID); err != nil {
			fmt.Fprintf(os.Stderr, "  [rgb skip] %s: %v\n", item.ID, err)
		}
	}

	fmt.Println("\nDone.")
	if failed > 0 {
		fmt.Printf("%d/%d downloads failed.\n", failed, total)
		os.Exit(1)
	}
	if skipped > 0 {
		fmt.Printf("%d/%d already existed, skipped.\n", skipped, total)
	}
}
