package telemetry

// Tests in this file override package-level function variables
// (configDirFn, detectRepoFn) via defer-restore. They must NOT use
// t.Parallel() — concurrent writes to these globals would race.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func resetFirstRun() {
	firstRunOnce = sync.Once{}
	firstRun.Store(false)
}

func TestRepoID_Deterministic(t *testing.T) {
	a := repoID("owner/repo")
	b := repoID("owner/repo")
	if a != b {
		t.Fatalf("same input should produce same ID: %s vs %s", a, b)
	}
	if len(a) != 36 {
		t.Fatalf("expected UUID length 36, got %d: %s", len(a), a)
	}
	// Version nibble (position 14) must be '5'.
	if a[14] != '5' {
		t.Fatalf("expected version nibble '5', got '%c'", a[14])
	}
	// Variant nibble (position 19) must be 8, 9, a, or b.
	v := a[19]
	if v != '8' && v != '9' && v != 'a' && v != 'b' {
		t.Fatalf("expected variant nibble in [89ab], got '%c'", v)
	}
}

func TestRepoID_DifferentRepos(t *testing.T) {
	a := repoID("owner/repo-a")
	b := repoID("owner/repo-b")
	if a == b {
		t.Fatal("different repos should produce different IDs")
	}
}

func TestRepoID_Empty(t *testing.T) {
	if id := repoID(""); id != "" {
		t.Fatalf("expected empty string for empty repo, got %q", id)
	}
}

func TestInitFirstRun(t *testing.T) {
	resetFirstRun()
	dir := t.TempDir()
	configDirFn = func() (string, error) { return dir, nil }
	defer func() { configDirFn = configDir }()

	firstRunOnce.Do(initFirstRun)
	if !firstRun.Load() {
		t.Fatal("expected firstRun to be true on fresh directory")
	}
	// Marker file should exist.
	if _, err := os.Stat(filepath.Join(dir, firstRunMarker)); err != nil {
		t.Fatalf("marker file should exist: %v", err)
	}

	// Reset and call again — marker exists, so firstRun should stay false.
	resetFirstRun()
	firstRunOnce.Do(initFirstRun)
	if firstRun.Load() {
		t.Fatal("expected firstRun to be false when marker already exists")
	}
}

func TestEnabled_Default(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CODECANARY_NO_TELEMETRY", "")
	if !Enabled() {
		t.Error("expected enabled by default")
	}
}

func TestEnabled_DO_NOT_TRACK(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "1")
	if Enabled() {
		t.Error("expected disabled with DO_NOT_TRACK=1")
	}
}

func TestEnabled_CODECANARY_NO_TELEMETRY(t *testing.T) {
	t.Setenv("CODECANARY_NO_TELEMETRY", "1")
	if Enabled() {
		t.Error("expected disabled with CODECANARY_NO_TELEMETRY=1")
	}
}

func TestSendReview_Payload(t *testing.T) {
	resetFirstRun()
	dir := t.TempDir()
	configDirFn = func() (string, error) { return dir, nil }
	defer func() { configDirFn = configDir }()

	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CODECANARY_NO_TELEMETRY", "")

	done := make(chan []byte, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		done <- body
		w.WriteHeader(200)
	}))
	defer ts.Close()

	origEndpoint := Endpoint
	Endpoint = ts.URL
	defer func() { Endpoint = origEndpoint }()

	SendReview(ReviewEvent{
		Repo:        "owner/repo",
		Version:     "1.2.3",
		Provider:    "anthropic",
		Platform:    "local",
		NewFindings: 5,
		BySeverity:  map[string]int{"bug": 3, "warning": 2},
	})

	select {
	case body := <-done:
		var ev ReviewEvent
		if err := json.Unmarshal(body, &ev); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if ev.Event != "review_completed" {
			t.Errorf("event = %q, want review_completed", ev.Event)
		}
		expectedID := repoID("owner/repo")
		if ev.InstallationID != expectedID {
			t.Errorf("installation_id = %q, want %q", ev.InstallationID, expectedID)
		}
		if ev.Version != "1.2.3" {
			t.Errorf("version = %q, want 1.2.3", ev.Version)
		}
		if ev.Provider != "anthropic" {
			t.Errorf("provider = %q, want anthropic", ev.Provider)
		}
		if ev.NewFindings != 5 {
			t.Errorf("new_findings = %d, want 5", ev.NewFindings)
		}
		if ev.OS == "" || ev.Arch == "" {
			t.Error("expected OS and Arch to be populated")
		}
		if ev.Timestamp == "" {
			t.Error("expected timestamp")
		}
		// Repo must NOT appear in the JSON payload.
		var raw map[string]any
		if err := json.Unmarshal(body, &raw); err != nil {
			t.Fatalf("unmarshal raw: %v", err)
		}
		if _, ok := raw["repo"]; ok {
			t.Error("repo field must not be serialized in the payload")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for telemetry POST")
	}
}

func TestSendReview_OptedOut(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "1")

	called := make(chan struct{}, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called <- struct{}{}
		w.WriteHeader(200)
	}))
	defer ts.Close()

	origEndpoint := Endpoint
	Endpoint = ts.URL
	defer func() { Endpoint = origEndpoint }()

	SendReview(ReviewEvent{Repo: "owner/repo", Version: "1.0.0"})

	select {
	case <-called:
		t.Error("telemetry was sent despite opt-out")
	case <-time.After(200 * time.Millisecond):
		// Good — no request was made.
	}
}

func TestSendReview_EmptyRepo(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CODECANARY_NO_TELEMETRY", "")

	called := make(chan struct{}, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called <- struct{}{}
		w.WriteHeader(200)
	}))
	defer ts.Close()

	origEndpoint := Endpoint
	Endpoint = ts.URL
	defer func() { Endpoint = origEndpoint }()

	SendReview(ReviewEvent{Version: "1.0.0"})

	select {
	case <-called:
		t.Error("telemetry was sent despite empty repo")
	case <-time.After(200 * time.Millisecond):
		// Good — no request was made.
	}
}

func TestSendSetup_Payload(t *testing.T) {
	resetFirstRun()
	dir := t.TempDir()
	configDirFn = func() (string, error) { return dir, nil }
	defer func() { configDirFn = configDir }()

	detectRepoFn = func() string { return "owner/repo" }
	defer func() { detectRepoFn = detectRepo }()

	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CODECANARY_NO_TELEMETRY", "")

	done := make(chan []byte, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		done <- body
		w.WriteHeader(200)
	}))
	defer ts.Close()

	origEndpoint := Endpoint
	Endpoint = ts.URL
	defer func() { Endpoint = origEndpoint }()

	SendSetup("0.9.0", "openai", "github")

	select {
	case body := <-done:
		var ev SetupEvent
		if err := json.Unmarshal(body, &ev); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if ev.Event != "setup_completed" {
			t.Errorf("event = %q, want setup_completed", ev.Event)
		}
		expectedID := repoID("owner/repo")
		if ev.InstallationID != expectedID {
			t.Errorf("installation_id = %q, want %q", ev.InstallationID, expectedID)
		}
		if ev.Provider != "openai" {
			t.Errorf("provider = %q, want openai", ev.Provider)
		}
		if ev.Platform != "github" {
			t.Errorf("platform = %q, want github", ev.Platform)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for telemetry POST")
	}
}
