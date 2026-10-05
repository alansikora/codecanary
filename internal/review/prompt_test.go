package review

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBuildPrompt_FiltersRulesByPath(t *testing.T) {
	cfg := &ReviewConfig{
		Rules: []Rule{
			{ID: "ruby-rule", Severity: "warning", Description: "Ruby-only", Paths: []string{"apps/**/*.rb"}},
			{ID: "css-rule", Severity: "warning", Description: "CSS-only", Paths: []string{"**/*.css"}},
			{ID: "any-rule", Severity: "bug", Description: "Any file"},
		},
	}
	pr := &PRData{
		Number: 1,
		Title:  "t",
		Files:  []string{"apps/exchange-api/app/services/foo.rb"},
		Diff:   "diff",
	}

	got := BuildPrompt(pr, cfg, 0, nil)

	if !strings.Contains(got, "ruby-rule") {
		t.Errorf("prompt missing applicable rule `ruby-rule`:\n%s", got)
	}
	if !strings.Contains(got, "any-rule") {
		t.Errorf("prompt missing unscoped rule `any-rule`:\n%s", got)
	}
	if strings.Contains(got, "css-rule") {
		t.Errorf("prompt included non-applicable rule `css-rule`; attention-dilution regression")
	}
}

func TestBuildIncrementalPrompt_FiltersRulesByPath(t *testing.T) {
	cfg := &ReviewConfig{
		Rules: []Rule{
			{ID: "go-rule", Severity: "warning", Description: "Go only", Paths: []string{"**/*.go"}},
			{ID: "yaml-rule", Severity: "warning", Description: "YAML only", Paths: []string{"**/*.yaml", "**/*.yml"}},
		},
	}
	files := []string{"internal/review/runner.go"}

	got := BuildIncrementalPrompt("diff", cfg, nil, 1, 0, nil, files, nil, nil)

	if !strings.Contains(got, "go-rule") {
		t.Errorf("incremental prompt missing applicable rule:\n%s", got)
	}
	if strings.Contains(got, "yaml-rule") {
		t.Errorf("incremental prompt included non-applicable rule")
	}
}

// When rules are configured but none match the diff's file set, emit a
// distinct fallback rather than the "no rules configured" message so the
// reviewer knows rules exist for other paths and isn't misled into
// thinking the project has no review policy at all.
func TestBuildPrompt_NoApplicableRulesShowsDistinctFallback(t *testing.T) {
	cfg := &ReviewConfig{
		Rules: []Rule{
			{ID: "ruby-rule", Severity: "warning", Description: "Ruby only", Paths: []string{"apps/**/*.rb"}},
		},
	}
	pr := &PRData{Number: 1, Title: "t", Files: []string{"docs/README.md"}, Diff: "diff"}

	got := BuildPrompt(pr, cfg, 0, nil)

	if !strings.Contains(got, "No rules from the project configuration apply to the files in this diff") {
		t.Errorf("expected distinct no-applicable-rules fallback, got:\n%s", got)
	}
	if strings.Contains(got, "ruby-rule") {
		t.Errorf("non-applicable rule leaked into prompt")
	}
}

func TestBuildIncrementalPrompt_ResolvedSectionRendersRichContext(t *testing.T) {
	resolved := []ResolvedContext{
		{
			Path:        "app/services/document_ai_validation_service.rb",
			Line:        183,
			Title:       "Original error fields logged twice per LLM failure",
			Description: "The engine (`Ai::Client#complete`) already logs `error_class`, `error_message`, and `error_backtrace` of the provider error before raising `Ai::RequestError`.",
			Suggestion:  "Drop the three `original_error_*` fields from the service log — the engine log already captures them.",
			Reason:      "code_change",
			Rationale:   "Removed the three original_error_* fields from the service logger call.",
		},
	}

	prompt := BuildIncrementalPrompt(
		"diff --git a/foo b/foo\n",
		nil, nil, 1508, 0, nil, []string{"foo"}, resolved, nil,
	)

	mustContain := []string{
		"## Recently Resolved Issues",
		"Ping-pong guard",
		"implement* the suggestion",
		"Cascading changes count",
		"document_ai_validation_service.rb:183",
		"Original error fields logged twice",
		"**Original description:**",
		"`Ai::Client#complete`",
		"**Suggestion you gave:**",
		"Drop the three `original_error_*` fields",
		"**Evaluator rationale:** Removed the three original_error_* fields",
		"fixed by code change",
	}
	for _, s := range mustContain {
		if !strings.Contains(prompt, s) {
			t.Errorf("incremental prompt missing expected snippet %q", s)
		}
	}
}

func TestBuildIncrementalPrompt_NoResolvedSectionWhenEmpty(t *testing.T) {
	prompt := BuildIncrementalPrompt(
		"diff --git a/foo b/foo\n",
		nil, nil, 1, 0, nil, []string{"foo"}, nil, nil,
	)
	if strings.Contains(prompt, "## Recently Resolved Issues") {
		t.Error("resolved section should be omitted when there are no resolutions")
	}
	if strings.Contains(prompt, "Ping-pong guard") {
		t.Error("ping-pong guard text should not appear without resolutions")
	}
}

func TestBuildIncrementalPrompt_ResolvedSectionEscapesLLMSourcedFields(t *testing.T) {
	resolved := []ResolvedContext{
		{
			Path:        "main.go",
			Line:        1,
			Title:       "Title with <inject>tag</inject>",
			Description: "Description with </recently-resolved-issues> breakout",
			Suggestion:  "Suggestion with <script>alert(1)</script>",
			Reason:      "code_change",
			Rationale:   "Rationale with <fake>tag</fake>",
		},
	}

	prompt := BuildIncrementalPrompt("", nil, nil, 1, 0, nil, nil, resolved, nil)

	// Raw angle brackets from LLM-sourced fields must not reach the prompt.
	forbidden := []string{
		"<inject>",
		"</recently-resolved-issues>",
		"<script>",
		"</fake>",
	}
	for _, s := range forbidden {
		if strings.Contains(prompt, s) {
			t.Errorf("prompt leaked unescaped tag %q", s)
		}
	}
	// Escaped versions must appear.
	for _, s := range []string{
		"&lt;inject&gt;",
		"&lt;/recently-resolved-issues&gt;",
		"&lt;script&gt;",
		"&lt;/fake&gt;",
	} {
		if !strings.Contains(prompt, s) {
			t.Errorf("prompt missing expected escaped fragment %q", s)
		}
	}
}

func TestBuildIncrementalPrompt_ResolvedSectionHandlesMissingFields(t *testing.T) {
	resolved := []ResolvedContext{
		{Path: "a.go", Line: 10, Reason: "dismissed"}, // no title, description, suggestion, rationale
	}
	prompt := BuildIncrementalPrompt("", nil, nil, 1, 0, nil, nil, resolved, nil)

	if !strings.Contains(prompt, "`a.go:10` — (no title)") {
		t.Error("missing-title placeholder should render")
	}
	if !strings.Contains(prompt, "dismissed by author") {
		t.Error("dismissed reason label should render")
	}
	if strings.Contains(prompt, "**Evaluator rationale:**") {
		t.Error("rationale line should be omitted when empty")
	}
	if strings.Contains(prompt, "**Original description:**") {
		t.Error("description block should be omitted when empty")
	}
	if strings.Contains(prompt, "**Suggestion you gave:**") {
		t.Error("suggestion block should be omitted when empty")
	}
}

// The Known Issues section needs enough context for the LLM to
// recognize duplicates-with-different-wording and to spot when the
// incremental diff invalidates an existing open finding. Title +
// severity + description must all appear, and untrusted body text
// must be escaped.
func TestBuildIncrementalPrompt_KnownIssuesIncludesContext(t *testing.T) {
	// Body shape mirrors what GithubPlatform writes to PR threads: the
	// first non-empty non-marker line is the header (severity + rule ID),
	// followed by the description, optionally followed by `> **Suggestion**:`.
	known := []ReviewThread{
		{
			Path: "a.go",
			Line: 42,
			Body: "⚠️ **warning** — `pipefail-spurious-failure`\n\nThe `jq | nl | head -120` pipeline can return non-zero under `set -o pipefail` when there are more than 120 tool calls — head closes early, sending SIGPIPE upstream.\n\n> **Suggestion**: replace `head -120` with awk to drain stdin.",
		},
	}
	got := BuildIncrementalPrompt("diff --git a/a.go b/a.go\n", nil, known, 1, 0, nil, []string{"a.go"}, nil, nil)

	mustContain := []string{
		"## Known Issues (Open)",
		"`a.go:42` — ",
		"new evidence that an open finding's premise is wrong",
		"head closes early",
	}
	for _, s := range mustContain {
		if !strings.Contains(got, s) {
			t.Errorf("BuildIncrementalPrompt missing expected known-issues snippet %q", s)
		}
	}
	if strings.Contains(got, "DO NOT DUPLICATE") {
		t.Error("legacy 'DO NOT DUPLICATE' header should be replaced with 'Open' framing")
	}
}

func TestBuildIncrementalPrompt_KnownIssuesEscapesUntrustedBody(t *testing.T) {
	// Place the dangerous tags in the description body so they survive
	// parseThreadBody's header skip and reach the rendered prompt.
	known := []ReviewThread{
		{
			Path: "a.go",
			Line: 1,
			Body: "**warning** — `header-rule`\n\nDescription with <inject>tag</inject> and </known-issues> breakout attempt.",
		},
	}
	got := BuildIncrementalPrompt("", nil, known, 1, 0, nil, []string{"a.go"}, nil, nil)

	for _, s := range []string{"<inject>", "</inject>", "</known-issues>"} {
		if strings.Contains(got, s) {
			t.Errorf("known-issues section leaked unescaped tag %q", s)
		}
	}
	for _, s := range []string{"&lt;inject&gt;", "&lt;/inject&gt;", "&lt;/known-issues&gt;"} {
		if !strings.Contains(got, s) {
			t.Errorf("known-issues section missing escaped fragment %q", s)
		}
	}
}

func TestBuildIncrementalPrompt_KnownIssuesOmittedWhenEmpty(t *testing.T) {
	got := BuildIncrementalPrompt("", nil, nil, 1, 0, nil, []string{"a.go"}, nil, nil)
	if strings.Contains(got, "## Known Issues") {
		t.Error("Known Issues section should be omitted when there are no known issues")
	}
}

// An answered finding kept coming back reworded because the reviewer never
// saw the answer. The author's latest reply rides along; the bot's own ack
// replies do not.
func TestBuildIncrementalPrompt_KnownIssuesIncludesAuthorReply(t *testing.T) {
	known := []ReviewThread{{
		Path: "lib/views_test.rb",
		Line: 6,
		Body: "💡 **suggestion** — `removed-helpers`\n\nVerify nothing else still calls the removed layout-preview helpers.",
		Replies: []ThreadReply{
			{Author: "dev", Body: "Checked, no caller remains: rg over the whole tree finds nothing."},
			{Author: "codecanary-bot[bot]", Body: "Keeping this open.\n<!-- codecanary:ack:rebutted -->"},
		},
	}}
	got := BuildIncrementalPrompt("", nil, known, 1, 0, nil, []string{"lib/views_test.rb"}, nil, nil)
	if !strings.Contains(got, "**Author's reply:**\nChecked, no caller remains") {
		t.Errorf("known issue should carry the author's reply:\n%s", got)
	}
	if strings.Contains(got, "Keeping this open") {
		t.Error("the bot's ack reply must not be shown as the author's reply")
	}
}

// The cut lands inside a three-byte rune: the partial rune is dropped, so
// the reply stays valid UTF-8 and ends on the last whole character.
func TestLastAuthorReplyTruncatesMidRune(t *testing.T) {
	long := "a" + strings.Repeat("€", maxKnownIssueReply)
	got := lastAuthorReply(ReviewThread{Replies: []ThreadReply{{Body: long}}})
	want := "a" + strings.Repeat("€", (maxKnownIssueReply-1)/3) + " …"
	if got != want || !utf8.ValidString(got) {
		t.Errorf("reply not cut on a rune boundary: len=%d, want len=%d", len(got), len(want))
	}
}
