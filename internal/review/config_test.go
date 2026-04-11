package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEffectiveTimeout_Default(t *testing.T) {
	cfg := &ReviewConfig{}
	if got := cfg.EffectiveTimeout(); got != 0 {
		t.Errorf("EffectiveTimeout() = %v, want 0 (provider chooses default)", got)
	}
}

func TestEffectiveTimeout_NilConfig(t *testing.T) {
	var cfg *ReviewConfig
	if got := cfg.EffectiveTimeout(); got != 0 {
		t.Errorf("EffectiveTimeout() on nil = %v, want 0 (provider chooses default)", got)
	}
}

func TestEffectiveTimeout_Custom(t *testing.T) {
	cfg := &ReviewConfig{TimeoutMins: 10}
	if got := cfg.EffectiveTimeout(); got != 10*time.Minute {
		t.Errorf("EffectiveTimeout() = %v, want 10m", got)
	}
}

func TestEffectiveMaxFileSize_Default(t *testing.T) {
	cfg := &ReviewConfig{}
	if got := cfg.EffectiveMaxFileSize(); got != 100*1024 {
		t.Errorf("EffectiveMaxFileSize() = %d, want %d", got, 100*1024)
	}
}

func TestEffectiveMaxFileSize_Custom(t *testing.T) {
	cfg := &ReviewConfig{MaxFileSize: 50000}
	if got := cfg.EffectiveMaxFileSize(); got != 50000 {
		t.Errorf("EffectiveMaxFileSize() = %d, want 50000", got)
	}
}

func TestEffectiveMaxTotalSize_Default(t *testing.T) {
	cfg := &ReviewConfig{}
	if got := cfg.EffectiveMaxTotalSize(); got != 500*1024 {
		t.Errorf("EffectiveMaxTotalSize() = %d, want %d", got, 500*1024)
	}
}

func TestEffectiveMaxTotalSize_Custom(t *testing.T) {
	cfg := &ReviewConfig{MaxTotalSize: 200000}
	if got := cfg.EffectiveMaxTotalSize(); got != 200000 {
		t.Errorf("EffectiveMaxTotalSize() = %d, want 200000", got)
	}
}

func TestValidate_ReviewModelRequired(t *testing.T) {
	cfg := &ReviewConfig{Provider: "anthropic", TriageModel: "claude-haiku-4-5-20251001"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing review_model")
	}
}

func TestValidate_TriageModelRequired(t *testing.T) {
	cfg := &ReviewConfig{Provider: "anthropic", ReviewModel: "claude-sonnet-4-6"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing triage_model")
	}
}

func TestValidate_ProviderRequired(t *testing.T) {
	cfg := &ReviewConfig{}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing provider")
	}
}

func TestValidate_InvalidProvider(t *testing.T) {
	cfg := &ReviewConfig{Provider: "gemini"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid provider")
	}
}

func TestValidate_InvalidModelForClaude(t *testing.T) {
	cfg := &ReviewConfig{Provider: "claude", ReviewModel: "gpt-4", TriageModel: "haiku"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid review_model on claude provider")
	}
	cfg = &ReviewConfig{Provider: "claude", ReviewModel: "sonnet", TriageModel: "invalid"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid triage_model on claude provider")
	}
}

func TestValidate_AnyModelForAnthropic(t *testing.T) {
	cfg := &ReviewConfig{Provider: "anthropic", ReviewModel: "claude-opus-4-6", TriageModel: "anything"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_ClaudeArgsBlockedReserved(t *testing.T) {
	reserved := []string{"--print", "--output-format", "--no-session-persistence", "--model", "--max-budget-usd", "--tools"}
	for _, arg := range reserved {
		cfg := &ReviewConfig{Provider: "claude", ReviewModel: "sonnet", TriageModel: "haiku", ClaudeArgs: []string{arg}}
		if err := cfg.Validate(); err == nil {
			t.Errorf("expected error for reserved arg %q", arg)
		}
		cfg2 := &ReviewConfig{Provider: "claude", ReviewModel: "sonnet", TriageModel: "haiku", ClaudeArgs: []string{arg + "=somevalue"}}
		if err := cfg2.Validate(); err == nil {
			t.Errorf("expected error for reserved arg %q (=value form)", arg)
		}
	}
}

func TestValidate_ClaudeArgsAllowed(t *testing.T) {
	cfg := &ReviewConfig{
		Provider: "claude", ReviewModel: "sonnet", TriageModel: "haiku",
		ClaudeArgs: []string{"--mcp-config=/path/to/config.json"},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_ClaudeArgsPositionalRejected(t *testing.T) {
	cfg := &ReviewConfig{
		Provider: "claude", ReviewModel: "sonnet", TriageModel: "haiku",
		ClaudeArgs: []string{"--mcp-config", "/path/to/config.json"},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for positional arg in claude_args")
	}
}

func TestValidate_ClaudeArgsNonClaudeProvider(t *testing.T) {
	cfg := &ReviewConfig{
		Provider: "anthropic", ReviewModel: "claude-sonnet-4-6", TriageModel: "claude-haiku-4-5-20251001",
		ClaudeArgs: []string{"--mcp-config", "/path/to/config.json"},
	}
	// Should not error — only warns to stderr.
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error for non-claude provider with claude_args: %v", err)
	}
}

func TestValidate_ClaudePathNonClaudeProvider(t *testing.T) {
	cfg := &ReviewConfig{
		Provider: "anthropic", ReviewModel: "claude-sonnet-4-6", TriageModel: "claude-haiku-4-5-20251001",
		ClaudePath: "/usr/local/bin/claude-beta",
	}
	// Should not error — only warns to stderr.
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error for non-claude provider with claude_path: %v", err)
	}
}

func TestValidate_ClaudePathCustom(t *testing.T) {
	cfg := &ReviewConfig{
		Provider: "claude", ReviewModel: "sonnet", TriageModel: "haiku",
		ClaudePath: "/usr/local/bin/claude-beta",
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_ValidCLIModels(t *testing.T) {
	for _, m := range []string{"haiku", "sonnet", "opus"} {
		cfg := &ReviewConfig{Provider: "claude", ReviewModel: m, TriageModel: m}
		if err := cfg.Validate(); err != nil {
			t.Errorf("unexpected error for model %q: %v", m, err)
		}
	}
}

func TestValidate_ValidProviders(t *testing.T) {
	models := map[string][2]string{
		"anthropic":  {"claude-sonnet-4-6", "claude-haiku-4-5-20251001"},
		"openai":     {"gpt-5.4", "gpt-5.4-mini"},
		"openrouter": {"anthropic/claude-sonnet-4-6", "anthropic/claude-haiku-4-5-20251001"},
		"claude":     {"sonnet", "haiku"},
	}
	for _, p := range []string{"anthropic", "openai", "openrouter", "claude"} {
		m := models[p]
		cfg := &ReviewConfig{Provider: p, ReviewModel: m[0], TriageModel: m[1]}
		if err := cfg.Validate(); err != nil {
			t.Errorf("unexpected error for provider %q: %v", p, err)
		}
	}
}

func TestLoadConfig_BothModels(t *testing.T) {
	// Verify the ModelConfig type exists and can be constructed from ReviewConfig fields.
	cfg := &ReviewConfig{
		Provider:    "anthropic",
		ReviewModel: "claude-sonnet-4-6",
		TriageModel: "claude-haiku-4-5-20251001",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	mc := &ModelConfig{
		Provider: cfg.Provider,
		Model:    cfg.ReviewModel,
		APIBase:  cfg.APIBase,
	}
	if mc.Model != "claude-sonnet-4-6" {
		t.Errorf("ModelConfig.Model = %q, want %q", mc.Model, "claude-sonnet-4-6")
	}
}

func TestLoadConfig_WithReviewPolicy(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, ".codecanary")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}

	configYAML := `version: 1
provider: anthropic
review_model: claude-sonnet-4-6
triage_model: claude-haiku-4-5-20251001
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yml"), []byte(configYAML), 0644); err != nil {
		t.Fatalf("writing config.yml: %v", err)
	}

	reviewYAML := `rules:
  - id: test-rule
    description: "Test rule"
    severity: warning
context: |
  Test project context.
ignore:
  - "dist/**"
`
	if err := os.WriteFile(filepath.Join(configDir, "review.yml"), []byte(reviewYAML), 0644); err != nil {
		t.Fatalf("writing review.yml: %v", err)
	}

	cfg, err := LoadConfig(filepath.Join(configDir, "config.yml"))
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg.Rules) != 1 || cfg.Rules[0].ID != "test-rule" {
		t.Errorf("expected 1 rule with id 'test-rule', got %v", cfg.Rules)
	}
	if !strings.Contains(cfg.Context, "Test project") {
		t.Errorf("expected context from review.yml, got %q", cfg.Context)
	}
	if len(cfg.Ignore) != 1 || cfg.Ignore[0] != "dist/**" {
		t.Errorf("expected ignore from review.yml, got %v", cfg.Ignore)
	}
}

func TestLoadConfig_WithoutReviewPolicy(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, ".codecanary")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}

	configYAML := `version: 1
provider: anthropic
review_model: claude-sonnet-4-6
triage_model: claude-haiku-4-5-20251001
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yml"), []byte(configYAML), 0644); err != nil {
		t.Fatalf("writing config.yml: %v", err)
	}

	cfg, err := LoadConfig(filepath.Join(configDir, "config.yml"))
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg.Rules) != 0 {
		t.Errorf("expected no rules, got %v", cfg.Rules)
	}
}

func TestLoadConfig_ConfigYmlIgnoresReviewFields(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, ".codecanary")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}

	// config.yml with rules/context — should be ignored
	configYAML := `version: 1
provider: anthropic
review_model: claude-sonnet-4-6
triage_model: claude-haiku-4-5-20251001
context: "should be ignored"
rules:
  - id: ignored-rule
    description: "Should be ignored"
    severity: bug
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yml"), []byte(configYAML), 0644); err != nil {
		t.Fatalf("writing config.yml: %v", err)
	}

	cfg, err := LoadConfig(filepath.Join(configDir, "config.yml"))
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg.Rules) != 0 {
		t.Errorf("expected rules in config.yml to be ignored, got %v", cfg.Rules)
	}
	if cfg.Context != "" {
		t.Errorf("expected context in config.yml to be ignored, got %q", cfg.Context)
	}
}

func TestLoadConfig_WithReviewLocalPolicy(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, ".codecanary")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}

	configYAML := `version: 1
provider: anthropic
review_model: claude-sonnet-4-6
triage_model: claude-haiku-4-5-20251001
`
	reviewYAML := `rules:
  - id: team-rule
    description: "Team rule"
    severity: warning
context: |
  Team context.
ignore:
  - "dist/**"
`
	localYAML := `rules:
  - id: personal-rule
    description: "Personal rule"
    severity: nitpick
context: |
  Personal context.
ignore:
  - "docs/**"
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yml"), []byte(configYAML), 0644); err != nil {
		t.Fatalf("writing config.yml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "review.yml"), []byte(reviewYAML), 0644); err != nil {
		t.Fatalf("writing review.yml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "review.local.yml"), []byte(localYAML), 0644); err != nil {
		t.Fatalf("writing review.local.yml: %v", err)
	}

	cfg, err := LoadConfig(filepath.Join(configDir, "config.yml"))
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// Rules: should contain both team and personal rules, in order.
	if len(cfg.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d: %v", len(cfg.Rules), cfg.Rules)
	}
	if cfg.Rules[0].ID != "team-rule" {
		t.Errorf("expected first rule to be 'team-rule', got %q", cfg.Rules[0].ID)
	}
	if cfg.Rules[1].ID != "personal-rule" {
		t.Errorf("expected second rule to be 'personal-rule', got %q", cfg.Rules[1].ID)
	}

	// Context: should contain both contexts.
	if !strings.Contains(cfg.Context, "Team context") {
		t.Errorf("expected context to contain 'Team context', got %q", cfg.Context)
	}
	if !strings.Contains(cfg.Context, "Personal context") {
		t.Errorf("expected context to contain 'Personal context', got %q", cfg.Context)
	}

	// Ignore: should contain both patterns.
	if len(cfg.Ignore) != 2 {
		t.Fatalf("expected 2 ignore patterns, got %d: %v", len(cfg.Ignore), cfg.Ignore)
	}
	if cfg.Ignore[0] != "dist/**" {
		t.Errorf("expected first ignore to be 'dist/**', got %q", cfg.Ignore[0])
	}
	if cfg.Ignore[1] != "docs/**" {
		t.Errorf("expected second ignore to be 'docs/**', got %q", cfg.Ignore[1])
	}
}

func TestLoadConfig_WithReviewLocalPolicyOnly(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, ".codecanary")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}

	configYAML := `version: 1
provider: anthropic
review_model: claude-sonnet-4-6
triage_model: claude-haiku-4-5-20251001
`
	localYAML := `rules:
  - id: local-only-rule
    description: "Local only"
    severity: suggestion
context: |
  Local only context.
ignore:
  - "tmp/**"
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yml"), []byte(configYAML), 0644); err != nil {
		t.Fatalf("writing config.yml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "review.local.yml"), []byte(localYAML), 0644); err != nil {
		t.Fatalf("writing review.local.yml: %v", err)
	}

	cfg, err := LoadConfig(filepath.Join(configDir, "config.yml"))
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if len(cfg.Rules) != 1 || cfg.Rules[0].ID != "local-only-rule" {
		t.Errorf("expected 1 rule 'local-only-rule', got %v", cfg.Rules)
	}
	if !strings.Contains(cfg.Context, "Local only context") {
		t.Errorf("expected context from review.local.yml, got %q", cfg.Context)
	}
	if len(cfg.Ignore) != 1 || cfg.Ignore[0] != "tmp/**" {
		t.Errorf("expected ignore from review.local.yml, got %v", cfg.Ignore)
	}
}

func TestLoadConfig_ReviewLocalPolicyEmptyFields(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, ".codecanary")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}

	configYAML := `version: 1
provider: anthropic
review_model: claude-sonnet-4-6
triage_model: claude-haiku-4-5-20251001
`
	reviewYAML := `rules:
  - id: team-rule
    description: "Team rule"
    severity: warning
context: |
  Team context.
ignore:
  - "dist/**"
`
	// Empty local policy — should not change anything.
	localYAML := `# empty local overrides
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yml"), []byte(configYAML), 0644); err != nil {
		t.Fatalf("writing config.yml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "review.yml"), []byte(reviewYAML), 0644); err != nil {
		t.Fatalf("writing review.yml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "review.local.yml"), []byte(localYAML), 0644); err != nil {
		t.Fatalf("writing review.local.yml: %v", err)
	}

	cfg, err := LoadConfig(filepath.Join(configDir, "config.yml"))
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if len(cfg.Rules) != 1 || cfg.Rules[0].ID != "team-rule" {
		t.Errorf("expected original rules unchanged, got %v", cfg.Rules)
	}
	if !strings.Contains(cfg.Context, "Team context") {
		t.Errorf("expected original context unchanged, got %q", cfg.Context)
	}
	if len(cfg.Ignore) != 1 || cfg.Ignore[0] != "dist/**" {
		t.Errorf("expected original ignore unchanged, got %v", cfg.Ignore)
	}
}

func TestMergeLocalPolicy(t *testing.T) {
	t.Run("BothNil", func(t *testing.T) {
		if got := mergeLocalPolicy(nil, nil); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("NilBase", func(t *testing.T) {
		local := &ReviewPolicy{Context: "local", Rules: []Rule{{ID: "r1"}}, Ignore: []string{"a"}}
		got := mergeLocalPolicy(nil, local)
		if got != local {
			t.Errorf("expected local policy returned as-is")
		}
	})

	t.Run("NilLocal", func(t *testing.T) {
		base := &ReviewPolicy{Context: "base", Rules: []Rule{{ID: "r1"}}, Ignore: []string{"a"}}
		got := mergeLocalPolicy(base, nil)
		if got != base {
			t.Errorf("expected base policy returned as-is")
		}
	})

	t.Run("BothPresent", func(t *testing.T) {
		base := &ReviewPolicy{
			Context: "base context\n",
			Rules:   []Rule{{ID: "base-rule"}},
			Ignore:  []string{"base-ignore"},
		}
		local := &ReviewPolicy{
			Context: "local context\n",
			Rules:   []Rule{{ID: "local-rule"}},
			Ignore:  []string{"local-ignore"},
		}
		got := mergeLocalPolicy(base, local)
		if !strings.Contains(got.Context, "base context") || !strings.Contains(got.Context, "local context") {
			t.Errorf("expected merged context, got %q", got.Context)
		}
		if len(got.Rules) != 2 || got.Rules[0].ID != "base-rule" || got.Rules[1].ID != "local-rule" {
			t.Errorf("expected merged rules [base-rule, local-rule], got %v", got.Rules)
		}
		if len(got.Ignore) != 2 || got.Ignore[0] != "base-ignore" || got.Ignore[1] != "local-ignore" {
			t.Errorf("expected merged ignore [base-ignore, local-ignore], got %v", got.Ignore)
		}
	})
}
