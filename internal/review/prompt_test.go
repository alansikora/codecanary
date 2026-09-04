package review

import (
	"strings"
	"testing"
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

	got := BuildIncrementalPrompt("diff", cfg, nil, 1, 0, nil, files, nil, nil, "")

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
		nil, nil, 1508, 0, nil, []string{"foo"}, resolved, nil, "",
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
		nil, nil, 1, 0, nil, []string{"foo"}, nil, nil, "",
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

	prompt := BuildIncrementalPrompt("", nil, nil, 1, 0, nil, nil, resolved, nil, "")

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
	prompt := BuildIncrementalPrompt("", nil, nil, 1, 0, nil, nil, resolved, nil, "")

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

// The Lifecycle Considerations section is what catches "behaves on PR
// branch but not on main" bugs (push triggers scoped to the PR branch,
// concurrency keys keyed on github.ref, etc.). It must appear in both
// full and incremental prompts and must remind the LLM to anchor
// findings to diff lines.
func TestBuildPrompt_IncludesLifecycleSection(t *testing.T) {
	pr := &PRData{Number: 1, Title: "t", Files: []string{"a.go"}, Diff: "diff"}
	got := BuildPrompt(pr, nil, 0, nil)

	mustContain := []string{
		"## Lifecycle Considerations",
		"behave differently between the PR branch",
		"github.ref",
		"anchor your finding's `file` and `line`",
	}
	for _, s := range mustContain {
		if !strings.Contains(got, s) {
			t.Errorf("BuildPrompt missing expected lifecycle snippet %q", s)
		}
	}
}

// The PR Description Cross-Check section lets the reviewer flag
// claims in the PR body that the diff contradicts (cost caps, "this
// PR does NOT do X", etc.). It must appear when a body is provided
// and be omitted when there isn't one.
func TestBuildPrompt_IncludesPRBodyCrossCheckWhenBodyPresent(t *testing.T) {
	pr := &PRData{
		Number: 1,
		Title:  "t",
		Body:   "Cost cap: ~$2-4/run.",
		Files:  []string{"a.go"},
		Diff:   "diff",
	}
	got := BuildPrompt(pr, nil, 0, nil)

	mustContain := []string{
		"## PR Description Cross-Check",
		"`<pr-body>`",
		"contradicts a specific factual claim",
		"anchor your finding's `file` and `line`",
	}
	for _, s := range mustContain {
		if !strings.Contains(got, s) {
			t.Errorf("BuildPrompt missing expected cross-check snippet %q", s)
		}
	}
}

func TestBuildPrompt_OmitsPRBodyCrossCheckWhenBodyAbsent(t *testing.T) {
	pr := &PRData{Number: 1, Title: "t", Files: []string{"a.go"}, Diff: "diff"} // no Body
	got := BuildPrompt(pr, nil, 0, nil)

	if strings.Contains(got, "## PR Description Cross-Check") {
		t.Error("cross-check section should be omitted when PR body is empty")
	}
}

func TestBuildIncrementalPrompt_IncludesPRBodyCrossCheckWhenBodyPresent(t *testing.T) {
	got := BuildIncrementalPrompt("diff --git a/foo b/foo\n", nil, nil, 1, 0, nil, []string{"foo"}, nil, nil, "Cost cap: ~$2-4/run.")

	mustContain := []string{
		"## Pull Request #1",
		"<pr-body>",
		"## PR Description Cross-Check",
		"contradicts a specific factual claim",
	}
	for _, s := range mustContain {
		if !strings.Contains(got, s) {
			t.Errorf("BuildIncrementalPrompt missing expected cross-check snippet %q", s)
		}
	}
	// The header must precede the body, otherwise it doesn't frame it.
	if i, j := strings.Index(got, "## Pull Request #1"), strings.Index(got, "<pr-body>"); i > j {
		t.Error("PR header should be emitted before the pr-body block")
	}
}

// Local reviews carry no PR number; the body still needs a label so the
// cross-check section has framing to refer back to.
func TestBuildIncrementalPrompt_PRBodyHeaderWithoutPRNumber(t *testing.T) {
	got := BuildIncrementalPrompt("diff --git a/foo b/foo\n", nil, nil, 0, 0, nil, []string{"foo"}, nil, nil, "Cost cap: ~$2-4/run.")

	if !strings.Contains(got, "## Pull Request\n<pr-body>") {
		t.Error("BuildIncrementalPrompt should label the pr-body block even without a PR number")
	}
	if strings.Contains(got, "## Pull Request #0") {
		t.Error("BuildIncrementalPrompt should not emit a zero PR number")
	}
}

func TestBuildIncrementalPrompt_OmitsPRBodyAndCrossCheckWhenEmpty(t *testing.T) {
	got := BuildIncrementalPrompt("diff --git a/foo b/foo\n", nil, nil, 1, 0, nil, []string{"foo"}, nil, nil, "")

	if strings.Contains(got, "<pr-body>") {
		t.Error("pr-body tag should be omitted when body is empty")
	}
	if strings.Contains(got, "## PR Description Cross-Check") {
		t.Error("cross-check section should be omitted when body is empty")
	}
}

func TestBuildIncrementalPrompt_IncludesLifecycleSection(t *testing.T) {
	got := BuildIncrementalPrompt("diff --git a/foo b/foo\n", nil, nil, 1, 0, nil, []string{"foo"}, nil, nil, "")

	mustContain := []string{
		"## Lifecycle Considerations",
		"behave differently between the PR branch",
		"github.ref",
		"anchor your finding's `file` and `line`",
	}
	for _, s := range mustContain {
		if !strings.Contains(got, s) {
			t.Errorf("BuildIncrementalPrompt missing expected lifecycle snippet %q", s)
		}
	}
}
