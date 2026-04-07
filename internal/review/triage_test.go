package review

import (
	"fmt"
	"strings"
	"testing"
)

// --- ExtractFileSnippet tests ---

func TestExtractFileSnippet_Basic(t *testing.T) {
	// 100-line file, finding at line 50, hunk at lines 45-55.
	var lines []string
	for i := 1; i <= 100; i++ {
		lines = append(lines, fmt.Sprintf("line %d content", i))
	}
	content := strings.Join(lines, "\n")
	diff := "@@ -40,10 +45,11 @@ func foo() {\n+added line\n"

	snippet := ExtractFileSnippet(content, 50, diff, 300)
	if snippet == "" {
		t.Fatal("expected non-empty snippet")
	}
	// Should contain the finding line.
	if !strings.Contains(snippet, "50: line 50 content") {
		t.Error("snippet should contain the finding line")
	}
	// Should contain hunk area.
	if !strings.Contains(snippet, "45: line 45 content") {
		t.Error("snippet should contain hunk start area")
	}
}

func TestExtractFileSnippet_MergesOverlappingRanges(t *testing.T) {
	var lines []string
	for i := 1; i <= 200; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	content := strings.Join(lines, "\n")
	// Two hunks close together — should merge into one contiguous range.
	diff := "@@ -10,5 +10,5 @@\n+a\n@@ -20,5 +20,5 @@\n+b\n"

	snippet := ExtractFileSnippet(content, 15, diff, 300)
	// Should NOT contain omission markers since ranges overlap/merge.
	if strings.Contains(snippet, "lines omitted") {
		t.Error("close hunks should merge without omission markers")
	}
}

func TestExtractFileSnippet_CapsAtMaxLines(t *testing.T) {
	var lines []string
	for i := 1; i <= 1000; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	content := strings.Join(lines, "\n")
	// Hunks spread across the file.
	diff := "@@ -10,5 +10,5 @@\n+a\n@@ -500,5 +500,5 @@\n+b\n@@ -900,5 +900,5 @@\n+c\n"

	snippet := ExtractFileSnippet(content, 50, diff, 150)
	snippetLines := strings.Split(strings.TrimRight(snippet, "\n"), "\n")
	if len(snippetLines) > 160 { // small buffer for omission markers
		t.Errorf("snippet should respect maxLines cap, got %d lines", len(snippetLines))
	}
	// Must contain the finding line.
	if !strings.Contains(snippet, "50: line 50") {
		t.Error("snippet must prioritize the finding line")
	}
}

func TestExtractFileSnippet_NoDiff(t *testing.T) {
	var lines []string
	for i := 1; i <= 100; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	content := strings.Join(lines, "\n")

	// Cross-file case: no diff for this file.
	snippet := ExtractFileSnippet(content, 50, "", 300)
	if snippet == "" {
		t.Fatal("expected non-empty snippet for cross-file case")
	}
	if !strings.Contains(snippet, "50: line 50") {
		t.Error("snippet should center on finding line")
	}
}

func TestExtractFileSnippet_ZeroCountHunk(t *testing.T) {
	// A hunk with count=0 (pure deletion) should not produce a range.
	ranges := parseHunkNewRanges("@@ -5,3 +10,0 @@\n-deleted line\n")
	if len(ranges) != 0 {
		t.Errorf("expected 0 ranges for zero-count hunk, got %d", len(ranges))
	}
}

func TestExtractFileSnippet_FindingLineZero(t *testing.T) {
	var lines []string
	for i := 1; i <= 100; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	content := strings.Join(lines, "\n")
	diff := "@@ -40,10 +40,10 @@\n+changed\n"

	// findingLine=0 should not panic and should anchor to hunk area.
	snippet := ExtractFileSnippet(content, 0, diff, 50)
	if snippet == "" {
		t.Fatal("expected non-empty snippet even with findingLine=0")
	}
	// Should contain hunk area, not be anchored to line 0.
	if !strings.Contains(snippet, "40: line 40") {
		t.Error("snippet should include hunk area when findingLine is 0")
	}
}

func TestExtractFileSnippet_EmptyContent(t *testing.T) {
	snippet := ExtractFileSnippet("", 10, "@@ -1,5 +1,5 @@\n", 300)
	if snippet != "" {
		t.Error("expected empty snippet for empty content")
	}
}

// --- Prompt builder tests ---

func TestBuildCodeChangePrompt_IncludesFileContext(t *testing.T) {
	tt := TriagedThread{
		Thread: ReviewThread{
			Path: "main.go",
			Line: 10,
			Body: "Found a bug",
		},
		FileDiff:    "+ fixed line",
		FileSnippet: "9: before\n10: the line\n11: after\n",
	}
	prompt := buildCodeChangePrompt(tt, nil)

	if !strings.Contains(prompt, "## Current File Content (around finding)") {
		t.Error("prompt should include file context section when FileSnippet is set")
	}
	if !strings.Contains(prompt, "10: the line") {
		t.Error("prompt should include the file snippet content")
	}
}

func TestBuildCodeChangePrompt_NoFileContextWhenEmpty(t *testing.T) {
	tt := TriagedThread{
		Thread: ReviewThread{
			Path: "main.go",
			Line: 10,
			Body: "Found a bug",
		},
		FileDiff: "+ fixed line",
	}
	prompt := buildCodeChangePrompt(tt, nil)

	if strings.Contains(prompt, "## Current File Content") {
		t.Error("prompt should NOT include file context section when FileSnippet is empty")
	}
}

func TestBuildCodeChangePrompt_StructuralChangeInstruction(t *testing.T) {
	tt := TriagedThread{
		Thread: ReviewThread{
			Path: "main.go",
			Line: 10,
			Body: "Found a bug",
		},
		FileDiff: "+ fixed line",
	}
	prompt := buildCodeChangePrompt(tt, nil)

	if !strings.Contains(prompt, "structural change") {
		t.Error("prompt should include structural change guidance")
	}
}

func TestBuildCrossFilePrompt_IncludesFileContext(t *testing.T) {
	tt := TriagedThread{
		Thread: ReviewThread{
			Path: "main.go",
			Line: 10,
			Body: "Found a bug",
		},
		FileDiff:    "+ change in other file",
		FileSnippet: "9: before\n10: the line\n11: after\n",
	}
	prompt := buildCrossFilePrompt(tt, nil)

	if !strings.Contains(prompt, "## Current File Content (around finding)") {
		t.Error("cross-file prompt should include file context section")
	}
	if !strings.Contains(prompt, "structural change") {
		t.Error("cross-file prompt should include structural change guidance")
	}
}

func TestBuildCodeChangePrompt_OnlyAllowsCodeChangeReason(t *testing.T) {
	tt := TriagedThread{
		Thread: ReviewThread{
			Path: "main.go",
			Line: 10,
			Body: "Found a bug",
		},
		FileDiff: "+ fixed line",
	}
	prompt := buildCodeChangePrompt(tt, nil)

	if strings.Contains(prompt, `"acknowledged"`) {
		t.Error("buildCodeChangePrompt should not offer 'acknowledged' as a reason")
	}
	if strings.Contains(prompt, `"dismissed"`) {
		t.Error("buildCodeChangePrompt should not offer 'dismissed' as a reason")
	}
	if strings.Contains(prompt, `"rebutted"`) {
		t.Error("buildCodeChangePrompt should not offer 'rebutted' as a reason")
	}
	if !strings.Contains(prompt, `"code_change"`) {
		t.Error("buildCodeChangePrompt must offer 'code_change' as a reason")
	}
}

func TestBuildCrossFilePrompt_OnlyAllowsCodeChangeReason(t *testing.T) {
	tt := TriagedThread{
		Thread: ReviewThread{
			Path: "main.go",
			Line: 10,
			Body: "Found a bug",
		},
		FileDiff: "+ fixed in other file",
	}
	prompt := buildCrossFilePrompt(tt, nil)

	if strings.Contains(prompt, `"acknowledged"`) {
		t.Error("buildCrossFilePrompt should not offer 'acknowledged' as a reason")
	}
	if strings.Contains(prompt, `"dismissed"`) {
		t.Error("buildCrossFilePrompt should not offer 'dismissed' as a reason")
	}
	if strings.Contains(prompt, `"rebutted"`) {
		t.Error("buildCrossFilePrompt should not offer 'rebutted' as a reason")
	}
	if !strings.Contains(prompt, `"code_change"`) {
		t.Error("buildCrossFilePrompt must offer 'code_change' as a reason")
	}
}

func TestBuildReplyPrompt_AllowsAllReasons(t *testing.T) {
	tt := TriagedThread{
		Thread: ReviewThread{
			Path: "main.go",
			Line: 10,
			Body: "Found a bug",
			Replies: []ThreadReply{
				{Author: "user1", Body: "Will fix later"},
			},
		},
		BotLogin: "codecanary-bot",
	}
	prompt := buildReplyPrompt(tt, nil)

	for _, reason := range []string{`"code_change"`, `"acknowledged"`, `"dismissed"`, `"rebutted"`} {
		if !strings.Contains(prompt, reason) {
			t.Errorf("buildReplyPrompt must offer %s as a reason", reason)
		}
	}
}

func TestBuildCodeChangeReplyPrompt_AllowsAllReasons(t *testing.T) {
	tt := TriagedThread{
		Thread: ReviewThread{
			Path: "main.go",
			Line: 10,
			Body: "Found a bug",
			Replies: []ThreadReply{
				{Author: "user1", Body: "Fixed it"},
			},
		},
		FileDiff: "+ fixed line",
		BotLogin: "codecanary-bot",
	}
	prompt := buildCodeChangeReplyPrompt(tt, nil)

	for _, reason := range []string{`"code_change"`, `"acknowledged"`, `"dismissed"`, `"rebutted"`} {
		if !strings.Contains(prompt, reason) {
			t.Errorf("buildCodeChangeReplyPrompt must offer %s as a reason", reason)
		}
	}
}

// --- ClassifyThreads diff scoping tests ---

func TestClassifyThreads_FileScopedDiffForCodeChanged(t *testing.T) {
	threads := []ReviewThread{
		{Path: "a.go", Line: 10, Body: "Issue in a.go", Outdated: true},
	}
	fullDiff := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -10,3 +10,3 @@\n-old\n+new\n" +
		"diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -5,3 +5,3 @@\n-old b\n+new b\n"

	triaged := ClassifyThreads(threads, fullDiff, fullDiff, "bot", []string{"a.go", "b.go"}, nil)

	if triaged[0].Class != TriageCodeChanged {
		t.Fatalf("expected TriageCodeChanged, got %d", triaged[0].Class)
	}
	if strings.Contains(triaged[0].FileDiff, "b.go") {
		t.Error("FileDiff for TriageCodeChanged should be file-scoped, not full PR diff")
	}
	if !strings.Contains(triaged[0].FileDiff, "a.go") {
		t.Error("FileDiff should contain the finding's file diff")
	}
	// FullDiff should contain the entire PR diff for widened-scope fallback.
	if !strings.Contains(triaged[0].FullDiff, "b.go") {
		t.Error("FullDiff should contain the full PR diff for fallback")
	}
}

func TestClassifyThreads_NoFullDiffForCrossFile(t *testing.T) {
	threads := []ReviewThread{
		{Path: "a.go", Line: 10, Body: "Issue in a.go"},
	}
	diff := "diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -5,3 +5,3 @@\n-old b\n+new b\n"

	triaged := ClassifyThreads(threads, diff, diff, "bot", []string{"a.go", "b.go"}, nil)

	if triaged[0].Class != TriageCrossFileChange {
		t.Fatalf("expected TriageCrossFileChange, got %d", triaged[0].Class)
	}
	// TriageCrossFileChange already gets the full diff as FileDiff — no fallback needed.
	if triaged[0].FullDiff != "" {
		t.Error("FullDiff should be empty for TriageCrossFileChange (no fallback needed)")
	}
}

func TestBuildWidenedScopePrompt(t *testing.T) {
	tt := TriagedThread{
		Thread: ReviewThread{
			Path: "main.go",
			Line: 10,
			Body: "Found a bug",
		},
		FullDiff:    "+ fix in other file",
		FileSnippet: "9: before\n10: the line\n11: after\n",
	}
	prompt := buildWidenedScopePrompt(tt, nil)

	if !strings.Contains(prompt, "full PR diff") {
		t.Error("widened prompt should mention full PR diff")
	}
	if !strings.Contains(prompt, "another file") {
		t.Error("widened prompt should guide LLM to check other files")
	}
	if !strings.Contains(prompt, "+ fix in other file") {
		t.Error("widened prompt should include FullDiff content")
	}
	if !strings.Contains(prompt, "## Current File Content") {
		t.Error("widened prompt should include file snippet")
	}
	// Should only allow code_change reason (no reply-based reasons).
	if strings.Contains(prompt, `"acknowledged"`) || strings.Contains(prompt, `"dismissed"`) {
		t.Error("widened prompt should not offer reply-based resolution reasons")
	}
}

func TestClassifyThreads_FullDiffForCrossFile(t *testing.T) {
	threads := []ReviewThread{
		{Path: "a.go", Line: 10, Body: "Issue in a.go"},
	}
	// Only b.go changed — finding's file (a.go) is not in the diff.
	diff := "diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -5,3 +5,3 @@\n-old b\n+new b\n"

	triaged := ClassifyThreads(threads, diff, diff, "bot", []string{"a.go", "b.go"}, nil)

	if triaged[0].Class != TriageCrossFileChange {
		t.Fatalf("expected TriageCrossFileChange, got %d", triaged[0].Class)
	}
	if !strings.Contains(triaged[0].FileDiff, "b.go") {
		t.Error("FileDiff for TriageCrossFileChange should contain the full PR diff")
	}
}

func TestValidateResolutionReason_RejectsInvalidReasonForCodeChangeOnly(t *testing.T) {
	// Simulate Claude returning "acknowledged" for a code-change-only thread.
	output := "```json\n{\"resolved\": true, \"reason\": \"acknowledged\"}\n```"
	parsed := parseThreadResolution(output, 0)

	// parseThreadResolution itself accepts any reason (it's just a parser).
	if !parsed.Resolved || parsed.Reason != "acknowledged" {
		t.Fatal("parseThreadResolution should parse the raw response as-is")
	}

	// For code-change-only classifications, invalid reasons should be rejected.
	for _, class := range []ThreadClassification{TriageCodeChanged, TriageCrossFileChange} {
		res := validateResolutionReason(parsed, class)
		if res.Resolved {
			t.Errorf("class %d: resolution with reason 'acknowledged' should be rejected", class)
		}
		if res.Reason != "" {
			t.Errorf("class %d: reason should be cleared, got %q", class, res.Reason)
		}
	}

	// For reply-based classifications, the same reason should be accepted.
	for _, class := range []ThreadClassification{TriageHasReply, TriageCodeChangedReply} {
		res := validateResolutionReason(parsed, class)
		if !res.Resolved {
			t.Errorf("class %d: resolution with reason 'acknowledged' should be accepted", class)
		}
		if res.Reason != "acknowledged" {
			t.Errorf("class %d: reason should be 'acknowledged', got %q", class, res.Reason)
		}
	}
}
