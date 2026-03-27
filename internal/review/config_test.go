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

func TestEffectiveReviewModel_Default(t *testing.T) {
	cfg := &ReviewConfig{}
	if got := cfg.EffectiveReviewModel(); got != "sonnet" {
		t.Errorf("EffectiveReviewModel() = %q, want %q", got, "sonnet")
	}
}

func TestEffectiveReviewModel_Custom(t *testing.T) {
	cfg := &ReviewConfig{ReviewModel: "opus"}
	if got := cfg.EffectiveReviewModel(); got != "opus" {
		t.Errorf("EffectiveReviewModel() = %q, want %q", got, "opus")
	}
}

func TestEffectiveTriageModel_Default(t *testing.T) {
	cfg := &ReviewConfig{}
	if got := cfg.EffectiveTriageModel(); got != "haiku" {
		t.Errorf("EffectiveTriageModel() = %q, want %q", got, "haiku")
	}
}

func TestEffectiveTriageModel_Custom(t *testing.T) {
	cfg := &ReviewConfig{TriageModel: "sonnet"}
	if got := cfg.EffectiveTriageModel(); got != "sonnet" {
		t.Errorf("EffectiveTriageModel() = %q, want %q", got, "sonnet")
	}
}

func TestValidate_InvalidModel(t *testing.T) {
	cfg := &ReviewConfig{ReviewModel: "gpt-4"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid review_model")
	}
	cfg = &ReviewConfig{TriageModel: "invalid"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid triage_model")
	}
}

func TestValidate_ValidModels(t *testing.T) {
	for _, m := range []string{"haiku", "sonnet", "opus"} {
		cfg := &ReviewConfig{ReviewModel: m, TriageModel: m}
		if err := cfg.Validate(); err != nil {
			t.Errorf("unexpected error for model %q: %v", m, err)
		}
	}
}
