package review

import (
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

func TestEffectiveReviewModel_Anthropic(t *testing.T) {
	cfg := &ReviewConfig{Provider: "anthropic"}
	if got := cfg.EffectiveReviewModel(); got != "claude-sonnet-4-6" {
		t.Errorf("EffectiveReviewModel() = %q, want %q", got, "claude-sonnet-4-6")
	}
}

func TestEffectiveReviewModel_Claude(t *testing.T) {
	cfg := &ReviewConfig{Provider: "claude"}
	if got := cfg.EffectiveReviewModel(); got != "claude-sonnet-4-6" {
		t.Errorf("EffectiveReviewModel() = %q, want %q", got, "claude-sonnet-4-6")
	}
}

func TestEffectiveReviewModel_Custom(t *testing.T) {
	cfg := &ReviewConfig{Provider: "anthropic", ReviewModel: "claude-opus-4-6"}
	if got := cfg.EffectiveReviewModel(); got != "claude-opus-4-6" {
		t.Errorf("EffectiveReviewModel() = %q, want %q", got, "claude-opus-4-6")
	}
}

func TestEffectiveTriageModel(t *testing.T) {
	cfg := &ReviewConfig{Provider: "anthropic", TriageModel: "claude-sonnet-4-20250514"}
	if got := cfg.EffectiveTriageModel(); got != "claude-sonnet-4-20250514" {
		t.Errorf("EffectiveTriageModel() = %q, want %q", got, "claude-sonnet-4-20250514")
	}
}

func TestValidate_TriageModelRequired(t *testing.T) {
	cfg := &ReviewConfig{Provider: "anthropic"}
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
	cfg = &ReviewConfig{Provider: "claude", TriageModel: "invalid"}
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

func TestValidate_ValidCLIModels(t *testing.T) {
	for _, m := range []string{"haiku", "sonnet", "opus"} {
		cfg := &ReviewConfig{Provider: "claude", ReviewModel: m, TriageModel: m}
		if err := cfg.Validate(); err != nil {
			t.Errorf("unexpected error for model %q: %v", m, err)
		}
	}
}

func TestValidate_CrossProviderModel_Anthropic(t *testing.T) {
	// OpenAI model on anthropic provider.
	cfg := &ReviewConfig{Provider: "anthropic", TriageModel: "gpt-5.4-mini"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for OpenAI model on anthropic provider")
	}
	// OpenRouter-style model on anthropic provider.
	cfg = &ReviewConfig{Provider: "anthropic", TriageModel: "openai/gpt-5.4"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for OpenRouter model on anthropic provider")
	}
	// Review model too.
	cfg = &ReviewConfig{Provider: "anthropic", ReviewModel: "o4-mini", TriageModel: "claude-haiku-4-5-20251001"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for OpenAI review_model on anthropic provider")
	}
}

func TestValidate_CrossProviderModel_OpenAI(t *testing.T) {
	// Anthropic model on openai provider.
	cfg := &ReviewConfig{Provider: "openai", TriageModel: "claude-haiku-4-5-20251001"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for Anthropic model on openai provider")
	}
	// Claude alias on openai provider.
	cfg = &ReviewConfig{Provider: "openai", TriageModel: "haiku"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for Claude alias on openai provider")
	}
	// OpenRouter-style model on openai provider.
	cfg = &ReviewConfig{Provider: "openai", ReviewModel: "anthropic/claude-sonnet-4-6", TriageModel: "gpt-5.4-mini"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for OpenRouter model on openai provider")
	}
}

func TestValidate_CrossProviderModel_OpenAI_CustomBase(t *testing.T) {
	// With api_base set, any model name is allowed (custom endpoint).
	cfg := &ReviewConfig{Provider: "openai", APIBase: "http://localhost:11434/v1", TriageModel: "claude-haiku-4-5-20251001"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error with custom api_base: %v", err)
	}
}

func TestValidate_ValidProviders(t *testing.T) {
	triageModels := map[string]string{
		"anthropic":  "claude-haiku-4-5-20251001",
		"openai":     "gpt-5.4-mini",
		"openrouter": "anthropic/claude-haiku-4-5-20251001",
		"claude":     "haiku",
	}
	for _, p := range []string{"anthropic", "openai", "openrouter", "claude"} {
		cfg := &ReviewConfig{Provider: p, TriageModel: triageModels[p]}
		if err := cfg.Validate(); err != nil {
			t.Errorf("unexpected error for provider %q: %v", p, err)
		}
	}
}
