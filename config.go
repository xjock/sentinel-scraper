package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// SatelliteType identifies the satellite mission and product type.
type SatelliteType string

const (
	SatS2L2A SatelliteType = "sentinel-2-l2a"
	SatS1GRD SatelliteType = "sentinel-1-grd"
	SatS1SLC SatelliteType = "sentinel-1-slc"
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
	default:
		return SatS2L2A
	}
}

type Config struct {
	BBox       []float64   `json:"bbox"`
	StartDate  string      `json:"start_date"`
	EndDate    string      `json:"end_date"`
	MinCloud   float64     `json:"min_cloud"`
	MaxCloud   float64     `json:"max_cloud"`
	Bands      []string    `json:"bands"`
	Limit      int         `json:"limit"`
	MaxWorkers int         `json:"max_workers"`
	MaxRetries int         `json:"max_retries"`
	Satellite  string      `json:"satellite,omitempty"`
	STACURL    string      `json:"stac_url,omitempty"`
	Collection string      `json:"collection,omitempty"`
	Auth       *AuthConfig `json:"auth,omitempty"`
}

type SearchOptions struct {
	Bbox       []float64
	StartDate  string
	EndDate    string
	Limit      int
	MinCloud   float64
	MaxCloud   float64
	STACURL    string
	Collection string
	Satellite  SatelliteType
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
	if cfg.MinCloud < 0 {
		return nil, fmt.Errorf("min_cloud must be >= 0, got %f", cfg.MinCloud)
	}
	if cfg.MaxCloud < 0 {
		return nil, fmt.Errorf("max_cloud must be >= 0, got %f", cfg.MaxCloud)
	}
	if cfg.MinCloud > 0 && cfg.MaxCloud > 0 && cfg.MinCloud > cfg.MaxCloud {
		return nil, fmt.Errorf("min_cloud (%.1f) cannot be greater than max_cloud (%.1f)", cfg.MinCloud, cfg.MaxCloud)
	}
	if cfg.Satellite == "" {
		if cfg.Collection != "" {
			cfg.Satellite = string(ParseSatelliteType(cfg.Collection))
		} else {
			cfg.Satellite = string(SatS2L2A)
		}
	}
	if cfg.STACURL == "" {
		cfg.STACURL = EarthSearchURL
	}
	if cfg.Collection == "" {
		cfg.Collection = Collection
	}
	return &cfg, nil
}

func mergeSettings(cfg *Config) {
	s, err := loadSettings()
	if err != nil || s == nil {
		return
	}
	if cfg.STACURL == "" || cfg.STACURL == EarthSearchURL {
		if s.STACURL != "" {
			cfg.STACURL = s.STACURL
		}
	}
	if cfg.Collection == "" || cfg.Collection == Collection {
		if s.Collection != "" {
			cfg.Collection = s.Collection
		}
	}
	if cfg.Satellite == "" || cfg.Satellite == string(SatS2L2A) {
		if s.Satellite != "" {
			cfg.Satellite = s.Satellite
		}
	}
	if cfg.Auth == nil || cfg.Auth.Username == "" {
		if s.Auth != nil && s.Auth.Username != "" {
			cfg.Auth = s.Auth
		}
	}
}
