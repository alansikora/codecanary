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

func TestBuildReplyPrompt_OffersReplyReasonsOnly(t *testing.T) {
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

	for _, reason := range []string{`"acknowledged"`, `"dismissed"`, `"rebutted"`} {
		if !strings.Contains(prompt, reason) {
			t.Errorf("buildReplyPrompt must offer %s as a reason", reason)
		}
	}
	// The reply prompt shows no code change, and validateResolutionReason
	// rejects code_change on TriageHasReply, so the prompt must not offer it.
	if strings.Contains(prompt, `"code_change"`) {
		t.Error("buildReplyPrompt must not offer \"code_change\" as a reason")
	}

	// resolve_accepted_replies still gets its rejection rationale here.
	explained := buildReplyPrompt(tt, &ReviewConfig{ResolveAcceptedReplies: true})
	if !strings.Contains(explained, "It is posted on the thread") || strings.Contains(explained, `"code_change"`) {
		t.Error("buildReplyPrompt with resolve_accepted_replies must ask for the rejection rationale and still not offer code_change")
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

func TestCountNonSkipped_IncludesPreviouslyAcked(t *testing.T) {
	// Regression: sticky-ack threads must count toward needsEval so
	// EvaluateThreadsParallel runs and emits the carried-forward fixedThread.
	// Excluding them caused the runner to skip eval entirely, leaving the
	// summary stuck on "Still unresolved" for threads the bot had already
	// acked.
	triaged := []TriagedThread{
		{Class: TriageSkip},
		{Class: TriageFileRemovedFromPR},
		{Class: TriagePreviouslyAcked, PriorAckReason: "acknowledged"},
		{Class: TriagePreviouslyAcked, PriorAckReason: "rebutted"},
	}
	if got := countNonSkipped(triaged); got != 2 {
		t.Errorf("countNonSkipped = %d, want 2 (the two PreviouslyAcked threads)", got)
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
		"writeResolutionFormat":              func(b *strings.Builder) { writeResolutionFormat(b, false) },
		"writeResolutionFormat/explain":      func(b *strings.Builder) { writeResolutionFormat(b, true) },
		"writeCodeChangeResolutionFormat":    writeCodeChangeResolutionFormat,
		"writeReplyResolutionFormat":         func(b *strings.Builder) { writeReplyResolutionFormat(b, false) },
		"writeReplyResolutionFormat/explain": func(b *strings.Builder) { writeReplyResolutionFormat(b, true) },
	} {
		var b strings.Builder
		fn(&b)
		out := b.String()
		if !strings.Contains(out, `"rationale"`) {
			t.Errorf("prompt format should request a rationale field, got:\n%s", out)
		}
	}
}

func TestValidateResolutionReason_ReplyOnlyCannotClaimCodeChange(t *testing.T) {
	// The author replies "fixed" but pushed nothing that touches the finding.
	// The reply prompt shows no diff, so a code_change verdict rests on the
	// author's word alone and must not resolve the thread.
	output := "```json\n{\"resolved\": true, \"reason\": \"code_change\"}\n```"
	parsed := parseThreadResolution(output, 0)

	res := validateResolutionReason(parsed, TriageHasReply)
	if res.Resolved || res.Reason != "" {
		t.Errorf("reply-only code_change should be rejected, got %+v", res)
	}
	// With resolve_accepted_replies the rationale of a rejected reply is
	// posted to the author: it must explain the rejection, not carry the
	// evaluator's case for resolving.
	withRationale := parsed
	withRationale.Rationale = "The author says the query was fixed."
	if res := validateResolutionReason(withRationale, TriageHasReply); res.Rationale != replyOnlyCodeChangeRationale {
		t.Errorf("rejected code_change keeps the evaluator's rationale: %q", res.Rationale)
	}

	// The same verdict stands when the code at the finding did change.
	for _, class := range []ThreadClassification{TriageCodeChanged, TriageCodeChangedReply, TriageCrossFileChange} {
		if res := validateResolutionReason(parsed, class); !res.Resolved || res.Reason != "code_change" {
			t.Errorf("class %d: code_change should be accepted, got %+v", class, res)
		}
	}

	// Reply-only threads still resolve by the reply's own reasons.
	for _, reason := range []string{"dismissed", "acknowledged", "rebutted"} {
		in := ThreadResolution{Resolved: true, Reason: reason}
		if res := validateResolutionReason(in, TriageHasReply); !res.Resolved || res.Reason != reason {
			t.Errorf("reply-only %s should be accepted, got %+v", reason, res)
		}
	}
}

// After a resolve step failed, a re-run on the same head has no new changes.
// The thread the earlier run judged fixed must be resolved, not skipped and
// left blocking, and a later reply must not reopen the judgment.
func TestClassifyThreads_PreviouslyFixedIsResolvedOnRerun(t *testing.T) {
	verdict := ThreadReply{Author: "bot", Body: fixedMarker + "\nFixed by a code change — resolving."}
	cases := []struct {
		name    string
		replies []ThreadReply
		want    ThreadClassification
	}{
		{"same head, no new changes", []ThreadReply{verdict}, TriagePreviouslyFixed},
		{"author replied after the verdict", []ThreadReply{verdict, {Author: "user", Body: "Fixed in 1a2b3c, see the test."}}, TriagePreviouslyFixed},
		{"marker typed by someone else", []ThreadReply{{Author: "user", Body: fixedMarker}}, TriageHasReply},
	}
	for _, c := range cases {
		threads := []ReviewThread{{Path: "a.go", Line: 10, Author: "bot", Replies: c.replies}}
		triaged := ClassifyThreads(threads, "", "", "bot", []string{"a.go"}, nil)
		if triaged[0].Class != c.want {
			t.Errorf("%s: class = %d, want %d", c.name, triaged[0].Class, c.want)
		}
	}
	// Without a known bot login no reply can be trusted.
	threads := []ReviewThread{{Path: "a.go", Line: 10, Replies: []ThreadReply{verdict}}}
	if got := ClassifyThreads(threads, "", "", "", []string{"a.go"}, nil)[0].Class; got == TriagePreviouslyFixed {
		t.Error("an unknown bot login must not trust the marker")
	}
	if n := countNonSkipped([]TriagedThread{{Class: TriagePreviouslyFixed}}); n != 0 {
		t.Errorf("countNonSkipped = %d; a previously fixed thread needs no model call", n)
	}
}

// Regression (thetechfx/techfx#125): the author fixed a finding in another
// file, pushed, and replied saying so. The finding's lines were untouched, so
// the thread was judged on the reply alone, with no diff, and a real fix
// could never resolve it.
func TestClassifyThreads_ReplyWithCrossFilePushSeesFullDiff(t *testing.T) {
	threads := []ReviewThread{{
		Path: ".cargo/config.toml", Line: 4, Body: "walk will descend into target/",
		Replies: []ThreadReply{{Author: "user", Body: "walk now skips target (commit 405c243)"}},
	}}
	fix := "diff --git a/check.test.mjs b/check.test.mjs\n--- a/check.test.mjs\n+++ b/check.test.mjs\n@@ -1,1 +1,2 @@\n+if (rel === 'target') continue\n"
	pr := "diff --git a/.cargo/config.toml b/.cargo/config.toml\n--- /dev/null\n+++ b/.cargo/config.toml\n@@ -0,0 +1,4 @@\n+[build]\n" + fix
	files := []string{".cargo/config.toml", "check.test.mjs"}

	tt := ClassifyThreads(threads, fix, pr, "bot", files, nil)[0]
	if tt.Class != TriageCodeChangedReply {
		t.Fatalf("expected TriageCodeChangedReply, got %d", tt.Class)
	}
	prompt := BuildPerThreadPrompt(tt, nil)
	if !strings.Contains(prompt, "if (rel === 'target') continue") {
		t.Errorf("prompt should carry the full PR diff so the cross-file fix is visible, got:\n%s", prompt)
	}
	if res := validateResolutionReason(ThreadResolution{Resolved: true, Reason: "code_change"}, tt.Class); !res.Resolved {
		t.Error("a code_change verdict backed by the shown diff should resolve")
	}

	// No push since the last review: the reply alone is judged, as before.
	if got := ClassifyThreads(threads, "", pr, "bot", files, nil)[0].Class; got != TriageHasReply {
		t.Errorf("reply without new changes: class = %d, want TriageHasReply", got)
	}
}

func TestClassifyThreads_ReplyWithSameFilePushIsFileScoped(t *testing.T) {
	threads := []ReviewThread{{
		Path: "a.go", Line: 10, Body: "Issue",
		Replies: []ThreadReply{{Author: "user", Body: "fixed"}},
	}}
	diff := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -40,1 +40,1 @@\n-x\n+y\n" +
		"diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -1,1 +1,1 @@\n-p\n+q\n"

	tt := ClassifyThreads(threads, diff, diff, "bot", []string{"a.go", "b.go"}, nil)[0]
	if tt.Class != TriageCodeChangedReply {
		t.Fatalf("expected TriageCodeChangedReply, got %d", tt.Class)
	}
	if strings.Contains(tt.FileDiff, "b.go") || !strings.Contains(tt.FileDiff, "a.go") {
		t.Errorf("same-file push should give the file-scoped diff, got:\n%s", tt.FileDiff)
	}
}
