package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"sentinel-scraper/internal/bundle"
)

var (
	EarthSearchURL  = "https://earth-search.aws.element84.com/v1"
	EarthdataURL    = "https://cmr.earthdata.nasa.gov/stac"
	Collection      = "sentinel-2-l2a"
	DownloadTimeout = 10 * time.Minute
	version         = "dev"
)

func main() {
	// CLI flags — designed for programmatic invocation by LLM agents.
	// Each usage string includes: purpose, type, default, required? yes/no, example.
	configPath := flag.String("config", "config.json",
		"Path to the search-configuration JSON file.\n"+
			"  Type:     string (file path)\n"+
			"  Default:  config.json\n"+
			"  Required: no (auto-created with sensible defaults if missing)\n"+
			"  Example:  -config config.json\n\n"+
			"  Config file fields (JSON):\n"+
			"    bbox        [float×4]  Bounding box [west, south, east, north] in degrees.\n"+
			"                         Example: [116.2, 39.8, 116.6, 40.0]\n"+
			"    start_date  string    Search start date in YYYY-MM-DD format.\n"+
			"                         Example: \"2024-01-01\"\n"+
			"    end_date    string    Search end date in YYYY-MM-DD format.\n"+
			"                         Example: \"2024-01-31\"\n"+
			"    min_cloud   float     Minimum cloud cover percentage (0–100). Only for optical.\n"+
			"                         Default: 0\n"+
			"    max_cloud   float     Maximum cloud cover percentage (0–100). Only for optical.\n"+
			"                         Default: 100 (no filter)\n"+
			"    bands       []string  Band keys to download.\n"+
			"                         S2 defaults: [\"red\", \"green\", \"blue\"]\n"+
			"                         S1 defaults: [\"vv\", \"vh\"]\n"+
			"                         HLS defaults: [\"red\", \"green\", \"blue\"]\n"+
			"    limit       int       Maximum number of STAC items to return.\n"+
			"                         Default: 20\n"+
			"    max_workers int       Number of concurrent download workers.\n"+
			"                         Default: 4\n"+
			"    max_retries int       Number of retry attempts for each failed download.\n"+
			"                         Default: 3\n"+
			"    satellite   string    Satellite mission. Values: \"sentinel-2\", \"sentinel-1\", \"s2\", \"s1\", \"hls\"\n"+
			"                         Default: \"sentinel-2\"\n"+
			"    product     string    Sentinel-1 product type. Values: \"grd\" | \"slc\"\n"+
			"                         Only used when satellite=\"sentinel-1\". Default: \"grd\"")
	destDir := flag.String("dest", "./sentinel_data",
		"Output directory where downloaded imagery bands and KML files are stored.\n"+
			"  Type:    string (directory path)\n"+
			"  Default: ./sentinel_data\n"+
			"  Required: no\n"+
			"  Example: -dest ./sentinel_data")
	setupAuth := flag.Bool("setup-auth", false,
		"Launch an interactive CLI wizard to configure authentication credentials.\n"+
			"  Type:    boolean flag (no value needed)\n"+
			"  Default: false\n"+
			"  Required: no\n"+
			"  Scope:   Prompts for CDSE (Copernicus) and Earthdata (NASA) username/password,\n"+
			"           then writes them to ~/.sentinel-scraper/settings.json\n"+
			"  Example: -setup-auth")
	setupFlag := flag.Bool("setup", false,
		"Open a web-based setup wizard in the default browser for GUI configuration.\n"+
			"  Type:    boolean flag (no value needed)\n"+
			"  Default: false\n"+
			"  Required: no\n"+
			"  Scope:   Same as -setup-auth but via a local HTTP page instead of CLI prompts.\n"+
			"  Example: -setup")
	versionFlag := flag.Bool("version", false,
		"Print the sentinel-scraper version and exit immediately.\n"+
			"  Type:    boolean flag (no value needed)\n"+
			"  Default: false\n"+
			"  Required: no\n"+
			"  Example: -version")
	defaultFlag := flag.Bool("default", false,
		"Generate a default config.json and exit immediately.\n"+
			"  Type:    boolean flag (no value needed)\n"+
			"  Default: false\n"+
			"  Required: no\n"+
			"  Scope:   Writes a default configuration file to the path specified by -config.\n"+
			"  Example: -default")
	orbitDownload := flag.Bool("orbit-download", false,
		"Download Sentinel-1 precise orbit files (POEORB/RESORB EOF) for .SAFE/.zip scenes.\n"+
			"  Type:     boolean flag (no value needed)\n"+
			"  Default:  false\n"+
			"  Required:  no\n"+
			"  Example:  -orbit-download -safe-dir ./SLC -orbit-dir ./orbits")
	safeDir := flag.String("safe-dir", "",
		"Directory containing .SAFE folders or .zip files for orbit matching.\n"+
			"  Used with -orbit-download.\n"+
			"  Type:     string (directory path)\n"+
			"  Required:  yes (when -orbit-download is set)\n"+
			"  Example:  -safe-dir ./SLC")
	orbitDir := flag.String("orbit-dir", "",
		"Output directory for downloaded orbit EOF files.\n"+
			"  Used with -orbit-download. Defaults to <safe-dir>/orbits.\n"+
			"  Type:     string (directory path)\n"+
			"  Required:  no\n"+
			"  Example:  -orbit-dir ./orbits")
	forceResorb := flag.Bool("resorb", false,
		"Force RESORB (restituted) orbits instead of auto-selecting POEORB/RESORB.\n"+
			"  Used with -orbit-download.\n"+
			"  Type:     boolean flag (no value needed)\n"+
			"  Default:  false\n"+
			"  Required:  no")
	flag.Parse()

	if len(os.Args) == 1 {
		flag.Usage()
		os.Exit(0)
	}

	if *versionFlag {
		fmt.Println(version)
		return
	}

	if *defaultFlag {
		if err := WriteDefaultConfig(*configPath); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create default config: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if _, err := bundle.EnsureExtracted(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to extract bundled assets: %v\n", err)
		os.Exit(1)
	}

	if *setupAuth {
		if err := setupAuthWizard(); err != nil {
			fmt.Fprintf(os.Stderr, "配置失败: %v\n", err)
			os.Exit(1)
		}
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

	if err := ensureDefaultConfig(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create default config: %v\n", err)
		os.Exit(1)
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}
	mergeSettings(cfg)

	if *orbitDownload {
		if *safeDir == "" {
			fmt.Fprintln(os.Stderr, "-safe-dir is required for orbit download")
			os.Exit(1)
		}
		orbitOutput := *orbitDir
		if orbitOutput == "" {
			orbitOutput = filepath.Join(*safeDir, "orbits")
		}
		maxWorkers := 4
		maxRetries := 3
		if cfg != nil {
			if cfg.MaxWorkers > 0 {
				maxWorkers = cfg.MaxWorkers
			}
			if cfg.MaxRetries >= 0 {
				maxRetries = cfg.MaxRetries
			}
		}
		settings, _ := loadSettings()
		if settings == nil || settings.EarthdataAuth == nil || settings.EarthdataAuth.Username == "" {
			fmt.Fprintln(os.Stderr, "Earthdata credentials not configured. Run -setup-auth first.")
			os.Exit(1)
		}
		auth := NewEarthdataAuth(settings.EarthdataAuth.Username, settings.EarthdataAuth.Password)

		fmt.Println("=== Sentinel-1 Orbit Download ===")
		fmt.Printf("SAFE dir: %s\n", *safeDir)
		fmt.Printf("Orbit dir: %s\n", orbitOutput)

		if err := runOrbitDownload(*safeDir, orbitOutput, auth, maxWorkers, maxRetries, *forceResorb); err != nil {
			fmt.Fprintf(os.Stderr, "Orbit download failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// 来源完全由程序根据卫星类型和可用认证自动编排
	runWithFallback(cfg, *destDir, *configPath)
}

func runSTACFlow(cfg *Config, auth Authenticator, destDir, configPath string) error {
	sat := SatelliteType(cfg.Satellite)
	if sat == "" {
		sat = SatS2L2A
	}
	sc := satelliteConfigs[sat]

	// 若 bands 未指定，使用卫星类型的默认波段
	if len(cfg.Bands) == 0 {
		cfg.Bands = sc.DefaultBands
	}

	opts := SearchOptions{
		Bbox:       cfg.BBox,
		StartDate:  cfg.StartDate,
		EndDate:    cfg.EndDate,
		Limit:      cfg.Limit,
		MinCloud:   cfg.MinCloud,
		MaxCloud:   cfg.MaxCloud,
		STACURL:    cfg.STACURL,
		Collection: cfg.Collection,
		Satellite:  sat,
	}

	authLabel := "none"
	if cfg.Auth != nil {
		if _, ok := auth.(*EarthdataAuth); ok {
			authLabel = "Bearer (Earthdata)"
		} else {
			authLabel = "OAuth2 (CDSE)"
		}
	}

	fmt.Printf("Searching %s data...\n", sc.Collection)
	fmt.Printf("  Config:    %s\n", configPath)
	fmt.Printf("  Dest:      %s\n", destDir)
	fmt.Printf("  STAC URL:  %s\n", opts.STACURL)
	fmt.Printf("  Collection: %s\n", opts.Collection)
	fmt.Printf("  Auth:      %s\n", authLabel)
	fmt.Printf("  BBox:      %v (west, south, east, north)\n", opts.Bbox)
	fmt.Printf("  Date:      %s to %s\n", opts.StartDate, opts.EndDate)
	if sc.NeedsCloudFilter {
		if cfg.MinCloud > 0 && cfg.MaxCloud > 0 {
			fmt.Printf("  Cloud:     %.0f%% - %.0f%%\n", cfg.MinCloud, cfg.MaxCloud)
		} else if cfg.MaxCloud > 0 {
			fmt.Printf("  Cloud:     <= %.0f%%\n", opts.MaxCloud)
		} else if cfg.MinCloud > 0 {
			fmt.Printf("  Cloud:     >= %.0f%%\n", cfg.MinCloud)
		} else {
			fmt.Printf("  Cloud:     no filter\n")
		}
	}
	fmt.Printf("  Bands:     %v\n", cfg.Bands)
	fmt.Printf("  Workers:   %d\n", cfg.MaxWorkers)
	fmt.Printf("  Retries:   %d\n\n", cfg.MaxRetries)

	stacCollection, err := SearchItems(opts, auth)
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	if len(stacCollection.Features) == 0 {
		fmt.Println("No items found.")
		return fmt.Errorf("no items found in collection %s", opts.Collection)
	}

	items := FilterItemsByCloud(stacCollection.Features, opts.MinCloud, opts.MaxCloud, sat)
	PrintItemSummary(items)

	// 为已有数据补生成 KML
	existingItems, err := scanExistingItems(destDir, sat)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to scan existing items: %v\n", err)
	}
	if len(existingItems) > 0 {
		fmt.Println("\n=== Checking existing KML ===")
		for itemID := range existingItems {
			kmlPath := filepath.Join(destDir, itemID+".kml")
			if _, err := os.Stat(kmlPath); err == nil {
				continue
			}
			fmt.Printf("  [kml fetch] %s\n", itemID)
			item, err := fetchItem(itemID, cfg.STACURL, cfg.Collection, auth)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  [kml fail] %s: %v\n", itemID, err)
				continue
			}
			if _, err := SaveKML(item, destDir); err != nil {
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
		if _, err := SaveKML(item, destDir); err != nil {
			fmt.Fprintf(os.Stderr, "  [kml skip] %s: %v\n", item.ID, err)
		}
		for _, band := range cfg.Bands {
			assetKey := resolveAssetKey(band, cfg.STACURL, sat)
			asset, ok := item.Assets[assetKey]
			if !ok {
				fmt.Printf("  [warn] band '%s' not available (tried '%s')\n", band, assetKey)
				continue
			}
			tasks <- downloadTask{itemID: item.ID, band: band, asset: asset, destDir: destDir, maxRetries: cfg.MaxRetries, auth: auth}
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

	if sc.SupportsRGB {
		fmt.Println("\n=== Building RGB ===")
		for _, item := range items {
			if err := BuildRGB(destDir, item.ID, sat); err != nil {
				fmt.Fprintf(os.Stderr, "  [rgb skip] %s: %v\n", item.ID, err)
			}
		}
	}

	fmt.Println("\nDone.")
	if failed > 0 {
		return fmt.Errorf("%d/%d downloads failed", failed, total)
	}
	if skipped > 0 {
		fmt.Printf("%d/%d already existed, skipped.\n", skipped, total)
	}
	return nil
}

// runWithFallback 按优先级自动尝试多个数据源，当一个来源失败时自动切换到下一个。
// S2 优先级: Earth Search → CDSE STAC → CDSE OData
// S1 优先级: Earth Search → ASF
// HLS 优先级: Earthdata
func runWithFallback(cfg *Config, destDir, configPath string) {
	sat := SatelliteType(cfg.Satellite)
	if sat == "" {
		sat = SatS2L2A
	}

	settings, _ := loadSettings()
	var cdseAuth, earthdataAuth *AuthConfig
	if settings != nil {
		if settings.CDSEAuth != nil && settings.CDSEAuth.Username != "" {
			cdseAuth = settings.CDSEAuth
		}
		if settings.EarthdataAuth != nil && settings.EarthdataAuth.Username != "" {
			earthdataAuth = settings.EarthdataAuth
		}
	}

	type source struct {
		name string
		try  func() error
	}

	var sources []source

	switch sat {
	case SatS2L2A:
		sources = append(sources, source{
			name: "Earth Search (STAC)",
			try: func() error {
				c := *cfg
				c.STACURL = EarthSearchURL
				c.Collection = satelliteConfigs[SatS2L2A].Collection
				return runSTACFlow(&c, NoOpAuth{}, destDir, configPath)
			},
		})
		if cdseAuth != nil {
			sources = append(sources, source{
				name: "CDSE STAC",
				try: func() error {
					c := *cfg
					c.STACURL = "https://stac.dataspace.copernicus.eu/v1"
					c.Collection = satelliteConfigs[SatS2L2A].CDSECollection
					return runSTACFlow(&c, NewCDSEAuth(cdseAuth.Username, cdseAuth.Password), destDir, configPath)
				},
			})
			sources = append(sources, source{
				name: "CDSE OData",
				try: func() error {
					c := *cfg
					return runODataFlow(&c, NewCDSEAuth(cdseAuth.Username, cdseAuth.Password), destDir)
				},
			})
		}
	case SatS1GRD, SatS1SLC:
		sources = append(sources, source{
			name: "Earth Search (STAC)",
			try: func() error {
				c := *cfg
				c.STACURL = EarthSearchURL
				c.Collection = satelliteConfigs[sat].Collection
				return runSTACFlow(&c, NoOpAuth{}, destDir, configPath)
			},
		})
		if earthdataAuth != nil {
			sources = append(sources, source{
				name: "ASF",
				try: func() error {
					c := *cfg
					return runASFFlow(&c, NewEarthdataAuth(earthdataAuth.Username, earthdataAuth.Password), destDir)
				},
			})
		}
	case SatHLS:
		if earthdataAuth != nil {
			sources = append(sources, source{
				name: "Earthdata (NASA)",
				try: func() error {
					c := *cfg
					c.STACURL = EarthdataURL + "/LPCLOUD"
					c.Collection = satelliteConfigs[SatHLS].Collection
					c.Satellite = string(SatHLS)
					return runSTACFlow(&c, NewEarthdataAuth(earthdataAuth.Username, earthdataAuth.Password), destDir, configPath)
				},
			})
		}
	}

	if len(sources) == 0 {
		fmt.Fprintln(os.Stderr, "No available sources for the specified satellite. Authentication may be required.")
		os.Exit(1)
	}

	var lastErr error
	for i, src := range sources {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf(">>> [%d/%d] Trying %s...\n", i+1, len(sources), src.name)

		err := src.try()
		if err == nil {
			fmt.Printf(">>> %s succeeded.\n", src.name)
			return
		}

		fmt.Printf(">>> %s failed: %v\n", src.name, err)
		lastErr = err
	}

	fmt.Fprintf(os.Stderr, "\nAll sources failed. Last error: %v\n", lastErr)
	os.Exit(1)
}
