package telemetry

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

func resetInstallID() {
	installOnce = sync.Once{}
	installID = ""
}

func TestNewUUIDv4_Format(t *testing.T) {
	id, err := newUUIDv4()
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 36 {
		t.Fatalf("expected length 36, got %d: %s", len(id), id)
	}
	// Version nibble (position 14) must be '4'.
	if id[14] != '4' {
		t.Fatalf("expected version nibble '4', got '%c'", id[14])
	}
	// Variant nibble (position 19) must be 8, 9, a, or b.
	v := id[19]
	if v != '8' && v != '9' && v != 'a' && v != 'b' {
		t.Fatalf("expected variant nibble in [89ab], got '%c'", v)
	}
}

func TestNewUUIDv4_Unique(t *testing.T) {
	a, _ := newUUIDv4()
	b, _ := newUUIDv4()
	if a == b {
		t.Fatal("two UUIDs should not be equal")
	}
}

func TestGetOrCreateID_Persists(t *testing.T) {
	resetInstallID()
	dir := t.TempDir()
	configDirFn = func() (string, error) { return dir, nil }
	defer func() { configDirFn = configDir }()

	id1 := getOrCreateID()
	if id1 == "" {
		t.Fatal("expected non-empty ID")
	}
	if len(id1) != 36 {
		t.Fatalf("expected UUID length 36, got %d", len(id1))
	}

	// Verify it was written to disk.
	data, err := os.ReadFile(filepath.Join(dir, idFileName))
	if err != nil {
		t.Fatalf("reading ID file: %v", err)
	}
	if got := string(data); got != id1+"\n" {
		t.Fatalf("file content mismatch: %q vs %q", got, id1+"\n")
	}

	// Reset in-memory cache, re-read from file.
	resetInstallID()
	id2 := getOrCreateID()
	if id2 != id1 {
		t.Fatalf("ID changed across restarts: %s vs %s", id1, id2)
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
	resetInstallID()
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
		if ev.InstallationID == "" {
			t.Error("expected non-empty installation_id")
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

	SendReview(ReviewEvent{Version: "1.0.0"})

	select {
	case <-called:
		t.Error("telemetry was sent despite opt-out")
	case <-time.After(200 * time.Millisecond):
		// Good — no request was made.
	}
}

func TestSendSetup_Payload(t *testing.T) {
	resetInstallID()
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
