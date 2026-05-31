# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`sentinel-scraper` is a Go CLI that queries STAC/OData APIs and downloads Sentinel-2 L2A satellite imagery bands as Cloud Optimized GeoTIFFs (or full SAFE ZIPs). It is pure standard library — zero external Go dependencies.

The code is split across 9 files in `package main`:

| File | Role |
|------|------|
| `main.go` | Entrypoint, CLI flags, STAC flow orchestration, worker pool setup |
| `config.go` | `Config`/`SearchOptions` types, `LoadConfig`, `mergeSettings` |
| `settings.go` | User-level persistent settings (`~/.sentinel-scraper/settings.json`), CLI/Web setup wizards |
| `auth.go` | `Authenticator` interface, `NoOpAuth`, `CDSEAuth` (Keycloak OAuth2 password grant) |
| `stac.go` | STAC search, cloud filtering, per-band downloads, KML generation |
| `odata.go` | CDSE OData catalog search, full-scene ZIP download, JPEG2000 extraction, worker pool |
| `gdal.go` | GDAL tool discovery, `BuildRGB`, `buildRGBByte`, `renewByteTIFF` |
| `kml.go` | Shared `writeKMLFile` helper for STAC and OData KML output |
| `download.go` | Shared `resumableDownload` with HTTP `Range` resume, `progressReader`, `formatBytes` |
| `main_test.go` | Unit tests for `LoadConfig`, `SearchItems`, `FilterItemsByCloud`, `DownloadAsset`, `downloadWorker` |

## Data Sources

The tool supports three interchangeable sources, selected via `settings.json` (`-setup` / `-setup-auth`):

| Source | Protocol | Auth | Notes |
|--------|----------|------|-------|
| **Earth Search** | STAC | None | Public COG access; may require VPN depending on region |
| **CDSE STAC** | STAC | OAuth2 (CDSE) | Per-band downloads; `resolveAssetKey` maps `red` → `B04_10m` |
| **CDSE OData** | OData (Copernicus) | OAuth2 (CDSE) | Full SAFE ZIP (~1 GB); no VPN needed for catalog/zipper endpoints |

## Common Commands

| Task | Command |
|------|---------|
| Build binary | `go build -o sentinel-scraper .` or `make build` |
| Run | `go run . -config config.json -dest ./sentinel2_data` or `make run` |
| Format | `go fmt ./...` or `make fmt` |
| Vet | `go vet ./...` or `make vet` |
| Test | `go test ./...` |
| Clean | `make clean` |
| Package (Windows + GDAL bundle) | `make package` |
| Docker build | `docker build -t sentinel-scraper .` or `make docker` |

CI runs `go build`, `go fmt` check, and `go vet`.

## CLI Flags

- `-config` — path to JSON config file (default: `config.json`)
- `-dest` — destination directory for downloads (default: `./sentinel2_data`)
- `-setup` — open web-based setup wizard in browser
- `-setup-auth` — interactive CLI authentication setup wizard

## Architecture

**Data flow (STAC mode):**
1. `LoadConfig(path)` reads `config.json` into `Config`. Defaults: `limit=20`, `max_workers=4`, `max_retries=3`.
2. `mergeSettings(cfg)` overlays `~/.sentinel-scraper/settings.json` (source, STAC URL, collection, auth) onto the loaded config.
3. `SearchItems(opts, auth)` performs an HTTP GET to the STAC `/search` endpoint with bbox, datetime range, limit, and optional server-side cloud filter. Returns `STACItemCollection`.
4. `FilterItemsByCloud(items, maxCloud)` filters the `features` slice. Missing `eo:cloud_cover` is treated as pass-through (optimistic).
5. `DownloadAsset(asset, destDir, itemID, bandName, auth)` downloads each requested band via `asset.Href` with HTTP `Range` resume support. Skips files that already exist.
6. `BuildRGB(destDir, itemID)` shells out to `gdalbuildvrt` and `gdal_translate` to produce an RGB composite TIFF, then runs `gdal_trace_outline` → `gdalwarp` → `pkRenew` to fix internal nodata pixels.

**Data flow (OData mode):**
1. `queryODataProducts(auth, cfg)` queries `catalogue.dataspace.copernicus.eu/odata/v1/Products`.
2. `downloadODataProduct(auth, product, destDir, maxRetries)` downloads the full SAFE ZIP via `zipper.dataspace.copernicus.eu`, with `Range` resume.
3. `processODataProduct(zipPath, destDir, productName)` extracts `R10m/B02/B03/B04` JP2s, calls `buildRGBByte`, then `renewByteTIFF`.

**Key types:**
- `Config` — mirrors the JSON config fields plus optional `Auth` (username/password).
- `STACItem` / `STACItemCollection` / `STACProperties` / `Asset` — STAC API response shapes.
- `SearchOptions` — internal struct used to pass query parameters to `SearchItems`.
- `Authenticator` — interface for attaching credentials to outbound requests.

**Important implementation details:**
- The STAC search timeout is hard-coded to 60 seconds in `SearchItems`.
- `resumableDownload` (shared by STAC and OData) uses `http.Client` with configurable timeout and handles HTTP 200 / 206 / 416 status codes.
- Bands are matched by string key against `item.Assets` (e.g., `"red"`, `"nir"`).
- File naming convention: `<item.ID>_<band>.tif`.
- GDAL binaries and DLLs are expected either in the current directory (Windows bundle) or on `PATH`. `gdal.go:findGDALTool` checks for `gdal305.dll` in the same directory before falling back to PATH.
- `gdal.go:gdalEnv()` injects `PROJ_DATA` so bundled PROJ data can be found.
- Settings are stored in `~/.sentinel-scraper/settings.json` with file mode `0600`. Credentials are stored **in plaintext** inside that file.
