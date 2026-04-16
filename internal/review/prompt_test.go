package review

import (
	"testing"
)

func TestFilterRulesForFiles_NoPaths(t *testing.T) {
	rules := []Rule{
		{ID: "no-paths", Description: "always applies", Severity: "warning"},
	}
	files := []string{"internal/auth/auth.go", "cmd/main.go"}
	got := filterRulesForFiles(rules, files)
	if len(got) != 1 || got[0].ID != "no-paths" {
		t.Errorf("expected rule with no paths to always be included, got %v", got)
	}
}

func TestFilterRulesForFiles_NoPaths_EmptyFiles(t *testing.T) {
	rules := []Rule{
		{ID: "no-paths", Description: "always applies", Severity: "warning"},
	}
	got := filterRulesForFiles(rules, nil)
	if len(got) != 1 {
		t.Errorf("expected all rules returned when files is empty, got %v", got)
	}
}

func TestFilterRulesForFiles_WithPaths_Matching(t *testing.T) {
	rules := []Rule{
		{ID: "auth-rule", Description: "auth only", Severity: "bug", Paths: []string{"internal/auth/**"}},
	}
	files := []string{"internal/auth/auth.go", "cmd/main.go"}
	got := filterRulesForFiles(rules, files)
	if len(got) != 1 || got[0].ID != "auth-rule" {
		t.Errorf("expected auth-rule to be included when matching file is present, got %v", got)
	}
}

func TestFilterRulesForFiles_WithPaths_NotMatching(t *testing.T) {
	rules := []Rule{
		{ID: "auth-rule", Description: "auth only", Severity: "bug", Paths: []string{"internal/auth/**"}},
	}
	files := []string{"cmd/main.go", "internal/review/prompt.go"}
	got := filterRulesForFiles(rules, files)
	if len(got) != 0 {
		t.Errorf("expected auth-rule to be excluded when no matching file is present, got %v", got)
	}
}

func TestFilterRulesForFiles_OnlyExcludePaths_SomeExcluded(t *testing.T) {
	rules := []Rule{
		{ID: "no-test-rule", Description: "skip test files", Severity: "warning", ExcludePaths: []string{"**/*_test.go"}},
	}
	// One test file and one non-test file — rule applies because a non-excluded file exists.
	files := []string{"internal/review/prompt_test.go", "internal/review/prompt.go"}
	got := filterRulesForFiles(rules, files)
	if len(got) != 1 || got[0].ID != "no-test-rule" {
		t.Errorf("expected rule to be included when at least one non-excluded file exists, got %v", got)
	}
}

func TestFilterRulesForFiles_OnlyExcludePaths_AllExcluded(t *testing.T) {
	rules := []Rule{
		{ID: "no-test-rule", Description: "skip test files", Severity: "warning", ExcludePaths: []string{"**/*_test.go"}},
	}
	// Only test files — rule is excluded.
	files := []string{"internal/review/prompt_test.go", "internal/review/config_test.go"}
	got := filterRulesForFiles(rules, files)
	if len(got) != 0 {
		t.Errorf("expected rule to be excluded when all files are excluded, got %v", got)
	}
}

func TestFilterRulesForFiles_PathsAndExcludePaths_SomeMatchSurvive(t *testing.T) {
	rules := []Rule{
		{
			ID:           "auth-non-test",
			Description:  "auth files, excluding tests",
			Severity:     "bug",
			Paths:        []string{"internal/auth/**"},
			ExcludePaths: []string{"**/*_test.go"},
		},
	}
	// One matching auth file (not a test) and one auth test file — rule applies
	// because internal/auth/auth.go matches Paths and is not excluded.
	files := []string{"internal/auth/auth.go", "internal/auth/auth_test.go"}
	got := filterRulesForFiles(rules, files)
	if len(got) != 1 || got[0].ID != "auth-non-test" {
		t.Errorf("expected rule included when a matching non-excluded file exists, got %v", got)
	}
}

func TestFilterRulesForFiles_PathsAndExcludePaths_AllMatchExcluded(t *testing.T) {
	rules := []Rule{
		{
			ID:           "auth-non-test",
			Description:  "auth files, excluding tests",
			Severity:     "bug",
			Paths:        []string{"internal/auth/**"},
			ExcludePaths: []string{"**/*_test.go"},
		},
	}
	// Only auth test files — all matching Paths files are also excluded.
	files := []string{"internal/auth/auth_test.go", "cmd/main.go"}
	got := filterRulesForFiles(rules, files)
	if len(got) != 0 {
		t.Errorf("expected rule excluded when all Paths-matching files are also excluded, got %v", got)
	}
}

func TestFilterRulesForFiles_EmptyFiles_ReturnsAll(t *testing.T) {
	rules := []Rule{
		{ID: "rule-a", Paths: []string{"internal/**"}, Severity: "warning"},
		{ID: "rule-b", ExcludePaths: []string{"**/*_test.go"}, Severity: "bug"},
		{ID: "rule-c", Severity: "nitpick"},
	}
	got := filterRulesForFiles(rules, []string{})
	if len(got) != len(rules) {
		t.Errorf("expected all %d rules returned for empty files, got %d", len(rules), len(got))
	}
}

func TestFilterRulesForFiles_MultipleRules_MixedResults(t *testing.T) {
	rules := []Rule{
		{ID: "always", Severity: "warning"},
		{ID: "auth-only", Severity: "bug", Paths: []string{"internal/auth/**"}},
		{ID: "no-tests", Severity: "nitpick", ExcludePaths: []string{"**/*_test.go"}},
	}
	files := []string{"cmd/main.go"} // no auth files, not a test file
	got := filterRulesForFiles(rules, files)
	if len(got) != 2 {
		t.Errorf("expected 2 rules (always + no-tests), got %d: %v", len(got), got)
	}
	ids := make(map[string]bool, len(got))
	for _, r := range got {
		ids[r.ID] = true
	}
	if !ids["always"] || !ids["no-tests"] {
		t.Errorf("expected 'always' and 'no-tests' to be included, got %v", ids)
	}
}
