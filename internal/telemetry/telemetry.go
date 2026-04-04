// Package telemetry collects anonymous, non-PII usage data for CodeCanary.
//
// What is collected (every field is documented on the event structs):
//   - A deterministic UUID derived from a SHA-256 hash of the repo name
//   - Event type, binary version, OS, architecture
//   - LLM provider name and platform (github/local)
//   - Aggregate counts: findings, tokens, cost, duration
//
// What is NEVER collected:
//   - Repository names, org names, or usernames
//   - Code, diffs, file paths, or review text
//   - API keys, tokens, or credentials
//
// Opt-out: set DO_NOT_TRACK=1 or CODECANARY_NO_TELEMETRY=1.
// All sends are fire-and-forget in a background goroutine with a 2 s timeout.
package telemetry

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Endpoint is the URL telemetry events are POSTed to.
// Exported so tests can point it at a local server.
var Endpoint = "https://telemetry.codecanary.sh/telemetry"

// sendTimeout caps the HTTP POST duration.
var sendTimeout = 2 * time.Second

// pending tracks in-flight sends so Wait() can block until they complete.
var pending sync.WaitGroup

// ---------- Repository-based ID ----------

const firstRunMarker = ".telemetry_seen"

// configDirFn returns the path to ~/.codecanary/. Tests can override this.
var configDirFn = configDir

// repoID derives a deterministic, anonymous ID from a repository name
// (e.g. "owner/repo"). The ID is a SHA-256 hash formatted as a UUID-like
// string, so the same repo always produces the same ID regardless of
// which machine or CI runner is used.
// Returns "" if repo is empty.
func repoID(repo string) string {
	if repo == "" {
		return ""
	}
	h := sha256.Sum256([]byte(repo))
	// Format first 16 bytes as UUID (version 5-style, SHA-based).
	b := h[:16]
	b[6] = (b[6] & 0x0f) | 0x50 // version 5
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// detectRepoFn resolves the current repo name. Tests can override this.
var detectRepoFn = detectRepo

// detectRepo returns the "owner/repo" for the current working directory
// by shelling out to gh. Returns "" on any failure.
func detectRepo() string {
	out, err := exec.Command("gh", "repo", "view",
		"--json", "nameWithOwner",
		"--jq", ".nameWithOwner",
	).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codecanary"), nil
}

var firstRun bool

// ---------- First-run detection ----------

// checkFirstRun returns true if this is the first telemetry send on
// this machine. It creates a marker file (~/.codecanary/.telemetry_seen)
// on first call and returns false on subsequent runs.
func checkFirstRun() bool {
	dir, err := configDirFn()
	if err != nil {
		return false
	}
	path := filepath.Join(dir, firstRunMarker)
	if _, err := os.Stat(path); err == nil {
		return false
	}
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(path, []byte("1\n"), 0o600)
	return true
}

// ---------- Opt-out ----------

// IsFirstRun reports whether the current process just created the
// first-run marker (i.e. this is the very first run on this machine).
// Must be called after SendSetup or SendReview to get a meaningful result.
func IsFirstRun() bool {
	return firstRun
}

// Enabled returns false when the user has opted out via environment variables.
func Enabled() bool {
	if os.Getenv("DO_NOT_TRACK") == "1" {
		return false
	}
	if os.Getenv("CODECANARY_NO_TELEMETRY") == "1" {
		return false
	}
	return true
}

// ---------- Event types ----------

// ReviewEvent is sent after a review completes.
type ReviewEvent struct {
	InstallationID string `json:"installation_id"`
	Event          string `json:"event"`

	// Repo is the owner/repo identifier used to derive InstallationID.
	// It is cleared before the event is sent (never transmitted).
	Repo string `json:"-"`

	// Binary metadata.
	Version string `json:"version"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`

	// Review context (no PII).
	Provider      string `json:"provider"`
	Platform      string `json:"platform"`
	IsIncremental bool   `json:"is_incremental"`

	// Aggregate finding counts.
	NewFindings      int            `json:"new_findings"`
	StillOpenFindings int           `json:"still_open_findings"`
	BySeverity       map[string]int `json:"by_severity"`

	// Aggregate token usage.
	InputTokens     int     `json:"input_tokens"`
	OutputTokens    int     `json:"output_tokens"`
	CacheReadTokens int     `json:"cache_read_tokens"`
	CostUSD         float64 `json:"cost_usd"`
	DurationMS      int64   `json:"duration_ms"`

	Timestamp string `json:"timestamp"`
}

// SetupEvent is sent after a setup flow completes.
type SetupEvent struct {
	InstallationID string `json:"installation_id"`
	Event          string `json:"event"`
	Version        string `json:"version"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	Provider       string `json:"provider"`
	Platform       string `json:"platform"` // "local" or "github"
	Timestamp      string `json:"timestamp"`
}

// ---------- Public API ----------

// resolveID returns a repo-based installation ID.
// If repo is empty, it falls back to auto-detection via gh.
// Returns "" if the repo cannot be determined.
func resolveID(repo string) string {
	if repo == "" {
		repo = detectRepoFn()
	}
	return repoID(repo)
}

// fireAndForget sends a telemetry payload in a background goroutine,
// checks the first-run marker, and tracks the in-flight send.
func fireAndForget(payload any) {
	firstRun = checkFirstRun()
	pending.Add(1)
	go func() {
		defer pending.Done()
		send(payload)
	}()
}

// SendReview fires a review_completed event in a background goroutine.
// Never blocks. Silently swallows all errors.
func SendReview(e ReviewEvent) {
	if !Enabled() {
		return
	}
	e.InstallationID = resolveID(e.Repo)
	if e.InstallationID == "" {
		return
	}
	e.Event = "review_completed"
	e.OS = runtime.GOOS
	e.Arch = runtime.GOARCH
	e.Timestamp = time.Now().UTC().Format(time.RFC3339)
	fireAndForget(e)
}

// SendSetup fires a setup_completed event in a background goroutine.
func SendSetup(version, provider, platform string) {
	if !Enabled() {
		return
	}
	id := resolveID("")
	if id == "" {
		return
	}
	e := SetupEvent{
		InstallationID: id,
		Event:          "setup_completed",
		Version:        version,
		OS:             runtime.GOOS,
		Arch:           runtime.GOARCH,
		Provider:       provider,
		Platform:       platform,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
	}
	fireAndForget(e)
}

// Wait blocks until all in-flight telemetry sends complete, with a hard
// ceiling of 5 s to guarantee the process exits even if a send hangs.
func Wait() {
	done := make(chan struct{})
	go func() {
		pending.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

// ---------- Internal ----------

func send(payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	client := &http.Client{Timeout: sendTimeout}
	resp, err := client.Post(Endpoint, "application/json", bytes.NewReader(data))
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}
