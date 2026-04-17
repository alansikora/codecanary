package review

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEmbedBaselineMarkerRoundTrip(t *testing.T) {
	sha := "6971e4164c0a4df9d89aefdb874174a56df420d4"
	marker := embedBaselineMarker(sha)

	prefix := reviewMarkerPrefixes[0]
	idx := strings.Index(marker, prefix)
	if idx < 0 {
		t.Fatalf("marker missing expected prefix %q: %q", prefix, marker)
	}
	start := idx + len(prefix)
	endIdx := strings.Index(marker[start:], reviewMarkerSuffix)
	if endIdx < 0 {
		t.Fatalf("marker missing expected suffix: %q", marker)
	}

	// FetchPreviousReviewSHA unmarshals into ReviewResult, so the minimal
	// {sha} payload must still populate ReviewResult.SHA correctly.
	var result ReviewResult
	if err := json.Unmarshal([]byte(marker[start:start+endIdx]), &result); err != nil {
		t.Fatalf("marker JSON does not round-trip: %v (raw=%q)", err, marker[start:start+endIdx])
	}
	if result.SHA != sha {
		t.Errorf("SHA = %q, want %q", result.SHA, sha)
	}
}

func TestEmbedBaselineMarkerEmptySHA(t *testing.T) {
	if got := embedBaselineMarker(""); got != "" {
		t.Errorf("expected empty marker for empty SHA, got %q", got)
	}
}

// TestEmbedBaselineMarkerFormat guards the full rendered snippet against
// accidental extra whitespace regressions. FormatReviewBody uses a single
// leading and trailing newline around the marker; baseline-marker bodies
// must match that convention so the rendered PR comment doesn't drift.
func TestEmbedBaselineMarkerFormat(t *testing.T) {
	got := embedBaselineMarker("abc123")
	want := "\n<!-- codecanary:review {\"sha\":\"abc123\"} -->\n"
	if got != want {
		t.Errorf("marker = %q, want %q", got, want)
	}
}
