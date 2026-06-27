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
	"strconv"
	"strings"
	"sync"
	"time"
)

var planetarySTACURL = "https://planetarycomputer.microsoft.com/api/stac/v1"

// planetaryTokenResponse is the SAS token returned by Planetary Computer.
type planetaryTokenResponse struct {
	Expiry string `json:"msft:expiry"`
	Token  string `json:"token"`
}

// planetaryTokenCache holds a cached SAS token and its expiry.
type planetaryTokenCache struct {
	mu        sync.RWMutex
	token     string
	expiry    time.Time
	account   string
	container string
}

var planetarySASCache = &planetaryTokenCache{}

// getPlanetarySASToken returns a cached SAS token or fetches a new one.
func getPlanetarySASToken(account, container string) (string, error) {
	planetarySASCache.mu.RLock()
	cached, expiry := planetarySASCache.token, planetarySASCache.expiry
	cachedAccount, cachedContainer := planetarySASCache.account, planetarySASCache.container
	planetarySASCache.mu.RUnlock()

	if cached != "" && time.Now().Add(5*time.Minute).Before(expiry) &&
		cachedAccount == account && cachedContainer == container {
		return cached, nil
	}

	planetarySASCache.mu.Lock()
	defer planetarySASCache.mu.Unlock()
	// double-check after acquiring write lock
	if planetarySASCache.token != "" && time.Now().Add(5*time.Minute).Before(planetarySASCache.expiry) &&
		planetarySASCache.account == account && planetarySASCache.container == container {
		return planetarySASCache.token, nil
	}

	tokenURL := fmt.Sprintf("https://planetarycomputer.microsoft.com/api/sas/v1/token/%s/%s", account, container)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	client := newHTTPClient(30 * time.Second)

	// The anonymous SAS token endpoint is rate-limited (HTTP 429). Retry a few
	// times, honoring Retry-After when the server provides it.
	const maxAttempts = 4
	var body []byte
	for attempt := 0; attempt < maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "GET", tokenURL, nil)
		if err != nil {
			return "", fmt.Errorf("create token request: %w", err)
		}

		resp, err := client.Do(req)
		if err != nil {
			if attempt < maxAttempts-1 {
				time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
				continue
			}
			return "", fmt.Errorf("token request failed: %w", err)
		}

		b, readErr := io.ReadAll(resp.Body)
		statusCode := resp.StatusCode
		retryAfter := resp.Header.Get("Retry-After")
		resp.Body.Close()
		if readErr != nil {
			return "", fmt.Errorf("read token response: %w", readErr)
		}

		if statusCode == http.StatusOK {
			body = b
			break
		}

		if (statusCode == http.StatusTooManyRequests || statusCode >= 500) && attempt < maxAttempts-1 {
			wait := time.Duration(attempt+1) * 2 * time.Second
			if retryAfter != "" {
				if secs, perr := strconv.Atoi(strings.TrimSpace(retryAfter)); perr == nil && secs > 0 {
					wait = time.Duration(secs) * time.Second
				}
			}
			time.Sleep(wait)
			continue
		}

		return "", fmt.Errorf("token endpoint returned %d: %s", statusCode, string(b))
	}
	if body == nil {
		return "", fmt.Errorf("SAS token endpoint rate-limited after %d attempts", maxAttempts)
	}

	var tr planetaryTokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if tr.Token == "" {
		return "", fmt.Errorf("empty SAS token returned")
	}

	expiry, perr := time.Parse(time.RFC3339, tr.Expiry)
	if perr != nil {
		expiry = time.Now().Add(1 * time.Hour)
	}

	planetarySASCache.token = tr.Token
	planetarySASCache.expiry = expiry
	planetarySASCache.account = account
	planetarySASCache.container = container
	return tr.Token, nil
}

// signPlanetaryAssetHref appends a SAS token to a Planetary Computer blob URL.
func signPlanetaryAssetHref(href string) (string, error) {
	u, err := url.Parse(href)
	if err != nil {
		return "", fmt.Errorf("parse asset href: %w", err)
	}
	if !strings.Contains(u.Host, ".blob.core.windows.net") {
		return href, nil
	}
	parts := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("unexpected blob path: %s", u.Path)
	}
	account := strings.TrimSuffix(u.Host, ".blob.core.windows.net")
	container := parts[0]

	token, err := getPlanetarySASToken(account, container)
	if err != nil {
		return "", err
	}

	sep := "?"
	if u.RawQuery != "" {
		sep = "&"
	}
	return href + sep + token, nil
}

// runPlanetaryFlow downloads Landsat data from Microsoft Planetary Computer.
func runPlanetaryFlow(cfg *Config, auth Authenticator, destDir string) error {
	sat := SatelliteType(cfg.Satellite)
	if sat == "" {
		sat = SatS2L2A
	}
	sc := satelliteConfigs[sat]

	if len(cfg.Bands) == 0 {
		cfg.Bands = sc.DefaultBands
	}

	c := *cfg
	c.STACURL = planetarySTACURL
	c.Collection = "landsat-c2-l2"

	fmt.Println("\n=== Planetary Computer Search ===")
	fmt.Printf("  Collection: %s\n", c.Collection)
	fmt.Printf("  BBox:       %v\n", c.BBox)
	fmt.Printf("  Date:       %s to %s\n", c.StartDate, c.EndDate)
	if sc.NeedsCloudFilter {
		fmt.Printf("  Cloud:      %.0f%% - %.0f%%\n", c.MinCloud, c.MaxCloud)
	}
	fmt.Printf("  Bands:      %v\n\n", c.Bands)

	// Reuse STAC search.
	opts := SearchOptions{
		Bbox:       c.BBox,
		StartDate:  c.StartDate,
		EndDate:    c.EndDate,
		Limit:      c.Limit,
		MinCloud:   c.MinCloud,
		MaxCloud:   c.MaxCloud,
		STACURL:    c.STACURL,
		Collection: c.Collection,
		Satellite:  sat,
	}
	// landsat-c2-l2 mixes Landsat 8 and 9; restrict to the requested platform.
	if sat == SatLandsat8 || sat == SatLandsat9 {
		opts.Platform = string(sat)
	}
	stacCollection, err := SearchItems(opts, NoOpAuth{})
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}
	if len(stacCollection.Features) == 0 {
		return fmt.Errorf("no items found")
	}

	items := FilterItemsByCloud(stacCollection.Features, c.MinCloud, c.MaxCloud, sat)
	PrintItemSummary(items)

	fmt.Println("\n=== Saving KML ===")
	for _, item := range items {
		if _, err := SaveKML(item, destDir); err != nil {
			fmt.Fprintf(os.Stderr, "  [kml skip] %s: %v\n", item.ID, err)
		}
	}

	fmt.Println("\n=== Downloading Bands ===")
	tasks := make(chan downloadTask, c.MaxWorkers*2)
	results := make(chan downloadResult, c.MaxWorkers*2)

	var wg sync.WaitGroup
	for i := 0; i < c.MaxWorkers; i++ {
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

	go func() {
		for _, item := range items {
			for _, band := range c.Bands {
				assetKey := resolveAssetKey(band, c.STACURL, sat)
				asset, ok := item.Assets[assetKey]
				if !ok {
					fmt.Printf("  [warn] band '%s' not available (tried '%s')\n", band, assetKey)
					continue
				}
				signedHref, err := signPlanetaryAssetHref(asset.Href)
				if err != nil {
					fmt.Fprintf(os.Stderr, "  [warn] failed to sign %s/%s: %v\n", item.ID, band, err)
					continue
				}
				asset.Href = signedHref
				tasks <- downloadTask{itemID: item.ID, band: band, asset: asset, destDir: destDir, maxRetries: c.MaxRetries, auth: NoOpAuth{}}
			}
		}
		close(tasks)
	}()

	failed := 0
	skipped := 0
	total := 0
	for res := range results {
		total++
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
