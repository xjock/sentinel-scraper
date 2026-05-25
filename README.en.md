# Sentinel-2 Go Fetcher

English | [中文](README.md)

A lightweight Go CLI for searching and downloading Sentinel-2 L2A satellite imagery. Supports multiple data sources, web/terminal setup wizards, resumable downloads, automatic RGB composition with black-border trimming, and outputs Cloud Optimized GeoTIFFs (COG). **Pure Go standard library — zero external Go dependencies.**

> Current version: **v1.0**

## Features

- **Three data sources**
  - Earth Search STAC (public AWS, no auth)
  - CDSE STAC (Copernicus Data Space, per-band COG)
  - CDSE OData (Copernicus Data Space, full-scene SAFE ZIP)
- **Setup wizards**: First run automatically opens a browser; SSH-friendly terminal mode also available
- **Friendly band names**: `red` / `green` / `blue` / `nir` etc., automatically mapped to provider-specific asset keys
- **Resume support**: HTTP `Range`-based resume; already-downloaded files are skipped
- **Concurrent downloads**: Configurable worker pool
- **RGB / RGBA composition**: Auto-builds 8-bit RGB composites via GDAL and **automatically trims black borders** to produce a clean RGBA output
- **KML output**: Per-scene KML file describing the image footprint, ready for Google Earth and similar tools
- **Authentication**: CDSE Keycloak OAuth2 password grant with automatic token refresh

## Quick Start

```bash
git clone <your-repo-url>
cd sentinel2-go
go build -o sentinel2-scraper .

# First run — automatically opens a browser setup page
./sentinel2-scraper
```

On first run, if `~/.sentinel2-go/settings.json` does not exist, the program starts a local HTTP server and opens your browser to choose a data source and enter credentials. The download flow resumes automatically once configuration is saved.

## Setup Wizard

### First Run (Auto)

```bash
./sentinel2-scraper
```

### Manual Reconfiguration

```bash
# Web wizard (opens a browser)
./sentinel2-scraper -setup

# Terminal wizard (no browser, SSH-friendly)
./sentinel2-scraper -setup-auth
```

### Data Source Options

| Option | Description | Authentication |
|--------|-------------|----------------|
| **Earth Search STAC API** | Public AWS-hosted STAC, per-band download | None |
| **CDSE STAC API** | Copernicus Data Space, per-band COG download | Username + Password |
| **CDSE OData API** | Copernicus Data Space, full-scene SAFE ZIP | Username + Password |
| **Custom STAC** | Any compatible STAC API endpoint | None |

### CDSE Registration Steps

1. Visit [dataspace.copernicus.eu](https://dataspace.copernicus.eu/) and register
2. Verify your email
3. In the setup wizard, enter your CDSE login email and password
4. Save and continue

Settings are stored in `~/.sentinel2-go/settings.json` with mode `0600` (owner read/write only). **Passwords are stored in plaintext** — protect your home directory permissions accordingly.

### Data Source Comparison

| Dimension | Earth Search STAC | CDSE STAC | CDSE OData |
|-----------|-------------------|-----------|------------|
| **Download granularity** | Per-band COG (50–200 MB / band) | Per-band COG (50–200 MB / band) | Full-scene ZIP (500 MB–1 GB+) |
| **Authentication** | None | CDSE account required | CDSE account required |
| **Speed** | Fast (AWS CloudFront CDN) | Medium (EU direct) | Slow (on-the-fly packaging + large files) |
| **Access from China** | Often requires VPN | Often requires VPN | Generally accessible without VPN |
| **Resume support** | ✅ | ✅ | ✅ |
| **RGB composite** | ✅ Auto | ✅ Auto | ✅ Auto (extracts R10m B02/B03/B04, then composites) |
| **KML output** | ✅ | ✅ | ✅ |

**Recommendations:**

- **Good network, want speed** → Earth Search STAC (default, fastest)
- **Earth Search unreachable, or need an official source** → CDSE STAC (per-band)
- **Need the full SAFE product (all bands + metadata) or VPN-free access** → CDSE OData

### `settings.json` Example

```json
{
  "source": "cdse",
  "stac_url": "https://stac.dataspace.copernicus.eu/v1",
  "collection": "sentinel-2-l2a",
  "auth": {
    "username": "your-email@example.com",
    "password": "your-password"
  }
}
```

`source` can be `earth_search` (or empty) / `cdse` / `cdse_odata` / `custom`.

## Configuration

### `config.json` — Query Parameters

```json
{
  "bbox": [116.2, 39.8, 116.6, 40.0],
  "start_date": "2026-04-01",
  "end_date": "2026-04-15",
  "max_cloud": 20.0,
  "bands": ["red", "green", "blue", "nir"],
  "limit": 20,
  "max_workers": 4,
  "max_retries": 3
}
```

| Field | Type | Description |
|-------|------|-------------|
| `bbox` | `[float64]` | Bounding box `[west, south, east, north]` |
| `start_date` | `string` | Start date `YYYY-MM-DD` |
| `end_date` | `string` | End date `YYYY-MM-DD` |
| `max_cloud` | `float64` | Maximum cloud cover percentage (0–100) |
| `bands` | `[string]` | List of bands to download (friendly names) |
| `limit` | `int` | Max STAC items returned (default: 20) |
| `max_workers` | `int` | Concurrent download workers (default: 4) |
| `max_retries` | `int` | Retry attempts per failed download (default: 3) |

### Command-line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-config` | `config.json` | Path to query configuration JSON |
| `-dest` | `./sentinel2_data` | Destination directory |
| `-setup` | — | Open the web setup wizard |
| `-setup-auth` | — | Open the terminal setup wizard |

### Environment Variables

`config.json` supports `${VAR}` substitution:

```json
{
  "auth": {
    "username": "${CDSE_USERNAME}",
    "password": "${CDSE_PASSWORD}"
  }
}
```

## Band Mapping

Use **friendly names** in `config.json`. They are automatically mapped to provider-specific asset keys.

### Earth Search Bands

| Friendly Name | Earth Search Key | Sentinel-2 Band |
|---------------|------------------|-----------------|
| `coastal` | `coastal` | B01 |
| `blue` | `blue` | B02 |
| `green` | `green` | B03 |
| `red` | `red` | B04 |
| `rededge1` | `rededge1` | B05 |
| `rededge2` | `rededge2` | B06 |
| `rededge3` | `rededge3` | B07 |
| `nir` | `nir` | B08 |
| `nir08` | `nir08` | B8A |
| `nir09` | `nir09` | B09 |
| `swir16` | `swir16` | B11 |
| `swir22` | `swir22` | B12 |
| `scl` | `scl` | SCL |

### CDSE Bands (Auto-mapped)

| Friendly Name | CDSE Asset Key | Resolution |
|---------------|----------------|------------|
| `coastal` | `B01_60m` | 60m |
| `blue` | `B02_10m` | 10m |
| `green` | `B03_10m` | 10m |
| `red` | `B04_10m` | 10m |
| `rededge1` | `B05_20m` | 20m |
| `rededge2` | `B06_20m` | 20m |
| `rededge3` | `B07_20m` | 20m |
| `nir` | `B08_10m` | 10m |
| `nir08` | `B8A_20m` | 20m |
| `nir09` | `B09_60m` | 60m |
| `swir16` | `B11_20m` | 20m |
| `swir22` | `B12_20m` | 20m |
| `scl` | `SCL_20m` | 20m |
| `aot` | `AOT_20m` | 20m |
| `wvp` | `WVP_10m` | 10m |
| `tci` | `TCI_10m` | 10m |

> Example: with `"bands": ["red", "green", "blue"]` against CDSE STAC, the program downloads `B04_10m` / `B03_10m` / `B02_10m` but saves them as `<item>_red.tif` / `<item>_green.tif` / `<item>_blue.tif` for compatibility with the RGB pipeline.

## Output

### STAC mode (Earth Search / CDSE STAC)

```
sentinel2_data/
  S2A_50TMK_20250105_0_L2A_red.tif
  S2A_50TMK_20250105_0_L2A_green.tif
  S2A_50TMK_20250105_0_L2A_blue.tif
  S2A_50TMK_20250105_0_L2A_nir.tif
  S2A_50TMK_20250105_0_L2A_rgba.tif    ← RGB + Alpha, black borders trimmed
  S2A_50TMK_20250105_0_L2A.kml         ← Footprint KML
  ...
```

CDSE STAC source files are JPEG 2000 (`.jp2`); GDAL reads them transparently. RGB output is stretched to 8-bit GeoTIFF (fixed 0–3000 → 0–255), then `gdal_trace_outline` + `gdalwarp` + `gdal_merge_simple` produce an RGBA image with the nodata black borders automatically removed.

### OData mode (CDSE OData)

```
sentinel2_data/
  S2A_T50TMK_20250105T030529_MSIL2A.zip            ← Full SAFE product
  S2A_T50TMK_20250105T030529_MSIL2A_rgba.tif       ← Auto RGB w/ borders trimmed
  S2A_T50TMK_20250105T030529_MSIL2A.kml            ← Footprint KML
  ...
```

OData mode also extracts `R10m/B02/B03/B04` from the ZIP, builds the RGB composite, and runs the same border-trimming pipeline. The original SAFE ZIP is always kept for downstream tools (SNAP, ENVI, etc.).

## Build & Run

```bash
# Direct build
go build -o sentinel2-scraper .

# Or use the Makefile
make build           # equivalent to go build -o sentinel2-scraper .
make run             # build and run
make fmt             # go fmt ./...
make vet             # go vet ./...
make clean           # remove build outputs
make package         # Windows + GDAL tooling bundle
make docker          # build Docker image
```

Tests:

```bash
go test ./...
```

## Docker

```bash
docker build -t sentinel2-scraper .
docker run --rm \
  -v $(pwd)/config.json:/app/config.json \
  -v $(pwd)/sentinel2_data:/app/sentinel2_data \
  sentinel2-scraper
```

## Project Structure

The code lives in `package main`, split into 9 Go files by responsibility:

| File | Role |
|------|------|
| `main.go` | CLI entrypoint, STAC flow orchestration, worker pool |
| `config.go` | `Config` / `SearchOptions` types, config loading & merging |
| `settings.go` | User-level persistent settings, web / CLI setup wizards |
| `auth.go` | Authenticator interface, CDSE Keycloak OAuth2 password grant |
| `stac.go` | STAC search, cloud filtering, per-band downloads |
| `odata.go` | CDSE OData search, full-scene ZIP download, JP2 extraction & RGB |
| `gdal.go` | GDAL tool discovery, `BuildRGB`, RGBA border-trimming pipeline |
| `kml.go` | Shared KML writer |
| `download.go` | Shared `Range`-resumable downloader and progress reporter |

## FAQ

**Q: Which data source should I use?**

- **Start with Earth Search**: fastest, AWS CloudFront global CDN; but may be unreachable from some networks
- **If Earth Search fails** → switch to **CDSE STAC**: per-band downloads, smaller files
- **Need the full SAFE product, or VPN-free access** → use **CDSE OData**: full-scene ZIP, slow but complete

**Q: Downloads fail or time out?**

- Earth Search / CDSE STAC: per-file ~50–200 MB, default timeout 10 minutes
- CDSE OData: full-scene ZIPs are typically 500 MB–1 GB+, single-file timeout 30 minutes
- On unstable networks, increase `max_retries` in `config.json` (e.g. 3 or 5)
- Reruns automatically resume from the previous progress

**Q: No items returned?**

- Ensure your date range falls within the Sentinel-2 archive
- Ensure the bbox covers land
- Increase `max_cloud` or remove the cloud filter
- Coverage may differ slightly between data sources

**Q: How do I switch data sources?**

```bash
./sentinel2-scraper -setup
```

You can switch any time; previously-downloaded files are unaffected.

**Q: Can I use a custom STAC API?**

Yes. Pick "Custom STAC API" in the wizard and provide the endpoint URL and collection name.

## License

MIT
