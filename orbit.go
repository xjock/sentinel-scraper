package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	asfPOEORBURL = "https://s1qc.asf.alaska.edu/aux_poeorb/"
	asfRESORBURL = "https://s1qc.asf.alaska.edu/aux_resorb/"
)

// safeItem holds parsed metadata from a .SAFE folder or .zip file.
type safeItem struct {
	name string
	date time.Time
	sat  string
}

// parseSafeDate extracts the acquisition date from a SAFE folder or zip name.
// S1A_IW_SLC__1SDV_20231231T034722_... → 2023-12-31
func parseSafeDate(safeName string) (time.Time, error) {
	re := regexp.MustCompile(`_(\d{8})T\d{6}_`)
	m := re.FindStringSubmatch(safeName)
	if m == nil {
		return time.Time{}, fmt.Errorf("no date found in %s", safeName)
	}
	return time.Parse("20060102", m[1])
}

// getSatelliteID extracts S1A/S1B/S1C from a SAFE name.
func getSatelliteID(safeName string) string {
	re := regexp.MustCompile(`^(S1[ABC])_`)
	m := re.FindStringSubmatch(safeName)
	if m != nil {
		return m[1]
	}
	return "S1A"
}

// fetchASFOrbitList fetches the HTML directory listing from ASF S1QC
// and extracts all .EOF filenames.
func fetchASFOrbitList(ctx context.Context, client *http.Client, listURL string, auth Authenticator) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if err := auth.Apply(req); err != nil {
		return nil, fmt.Errorf("authenticate: %w", err)
	}
	req.Header.Set("User-Agent", "sentinel-scraper/"+version)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch listing: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ASF returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	re := regexp.MustCompile(`href="([^"]+\.EOF)"`)
	matches := re.FindAllStringSubmatch(string(body), -1)
	files := make([]string, 0, len(matches))
	for _, m := range matches {
		files = append(files, m[1])
	}
	sort.Strings(files)
	return files, nil
}

// findMatchingOrbit finds an orbit file whose validity window covers the acquisition date.
// POEORB naming: S1A_OPER_AUX_POEORB_OPOD_{publish}_V{start}_{end}.EOF
func findMatchingOrbit(orbitFiles []string, sat string, acqDate time.Time, orbitType string) string {
	prefix := fmt.Sprintf("%s_OPER_AUX_%s_OPOD_", sat, orbitType)
	re := regexp.MustCompile(`_V(\d{8})T\d{6}_(\d{8})T\d{6}\.EOF$`)

	for _, f := range orbitFiles {
		if !strings.HasPrefix(f, prefix) {
			continue
		}
		m := re.FindStringSubmatch(f)
		if m == nil {
			continue
		}
		vStart, _ := time.Parse("20060102", m[1])
		vEnd, _ := time.Parse("20060102", m[2])
		if !vStart.IsZero() && !vEnd.IsZero() && !acqDate.Before(vStart) && !acqDate.After(vEnd) {
			return f
		}
	}
	return ""
}

// downloadOrbit downloads a single EOF file using the shared resumableDownload.
func downloadOrbit(ctx context.Context, client *http.Client, url string, auth Authenticator, outPath string, maxRetries int) error {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			wait := time.Duration(attempt) * time.Second
			time.Sleep(wait)
		}
		_, _, skipped, err := resumableDownload(ctx, client, url, auth, outPath, filepath.Base(outPath), 0)
		if err == nil {
			if skipped {
				fmt.Printf("  [skip] %s already exists\n", filepath.Base(outPath))
			} else {
				size := int64(0)
				if info, statErr := os.Stat(outPath); statErr == nil {
					size = info.Size()
				}
				fmt.Printf("  [saved] %s (%s)\n", filepath.Base(outPath), formatBytes(size))
			}
			return nil
		}
		lastErr = err
		fmt.Fprintf(os.Stderr, "  [retry] %s in %.0fs (attempt %d/%d): %v\n",
			filepath.Base(outPath), time.Duration(attempt+1).Seconds(), attempt+1, maxRetries, err)
	}
	return lastErr
}

// buildOrbitClient creates an HTTP client suitable for ASF S1QC requests.
// For Earthdata auth it performs URS login to obtain a cookie jar.
func buildOrbitClient(auth Authenticator) *http.Client {
	if ea, ok := auth.(*EarthdataAuth); ok {
		jar, err := ursLogin(ea.Username, ea.Password)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [warn] URS login failed: %v (downloads may fail)\n", err)
			return &http.Client{Timeout: 30 * time.Minute}
		}
		return &http.Client{
			Timeout: 30 * time.Minute,
			Jar:     jar,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) > 0 {
					if h := via[0].Header.Get("Authorization"); h != "" {
						req.Header.Set("Authorization", h)
					}
				}
				return nil
			},
		}
	}
	return &http.Client{Timeout: 30 * time.Minute}
}

// runOrbitDownload downloads orbit EOF files for all SAFE/zips in safeDir.
func runOrbitDownload(safeDir string, orbitDir string, auth Authenticator, maxWorkers int, maxRetries int, forceResorb bool) error {
	if err := os.MkdirAll(orbitDir, 0755); err != nil {
		return fmt.Errorf("mkdir orbit dir: %w", err)
	}

	entries, err := os.ReadDir(safeDir)
	if err != nil {
		return fmt.Errorf("read safe dir: %w", err)
	}

	var items []safeItem
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".SAFE") && !strings.HasSuffix(name, ".zip") {
			continue
		}
		date, err := parseSafeDate(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [warn] skip %s: %v\n", name, err)
			continue
		}
		items = append(items, safeItem{name: name, date: date, sat: getSatelliteID(name)})
	}

	if len(items) == 0 {
		return fmt.Errorf("no SAFE/zip files found in %s", safeDir)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].date.Before(items[j].date)
	})

	// Determine orbit type
	orbitType := "POEORB"
	baseURL := asfPOEORBURL
	if !forceResorb {
		now := time.Now()
		maxDate := items[len(items)-1].date
		daysSinceLast := int(now.Sub(maxDate).Hours() / 24)
		if daysSinceLast <= 20 {
			orbitType = "RESORB"
			baseURL = asfRESORBURL
			fmt.Printf("Most recent scene is %d days old, using RESORB.\n", daysSinceLast)
		}
	} else {
		orbitType = "RESORB"
		baseURL = asfRESORBURL
		fmt.Println("Forced RESORB mode.")
	}

	fmt.Printf("Orbit type: %s\n", orbitType)
	fmt.Printf("SAFE count: %d\n", len(items))

	client := buildOrbitClient(auth)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	fmt.Println("Fetching orbit file list from ASF...")
	orbitFiles, err := fetchASFOrbitList(ctx, client, baseURL, auth)
	if err != nil {
		return fmt.Errorf("fetch orbit list: %w", err)
	}
	fmt.Printf("  Found %d %s files on ASF\n", len(orbitFiles), orbitType)

	// Deduplicate by (sat, date) — same orbit covers multiple scenes
	uniqueDates := make(map[string][]string)
	for _, item := range items {
		key := item.sat + "|" + item.date.Format("2006-01-02")
		uniqueDates[key] = append(uniqueDates[key], item.name)
	}

	fmt.Printf("Matching orbits for %d unique (sat, date) combinations...\n", len(uniqueDates))

	type orbitTask struct {
		url       string
		orbitName string
	}

	tasks := make(chan orbitTask, maxWorkers*2)
	type orbitResult struct {
		err     error
		skipped bool
	}
	results := make(chan orbitResult, maxWorkers*2)

	var wg sync.WaitGroup
	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range tasks {
				outPath := filepath.Join(orbitDir, t.orbitName)
				if _, err := os.Stat(outPath); err == nil {
					fmt.Printf("  [skip] %s already exists\n", t.orbitName)
					results <- orbitResult{skipped: true}
					continue
				}
				fmt.Printf("  Downloading %s...\n", t.orbitName)
				dlCtx, dlCancel := context.WithTimeout(context.Background(), 30*time.Minute)
				err := downloadOrbit(dlCtx, client, t.url, auth, outPath, maxRetries)
				dlCancel()
				results <- orbitResult{err: err}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	downloaded := 0
	skipped := 0
	failed := 0

	for key, scenes := range uniqueDates {
		parts := strings.SplitN(key, "|", 2)
		sat := parts[0]
		date, _ := time.Parse("2006-01-02", parts[1])

		fmt.Printf("  %s %s (%d scenes)\n", sat, date.Format("2006-01-02"), len(scenes))

		orbitName := findMatchingOrbit(orbitFiles, sat, date, orbitType)
		orbitURL := baseURL + orbitName

		if orbitName == "" {
			// Fallback to other orbit type
			fallbackType := "RESORB"
			fallbackURL := asfRESORBURL
			if orbitType == "RESORB" {
				fallbackType = "POEORB"
				fallbackURL = asfPOEORBURL
			}
			fmt.Printf("    No %s match, trying %s...\n", orbitType, fallbackType)
			fbCtx, fbCancel := context.WithTimeout(context.Background(), 5*time.Minute)
			fbFiles, err := fetchASFOrbitList(fbCtx, client, fallbackURL, auth)
			fbCancel()
			if err != nil {
				fmt.Fprintf(os.Stderr, "    Failed to fetch %s list: %v\n", fallbackType, err)
			} else {
				orbitName = findMatchingOrbit(fbFiles, sat, date, fallbackType)
				if orbitName != "" {
					orbitURL = fallbackURL + orbitName
				}
			}
		}

		if orbitName == "" {
			fmt.Printf("    No orbit found!\n")
			failed++
			continue
		}

		tasks <- orbitTask{url: orbitURL, orbitName: orbitName}
	}
	close(tasks)

	for r := range results {
		if r.err != nil {
			failed++
		} else if r.skipped {
			skipped++
		} else {
			downloaded++
		}
	}

	fmt.Printf("\nResults: %d downloaded, %d skipped, %d failed\n", downloaded, skipped, failed)
	fmt.Printf("Orbit files in: %s\n", orbitDir)

	if failed > 0 {
		return fmt.Errorf("%d orbit downloads failed", failed)
	}
	return nil
}
