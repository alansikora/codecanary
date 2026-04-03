package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	repo     = "alansikora/codecanary"
	apiBase  = "https://api.github.com/repos/" + repo
	cacheTTL = 24 * time.Hour
)

type release struct {
	TagName    string  `json:"tag_name"`
	Prerelease bool    `json:"prerelease"`
	Assets     []asset `json:"assets"`
}

type asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type versionCache struct {
	LatestVersion string    `json:"latest_version"`
	CheckedAt     time.Time `json:"checked_at"`
}

// CheckLatest fetches the latest release tag from GitHub.
// If canary is true, returns the latest prerelease tag instead.
func CheckLatest(ctx context.Context, canary bool) (string, error) {
	if canary {
		return checkLatestPrerelease(ctx)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", apiBase+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github api: %s", resp.Status)
	}
	var r release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	return r.TagName, nil
}

func checkLatestPrerelease(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", apiBase+"/releases?per_page=10", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github api: %s", resp.Status)
	}
	var releases []release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", err
	}
	for _, r := range releases {
		if r.Prerelease {
			return r.TagName, nil
		}
	}
	return "", fmt.Errorf("no prerelease found")
}

// IsNewer returns true if latest is a newer semver than current.
func IsNewer(current, latest string) bool {
	cur := parseSemver(current)
	lat := parseSemver(latest)
	if cur == nil || lat == nil {
		return false
	}
	for i := 0; i < 3; i++ {
		if lat[i] > cur[i] {
			return true
		}
		if lat[i] < cur[i] {
			return false
		}
	}
	return false
}

func parseSemver(v string) []int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return nil
	}
	// Strip pre-release suffix from patch (e.g., "3-rc1").
	parts[2] = strings.SplitN(parts[2], "-", 2)[0]
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		nums[i] = n
	}
	return nums
}

// Update checks for the latest release and installs it if newer than currentVersion.
// Returns the latest version tag. If already up to date, returns the tag without error.
func Update(ctx context.Context, currentVersion string, canary bool) (string, error) {
	latest, err := CheckLatest(ctx, canary)
	if err != nil {
		return "", fmt.Errorf("checking latest version: %w", err)
	}

	if currentVersion == latest || (!canary && !IsNewer(currentVersion, latest)) {
		return latest, nil
	}

	if err := install(ctx, latest); err != nil {
		return "", err
	}
	return latest, nil
}

func install(ctx context.Context, tag string) error {
	rel, err := fetchRelease(ctx, tag)
	if err != nil {
		return fmt.Errorf("fetching release %s: %w", tag, err)
	}

	archiveAsset, checksumAsset := findAssets(rel)
	if archiveAsset == nil {
		return fmt.Errorf("no asset for %s/%s in release %s", runtime.GOOS, runtime.GOARCH, tag)
	}

	tmpDir, err := os.MkdirTemp("", "codecanary-update-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	archivePath := filepath.Join(tmpDir, archiveAsset.Name)
	if err := download(ctx, archiveAsset.BrowserDownloadURL, archivePath); err != nil {
		return fmt.Errorf("downloading: %w", err)
	}

	if checksumAsset != nil {
		checksumPath := filepath.Join(tmpDir, "checksums.txt")
		if err := download(ctx, checksumAsset.BrowserDownloadURL, checksumPath); err != nil {
			return fmt.Errorf("downloading checksums: %w", err)
		}
		if err := verifyChecksum(archivePath, checksumPath, archiveAsset.Name); err != nil {
			return fmt.Errorf("checksum mismatch: %w", err)
		}
	}

	binaryPath := filepath.Join(tmpDir, "codecanary")
	if err := extractBinary(archivePath, binaryPath); err != nil {
		return fmt.Errorf("extracting: %w", err)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating current binary: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolving binary path: %w", err)
	}

	if err := replaceBinary(binaryPath, exe); err != nil {
		return fmt.Errorf("replacing binary at %s: %w", exe, err)
	}

	_ = writeCache(versionCache{LatestVersion: tag, CheckedAt: time.Now()})
	return nil
}

// CachedLatestVersion returns the latest known version, refreshing the cache if stale.
// Uses a short timeout to avoid blocking CLI commands.
func CachedLatestVersion() string {
	c := readCache()
	if c != nil && time.Since(c.CheckedAt) < cacheTTL {
		return c.LatestVersion
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	latest, err := CheckLatest(ctx, false)
	if err != nil {
		if c != nil {
			return c.LatestVersion
		}
		return ""
	}
	_ = writeCache(versionCache{LatestVersion: latest, CheckedAt: time.Now()})
	return latest
}

func fetchRelease(ctx context.Context, tag string) (*release, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", apiBase+"/releases/tags/"+tag, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api: %s", resp.Status)
	}
	var r release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

func findAssets(rel *release) (archive *asset, checksum *asset) {
	suffix := fmt.Sprintf("_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	for i := range rel.Assets {
		if strings.HasSuffix(rel.Assets[i].Name, suffix) {
			archive = &rel.Assets[i]
		}
		if rel.Assets[i].Name == "checksums.txt" {
			checksum = &rel.Assets[i]
		}
	}
	return
}

func download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d: %s", resp.StatusCode, url)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(f, resp.Body)
	return err
}

func verifyChecksum(archivePath, checksumPath, archiveName string) error {
	data, err := os.ReadFile(checksumPath)
	if err != nil {
		return err
	}
	var expected string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, archiveName) {
			fields := strings.Fields(line)
			if len(fields) >= 1 {
				expected = fields[0]
				break
			}
		}
	}
	if expected == "" {
		return fmt.Errorf("no checksum for %s", archiveName)
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expected {
		return fmt.Errorf("expected %s, got %s", expected, actual)
	}
	return nil
}

func extractBinary(archivePath, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if filepath.Base(hdr.Name) == "codecanary" && hdr.Typeflag == tar.TypeReg {
			out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return err
			}
			return out.Close()
		}
	}
	return fmt.Errorf("binary not found in archive")
}

func replaceBinary(newPath, currentPath string) error {
	dir := filepath.Dir(currentPath)
	tmp, err := os.CreateTemp(dir, "codecanary-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()

	src, err := os.Open(newPath)
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if _, err := io.Copy(tmp, src); err != nil {
		_ = src.Close()
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	_ = src.Close()
	_ = tmp.Close()

	if err := os.Chmod(tmpPath, 0755); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, currentPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func cacheFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codecanary", "version-check.json")
}

func readCache() *versionCache {
	path := cacheFilePath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var c versionCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil
	}
	return &c
}

func writeCache(c versionCache) error {
	path := cacheFilePath()
	if path == "" {
		return fmt.Errorf("cannot determine home directory")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
