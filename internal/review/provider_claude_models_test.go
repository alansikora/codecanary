package review

import "testing"

// The README suggests pinning the current Sonnet with provider: claude; every
// current model ID and alias must pass validation, and junk must not.
func TestValidateClaudeModels(t *testing.T) {
	for _, m := range []string{"sonnet", "opus", "haiku", "fable", "claude-sonnet-5-5", "claude-opus-5-5", "claude-fable-5-1", "claude-haiku-4-5", "claude-sonnet-4-6"} {
		if err := validateClaude(&ModelConfig{Model: m}); err != nil {
			t.Errorf("validateClaude(%q) = %v, want nil", m, err)
		}
	}
	for _, m := range []string{"gpt-5.4", "--dangerously-skip-permissions", "claude-sonnet-9"} {
		if err := validateClaude(&ModelConfig{Model: m}); err == nil {
			t.Errorf("validateClaude(%q) = nil, want an error", m)
		}
	}
}
