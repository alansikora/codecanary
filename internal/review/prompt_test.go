package review

import (
	"strings"
	"testing"
)

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
