package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
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
	ASFProductType   string
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
		ASFProductType:   "GRD_HD",
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
		ASFProductType:   "SLC",
	},
	SatHLS: {
		Collection:       "HLSS30.v2.0",
		CDSECollection:   "",
		NeedsCloudFilter: true,
		SupportsRGB:      true,
		DefaultBands:     []string{"red", "green", "blue"},
		BandMap: map[string]string{
			"coastal": "B01", "blue": "B02", "green": "B03", "red": "B04",
			"nir": "B08", "nir08": "B8A", "nir09": "B09",
			"swir16": "B11", "swir22": "B12", "fmask": "Fmask",
		},
		KnownBands: []string{"coastal", "blue", "green", "red", "nir", "nir08", "nir09", "swir16", "swir22", "fmask"},
	},
	SatLandsat8: {
		Collection:       "landsat-8-c2-l2",
		NeedsCloudFilter: true,
		SupportsRGB:      true,
		DefaultBands:     []string{"red", "green", "blue"},
		BandMap: map[string]string{
			"coastal": "SR_B1", "blue": "SR_B2", "green": "SR_B3", "red": "SR_B4",
			"nir": "SR_B5", "swir16": "SR_B6", "swir22": "SR_B7",
			"panchromatic": "SR_B8", "cirrus": "SR_B9", "tirs1": "ST_B10",
			"qa": "QA_PIXEL",
		},
		KnownBands: []string{"coastal", "blue", "green", "red", "nir", "swir16", "swir22", "panchromatic", "cirrus", "tirs1", "qa"},
	},
	SatLandsat9: {
		Collection:       "landsat-9-c2-l2",
		NeedsCloudFilter: true,
		SupportsRGB:      true,
		DefaultBands:     []string{"red", "green", "blue"},
		BandMap: map[string]string{
			"coastal": "SR_B1", "blue": "SR_B2", "green": "SR_B3", "red": "SR_B4",
			"nir": "SR_B5", "swir16": "SR_B6", "swir22": "SR_B7",
			"panchromatic": "SR_B8", "cirrus": "SR_B9", "tirs1": "ST_B10",
			"qa": "QA_PIXEL",
		},
		KnownBands: []string{"coastal", "blue", "green", "red", "nir", "swir16", "swir22", "panchromatic", "cirrus", "tirs1", "qa"},
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
	Links    []STACLink `json:"links,omitempty"`
}

// STACLink is a STAC navigation link. The "next" link drives pagination; Earth
// Search returns it either as a GET href or a POST href+body (token).
type STACLink struct {
	Rel    string                 `json:"rel"`
	Href   string                 `json:"href"`
	Method string                 `json:"method,omitempty"`
	Body   map[string]interface{} `json:"body,omitempty"`
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
	Datetime              string   `json:"datetime"`
	Created               string   `json:"created"`
	CloudCover            *float64 `json:"eo:cloud_cover,omitempty"`
	NodataPixelPercentage *float64 `json:"s2:nodata_pixel_percentage,omitempty"`
	GranuleID             string   `json:"s2:granule_id,omitempty"`
	GridCode              string   `json:"grid:code,omitempty"`
	// Statistics carries CDSE's nested per-class pixel percentages (0-100),
	// e.g. {"nodata": 45.9, "vegetation": 30.3, ...}. CDSE has no top-level
	// s2:nodata_pixel_percentage, so coverage is read from Statistics["nodata"].
	Statistics map[string]float64 `json:"statistics,omitempty"`
}

// EffectiveNodata returns the nodata pixel percentage (0-100) from whichever
// field the STAC backend provides: Earth Search's s2:nodata_pixel_percentage
// or CDSE's nested statistics.nodata (both already expressed as percentages).
// ok is false when neither field is present.
func (p STACProperties) EffectiveNodata() (float64, bool) {
	if p.NodataPixelPercentage != nil {
		return *p.NodataPixelPercentage, true
	}
	if v, ok := p.Statistics["nodata"]; ok {
		return v, true
	}
	return 0, false
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
		opts.Limit = 20
	}
	if opts.GridCode == "" && len(opts.Bbox) != 4 {
		return nil, fmt.Errorf("bbox must have 4 elements [west,south,east,north]")
	}
	var bboxStr string
	if len(opts.Bbox) == 4 {
		bboxStr = fmt.Sprintf("%f,%f,%f,%f", opts.Bbox[0], opts.Bbox[1], opts.Bbox[2], opts.Bbox[3])
	}
	startDT := opts.StartDate
	if !strings.Contains(startDT, "T") {
		startDT += "T00:00:00Z"
	}
	endDT := opts.EndDate
	if !strings.Contains(endDT, "T") {
		endDT += "T23:59:59Z"
	}
	datetime := startDT + "/" + endDT

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
	if opts.GridCode != "" {
		q.Del("bbox")
	} else {
		q.Set("bbox", bboxStr)
	}
	q.Set("datetime", datetime)
	q.Set("limit", fmt.Sprintf("%d", opts.Limit))

	queryParts := map[string]interface{}{}
	if opts.GridCode != "" {
		queryParts["grid:code"] = map[string]string{"eq": opts.GridCode}
	}
	if cfg.NeedsCloudFilter {
		cc := map[string]float64{}
		if opts.MinCloud > 0 {
			cc["gte"] = opts.MinCloud
		}
		if opts.MaxCloud > 0 {
			cc["lte"] = opts.MaxCloud
		}
		if len(cc) > 0 {
			queryParts["eo:cloud_cover"] = cc
		}
	}
	// CDSE has no queryable s2:nodata_pixel_percentage field (sending it returns
	// zero results); its coverage lives in the nested statistics.nodata and is
	// filtered client-side via FilterItemsByNodata. Only Earth Search-style
	// backends support the server-side nodata filter.
	isCDSE := strings.Contains(stacURL, "stac.dataspace.copernicus.eu")
	if opts.Satellite == SatS2L2A && opts.MaxNodata >= 0 && opts.MaxNodata < 100 && !isCDSE {
		queryParts["s2:nodata_pixel_percentage"] = map[string]float64{"lte": opts.MaxNodata}
	}
	if opts.Platform != "" {
		queryParts["platform"] = map[string]string{"eq": opts.Platform}
	}
	if len(queryParts) > 0 {
		qb, err := json.Marshal(queryParts)
		if err != nil {
			return nil, fmt.Errorf("marshal query filter: %w", err)
		}
		q.Set("query", string(qb))
	}
	if len(opts.SortBy) > 0 {
		// STAC GET search expects sortby as comma-separated [+-]field tokens
		// (e.g. "+properties.eo:cloud_cover"), NOT a JSON array. The JSON form
		// is only valid in a POST body; sending it on GET yields HTTP 400.
		tokens := make([]string, 0, len(opts.SortBy))
		for _, s := range opts.SortBy {
			sign := "+"
			if strings.EqualFold(s.Direction, "desc") {
				sign = "-"
			}
			tokens = append(tokens, sign+s.Field)
		}
		q.Set("sortby", strings.Join(tokens, ","))
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

	client := newHTTPClient(60 * time.Second)
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

// extractTileFromGridCode returns the MGRS tile code from a grid:code such as "MGRS-37MDS".
func extractTileFromGridCode(gridCode string) string {
	parts := strings.SplitN(gridCode, "-", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return gridCode
}

// fetchSTACPage follows a STAC pagination link (GET href or POST href+body).
func fetchSTACPage(link STACLink, auth Authenticator) (*STACItemCollection, error) {
	method := link.Method
	if method == "" {
		method = http.MethodGet
	}
	var req *http.Request
	var err error
	if strings.EqualFold(method, http.MethodPost) {
		body, _ := json.Marshal(link.Body)
		req, err = http.NewRequest(http.MethodPost, link.Href, bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	} else {
		req, err = http.NewRequest(http.MethodGet, link.Href, nil)
	}
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/geo+json")
	if err := auth.Apply(req); err != nil {
		return nil, err
	}
	resp, err := newHTTPClient(60 * time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("STAC page returned %d: %s", resp.StatusCode, string(b))
	}
	var result STACItemCollection
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func nextLink(links []STACLink) *STACLink {
	for i := range links {
		if strings.EqualFold(links[i].Rel, "next") && links[i].Href != "" {
			return &links[i]
		}
	}
	return nil
}

// discoverTiles returns the unique MGRS tile codes intersecting the search bbox.
// It paginates through all matching scenes so large areas are fully enumerated
// (a single page would silently miss tiles whose clear scenes fall later).
func discoverTiles(opts SearchOptions, auth Authenticator) ([]string, error) {
	const maxPages = 50
	discoverOpts := opts
	discoverOpts.GridCode = ""
	discoverOpts.SortBy = nil
	// Tile discovery must enumerate every intersecting tile, so never restrict
	// by coverage here (a partial-granule scene still proves the tile exists).
	discoverOpts.MaxNodata = 100
	if discoverOpts.Limit < 100 {
		discoverOpts.Limit = 100
	}
	collection, err := SearchItems(discoverOpts, auth)
	if err != nil {
		return nil, fmt.Errorf("discover tiles: %w", err)
	}
	seen := make(map[string]bool)
	var tiles []string
	collect := func(c *STACItemCollection) {
		for _, item := range c.Features {
			tile := extractTileFromGridCode(item.Properties.GridCode)
			if tile != "" && !seen[tile] {
				seen[tile] = true
				tiles = append(tiles, tile)
			}
		}
	}
	collect(collection)
	for page := 0; page < maxPages; page++ {
		next := nextLink(collection.Links)
		if next == nil {
			break
		}
		collection, err = fetchSTACPage(*next, auth)
		if err != nil {
			// Stop gracefully on pagination error; return what we have.
			fmt.Fprintf(os.Stderr, "[warn] tile discovery stopped early: %v\n", err)
			break
		}
		if len(collection.Features) == 0 {
			break
		}
		collect(collection)
	}
	sort.Strings(tiles)
	return tiles, nil
}

// SearchItemsClearestPerTile queries each MGRS tile independently and returns the
// single clearest (lowest cloud cover) scene per tile that also satisfies the
// max_cloud and max_nodata filters.
func SearchItemsClearestPerTile(opts SearchOptions, auth Authenticator) (*STACItemCollection, error) {
	if opts.Satellite == "" {
		opts.Satellite = SatS2L2A
	}
	if opts.Satellite != SatS2L2A {
		return nil, fmt.Errorf("clearest-per-tile is only supported for sentinel-2, got %s", opts.Satellite)
	}

	tiles := opts.Tiles
	if len(tiles) == 0 {
		var err error
		tiles, err = discoverTiles(opts, auth)
		if err != nil {
			return nil, err
		}
		if len(tiles) == 0 {
			return nil, fmt.Errorf("no MGRS tiles found for the requested area")
		}
	}

	sortBy := []SortSpec{{Field: "properties.eo:cloud_cover", Direction: "asc"}}

	var allFeatures []STACItem
	for _, tile := range tiles {
		tileOpts := opts
		tileOpts.GridCode = "MGRS-" + tile
		tileOpts.SortBy = sortBy
		// Fetch several candidates (clearest-first) rather than just one:
		// backends like CDSE cannot filter nodata server-side, so the single
		// clearest scene may be a partial granule. We drop partials client-side
		// and then keep the clearest survivor.
		tileOpts.Limit = 50
		collection, err := SearchItems(tileOpts, auth)
		if err != nil {
			return nil, fmt.Errorf("search tile %s: %w", tile, err)
		}
		cands := FilterItemsByNodata(collection.Features, opts.MaxNodata, opts.Satellite)
		if len(cands) == 0 {
			fmt.Fprintf(os.Stderr, "[warn] no full-coverage scene found for tile %s with the current filters\n", tile)
			continue
		}
		// Pick the clearest (lowest cloud cover) survivor; do not rely on the
		// server honouring sortby so the choice is backend-agnostic.
		best := cands[0]
		for _, c := range cands[1:] {
			if cloudCoverOrInf(c) < cloudCoverOrInf(best) {
				best = c
			}
		}
		allFeatures = append(allFeatures, best)
	}

	return &STACItemCollection{Type: "FeatureCollection", Features: allFeatures}, nil
}

// cloudCoverOrInf returns the item's cloud cover, or +Inf when absent so such
// items sort last when picking the clearest scene.
func cloudCoverOrInf(item STACItem) float64 {
	if item.Properties.CloudCover != nil {
		return *item.Properties.CloudCover
	}
	return math.Inf(1)
}

// pointInRing reports whether (lon,lat) lies inside the polygon ring using the
// ray-casting (even-odd) rule. ring is a slice of [lon,lat] pairs.
func pointInRing(lon, lat float64, ring [][]float64) bool {
	in := false
	n := len(ring)
	if n < 3 {
		return false
	}
	j := n - 1
	for i := 0; i < n; i++ {
		xi, yi := ring[i][0], ring[i][1]
		xj, yj := ring[j][0], ring[j][1]
		if (yi > lat) != (yj > lat) && lon < (xj-xi)*(lat-yi)/(yj-yi)+xi {
			in = !in
		}
		j = i
	}
	return in
}

// greedyCoverScenes selects the fewest scenes whose footprints jointly cover the
// area reachable by any candidate (the per-tile target), to coverageTarget ratio.
// It rasterises the candidates' combined extent into a grid and runs a greedy
// set cover over grid cells (pure stdlib; no geometry library). Ties break toward
// lower cloud cover. Returns the chosen scenes in selection order.
func greedyCoverScenes(cands []STACItem, coverageTarget float64, maxPerTile int) []STACItem {
	type fp struct {
		item STACItem
		ring [][]float64
	}
	var fps []fp
	minLon, minLat := math.Inf(1), math.Inf(1)
	maxLon, maxLat := math.Inf(-1), math.Inf(-1)
	for _, it := range cands {
		if it.Geometry.Type != "Polygon" || len(it.Geometry.Coordinates) == 0 {
			continue
		}
		ring := it.Geometry.Coordinates[0]
		if len(ring) < 3 {
			continue
		}
		fps = append(fps, fp{it, ring})
		for _, p := range ring {
			minLon, maxLon = math.Min(minLon, p[0]), math.Max(maxLon, p[0])
			minLat, maxLat = math.Min(minLat, p[1]), math.Max(maxLat, p[1])
		}
	}
	if len(fps) == 0 {
		return nil
	}

	// Grid step ~0.02 deg (~2 km); cap total cells so worst cases stay cheap.
	step := 0.02
	nx := int((maxLon-minLon)/step) + 1
	ny := int((maxLat-minLat)/step) + 1
	for nx*ny > 40000 {
		step *= 1.5
		nx = int((maxLon-minLon)/step) + 1
		ny = int((maxLat-minLat)/step) + 1
	}

	ncells := nx * ny
	// coveredBy[c] = list of candidate indices whose footprint covers cell c.
	cellCands := make([][]int, ncells)
	inTarget := make([]bool, ncells) // cell reachable by >=1 candidate
	cellCoveredCount := 0
	for ci := 0; ci < ncells; ci++ {
		gx := ci % nx
		gy := ci / nx
		lon := minLon + (float64(gx)+0.5)*step
		lat := minLat + (float64(gy)+0.5)*step
		for fi := range fps {
			if pointInRing(lon, lat, fps[fi].ring) {
				cellCands[ci] = append(cellCands[ci], fi)
			}
		}
		if len(cellCands[ci]) > 0 {
			inTarget[ci] = true
			cellCoveredCount++
		}
	}
	if cellCoveredCount == 0 {
		return nil
	}

	// Greedy set cover over target cells.
	covered := make([]bool, ncells)
	used := make([]bool, len(fps))
	var chosen []STACItem
	coveredTarget := 0
	for len(chosen) < maxPerTile {
		bestFi, bestGain := -1, 0
		for fi := range fps {
			if used[fi] {
				continue
			}
			gain := 0
			// Count newly covered target cells contributed by this footprint.
			for ci := 0; ci < ncells; ci++ {
				if !inTarget[ci] || covered[ci] {
					continue
				}
				for _, c := range cellCands[ci] {
					if c == fi {
						gain++
						break
					}
				}
			}
			if gain > bestGain || (gain == bestGain && gain > 0 && bestFi >= 0 &&
				cloudCoverOrInf(fps[fi].item) < cloudCoverOrInf(fps[bestFi].item)) {
				bestFi, bestGain = fi, gain
			}
		}
		if bestFi < 0 || bestGain == 0 {
			break
		}
		used[bestFi] = true
		chosen = append(chosen, fps[bestFi].item)
		for ci := 0; ci < ncells; ci++ {
			if inTarget[ci] && !covered[ci] {
				for _, c := range cellCands[ci] {
					if c == bestFi {
						covered[ci] = true
						coveredTarget++
						break
					}
				}
			}
		}
		if float64(coveredTarget)/float64(cellCoveredCount) >= coverageTarget {
			break
		}
	}
	return chosen
}

// SearchItemsCloudFreeCover queries each MGRS tile independently and selects the
// fewest <max_cloud scenes whose footprints jointly cover the tile (to
// coverage_target), so stacking them yields a gap-free, low-cloud mosaic. Unlike
// clearest-per-tile it may return several complementary scenes per tile to fill
// partial-granule swaths.
func SearchItemsCloudFreeCover(opts SearchOptions, auth Authenticator) (*STACItemCollection, error) {
	if opts.Satellite == "" {
		opts.Satellite = SatS2L2A
	}
	if opts.Satellite != SatS2L2A {
		return nil, fmt.Errorf("cloud-free-cover is only supported for sentinel-2, got %s", opts.Satellite)
	}
	coverageTarget := opts.CoverageTarget
	if coverageTarget <= 0 || coverageTarget > 1 {
		coverageTarget = 0.995
	}
	maxPerTile := opts.MaxPerTile
	if maxPerTile <= 0 {
		maxPerTile = 6
	}

	// Set cover needs partial granules, so never filter nodata server-side here.
	opts.MaxNodata = 100

	tiles := opts.Tiles
	if len(tiles) == 0 {
		var err error
		tiles, err = discoverTiles(opts, auth)
		if err != nil {
			return nil, err
		}
		if len(tiles) == 0 {
			return nil, fmt.Errorf("no MGRS tiles found for the requested area")
		}
	}

	sortBy := []SortSpec{{Field: "properties.eo:cloud_cover", Direction: "asc"}}
	var allFeatures []STACItem
	for _, tile := range tiles {
		tileOpts := opts
		tileOpts.GridCode = "MGRS-" + tile
		tileOpts.SortBy = sortBy
		tileOpts.Limit = 100
		collection, err := SearchItems(tileOpts, auth)
		if err != nil {
			return nil, fmt.Errorf("search tile %s: %w", tile, err)
		}
		chosen := greedyCoverScenes(collection.Features, coverageTarget, maxPerTile)
		if len(chosen) == 0 {
			fmt.Fprintf(os.Stderr, "[warn] no scene found for tile %s with the current filters\n", tile)
			continue
		}
		fmt.Fprintf(os.Stderr, "[cloud-free-cover] tile %s: %d scene(s)\n", tile, len(chosen))
		allFeatures = append(allFeatures, chosen...)
	}
	return &STACItemCollection{Type: "FeatureCollection", Features: allFeatures}, nil
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

// FilterItemsByNodata drops Sentinel-2 items whose s2:nodata_pixel_percentage exceeds maxNodata.
// For non-S2 satellites or when maxNodata is unset (>=100), items pass through unchanged.
func FilterItemsByNodata(items []STACItem, maxNodata float64, sat SatelliteType) []STACItem {
	if sat == "" {
		sat = SatS2L2A
	}
	if sat != SatS2L2A || maxNodata < 0 || maxNodata >= 100 {
		return items
	}
	var filtered []STACItem
	for _, item := range items {
		nd, ok := item.Properties.EffectiveNodata()
		if !ok {
			// Neither Earth Search nor CDSE coverage field present;
			// keep the item to avoid silent data loss.
			filtered = append(filtered, item)
			continue
		}
		if nd <= maxNodata {
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

	client := newHTTPClient(30 * time.Second)
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
	client := newHTTPClient(DownloadTimeout)
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
