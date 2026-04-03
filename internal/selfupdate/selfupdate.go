package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	repo             = "alansikora/codecanary"
	apiBase          = "https://api.github.com"
	cacheTTL         = 24 * time.Hour
	checkTimeout     = 3 * time.Second
	upgradeTimeout   = 5 * time.Minute
	maxDownloadBytes = 256 << 20 // 256 MB
	cacheFile        = "version-check.json"
)

// Release represents a GitHub release.
type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

// Asset represents a release asset.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// versionCache stores the result of a version check.
type versionCache struct {
	LatestVersion string    `json:"latest_version"`
	CheckedAt     time.Time `json:"checked_at"`
}

// fetchRelease fetches a release from GitHub by tag ("latest" or a specific tag).
func fetchRelease(ctx context.Context, tag string) (*Release, error) {
	var url string
	if tag == "" || tag == "latest" {
		url = fmt.Sprintf("%s/repos/%s/releases/latest", apiBase, repo)
	} else {
		url = fmt.Sprintf("%s/repos/%s/releases/tags/%s", apiBase, repo, tag)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// IsNewer returns true if latest is a newer semver than current.
// Both should be in "vX.Y.Z" or "X.Y.Z" format.
func IsNewer(current, latest string) bool {
	cur := parseSemver(current)
	lat := parseSemver(latest)
	if cur == nil || lat == nil {
		return false
	}
	if lat[0] != cur[0] {
		return lat[0] > cur[0]
	}
	if lat[1] != cur[1] {
		return lat[1] > cur[1]
	}
	return lat[2] > cur[2]
}

func parseSemver(v string) []int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return nil
	}
	nums := make([]int, 3)
	for i, p := range parts {
		if idx := strings.IndexByte(p, '-'); idx >= 0 {
			p = p[:idx]
		}
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				return nil
			}
			n = n*10 + int(c-'0')
		}
		nums[i] = n
	}
	return nums
}

func cacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codecanary"), nil
}

func readCache() (*versionCache, error) {
	dir, err := cacheDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, cacheFile))
	if err != nil {
		return nil, err
	}
	var cache versionCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	return &cache, nil
}

func writeCache(latest string) error {
	dir, err := cacheDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(versionCache{
		LatestVersion: latest,
		CheckedAt:     time.Now(),
	})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, cacheFile), data, 0o644)
}

// pending tracks background cache refresh goroutines.
var pending sync.WaitGroup

// CheckCached returns the latest version using a 24h cache.
// On cache hit, returns immediately. On cache miss, starts a background
// refresh (result available next run) and returns the stale value if any.
// Call Wait() before process exit to let the refresh complete.
func CheckCached(currentVersion string) (latest string, hasUpdate bool) {
	if currentVersion == "dev" || currentVersion == "" {
		return "", false
	}

	cache, err := readCache()
	if err == nil && time.Since(cache.CheckedAt) < cacheTTL {
		return cache.LatestVersion, IsNewer(currentVersion, cache.LatestVersion)
	}

	// Cache stale or missing — refresh in background so we never block.
	pending.Add(1)
	go func() {
		defer pending.Done()
		ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
		defer cancel()
		if rel, err := fetchRelease(ctx, "latest"); err == nil {
			_ = writeCache(rel.TagName)
		}
	}()

	// Return stale cache value if available.
	if cache != nil {
		return cache.LatestVersion, IsNewer(currentVersion, cache.LatestVersion)
	}
	return "", false
}

// Wait blocks until any pending background cache refresh completes.
// Call this before process exit.
func Wait() {
	pending.Wait()
}

// Upgrade downloads and installs the specified release (or latest).
// tag should be "" for latest, "canary" for canary, or a specific version tag.
func Upgrade(ctx context.Context, currentVersion, tag string, w io.Writer) error {
	if tag == "" {
		tag = "latest"
	}

	_, _ = fmt.Fprintf(w, "Checking for updates...\n")

	apiCtx, apiCancel := context.WithTimeout(ctx, 30*time.Second)
	defer apiCancel()

	rel, err := fetchRelease(apiCtx, tag)
	if err != nil {
		return fmt.Errorf("fetching release: %w", err)
	}

	if tag == "latest" && currentVersion != "dev" && !IsNewer(currentVersion, rel.TagName) {
		_, _ = fmt.Fprintf(w, "Already up to date (%s).\n", currentVersion)
		return nil
	}

	osName := runtime.GOOS
	arch := runtime.GOARCH
	suffix := fmt.Sprintf("_%s_%s.tar.gz", osName, arch)

	var asset *Asset
	for i := range rel.Assets {
		if strings.HasSuffix(rel.Assets[i].Name, suffix) {
			asset = &rel.Assets[i]
			break
		}
	}
	if asset == nil {
		return fmt.Errorf("no release asset found for %s/%s in %s", osName, arch, rel.TagName)
	}

	// Find checksums asset — required for integrity verification.
	var checksumsAsset *Asset
	for i := range rel.Assets {
		if rel.Assets[i].Name == "checksums.txt" {
			checksumsAsset = &rel.Assets[i]
			break
		}
	}
	if checksumsAsset == nil {
		return fmt.Errorf("release %s is missing checksums.txt — refusing to install unverified binary", rel.TagName)
	}

	_, _ = fmt.Fprintf(w, "Downloading codecanary %s for %s/%s...\n", rel.TagName, osName, arch)

	// Each download gets its own timeout so a slow archive download
	// cannot starve the checksums fetch.
	dlCtx, dlCancel := context.WithTimeout(ctx, upgradeTimeout)
	defer dlCancel()

	archiveData, err := downloadAsset(dlCtx, asset.BrowserDownloadURL)
	if err != nil {
		return fmt.Errorf("downloading archive: %w", err)
	}

	csCtx, csCancel := context.WithTimeout(ctx, 30*time.Second)
	defer csCancel()

	checksumsData, err := downloadAsset(csCtx, checksumsAsset.BrowserDownloadURL)
	if err != nil {
		return fmt.Errorf("downloading checksums: %w", err)
	}
	if err := verifyChecksum(archiveData, asset.Name, checksumsData); err != nil {
		return fmt.Errorf("checksum verification failed: %w", err)
	}

	// Extract binary from tarball.
	binary, err := extractBinary(archiveData)
	if err != nil {
		return fmt.Errorf("extracting binary: %w", err)
	}

	// Replace current binary.
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding current binary: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolving binary path: %w", err)
	}

	if err := replaceBinary(execPath, binary); err != nil {
		return fmt.Errorf("replacing binary: %w", err)
	}

	// macOS: ad-hoc re-sign.
	if runtime.GOOS == "darwin" {
		_ = exec.Command("codesign", "--force", "--sign", "-", execPath).Run()
	}

	// Update cache.
	if tag == "latest" {
		_ = writeCache(rel.TagName)
	}

	_, _ = fmt.Fprintf(w, "Upgraded to codecanary %s.\n", rel.TagName)
	return nil
}

func downloadAsset(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes))
}

func verifyChecksum(archiveData []byte, assetName string, checksumsData []byte) error {
	hash := sha256.Sum256(archiveData)
	got := hex.EncodeToString(hash[:])

	for _, line := range strings.Split(string(checksumsData), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == assetName {
			if fields[0] == got {
				return nil
			}
			return fmt.Errorf("expected %s, got %s", fields[0], got)
		}
	}
	return fmt.Errorf("asset %s not found in checksums", assetName)
}

func extractBinary(archiveData []byte) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(archiveData))
	if err != nil {
		return nil, fmt.Errorf("opening gzip: %w", err)
	}
	defer func() { _ = gr.Close() }()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// Validate the entry name: must be a clean relative path with no traversal.
		clean := filepath.Clean(hdr.Name)
		if strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "..") {
			continue
		}
		if filepath.Base(clean) == "codecanary" {
			return io.ReadAll(io.LimitReader(tr, maxDownloadBytes))
		}
	}
	return nil, fmt.Errorf("codecanary binary not found in archive")
}

func replaceBinary(path string, newBinary []byte) error {
	dir := filepath.Dir(path)

	// Write to temp file in the same directory for atomic rename.
	tmp, err := os.CreateTemp(dir, "codecanary-upgrade-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}

	if _, err := tmp.Write(newBinary); err != nil {
		cleanup()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		cleanup()
		return fmt.Errorf("setting permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	// Atomic rename.
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replacing binary: %w", err)
	}
	return nil
}
