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