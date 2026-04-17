package review

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEmbedBaselineMarkerRoundTrip(t *testing.T) {
	sha := "6971e4164c0a4df9d89aefdb874174a56df420d4"
	marker := embedBaselineMarker("owner/repo", 1502, sha)

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

	var result ReviewResult
	if err := json.Unmarshal([]byte(marker[start:start+endIdx]), &result); err != nil {
		t.Fatalf("marker JSON does not round-trip: %v (raw=%q)", err, marker[start:start+endIdx])
	}
	if result.SHA != sha {
		t.Errorf("SHA = %q, want %q", result.SHA, sha)
	}
	if result.PRNumber != 1502 {
		t.Errorf("PRNumber = %d, want 1502", result.PRNumber)
	}
	if result.Repo != "owner/repo" {
		t.Errorf("Repo = %q, want owner/repo", result.Repo)
	}
}

func TestEmbedBaselineMarkerEmptySHA(t *testing.T) {
	if got := embedBaselineMarker("owner/repo", 1, ""); got != "" {
		t.Errorf("expected empty marker for empty SHA, got %q", got)
	}
}
