package review

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return p
}

func TestFetchFileContents_Partition(t *testing.T) {
	dir := t.TempDir()
	small := writeTestFile(t, dir, "small.go", []byte("package x\n"))
	big := writeTestFile(t, dir, "big_test.rb", []byte(strings.Repeat("a", 200)))
	mid1 := writeTestFile(t, dir, "mid1.go", []byte(strings.Repeat("b", 80)))
	mid2 := writeTestFile(t, dir, "mid2.go", []byte(strings.Repeat("c", 80)))
	bin := writeTestFile(t, dir, "image.png", []byte{0x89, 'P', 'N', 'G', 0, 1, 2})
	ignored := writeTestFile(t, dir, "app.lock", []byte("lock"))
	deleted := filepath.Join(dir, "deleted.go")

	files := []string{small, big, mid1, mid2, bin, ignored, deleted}
	res := FetchFileContents(files, []string{"**/*.lock"}, 100, 120)

	if _, ok := res.Contents[small]; !ok {
		t.Errorf("small file should have contents")
	}
	if _, ok := res.Contents[mid1]; !ok {
		t.Errorf("mid1 fits the budget and should have contents")
	}
	if want := []string{bin, ignored}; !sameSet(res.Excluded, want) {
		t.Errorf("Excluded = %v, want %v", res.Excluded, want)
	}
	// big is over max_file_size, mid2 is over the remaining total budget —
	// both stay in the review from the diff alone.
	if want := []string{big, mid2}; !reflect.DeepEqual(res.DiffOnly, want) {
		t.Errorf("DiffOnly = %v, want %v", res.DiffOnly, want)
	}
	for _, p := range []string{big, mid2, bin, ignored, deleted} {
		if _, ok := res.Contents[p]; ok {
			t.Errorf("%s should not have contents", p)
		}
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]bool{}
	for _, x := range a {
		m[x] = true
	}
	for _, x := range b {
		if !m[x] {
			return false
		}
	}
	return true
}

// testFileDiff builds a one-hunk diff block adding n lines to path.
func testFileDiff(path string, n int) string {
	var b strings.Builder
	b.WriteString("diff --git a/" + path + " b/" + path + "\n")
	b.WriteString("--- a/" + path + "\n+++ b/" + path + "\n")
	b.WriteString("@@ -0,0 +1," + strconv.Itoa(n) + " @@\n")
	for i := 0; i < n; i++ {
		b.WriteString("+line " + strconv.Itoa(i) + "\n")
	}
	return b.String()
}

func TestScopePRForPrompt_KeepsTooLargeFiles(t *testing.T) {
	diff := testFileDiff("app.go", 3) + testFileDiff("cli_test.rb", 3) + testFileDiff("yarn.lock", 3)
	pr := &PRData{Diff: diff, Files: []string{"app.go", "cli_test.rb", "yarn.lock"}}
	fc := FileContentsResult{
		Contents: map[string]string{"app.go": "package x\n"},
		DiffOnly: []string{"cli_test.rb"},
		Excluded: []string{"yarn.lock"},
	}

	cov := scopePRForPrompt(pr, fc, 1<<20)

	if want := []string{"app.go", "cli_test.rb"}; !reflect.DeepEqual(pr.Files, want) {
		t.Errorf("Files = %v, want %v (too-large files must stay in the review)", pr.Files, want)
	}
	if !strings.Contains(pr.Diff, "+++ b/cli_test.rb") {
		t.Errorf("diff-only file hunks must stay in the prompt diff")
	}
	if strings.Contains(pr.Diff, "yarn.lock") {
		t.Errorf("excluded file hunks must be removed from the prompt diff")
	}
	if pr.ValidationDiff() != diff {
		t.Errorf("ValidationDiff must be the untouched PR diff")
	}
	if !reflect.DeepEqual(cov.DiffOnly, []string{"cli_test.rb"}) || !reflect.DeepEqual(cov.Excluded, []string{"yarn.lock"}) || len(cov.TruncatedDiff) != 0 {
		t.Errorf("unexpected coverage %+v", cov)
	}
	if pr.FileContents["app.go"] != "package x\n" {
		t.Errorf("file contents not stored on pr")
	}
}

func TestScopePRForPrompt_TrimsDiff(t *testing.T) {
	diff := testFileDiff("small.go", 2) + testFileDiff("db/structure.sql", 2000)
	pr := &PRData{Diff: diff, Files: []string{"small.go", "db/structure.sql"}}

	cov := scopePRForPrompt(pr, FileContentsResult{}, 2000)

	if !reflect.DeepEqual(cov.TruncatedDiff, []string{"db/structure.sql"}) {
		t.Errorf("TruncatedDiff = %v", cov.TruncatedDiff)
	}
	if !reflect.DeepEqual(pr.Files, []string{"small.go", "db/structure.sql"}) {
		t.Errorf("truncated files must stay in pr.Files, got %v", pr.Files)
	}
	if pr.ValidationDiff() != diff {
		t.Errorf("ValidationDiff must be the untouched PR diff")
	}
}

func TestCapDiff_FitsUnchanged(t *testing.T) {
	diff := testFileDiff("a.go", 5) + testFileDiff("b.go", 5)
	got, truncated := capDiff(diff, len(diff))
	if got != diff || truncated != nil {
		t.Errorf("diff within budget must be unchanged")
	}
	if got, _ := capDiff(diff, 0); got != diff {
		t.Errorf("zero budget means no cap")
	}
}

func TestCapDiff_CutsLargestFirst(t *testing.T) {
	small := testFileDiff("small.go", 3)
	medium := testFileDiff("medium.go", 40)
	huge := testFileDiff("huge.sql", 5000)
	diff := "preamble\n" + huge + small + medium
	budget := 2000

	got, truncated := capDiff(diff, budget)

	if !reflect.DeepEqual(truncated, []string{"huge.sql"}) {
		t.Fatalf("truncated = %v, want [huge.sql]", truncated)
	}
	if !strings.HasPrefix(got, "preamble\n") {
		t.Errorf("text before the first file must be kept")
	}
	if !strings.Contains(got, small) || !strings.Contains(got, medium) {
		t.Errorf("files under their share must keep their whole diff")
	}
	if !strings.Contains(got, "+++ b/huge.sql\n@@ -0,0 +1,5000 @@\n+line 0\n") {
		t.Errorf("cut file must keep its header and first lines")
	}
	if !strings.Contains(got, "[codecanary: diff truncated to fit max_diff_size") {
		t.Errorf("cut file must carry a truncation marker")
	}
	// Headers and the marker may overshoot slightly, never by much.
	if len(got) > budget+200 {
		t.Errorf("capped diff is %d bytes, budget %d", len(got), budget)
	}
	if !reflect.DeepEqual(FilesFromDiff(got), []string{"huge.sql", "small.go", "medium.go"}) {
		t.Errorf("file order must be preserved, got %v", FilesFromDiff(got))
	}
	if again, _ := capDiff(diff, budget); again != got {
		t.Errorf("capDiff must be deterministic")
	}
}

func TestCapDiff_DeletedFilePath(t *testing.T) {
	var b strings.Builder
	b.WriteString("diff --git a/old.sql b/old.sql\ndeleted file mode 100644\n--- a/old.sql\n+++ /dev/null\n@@ -1,3000 +0,0 @@\n")
	for i := 0; i < 3000; i++ {
		b.WriteString("-gone\n")
	}
	_, truncated := capDiff(b.String(), 500)
	if !reflect.DeepEqual(truncated, []string{"old.sql"}) {
		t.Errorf("truncated = %v, want [old.sql]", truncated)
	}
}

func TestSplitDiffBlocks_RoundTrip(t *testing.T) {
	diff := "header\n" + testFileDiff("a.go", 2) + testFileDiff("b.go", 2)
	prefix, blocks := splitDiffBlocks(diff)
	if prefix != "header\n" || len(blocks) != 2 {
		t.Fatalf("prefix=%q blocks=%d", prefix, len(blocks))
	}
	if prefix+strings.Join(blocks, "") != diff {
		t.Errorf("split must round-trip")
	}
}

func TestRenderCoverageNote(t *testing.T) {
	if got := renderCoverageNote(nil); got != "" {
		t.Errorf("nil coverage should render nothing, got %q", got)
	}
	if got := renderCoverageNote(&ReviewCoverage{}); got != "" {
		t.Errorf("empty coverage should render nothing, got %q", got)
	}

	got := renderCoverageNote(&ReviewCoverage{
		DiffOnly:      []string{"cli_test.rb"},
		TruncatedDiff: []string{"db/structure.sql"},
		Excluded:      []string{"yarn.lock"},
	})
	for _, want := range []string{
		"<details>",
		"Review coverage: 2 files reviewed partially, 1 not reviewed",
		"**Reviewed from the diff only",
		"- `cli_test.rb`",
		"**Diff truncated",
		"- `db/structure.sql`",
		"**Not reviewed",
		"- `yarn.lock`",
		"</details>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("note missing %q:\n%s", want, got)
		}
	}

	onlyExcluded := renderCoverageNote(&ReviewCoverage{Excluded: []string{"a.lock"}})
	if strings.Contains(onlyExcluded, "partially") || strings.Contains(onlyExcluded, "Diff truncated") {
		t.Errorf("only non-empty groups should render:\n%s", onlyExcluded)
	}
}

func TestCoverageRenderedInBodies(t *testing.T) {
	cov := &ReviewCoverage{DiffOnly: []string{"cli_test.rb"}}

	clean := buildCleanReviewBody("abc123", ReviewSummary{}, cov)
	if !strings.Contains(clean, "cli_test.rb") {
		t.Errorf("clean review body should carry the coverage note:\n%s", clean)
	}
	if !strings.HasSuffix(strings.TrimSpace(clean), reviewMarkerSuffix) {
		t.Errorf("baseline marker must stay last")
	}

	result := &ReviewResult{PRNumber: 1, Coverage: cov, Findings: []Finding{{ID: "x", File: "a.go", Line: 1, Severity: "bug", Title: "t"}}}
	body := FormatReviewBody(result, func(Finding) bool { return true })
	if !strings.Contains(body, "Review coverage") {
		t.Errorf("review body should carry the coverage note")
	}
	if !strings.Contains(FormatMarkdown(result), "Review coverage") {
		t.Errorf("markdown output should carry the coverage note")
	}

	term := FormatTerminal(&ReviewResult{Coverage: cov})
	if !strings.Contains(term, "Reviewed from the diff only") || !strings.Contains(term, "cli_test.rb") {
		t.Errorf("terminal output should list diff-only files:\n%s", term)
	}
	if strings.Contains(FormatTerminal(&ReviewResult{}), "Reviewed from") {
		t.Errorf("terminal output should have no coverage footer when coverage is complete")
	}

	js, err := FormatJSON(&ReviewResult{Coverage: cov})
	if err != nil || !strings.Contains(js, `"diff_only"`) {
		t.Errorf("JSON output should include coverage, got %s (%v)", js, err)
	}
	js, _ = FormatJSON(&ReviewResult{})
	if strings.Contains(js, "coverage") {
		t.Errorf("JSON output should omit empty coverage")
	}
}
