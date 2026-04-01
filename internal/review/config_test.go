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
	if got := cfg.EffectiveReviewModel(); got != "claude-sonnet-4-20250514" {
		t.Errorf("EffectiveReviewModel() = %q, want %q", got, "claude-sonnet-4-20250514")
	}
}

func TestEffectiveReviewModel_Claude(t *testing.T) {
	cfg := &ReviewConfig{Provider: "claude"}
	if got := cfg.EffectiveReviewModel(); got != "sonnet" {
		t.Errorf("EffectiveReviewModel() = %q, want %q", got, "sonnet")
	}
}

func TestEffectiveReviewModel_Custom(t *testing.T) {
	cfg := &ReviewConfig{Provider: "anthropic", ReviewModel: "claude-opus-4-6"}
	if got := cfg.EffectiveReviewModel(); got != "claude-opus-4-6" {
		t.Errorf("EffectiveReviewModel() = %q, want %q", got, "claude-opus-4-6")
	}
}

func TestEffectiveTriageModel_Anthropic(t *testing.T) {
	cfg := &ReviewConfig{Provider: "anthropic"}
	if got := cfg.EffectiveTriageModel(); got != "claude-haiku-4-5-20251001" {
		t.Errorf("EffectiveTriageModel() = %q, want %q", got, "claude-haiku-4-5-20251001")
	}
}

func TestEffectiveTriageModel_Claude(t *testing.T) {
	cfg := &ReviewConfig{Provider: "claude"}
	if got := cfg.EffectiveTriageModel(); got != "haiku" {
		t.Errorf("EffectiveTriageModel() = %q, want %q", got, "haiku")
	}
}

func TestEffectiveTriageModel_Custom(t *testing.T) {
	cfg := &ReviewConfig{Provider: "anthropic", TriageModel: "claude-sonnet-4-20250514"}
	if got := cfg.EffectiveTriageModel(); got != "claude-sonnet-4-20250514" {
		t.Errorf("EffectiveTriageModel() = %q, want %q", got, "claude-sonnet-4-20250514")
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
	cfg := &ReviewConfig{Provider: "claude", ReviewModel: "gpt-4"}
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

func TestValidate_ValidProviders(t *testing.T) {
	for _, p := range []string{"anthropic", "openai", "openrouter", "claude"} {
		cfg := &ReviewConfig{Provider: p}
		if err := cfg.Validate(); err != nil {
			t.Errorf("unexpected error for provider %q: %v", p, err)
		}
	}
}
