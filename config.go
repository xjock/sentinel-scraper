package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// SatelliteType identifies the satellite mission and product type.
type SatelliteType string

const (
	SatS2L2A    SatelliteType = "sentinel-2-l2a"
	SatS1GRD    SatelliteType = "sentinel-1-grd"
	SatS1SLC    SatelliteType = "sentinel-1-slc"
	SatHLS      SatelliteType = "hls"
	SatLandsat8 SatelliteType = "landsat-8"
	SatLandsat9 SatelliteType = "landsat-9"
)

// ParseSatelliteType infers the satellite type from a collection name.
func ParseSatelliteType(collection string) SatelliteType {
	lower := strings.ToLower(collection)
	switch {
	case strings.Contains(lower, "sentinel-1") || strings.Contains(lower, "sentinel1"):
		if strings.Contains(lower, "slc") {
			return SatS1SLC
		}
		return SatS1GRD
	case strings.Contains(lower, "hls"):
		return SatHLS
	case strings.Contains(lower, "landsat-8") || strings.Contains(lower, "landsat8") || lower == "l8":
		return SatLandsat8
	case strings.Contains(lower, "landsat-9") || strings.Contains(lower, "landsat9") || lower == "l9":
		return SatLandsat9
	default:
		return SatS2L2A
	}
}

// ResolveSatelliteType resolves the satellite type from two-level params.
// satellite: "sentinel-1", "sentinel-2", "s1", "s2", "hls", "landsat-8", "l8", etc.
// product:   "grd", "slc" (only for sentinel-1)
func ResolveSatelliteType(satellite, product string) SatelliteType {
	lower := strings.ToLower(satellite)
	// If the satellite string already contains the full type, use it directly.
	if strings.Contains(lower, "slc") {
		return SatS1SLC
	}
	if strings.Contains(lower, "grd") {
		return SatS1GRD
	}
	switch {
	case strings.Contains(lower, "hls"):
		return SatHLS
	case strings.Contains(lower, "landsat-8") || lower == "l8":
		return SatLandsat8
	case strings.Contains(lower, "landsat-9") || lower == "l9":
		return SatLandsat9
	case strings.Contains(lower, "sentinel-2") || lower == "s2":
		return SatS2L2A
	case strings.Contains(lower, "sentinel-1") || lower == "s1":
		if strings.Contains(strings.ToLower(product), "slc") {
			return SatS1SLC
		}
		return SatS1GRD
	}
	// Fallback: treat satellite as a collection name (backward compat)
	return ParseSatelliteType(satellite)
}

// SelectMode controls how scenes are chosen from STAC search results.
type SelectMode string

const (
	SelectAll             SelectMode = "all"               // return all matching scenes
	SelectClearestPerTile SelectMode = "clearest-per-tile" // S2 only: one clearest full-coverage scene per MGRS tile
	SelectCloudFreeCover  SelectMode = "cloud-free-cover"  // S2 only: minimal set of scenes per MGRS tile whose footprints fully cover it
)

type Config struct {
	BBox       []float64 `json:"bbox"`
	StartDate  string    `json:"start_date"`
	EndDate    string    `json:"end_date"`
	MinCloud   float64   `json:"min_cloud"`
	MaxCloud   float64   `json:"max_cloud"`
	MaxNodata  float64   `json:"max_nodata"` // S2 only: max s2:nodata_pixel_percentage (0-100, 100=no filter)
	Bands      []string  `json:"bands"`
	Limit      int       `json:"limit"`
	MaxWorkers int       `json:"max_workers"`
	MaxRetries int       `json:"max_retries"`
	Satellite  string    `json:"satellite,omitempty"`   // sentinel-1, sentinel-2, s1, s2, hls
	Product    string    `json:"product,omitempty"`     // grd, slc (仅 sentinel-1)
	SelectMode string    `json:"select_mode,omitempty"` // "all" | "clearest-per-tile" | "cloud-free-cover"
	Tiles      []string  `json:"tiles,omitempty"`       // optional explicit MGRS tile list for per-tile select modes
	// CoverageTarget is the per-tile fill ratio (0-1) for cloud-free-cover. Default 0.995.
	CoverageTarget float64 `json:"coverage_target,omitempty"`
	// MaxPerTile caps how many scenes cloud-free-cover may stack per tile. Default 6.
	MaxPerTile int `json:"max_per_tile,omitempty"`

	// Internal fields, populated by mergeSettings or runWithFallback.
	Auth       *AuthConfig `json:"-"`
	STACURL    string      `json:"-"`
	Collection string      `json:"collection,omitempty"`
}

type SearchOptions struct {
	Bbox       []float64
	StartDate  string
	EndDate    string
	Limit      int
	MinCloud   float64
	MaxCloud   float64
	MaxNodata  float64 // S2 only
	STACURL    string
	Collection string
	Satellite  SatelliteType
	Platform   string // optional STAC "platform" filter (e.g. "landsat-8", "landsat-9")
	SelectMode SelectMode
	Tiles      []string   // explicit MGRS tile list; if empty and a per-tile mode is set, auto-discover
	GridCode   string     // if set, search this tile instead of bbox
	SortBy     []SortSpec // STAC sortby specs
	// CoverageTarget and MaxPerTile drive the cloud-free-cover greedy set cover.
	CoverageTarget float64
	MaxPerTile     int
}

// SortSpec describes a single STAC sort criterion.
type SortSpec struct {
	Field     string `json:"field"`
	Direction string `json:"direction"` // "asc" or "desc"
}

type AuthConfig struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}
	if cfg.Limit == 0 {
		cfg.Limit = 20
	}
	if cfg.MaxWorkers == 0 {
		cfg.MaxWorkers = 4
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	// max_nodata unset (zero) means "no coverage filter" (documented default 100),
	// consistent with limit/max_workers where 0 selects the default.
	if cfg.MaxNodata == 0 {
		cfg.MaxNodata = 100
	}
	if len(cfg.BBox) != 0 && len(cfg.BBox) != 4 {
		return nil, fmt.Errorf("bbox must have 4 elements [west,south,east,north], got %d", len(cfg.BBox))
	}
	if cfg.MinCloud < 0 {
		return nil, fmt.Errorf("min_cloud must be >= 0, got %f", cfg.MinCloud)
	}
	if cfg.MaxCloud < 0 {
		return nil, fmt.Errorf("max_cloud must be >= 0, got %f", cfg.MaxCloud)
	}
	if cfg.MinCloud > 0 && cfg.MaxCloud > 0 && cfg.MinCloud > cfg.MaxCloud {
		return nil, fmt.Errorf("min_cloud (%.1f) cannot be greater than max_cloud (%.1f)", cfg.MinCloud, cfg.MaxCloud)
	}
	if cfg.MaxNodata < 0 || cfg.MaxNodata > 100 {
		return nil, fmt.Errorf("max_nodata must be between 0 and 100, got %f", cfg.MaxNodata)
	}
	if cfg.SelectMode == "" {
		cfg.SelectMode = string(SelectAll)
	}
	switch SelectMode(cfg.SelectMode) {
	case SelectAll, SelectClearestPerTile, SelectCloudFreeCover:
		// valid
	default:
		return nil, fmt.Errorf("select_mode must be \"all\", \"clearest-per-tile\" or \"cloud-free-cover\", got %q", cfg.SelectMode)
	}
	if cfg.CoverageTarget == 0 {
		cfg.CoverageTarget = 0.995
	}
	if cfg.CoverageTarget <= 0 || cfg.CoverageTarget > 1 {
		return nil, fmt.Errorf("coverage_target must be in (0,1], got %f", cfg.CoverageTarget)
	}
	if cfg.MaxPerTile == 0 {
		cfg.MaxPerTile = 6
	}
	if cfg.Satellite == "" {
		if cfg.Collection != "" {
			cfg.Satellite = string(ParseSatelliteType(cfg.Collection))
		} else {
			cfg.Satellite = string(SatS2L2A)
		}
	} else {
		// New two-level format: satellite + product
		sat := ResolveSatelliteType(cfg.Satellite, cfg.Product)
		if sat != "" {
			cfg.Satellite = string(sat)
		}
	}
	if (SelectMode(cfg.SelectMode) == SelectClearestPerTile || SelectMode(cfg.SelectMode) == SelectCloudFreeCover) && SatelliteType(cfg.Satellite) != SatS2L2A {
		return nil, fmt.Errorf("select_mode=%q is only supported for sentinel-2, got %s", cfg.SelectMode, cfg.Satellite)
	}
	return &cfg, nil
}

func defaultConfigJSON() ([]byte, error) {
	now := time.Now()
	defaultCfg := Config{
		BBox:       []float64{116.2, 39.8, 116.6, 40.0},
		StartDate:  now.AddDate(0, 0, -30).Format("2006-01-02"),
		EndDate:    now.Format("2006-01-02"),
		MinCloud:   0,
		MaxCloud:   100,
		MaxNodata:  100,
		Bands:      []string{"red", "green", "blue"},
		Limit:      20,
		MaxWorkers: 4,
		MaxRetries: 3,
		SelectMode: string(SelectAll),
	}
	return json.MarshalIndent(defaultCfg, "", "  ")
}

func ensureDefaultConfig(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return WriteDefaultConfig(path)
}

func WriteDefaultConfig(path string) error {
	data, err := defaultConfigJSON()
	if err != nil {
		return fmt.Errorf("marshal default config: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write default config: %w", err)
	}
	fmt.Printf("Created default config: %s\n", path)
	return nil
}

func mergeSettings(cfg *Config) {
	s, err := loadSettings()
	if err != nil || s == nil {
		return
	}
	// Only merge authentication credentials from settings.
	if cfg.Auth == nil || cfg.Auth.Username == "" {
		auth := getSettingsAuth(s)
		if auth != nil {
			cfg.Auth = auth
		}
	}
}
