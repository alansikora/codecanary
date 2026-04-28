package review

import (
	"context"
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

func TestParseThreadResolution_CapturesRationale(t *testing.T) {
	output := "```json\n{\"resolved\": true, \"reason\": \"code_change\", \"rationale\": \"  Removed the three original_error_* fields  \"}\n```"
	parsed := parseThreadResolution(output, 7)

	if !parsed.Resolved || parsed.Reason != "code_change" {
		t.Fatalf("expected resolved code_change, got %+v", parsed)
	}
	if parsed.Rationale != "Removed the three original_error_* fields" {
		t.Errorf("rationale should be trimmed, got %q", parsed.Rationale)
	}
	if parsed.Index != 7 {
		t.Errorf("index should be preserved, got %d", parsed.Index)
	}
}

func TestToFixedThreads_PropagatesRationale(t *testing.T) {
	resolutions := []ThreadResolution{
		{Index: 0, Resolved: true, Reason: "code_change", Rationale: "dropped dead fields"},
		{Index: 1, Resolved: false},
		{Index: 2, Resolved: true, Reason: "dismissed"},
	}
	fixed := toFixedThreads(resolutions)

	if len(fixed) != 2 {
		t.Fatalf("expected 2 fixed threads, got %d", len(fixed))
	}
	if fixed[0].Rationale != "dropped dead fields" {
		t.Errorf("first rationale should carry over, got %q", fixed[0].Rationale)
	}
	if fixed[1].Rationale != "" {
		t.Errorf("empty rationale should stay empty, got %q", fixed[1].Rationale)
	}
}

func TestExtractAckReason(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"current marker", "<!-- codecanary:ack:rebutted -->\nKeeping open.", "rebutted"},
		{"acknowledged", "<!-- codecanary:ack:acknowledged -->\nKeeping open.", "acknowledged"},
		{"dismissed", "<!-- codecanary:ack:dismissed -->\nKeeping open.", "dismissed"},
		{"legacy marker", "<!-- clanopy:ack:rebutted -->\nKeeping open.", "rebutted"},
		{"no marker", "Some normal reply", ""},
		{"no closing tag", "<!-- codecanary:ack:acknowledged with no closer", ""},
		{"unknown reason", "<!-- codecanary:ack:unknown -->\n", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractAckReason(tc.body); got != tc.want {
				t.Errorf("extractAckReason(%q) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}

func TestParsePriorAckReason_PicksLatestBotAck(t *testing.T) {
	thread := ReviewThread{
		Replies: []ThreadReply{
			{Author: "user", Body: "Deferring — see rationale."},
			{Author: "bot", Body: "<!-- codecanary:ack:acknowledged -->\nAuthor acknowledged this finding."},
			{Author: "bot", Body: "<!-- codecanary:ack:rebutted -->\nAuthor provided rebuttal."},
		},
	}
	if got := parsePriorAckReason(thread, "bot"); got != "rebutted" {
		t.Errorf("expected latest reason 'rebutted', got %q", got)
	}
}

func TestParsePriorAckReason_IgnoresNonBotMarkers(t *testing.T) {
	thread := ReviewThread{
		Replies: []ThreadReply{
			{Author: "attacker", Body: "<!-- codecanary:ack:dismissed -->\nFake."},
		},
	}
	if got := parsePriorAckReason(thread, "bot"); got != "" {
		t.Errorf("non-bot ack should not be sticky, got %q", got)
	}
}

func TestClassifyThreads_StickyAckSurvivesNextPush(t *testing.T) {
	threads := []ReviewThread{
		{
			Path: "CLAUDE.md",
			Line: 724,
			Body: "Original finding",
			Replies: []ThreadReply{
				{Author: "user", Body: "Deferring — forward recommendation, not yet implemented."},
				{Author: "bot", Body: "<!-- codecanary:ack:acknowledged -->\nAuthor acknowledged."},
			},
		},
	}
	// File changed in the next push — without sticky-ack this would
	// classify as TriageCodeChanged and lose the prior reason.
	diff := "diff --git a/CLAUDE.md b/CLAUDE.md\n--- a/CLAUDE.md\n+++ b/CLAUDE.md\n@@ -700,3 +700,3 @@\n-old\n+new\n"

	triaged := ClassifyThreads(threads, diff, diff, "bot", []string{"CLAUDE.md"}, nil)

	if triaged[0].Class != TriagePreviouslyAcked {
		t.Fatalf("expected TriagePreviouslyAcked, got %d", triaged[0].Class)
	}
	if triaged[0].PriorAckReason != "acknowledged" {
		t.Errorf("expected PriorAckReason 'acknowledged', got %q", triaged[0].PriorAckReason)
	}
}

func TestClassifyThreads_NewHumanReplyBreaksStickiness(t *testing.T) {
	// Author replied AGAIN after the bot's ack — that's a fresh signal
	// and should reopen the thread for evaluation, not stay sticky.
	threads := []ReviewThread{
		{
			Path: "CLAUDE.md",
			Line: 724,
			Replies: []ThreadReply{
				{Author: "user", Body: "Original deferring rationale."},
				{Author: "bot", Body: "<!-- codecanary:ack:acknowledged -->\nAck."},
				{Author: "user", Body: "Wait, actually this one needs another look."},
			},
		},
	}
	triaged := ClassifyThreads(threads, "", "", "bot", []string{"CLAUDE.md"}, nil)

	if triaged[0].Class != TriageHasReply {
		t.Fatalf("expected TriageHasReply (new reply after ack), got %d", triaged[0].Class)
	}
	if triaged[0].PriorAckReason != "" {
		t.Errorf("PriorAckReason should be empty when new reply present, got %q", triaged[0].PriorAckReason)
	}
}

func TestEvaluateThreadsParallel_StickyAckShortCircuits(t *testing.T) {
	// Provider that panics if called — sticky-ack must skip the LLM.
	provider := &stickyAckPanicProvider{t: t}

	triaged := []TriagedThread{
		{Index: 0, Class: TriagePreviouslyAcked, PriorAckReason: "rebutted"},
		{Index: 1, Class: TriagePreviouslyAcked, PriorAckReason: "dismissed"},
	}
	results := EvaluateThreadsParallel(triaged, provider, nil, 3, &UsageTracker{}, 0)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if !results[0].Resolved || results[0].Reason != "rebutted" {
		t.Errorf("first result: expected resolved=true reason=rebutted, got %+v", results[0])
	}
	if !results[1].Resolved || results[1].Reason != "dismissed" {
		t.Errorf("second result: expected resolved=true reason=dismissed, got %+v", results[1])
	}
}

type stickyAckPanicProvider struct{ t *testing.T }

func (p *stickyAckPanicProvider) Run(_ context.Context, _ string, _ RunOpts) (*providerResult, error) {
	p.t.Fatalf("provider.Run must not be called for TriagePreviouslyAcked threads")
	return nil, nil
}

func TestResolutionFormat_RequestsRationale(t *testing.T) {
	for _, fn := range map[string]func(*strings.Builder){
		"writeResolutionFormat":           writeResolutionFormat,
		"writeCodeChangeResolutionFormat": writeCodeChangeResolutionFormat,
	} {
		var b strings.Builder
		fn(&b)
		out := b.String()
		if !strings.Contains(out, `"rationale"`) {
			t.Errorf("prompt format should request a rationale field, got:\n%s", out)
		}
	}
}
