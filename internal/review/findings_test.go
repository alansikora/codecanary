package review

import (
	"testing"
)

func boolPtr(v bool) *bool { return &v }

func TestParseFindingsWithEmbeddedCodeFence(t *testing.T) {
	// Reproduces the bug where a suggestion containing ```bash causes the
	// non-greedy regex to match too early, truncating the JSON.
	output := "Here are the findings:\n\n```json\n[\n  {\n    \"id\": \"missing-check\",\n    \"file\": \"bin/setup\",\n    \"line\": 139,\n    \"severity\": \"warning\",\n    \"title\": \"Missing CLI check\",\n    \"description\": \"The check is missing.\",\n    \"suggestion\": \"Add a check:\\n\\n```bash\\nif ! command -v op &> /dev/null; then\\n  echo \\\"Error\\\" >&2\\n  exit 1\\nfi\\n```\\n\\nThis fixes it.\",\n    \"fix_ref\": \"1121-9\",\n    \"actionable\": true\n  }\n]\n```\n"

	findings, err := ParseFindings(output)
	if err != nil {
		t.Fatalf("ParseFindings() error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].ID != "missing-check" {
		t.Errorf("expected ID 'missing-check', got %q", findings[0].ID)
	}
}

func TestParseFindingsCleanOutput(t *testing.T) {
	// Normal case without embedded fences — regex should still work.
	output := "```json\n[\n  {\n    \"id\": \"test\",\n    \"file\": \"main.go\",\n    \"line\": 1,\n    \"severity\": \"warning\",\n    \"title\": \"Test\",\n    \"description\": \"Desc\",\n    \"fix_ref\": \"1-0\"\n  }\n]\n```\n"

	findings, err := ParseFindings(output)
	if err != nil {
		t.Fatalf("ParseFindings() error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
}

func TestParseFindingsEmptyArray(t *testing.T) {
	output := "```json\n[]\n```\n"
	findings, err := ParseFindings(output)
	if err != nil {
		t.Fatalf("ParseFindings() error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings, got %d", len(findings))
	}
}

func TestFilterNonActionable(t *testing.T) {
	tests := []struct {
		name      string
		findings  []Finding
		wantCount int
	}{
		{
			name: "actionable false is dropped",
			findings: []Finding{
				{ID: "a", Actionable: boolPtr(false)},
			},
			wantCount: 0,
		},
		{
			name: "actionable true is kept",
			findings: []Finding{
				{ID: "a", Actionable: boolPtr(true)},
			},
			wantCount: 1,
		},
		{
			name: "actionable nil is kept",
			findings: []Finding{
				{ID: "a", Actionable: nil},
			},
			wantCount: 1,
		},
		{
			name: "mixed findings",
			findings: []Finding{
				{ID: "keep-1", Actionable: boolPtr(true)},
				{ID: "drop", Actionable: boolPtr(false)},
				{ID: "keep-2", Actionable: nil},
			},
			wantCount: 2,
		},
		{
			name:      "empty input",
			findings:  []Finding{},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterNonActionable(tt.findings)
			if len(got) != tt.wantCount {
				t.Errorf("FilterNonActionable() returned %d findings, want %d", len(got), tt.wantCount)
			}
		})
	}
}
