package review

import (
	"os"
	"path/filepath"
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

func TestApplyLocalConfigOverlay_MergesPointers(t *testing.T) {
	base := &ReviewConfig{
		Version:      1,
		Provider:     "anthropic",
		ReviewModel:  "claude-sonnet-4-6",
		TriageModel:  "claude-haiku-4-5-20251001",
		MaxBudgetUSD: 5,
		Rules:        []Rule{{ID: "r1", Description: "keep"}},
	}
	claude := "claude"
	haiku := "haiku"
	zeroBudget := 0.0
	merged := applyLocalConfigOverlay(base, &configLocalOverlayYAML{
		Provider:     &claude,
		TriageModel:  &haiku,
		MaxBudgetUSD: &zeroBudget,
	})
	if merged.Provider != "claude" || merged.TriageModel != "haiku" {
		t.Fatalf("merged = %+v", merged)
	}
	if merged.ReviewModel != "claude-sonnet-4-6" {
		t.Errorf("expected review model inherited, got %q", merged.ReviewModel)
	}
	if merged.MaxBudgetUSD != 0 {
		t.Errorf("expected max_budget_usd reset to 0, got %v", merged.MaxBudgetUSD)
	}
	if len(merged.Rules) != 1 || merged.Rules[0].ID != "r1" {
		t.Errorf("expected rules inherited, got %+v", merged.Rules)
	}
}

func TestApplyConfigLocalOverlay(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "config.yml")
	overlayPath := filepath.Join(dir, "config.local.yml")
	if err := os.WriteFile(mainPath, []byte(`version: 1
provider: anthropic
triage_model: claude-haiku-4-5-20251001
`), 0644); err != nil {
		t.Fatal(err)
	}
	base, err := LoadConfig(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overlayPath, []byte(`provider: claude
triage_model: haiku
`), 0644); err != nil {
		t.Fatal(err)
	}
	merged, err := ApplyConfigLocalOverlay(mainPath, base)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Provider != "claude" || merged.TriageModel != "haiku" {
		t.Fatalf("merged = %+v", merged)
	}
}

func TestApplyConfigLocalOverlay_YAMLResetsMaxBudgetToZero(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "config.yml")
	overlayPath := filepath.Join(dir, "config.local.yml")
	if err := os.WriteFile(mainPath, []byte(`version: 1
provider: anthropic
triage_model: claude-haiku-4-5-20251001
max_budget_usd: 5
`), 0644); err != nil {
		t.Fatal(err)
	}
	base, err := LoadConfig(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overlayPath, []byte(`max_budget_usd: 0
`), 0644); err != nil {
		t.Fatal(err)
	}
	merged, err := ApplyConfigLocalOverlay(mainPath, base)
	if err != nil {
		t.Fatal(err)
	}
	if merged.MaxBudgetUSD != 0 {
		t.Fatalf("expected max_budget_usd 0 from overlay, got %v", merged.MaxBudgetUSD)
	}
	if merged.Provider != "anthropic" {
		t.Fatalf("expected provider unchanged, got %q", merged.Provider)
	}
}
