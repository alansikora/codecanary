package review

import (
	"os"
	"testing"
	"time"
)

func TestEffectiveTimeout_Default(t *testing.T) {
	cfg := &ReviewConfig{}
	if got := cfg.EffectiveTimeout(); got != 5*time.Minute {
		t.Errorf("EffectiveTimeout() = %v, want 5m", got)
	}
}

func TestEffectiveTimeout_NilConfig(t *testing.T) {
	var cfg *ReviewConfig
	if got := cfg.EffectiveTimeout(); got != 5*time.Minute {
		t.Errorf("EffectiveTimeout() on nil = %v, want 5m", got)
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

func TestValidate_ReviewProviderRequired(t *testing.T) {
	cfg := &ReviewConfig{
		Review: ModelConfig{Model: "claude-sonnet-4-6"},
		Triage: ModelConfig{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing review.provider")
	}
}

func TestValidate_ReviewModelRequired(t *testing.T) {
	cfg := &ReviewConfig{
		Review: ModelConfig{Provider: "anthropic"},
		Triage: ModelConfig{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing review.model")
	}
}

func TestValidate_TriageProviderRequired(t *testing.T) {
	cfg := &ReviewConfig{
		Review: ModelConfig{Provider: "anthropic", Model: "claude-sonnet-4-6"},
		Triage: ModelConfig{Model: "claude-haiku-4-5-20251001"},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing triage.provider")
	}
}

func TestValidate_TriageModelRequired(t *testing.T) {
	cfg := &ReviewConfig{
		Review: ModelConfig{Provider: "anthropic", Model: "claude-sonnet-4-6"},
		Triage: ModelConfig{Provider: "anthropic"},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing triage.model")
	}
}

func TestValidate_InvalidProvider(t *testing.T) {
	cfg := &ReviewConfig{
		Review: ModelConfig{Provider: "gemini", Model: "gemini-pro"},
		Triage: ModelConfig{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid provider")
	}
}

func TestValidate_InvalidModelForClaude(t *testing.T) {
	cfg := &ReviewConfig{
		Review: ModelConfig{Provider: "claude", Model: "gpt-4"},
		Triage: ModelConfig{Provider: "claude", Model: "haiku"},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid review model on claude provider")
	}
	cfg = &ReviewConfig{
		Review: ModelConfig{Provider: "claude", Model: "sonnet"},
		Triage: ModelConfig{Provider: "claude", Model: "invalid"},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid triage model on claude provider")
	}
}

func TestValidate_AnyModelForAnthropic(t *testing.T) {
	cfg := &ReviewConfig{
		Review: ModelConfig{Provider: "anthropic", Model: "claude-opus-4-6"},
		Triage: ModelConfig{Provider: "anthropic", Model: "anything"},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_ValidCLIModels(t *testing.T) {
	for _, m := range []string{"haiku", "sonnet", "opus"} {
		cfg := &ReviewConfig{
			Review: ModelConfig{Provider: "claude", Model: m},
			Triage: ModelConfig{Provider: "claude", Model: m},
		}
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
		cfg := &ReviewConfig{
			Review: ModelConfig{Provider: p, Model: models[p][0]},
			Triage: ModelConfig{Provider: p, Model: models[p][1]},
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("unexpected error for provider %q: %v", p, err)
		}
	}
}

func TestValidate_MixedProviders(t *testing.T) {
	cfg := &ReviewConfig{
		Review: ModelConfig{Provider: "anthropic", Model: "claude-sonnet-4-6"},
		Triage: ModelConfig{Provider: "openai", Model: "gpt-5.4-mini"},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error for mixed providers: %v", err)
	}
}

func TestLoadConfig_NewFormat(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.yml"

	yaml := `version: 1
review:
  provider: anthropic
  model: claude-sonnet-4-6
triage:
  provider: openai
  model: gpt-5.4-mini
`
	if err := writeTestFile(path, yaml); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Review.Provider != "anthropic" {
		t.Errorf("Review.Provider = %q, want %q", cfg.Review.Provider, "anthropic")
	}
	if cfg.Triage.Provider != "openai" {
		t.Errorf("Triage.Provider = %q, want %q", cfg.Triage.Provider, "openai")
	}
	if cfg.Triage.Model != "gpt-5.4-mini" {
		t.Errorf("Triage.Model = %q, want %q", cfg.Triage.Model, "gpt-5.4-mini")
	}
}

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}
