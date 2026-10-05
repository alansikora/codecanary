package review

import "testing"

const lateIncrementalDiff = `diff --git a/lib/lock.rb b/lib/lock.rb
--- a/lib/lock.rb
+++ b/lib/lock.rb
@@ -100,2 +100,3 @@ class Lock
   def take
+    @file.rewind
     @file.write(line)
`

func findingIDs(fs []Finding) []string {
	ids := make([]string, len(fs))
	for i, f := range fs {
		ids[i] = f.ID
	}
	return ids
}

func TestFilterLateFindings(t *testing.T) {
	findings := []Finding{
		{ID: "near-new-code", File: "lib/lock.rb", Line: 103, Severity: "suggestion"},
		{ID: "old-code-suggestion", File: "lib/lock.rb", Line: 140, Severity: "suggestion"},
		{ID: "old-code-warning", File: "lib/lock.rb", Line: 20, Severity: "warning"},
		{ID: "old-code-bug", File: "lib/lock.rb", Line: 140, Severity: "bug"},
		{ID: "untouched-file", File: "lib/devices.rb", Line: 96, Severity: "suggestion"},
		{ID: "untouched-file-critical", File: "lib/devices.rb", Line: 96, Severity: "critical"},
		{ID: "no-line", File: "lib/lock.rb", Severity: "nitpick"},
	}
	got := findingIDs(FilterLateFindings(findings, lateIncrementalDiff))
	want := []string{"near-new-code", "old-code-bug", "untouched-file-critical", "no-line"}
	if len(got) != len(want) {
		t.Fatalf("kept %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kept %v, want %v", got, want)
		}
	}
}

func TestFilterLateFindingsNoDiffIsNoop(t *testing.T) {
	findings := []Finding{{ID: "a", File: "x.go", Line: 500, Severity: "nitpick"}}
	if got := FilterLateFindings(findings, "  \n"); len(got) != 1 {
		t.Errorf("expected no-op without an incremental diff, got %v", findingIDs(got))
	}
}

// The three titles below are the same finding, raised three times on one
// PR after the author had answered it.
func TestFilterKnownDuplicatesRewordedTitle(t *testing.T) {
	known := []Finding{{
		ID: "removed-helpers-callers", File: "lib/views_test.rb", Line: 6,
		Title: "Verify nothing else still calls the removed layout-preview helpers",
	}}
	findings := []Finding{
		{ID: "verify-removed-helpers", File: "lib/views_test.rb", Line: 6,
			Title: "Verify nothing outside the diff still calls the removed FigmaViews layout-preview helpers"},
		{ID: "removed-figmaviews-helpers", File: "lib/views_test.rb", Line: 9,
			Title: "Verify nothing still calls the removed FigmaViews and layout helpers"},
		{ID: "fresh", File: "lib/views_test.rb", Line: 8,
			Title: "Test asserts on the wrong fixture"},
	}
	got := findingIDs(FilterKnownDuplicates(findings, known))
	if len(got) != 1 || got[0] != "fresh" {
		t.Errorf("kept %v, want [fresh]", got)
	}
}

func TestFilterKnownDuplicatesNeedsSameAreaAndFile(t *testing.T) {
	known := []Finding{{ID: "stale-offset", File: "lib/lock.rb", Line: 110, Title: "Holder info is written at a stale file offset"}}
	findings := []Finding{
		{ID: "stale-offset", File: "lib/lock.rb", Line: 112, Title: "Different wording entirely"},
		{ID: "stale-offset", File: "lib/lock.rb", Line: 300, Title: "Holder info is written at a stale file offset"},
		{ID: "stale-offset", File: "lib/other.rb", Line: 110, Title: "Holder info is written at a stale file offset"},
		{ID: "x", File: "lib/lock.rb", Line: 0, Title: "Holder info written at stale offset after waiting"},
	}
	got := findingIDs(FilterKnownDuplicates(findings, known))
	want := []string{"stale-offset", "stale-offset"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("kept %v, want the far-away and other-file findings only", got)
	}
}

func TestIsBlocking(t *testing.T) {
	for sev, want := range map[string]bool{
		"critical": true, "bug": true, "warning": true,
		"suggestion": false, "nitpick": false, "": true, "weird": true,
	} {
		if got := isBlocking(sev); got != want {
			t.Errorf("isBlocking(%q) = %v, want %v", sev, got, want)
		}
	}
}
