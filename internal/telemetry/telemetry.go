// Package telemetry collects anonymous, non-PII usage data for CodeCanary.
//
// What is collected (every field is documented on the event structs):
//   - A random installation UUID (not tied to any user, repo, or org)
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
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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

// configDirFn returns the path to ~/.codecanary/. Tests can override this.
var configDirFn = configDir

// ---------- Installation ID ----------

const idFileName = "installation_id"

var uuidV4Re = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

var (
	installOnce sync.Once
	installID   string
)

// getOrCreateID returns a persistent anonymous UUID.
// It reads from ~/.codecanary/installation_id or generates a new one.
// Returns "" if anything goes wrong (errors are swallowed).
func getOrCreateID() string {
	installOnce.Do(func() {
		dir, err := configDirFn()
		if err != nil {
			return
		}
		path := filepath.Join(dir, idFileName)

		if data, err := os.ReadFile(path); err == nil {
			id := strings.TrimSpace(string(data))
			if uuidV4Re.MatchString(id) {
				installID = id
				return
			}
		}

		id, err := newUUIDv4()
		if err != nil {
			return
		}
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(path, []byte(id+"\n"), 0o600)
		fmt.Fprintf(os.Stderr, "Anonymous telemetry is on. Opt out: CODECANARY_NO_TELEMETRY=1\n")
		installID = id
	})
	return installID
}

func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codecanary"), nil
}

// newUUIDv4 generates a RFC 4122 version-4 UUID using crypto/rand.
func newUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// ---------- Opt-out ----------

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

// SendReview fires a review_completed event in a background goroutine.
// Never blocks. Silently swallows all errors.
func SendReview(e ReviewEvent) {
	if !Enabled() {
		return
	}
	e.InstallationID = getOrCreateID()
	if e.InstallationID == "" {
		return
	}
	e.Event = "review_completed"
	e.OS = runtime.GOOS
	e.Arch = runtime.GOARCH
	e.Timestamp = time.Now().UTC().Format(time.RFC3339)
	pending.Add(1)
	go func() {
		defer pending.Done()
		send(e)
	}()
}

// SendSetup fires a setup_completed event in a background goroutine.
func SendSetup(version, provider, platform string) {
	if !Enabled() {
		return
	}
	id := getOrCreateID()
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
	pending.Add(1)
	go func() {
		defer pending.Done()
		send(e)
	}()
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
