package review

import (
	"strings"
	"testing"
)

func TestBuildFixAllPrompt_WithSuggestions(t *testing.T) {
	findings := []Finding{
		{
			ID:          "null-check",
			File:        "src/main.go",
			Line:        42,
			Severity:    "bug",
			Title:       "Missing nil check",
			Description: "The response may be nil.",
			Suggestion:  "Add a nil check before use.",
			FixRef:      "1-1",
		},
	}

	prompt := buildFixAllPrompt(findings)

	if !strings.Contains(prompt, "File: src/main.go, Line: 42") {
		t.Error("expected file and line reference")
	}
	if !strings.Contains(prompt, "**Issue (bug):** Missing nil check") {
		t.Error("expected severity and title")
	}
	if !strings.Contains(prompt, "Suggested fix: Add a nil check before use.") {
		t.Error("expected suggestion")
	}
}

func TestBuildFixAllPrompt_WithoutSuggestions(t *testing.T) {
	findings := []Finding{
		{
			ID:          "perf-issue",
			File:        "lib/cache.go",
			Line:        10,
			Severity:    "warning",
			Title:       "Slow loop",
			Description: "This loop is O(n^2).",
			FixRef:      "1-1",
		},
	}

	prompt := buildFixAllPrompt(findings)

	if strings.Contains(prompt, "Suggested fix:") {
		t.Error("should not contain suggestion line when suggestion is empty")
	}
	if !strings.Contains(prompt, "This loop is O(n^2).") {
		t.Error("expected description")
	}
}

func TestFormatReviewBody_ContainsFixAll(t *testing.T) {
	result := &ReviewResult{
		PRNumber: 42,
		Findings: []Finding{
			{
				ID:       "test-id",
				File:     "a.go",
				Line:     1,
				Severity: "warning",
				Title:    "Test",
				Description: "Test desc",
				FixRef:   "42-1",
			},
		},
	}

	body := FormatReviewBody(result, func(f Finding) bool { return true })

	if !strings.Contains(body, "<details>") {
		t.Error("expected <details> block")
	}
	if !strings.Contains(body, "Fix all with AI") {
		t.Error("expected fix-all summary text")
	}
	if !strings.Contains(body, "File: a.go, Line: 1") {
		t.Error("expected finding in fix-all prompt")
	}
}

func TestFormatReviewBody_NoFindings_NoFixAll(t *testing.T) {
	result := &ReviewResult{
		PRNumber: 42,
		Findings: []Finding{},
	}

	body := FormatReviewBody(result, func(f Finding) bool { return false })

	if strings.Contains(body, "<details>") {
		t.Error("should not contain <details> when there are no findings")
	}
}

func TestCodeFence(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"no backticks", "hello world", "```"},
		{"triple backticks", "some ```code``` here", "````"},
		{"quad backticks", "some ````code```` here", "`````"},
		{"mixed runs", "` `` ``` ```` ``", "`````"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := codeFence(tt.content)
			if got != tt.want {
				t.Errorf("codeFence(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}
