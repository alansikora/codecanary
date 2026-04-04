package review

import (
	"strings"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"abcd", 1},
		{"ab", 0},          // 2/4 = 0 (integer division)
		{"abcdefgh", 2},    // 8/4 = 2
		{strings.Repeat("x", 1000), 250},
	}
	for _, tt := range tests {
		got := estimateTokens(tt.input)
		if got != tt.want {
			t.Errorf("estimateTokens(%d chars) = %d, want %d", len(tt.input), got, tt.want)
		}
	}
}

func TestLookupContextWindow(t *testing.T) {
	tests := []struct {
		model string
		want  int
	}{
		{"claude-sonnet-4-6", 200_000},
		{"claude-opus-4-6-20260101", 200_000},
		{"claude-haiku-4-5-20251001", 200_000},
		{"gpt-4o-2024-08-06", 128_000},
		{"gpt-4.1-mini", 1_047_576},
		{"gpt-5.4", 1_000_000},
		// Catch-all entries cover unknown Claude/GPT variants.
		{"claude-future-model", 200_000},
		{"gpt-99", 128_000},
		{"unknown-model-xyz", defaultContextWindow}, // fallback
	}
	for _, tt := range tests {
		got := lookupContextWindow(tt.model)
		if got != tt.want {
			t.Errorf("lookupContextWindow(%q) = %d, want %d", tt.model, got, tt.want)
		}
	}
}

func TestFitToContextWindow_UnderBudget(t *testing.T) {
	fileContents := map[string]string{"a.go": "package main"}
	files := []string{"a.go"}
	diff := "+package main"

	buildFn := func(fc map[string]string, d string) string {
		return "small prompt: " + d
	}
	initial := buildFn(fileContents, diff)

	prompt, trimmed := fitToContextWindow(initial, buildFn, fileContents, files, diff, 100_000)
	if trimmed {
		t.Error("expected no trimming for prompt under budget")
	}
	if !strings.Contains(prompt, diff) {
		t.Error("expected prompt to contain original diff")
	}
}

func TestFitToContextWindow_SkipsRebuild_WhenUnderBudget(t *testing.T) {
	// Verify that buildFn is never called when the initial prompt fits.
	called := false
	buildFn := func(fc map[string]string, d string) string {
		called = true
		return "rebuilt"
	}

	prompt, trimmed := fitToContextWindow("small", buildFn, nil, nil, "", 100_000)
	if trimmed {
		t.Error("expected no trimming")
	}
	if called {
		t.Error("buildFn should not be called when prompt already fits")
	}
	if prompt != "small" {
		t.Errorf("expected original prompt, got %q", prompt)
	}
}

func TestFitToContextWindow_DropFiles(t *testing.T) {
	// Create file contents that push the prompt over budget.
	largeContent := strings.Repeat("x", 4000) // ~1000 tokens
	smallContent := strings.Repeat("y", 400)  // ~100 tokens
	fileContents := map[string]string{
		"large.go": largeContent,
		"small.go": smallContent,
	}
	files := []string{"large.go", "small.go"}
	diff := "+line"

	var lastFC map[string]string
	buildFn := func(fc map[string]string, d string) string {
		lastFC = fc
		var b strings.Builder
		b.WriteString("header\n")
		for _, f := range files {
			if c, ok := fc[f]; ok {
				b.WriteString(c)
			}
		}
		b.WriteString(d)
		return b.String()
	}
	initial := buildFn(fileContents, diff)

	// Budget that fits small.go + diff but not large.go.
	// header(7) + small(400) + diff(5) = 412 chars ≈ 103 tokens
	// header(7) + large(4000) + small(400) + diff(5) = 4412 chars ≈ 1103 tokens
	prompt, trimmed := fitToContextWindow(initial, buildFn, fileContents, files, diff, 200)
	if !trimmed {
		t.Error("expected trimming to occur")
	}
	if _, hasLarge := lastFC["large.go"]; hasLarge {
		t.Error("expected large.go to be dropped first")
	}
	if _, hasSmall := lastFC["small.go"]; !hasSmall {
		t.Error("expected small.go to be retained")
	}
	_ = prompt
}

func TestFitToContextWindow_TruncateDiff(t *testing.T) {
	// No file contents, just a huge diff.
	fileContents := map[string]string{}
	files := []string{}
	diff := strings.Repeat("+line\n", 10000) // ~60000 chars ≈ 15000 tokens

	buildFn := func(fc map[string]string, d string) string {
		return "header\n" + d
	}
	initial := buildFn(fileContents, diff)

	// Budget much smaller than the diff.
	prompt, trimmed := fitToContextWindow(initial, buildFn, fileContents, files, diff, 1000)
	if !trimmed {
		t.Error("expected trimming to occur")
	}
	if !strings.Contains(prompt, "[diff truncated to fit context window]") {
		t.Error("expected truncation marker in prompt")
	}
	if estimateTokens(prompt) > 1000 {
		t.Errorf("prompt still over budget: %d tokens", estimateTokens(prompt))
	}
}

func TestFitToContextWindow_TruncateDiff_Terminates(t *testing.T) {
	// Regression: verify the loop terminates even when base template
	// alone exceeds budget (no amount of diff trimming can help).
	bigHeader := strings.Repeat("H", 8000) // ~2000 tokens
	fileContents := map[string]string{}
	files := []string{}
	diff := strings.Repeat("+line\n", 1000)

	buildFn := func(fc map[string]string, d string) string {
		return bigHeader + d
	}
	initial := buildFn(fileContents, diff)

	// Budget smaller than the header alone — loop must still terminate.
	prompt, trimmed := fitToContextWindow(initial, buildFn, fileContents, files, diff, 100)
	if !trimmed {
		t.Error("expected trimming to occur")
	}
	// The prompt will still be over budget but the function should return
	// without infinite-looping.
	_ = prompt
}

func TestFitToContextWindow_DropFilesBeforeDiff(t *testing.T) {
	// Verify that files are dropped before diff is truncated.
	fileContents := map[string]string{
		"a.go": strings.Repeat("a", 2000),
	}
	files := []string{"a.go"}
	diff := "+small diff"

	buildFn := func(fc map[string]string, d string) string {
		var b strings.Builder
		for _, f := range files {
			if c, ok := fc[f]; ok {
				b.WriteString(c)
			}
		}
		b.WriteString(d)
		return b.String()
	}
	initial := buildFn(fileContents, diff)

	// Budget that fits the diff alone but not with file contents.
	// diff only: 11 chars ≈ 2 tokens
	// with file: 2011 chars ≈ 502 tokens
	prompt, trimmed := fitToContextWindow(initial, buildFn, fileContents, files, diff, 100)
	if !trimmed {
		t.Error("expected trimming")
	}
	// Should have dropped files, and the diff should be intact (not truncated).
	if strings.Contains(prompt, "[diff truncated") {
		t.Error("diff should not be truncated when dropping files is sufficient")
	}
}
