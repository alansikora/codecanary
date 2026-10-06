package review

import "testing"

// The README suggests pinning the current Sonnet with provider: claude. Any
// well-formed Claude model ID passes (including ones released after this
// build); flag-shaped values, other vendors' IDs and malformed IDs don't.
func TestValidateClaudeModels(t *testing.T) {
	for _, m := range []string{"sonnet", "opus", "haiku", "fable", "claude-sonnet-5-5", "claude-opus-5-5", "claude-fable-5-1", "claude-haiku-4-5", "claude-sonnet-4-6", "claude-sonnet-9", "claude-haiku-4-5-20251001"} {
		if err := validateClaude(&ModelConfig{Model: m}); err != nil {
			t.Errorf("validateClaude(%q) = %v, want nil", m, err)
		}
	}
	for _, m := range []string{"gpt-5.4", "--dangerously-skip-permissions", "claude-sonnet-5-5 --tools Bash", "Claude-Sonnet", "claude-", "anthropic/claude-sonnet-5-5"} {
		if err := validateClaude(&ModelConfig{Model: m}); err == nil {
			t.Errorf("validateClaude(%q) = nil, want an error", m)
		}
	}
}
