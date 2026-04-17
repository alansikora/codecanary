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

func TestBuildCleanReviewBody(t *testing.T) {
	got := buildCleanReviewBody("abc123", ReviewSummary{})
	want := "CodeCanary reviewed this PR \u2014 no issues found.\n<!-- codecanary:review {\"sha\":\"abc123\"} -->\n"
	if got != want {
		t.Errorf("body =\n%q\nwant\n%q", got, want)
	}
}

func TestBuildCleanReviewBodyNoSHA(t *testing.T) {
	got := buildCleanReviewBody("", ReviewSummary{})
	want := "CodeCanary reviewed this PR \u2014 no issues found."
	if got != want {
		t.Errorf("body =\n%q\nwant\n%q", got, want)
	}
}

func TestBuildCleanReviewBodyWithSummary(t *testing.T) {
	got := buildCleanReviewBody("abc123", ReviewSummary{NewFindings: 2, StillOpen: 1})
	if !strings.Contains(got, statusBlockOpen) || !strings.Contains(got, statusBlockClose) {
		t.Errorf("body missing status markers:\n%q", got)
	}
	if !strings.Contains(got, "New findings: **2**") {
		t.Errorf("body missing new findings count:\n%q", got)
	}
	if !strings.Contains(got, "Still unresolved: **1**") {
		t.Errorf("body missing still-unresolved count:\n%q", got)
	}
	if !strings.HasSuffix(got, "\n<!-- codecanary:review {\"sha\":\"abc123\"} -->\n") {
		t.Errorf("body missing trailing baseline marker:\n%q", got)
	}
}

func TestBuildAllClearReviewBody(t *testing.T) {
	got := buildAllClearReviewBody("abc123", false, ReviewSummary{})
	wantSuffix := "\n<!-- codecanary:review {\"sha\":\"abc123\"} -->\n"
	if !strings.HasSuffix(got, wantSuffix) {
		t.Errorf("body missing marker suffix:\n%q", got)
	}
	if strings.Contains(got, "could not be minimized") {
		t.Errorf("unexpected minimize-warning text when minimizeFailed=false:\n%q", got)
	}
	if !strings.Contains(got, "All previous findings have been addressed") {
		t.Errorf("body missing expected all-clear copy:\n%q", got)
	}
}

func TestBuildAllClearReviewBodyMinimizeFailed(t *testing.T) {
	got := buildAllClearReviewBody("abc123", true, ReviewSummary{})
	if !strings.Contains(got, "could not be minimized") {
		t.Errorf("expected minimize-warning text, got:\n%q", got)
	}
	wantSuffix := "\n<!-- codecanary:review {\"sha\":\"abc123\"} -->\n"
	if !strings.HasSuffix(got, wantSuffix) {
		t.Errorf("body missing marker suffix:\n%q", got)
	}
}

func TestBuildActivityReviewBody(t *testing.T) {
	got := buildActivityReviewBody("abc123", ReviewSummary{Dismissed: 1, Rebutted: 2, StillOpen: 3})
	if !strings.Contains(got, "no new issues found") {
		t.Errorf("body missing activity copy:\n%q", got)
	}
	if !strings.Contains(got, "Dismissed by author: **1**") {
		t.Errorf("body missing dismissed count:\n%q", got)
	}
	if !strings.Contains(got, "Rebutted by author: **2**") {
		t.Errorf("body missing rebutted count:\n%q", got)
	}
	if !strings.HasSuffix(got, "\n<!-- codecanary:review {\"sha\":\"abc123\"} -->\n") {
		t.Errorf("body missing trailing baseline marker:\n%q", got)
	}
}
