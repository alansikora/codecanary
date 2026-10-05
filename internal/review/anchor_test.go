package review

import (
	"encoding/json"
	"strings"
	"testing"
)

// deletionOnlyDiff mirrors thetechfx/bedrock#6069: render_test_case.rb only
// loses lines, so it has context lines but no added line to anchor to, while
// cli.rb gains a line at 63.
const deletionOnlyDiff = `diff --git a/render_test_case.rb b/render_test_case.rb
--- a/render_test_case.rb
+++ b/render_test_case.rb
@@ -67,6 +67,3 @@ def self.boot!
     # context
-    module ChromeSelection
-    end
-
     class RenderTestCase < Minitest::Test
diff --git a/cli.rb b/cli.rb
--- a/cli.rb
+++ b/cli.rb
@@ -62,2 +62,3 @@
     opts = {}
+    parse!(argv)
     opts
`

var anchorPRFiles = []string{"render_test_case.rb", "cli.rb"}

func TestAnchorFinding(t *testing.T) {
	files := fileSet(anchorPRFiles)
	lines := parseDiffLines(deletionOnlyDiff)

	tests := []struct {
		name     string
		f        Finding
		wantKind anchorKind
		wantLine int
	}{
		{"added line", Finding{File: "cli.rb", Line: 63}, anchorLine, 63},
		{"near added line snaps", Finding{File: "cli.rb", Line: 70}, anchorLine, 63},
		{"far from added line", Finding{File: "cli.rb", Line: 63 + MaxFindingProximity + 1}, anchorNone, 0},
		{"deletion-only file", Finding{File: "render_test_case.rb", Line: 70}, anchorFile, 0},
		{"no line in PR file", Finding{File: "cli.rb"}, anchorFile, 0},
		{"file outside PR", Finding{File: "other.rb", Line: 1}, anchorNone, 0},
		{"no file", Finding{Line: 5}, anchorNone, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := anchorFinding(tt.f, files, lines)
			if got.Kind != tt.wantKind || got.Line != tt.wantLine {
				t.Errorf("anchorFinding(%+v) = %+v, want kind %d line %d", tt.f, got, tt.wantKind, tt.wantLine)
			}
		})
	}
}

// validateFindings and buildReviewPosts must agree: every finding that
// survives validation is posted as an inline or a file-level comment.
func TestValidatedFindingsAreAllPosted(t *testing.T) {
	in := []Finding{
		{ID: "inline", File: "cli.rb", Line: 63, Severity: "warning"},
		{ID: "file-level", File: "render_test_case.rb", Line: 70, Severity: "suggestion"},
		{ID: "outside", File: "other.rb", Line: 1, Severity: "bug"},
		{ID: "far", File: "cli.rb", Line: 500, Severity: "bug"},
		{ID: "no-file", Severity: "bug"},
	}
	kept := validateFindings(in, anchorPRFiles, deletionOnlyDiff)
	if len(kept) != 2 {
		t.Fatalf("validateFindings kept %d findings, want 2: %+v", len(kept), kept)
	}

	result := &ReviewResult{PRNumber: 6069, Findings: kept, SHA: "abc"}
	payload, fileComments := buildReviewPosts(result, anchorPRFiles, deletionOnlyDiff, "abc", "", ReviewSummary{NewFindings: len(kept)})
	if got := len(payload.Comments) + len(fileComments); got != len(kept) {
		t.Fatalf("posted %d comments for %d validated findings", got, len(kept))
	}
}

func TestBuildReviewPosts_FileLevelPayload(t *testing.T) {
	result := &ReviewResult{
		PRNumber: 6069,
		Findings: []Finding{
			{ID: "removed-module", File: "render_test_case.rb", Line: 70, Severity: "suggestion", Title: "T", Description: "D"},
			{ID: "positional-ids", File: "cli.rb", Line: 63, Severity: "warning", Title: "T2", Description: "D2"},
		},
		SHA: "deadbeef",
	}
	payload, fileComments := buildReviewPosts(result, anchorPRFiles, deletionOnlyDiff, "deadbeef", "", ReviewSummary{NewFindings: 2})

	if len(payload.Comments) != 1 || payload.Comments[0].Path != "cli.rb" || payload.Comments[0].Line != 63 {
		t.Fatalf("inline comments = %+v, want one on cli.rb:63", payload.Comments)
	}
	if payload.CommitID != "deadbeef" || payload.Event != "COMMENT" {
		t.Errorf("review payload commit/event = %q/%q", payload.CommitID, payload.Event)
	}
	if len(fileComments) != 1 {
		t.Fatalf("file comments = %+v, want one", fileComments)
	}

	raw, err := json.Marshal(fileComments[0])
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"commit_id":    "deadbeef",
		"path":         "render_test_case.rb",
		"subject_type": "file",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("file comment %s = %v, want %v", k, got[k], v)
		}
	}
	if _, ok := got["line"]; ok {
		t.Errorf("file comment must not carry a line: %s", raw)
	}

	// The marker must round-trip so LoadPreviousFindings and
	// `codecanary findings` pick the thread up, with the finding's line.
	body, _ := got["body"].(string)
	f, ok := findingFromEmbeddedJSON(body)
	if !ok || f.ID != "removed-module" || f.Line != 70 {
		t.Errorf("file comment marker = %+v (ok=%v), want removed-module at line 70", f, ok)
	}
	thread := FindingFromThread(ReviewThread{Path: "render_test_case.rb", Line: 70, Body: body})
	if thread.ID != "removed-module" {
		t.Errorf("FindingFromThread ID = %q", thread.ID)
	}

	// Nothing about either finding is left only in the review body.
	if strings.Contains(payload.Body, "**suggestion** \u2014 `removed-module`") {
		t.Errorf("file-level finding should not be rendered inline in the review body:\n%s", payload.Body)
	}
	if !strings.Contains(payload.Body, "See the review comments") {
		t.Errorf("review body should point to the comments:\n%s", payload.Body)
	}
}

func TestParseFindings_NeedsVerification(t *testing.T) {
	out := "```json\n[\n" +
		`{"id":"a","file":"x.go","line":1,"severity":"suggestion","title":"Verify callers","description":"d","fix_ref":"1-1","needs_verification":true},` +
		`{"id":"b","file":"x.go","line":2,"severity":"warning","title":"Real","description":"d","fix_ref":"1-2"}` +
		"\n]\n```\n"
	findings, err := ParseFindings(out)
	if err != nil {
		t.Fatal(err)
	}
	if !findings[0].NeedsVerification || findings[1].NeedsVerification {
		t.Fatalf("needs_verification parsed as %v/%v, want true/false", findings[0].NeedsVerification, findings[1].NeedsVerification)
	}

	kept, questions := SplitOpenQuestions(findings)
	if len(kept) != 1 || kept[0].ID != "b" || len(questions) != 1 || questions[0].ID != "a" {
		t.Fatalf("SplitOpenQuestions = %+v / %+v", kept, questions)
	}
}

// Questions never reach the summary counts or the commit status: the
// pipeline splits them off before computeReviewSummary sees the findings.
func TestOpenQuestionsDoNotCount(t *testing.T) {
	findings := []Finding{
		{ID: "q", File: "x.go", Line: 1, Severity: "warning", NeedsVerification: true},
	}
	kept, questions := SplitOpenQuestions(findings)
	s := computeReviewSummary(nil, nil, kept)
	if s.NewFindings != 0 || s.Blocking != 0 {
		t.Errorf("summary = %+v, want no new or blocking findings", s)
	}
	if state, desc := commitStatusFromSummary(s); state != "success" || desc != "no findings" {
		t.Errorf("commit status = %s/%q, want success/no findings", state, desc)
	}
	if len(questions) != 1 {
		t.Fatalf("want 1 question, got %d", len(questions))
	}

	// Local state persists result.Findings + still-open only.
	all := combineFindings(nil, kept)
	if len(all) != 0 {
		t.Errorf("persisted findings = %+v, want none", all)
	}
}

func TestFormatOpenQuestions(t *testing.T) {
	if got := formatOpenQuestions(nil); got != "" {
		t.Errorf("no questions should render nothing, got %q", got)
	}
	got := formatOpenQuestions([]Finding{
		{Title: "Verify nothing still calls the removed helpers", File: "a.rb", Line: 12, Description: "Callers are outside the diff."},
		{Title: "Does the dispatcher pass ids?", File: "b.rb"},
	})
	for _, s := range []string{
		"<details>", "Open questions (2)", "</details>",
		"**Verify nothing still calls the removed helpers** (`a.rb:12`)",
		"Callers are outside the diff.",
		"(`b.rb`)",
	} {
		if !strings.Contains(got, s) {
			t.Errorf("open questions section missing %q:\n%s", s, got)
		}
	}
	if strings.Contains(got, findingMarkerPrefix) {
		t.Error("open questions must not carry a finding marker (they are not threads)")
	}
}

func TestReviewBodiesRenderOpenQuestions(t *testing.T) {
	q := []Finding{{Title: "Verify X", File: "a.go", Line: 3}}
	notes := formatOpenQuestions(q)

	full := FormatReviewBody(&ReviewResult{PRNumber: 1, Findings: []Finding{{ID: "f", File: "a.go", Line: 1, Severity: "warning"}}, Questions: q}, true)
	clean := buildCleanReviewBody("sha", notes, ReviewSummary{})
	activity := buildActivityReviewBody("sha", notes, ReviewSummary{StillOpen: 1})
	allClear := buildAllClearReviewBody("sha", false, notes, ReviewSummary{ResolvedByCode: 1})
	for name, body := range map[string]string{"full": full, "clean": clean, "activity": activity, "all-clear": allClear} {
		if !strings.Contains(body, "Open questions (1)") {
			t.Errorf("%s body missing open questions:\n%s", name, body)
		}
	}

	local := FormatMarkdown(&ReviewResult{Questions: q})
	if !strings.Contains(local, "Open questions (1)") {
		t.Errorf("local markdown missing open questions:\n%s", local)
	}
	term := FormatTerminal(&ReviewResult{Questions: q})
	if !strings.Contains(term, "Open questions (1)") || !strings.Contains(term, "a.go:3") {
		t.Errorf("terminal output missing open questions:\n%s", term)
	}
}

func TestPromptsDescribeNeedsVerification(t *testing.T) {
	full := BuildPrompt(&PRData{Number: 1, Files: []string{"a.go"}, Diff: "diff"}, nil, 0, nil)
	inc := BuildIncrementalPrompt("diff", nil, nil, 1, 0, nil, []string{"a.go"}, nil, nil)
	for name, p := range map[string]string{"full": full, "incremental": inc} {
		if !strings.Contains(p, "`needs_verification` (boolean, optional)") {
			t.Errorf("%s prompt does not document needs_verification", name)
		}
		if !strings.Contains(p, "set `needs_verification: true`") {
			t.Errorf("%s prompt's uncertainty rule does not mention the flag", name)
		}
	}
	if !strings.Contains(inc, "Only report NEW issues found in the incremental diff") {
		t.Error("incremental prompt lost its closing instruction")
	}
	if strings.Contains(full, "Only report NEW issues") {
		t.Error("full prompt must not carry the incremental closing instruction")
	}
}
