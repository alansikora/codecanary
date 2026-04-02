package review

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// isValidURL checks that a string looks like an HTTP(S) URL.
func isValidURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

type ReviewConfig struct {
	Version      int               `yaml:"version"`
	Rules        []Rule            `yaml:"rules"`
	Context      string            `yaml:"context"`
	Ignore       []string          `yaml:"ignore"`
	MaxFileSize  int               `yaml:"max_file_size"`   // per-file content limit in bytes (default 100KB)
	MaxTotalSize int               `yaml:"max_total_size"`  // total file content limit in bytes (default 500KB)
	MaxBudgetUSD float64           `yaml:"max_budget_usd"`  // per-invocation spending limit in USD (default 0 = unlimited)
	TimeoutMins  int               `yaml:"timeout_minutes"` // per-invocation timeout in minutes (default 5)
	ReviewModel  string            `yaml:"review_model"`    // model for main review (default: sonnet)
	TriageModel  string            `yaml:"triage_model"`    // model for thread re-evaluation (required)
	Provider     string            `yaml:"provider"`        // "anthropic", "openai", "openrouter", or "claude"
	APIBase      string            `yaml:"api_base"`        // override base URL (openai provider only)
	APIKeyEnv    string            `yaml:"api_key_env"`     // env var name for API key (default depends on provider)
	Evaluation   *EvaluationConfig `yaml:"evaluation"`
}

// EffectiveReviewModel returns the configured review model.
// Each provider has its own default when review_model is not set.
// Panics on nil config or unknown provider — both should be caught by Validate().
func (c *ReviewConfig) EffectiveReviewModel() string {
	if c == nil {
		panic("EffectiveReviewModel called with nil config")
	}
	if c.ReviewModel != "" {
		return c.ReviewModel
	}
	pf, ok := providers[c.Provider]
	if !ok {
		panic(fmt.Sprintf("unknown provider %q (should have been caught by config validation)", c.Provider))
	}
	return pf.SuggestedReviewModel
}

// EffectiveTriageModel returns the configured triage model.
// triage_model is required — Validate() rejects configs without it.
func (c *ReviewConfig) EffectiveTriageModel() string {
	if c == nil {
		panic("EffectiveTriageModel called with nil config")
	}
	return c.TriageModel
}

// EvaluationConfig holds per-evaluation-type settings for re-evaluation prompts.
type EvaluationConfig struct {
	CodeChange EvalTypeConfig `yaml:"code_change"`
	Reply      EvalTypeConfig `yaml:"reply"`
}

// EvalTypeConfig holds settings for a specific evaluation type.
type EvalTypeConfig struct {
	Context string `yaml:"context"`
}

// EffectiveMaxFileSize returns the per-file size limit, defaulting to 100KB.
func (c *ReviewConfig) EffectiveMaxFileSize() int {
	if c != nil && c.MaxFileSize > 0 {
		return c.MaxFileSize
	}
	return 100 * 1024
}

// EffectiveMaxTotalSize returns the total file content limit, defaulting to 500KB.
func (c *ReviewConfig) EffectiveMaxTotalSize() int {
	if c != nil && c.MaxTotalSize > 0 {
		return c.MaxTotalSize
	}
	return 500 * 1024
}

// EffectiveTimeout returns the per-invocation timeout, defaulting to 5 minutes.
func (c *ReviewConfig) EffectiveTimeout() time.Duration {
	if c != nil && c.TimeoutMins > 0 {
		return time.Duration(c.TimeoutMins) * time.Minute
	}
	return 5 * time.Minute
}

type Rule struct {
	ID           string   `yaml:"id"`
	Description  string   `yaml:"description"`
	Severity     string   `yaml:"severity"` // One of: critical, bug, warning, suggestion, nitpick
	Paths        []string `yaml:"paths"`
	ExcludePaths []string `yaml:"exclude_paths"`
}

// validSeverities is the set of allowed severity values for rules.
var validSeverities = map[string]bool{
	"critical": true, "bug": true, "warning": true, "suggestion": true, "nitpick": true,
}

// Validate checks that config field values are within expected ranges.
func (c *ReviewConfig) Validate() error {
	if c.Version != 0 && c.Version != 1 {
		return fmt.Errorf("unsupported config version: %d", c.Version)
	}
	if c.MaxFileSize < 0 {
		return fmt.Errorf("max_file_size must be non-negative, got %d", c.MaxFileSize)
	}
	if c.MaxTotalSize < 0 {
		return fmt.Errorf("max_total_size must be non-negative, got %d", c.MaxTotalSize)
	}
	if c.TimeoutMins < 0 {
		return fmt.Errorf("timeout_minutes must be non-negative, got %d", c.TimeoutMins)
	}
	if c.MaxBudgetUSD < 0 {
		return fmt.Errorf("max_budget_usd must be non-negative, got %f", c.MaxBudgetUSD)
	}
	if c.Provider == "" {
		return fmt.Errorf("provider is required (valid: %s)", strings.Join(providerNames(), ", "))
	}
	if c.TriageModel == "" {
		return fmt.Errorf("triage_model is required — set the model used for thread re-evaluation")
	}
	pf, ok := providers[c.Provider]
	if !ok {
		return fmt.Errorf("invalid provider %q (valid: %s)", c.Provider, strings.Join(providerNames(), ", "))
	}
	if pf.Validate != nil {
		if err := pf.Validate(c); err != nil {
			return err
		}
	}
	for i, r := range c.Rules {
		if r.Severity != "" && !validSeverities[r.Severity] {
			return fmt.Errorf("rule %d (%q): invalid severity %q", i, r.ID, r.Severity)
		}
	}
	return nil
}

// LoadConfig reads and parses a review config YAML file from the given path.
func LoadConfig(path string) (*ReviewConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg ReviewConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// configLocalOverlayYAML is the YAML shape of config.local.yml. Pointer fields
// mean the key was present in the file; nil means inherit from the main config.
// That allows explicit numeric zero (e.g. max_budget_usd: 0 for unlimited) to
// override a non-zero value from config.yml.
type configLocalOverlayYAML struct {
	Version      *int              `yaml:"version"`
	Provider     *string           `yaml:"provider"`
	ReviewModel  *string           `yaml:"review_model"`
	TriageModel  *string           `yaml:"triage_model"`
	APIBase      *string           `yaml:"api_base"`
	APIKeyEnv    *string           `yaml:"api_key_env"`
	Context      *string           `yaml:"context"`
	MaxFileSize  *int              `yaml:"max_file_size"`
	MaxTotalSize *int              `yaml:"max_total_size"`
	MaxBudgetUSD *float64          `yaml:"max_budget_usd"`
	TimeoutMins  *int              `yaml:"timeout_minutes"`
	Ignore       *[]string         `yaml:"ignore"`
	Rules        *[]Rule           `yaml:"rules"`
	Evaluation   *EvaluationConfig `yaml:"evaluation"`
}

func applyLocalConfigOverlay(base *ReviewConfig, o *configLocalOverlayYAML) *ReviewConfig {
	if base == nil {
		return nil
	}
	out := *base
	if o == nil {
		return &out
	}
	if o.Version != nil {
		out.Version = *o.Version
	}
	if o.Provider != nil {
		out.Provider = *o.Provider
	}
	if o.ReviewModel != nil {
		out.ReviewModel = *o.ReviewModel
	}
	if o.TriageModel != nil {
		out.TriageModel = *o.TriageModel
	}
	if o.APIBase != nil {
		out.APIBase = *o.APIBase
	}
	if o.APIKeyEnv != nil {
		out.APIKeyEnv = *o.APIKeyEnv
	}
	if o.Context != nil {
		out.Context = *o.Context
	}
	if o.MaxFileSize != nil {
		out.MaxFileSize = *o.MaxFileSize
	}
	if o.MaxTotalSize != nil {
		out.MaxTotalSize = *o.MaxTotalSize
	}
	if o.MaxBudgetUSD != nil {
		out.MaxBudgetUSD = *o.MaxBudgetUSD
	}
	if o.TimeoutMins != nil {
		out.TimeoutMins = *o.TimeoutMins
	}
	if o.Ignore != nil {
		out.Ignore = append([]string(nil), (*o.Ignore)...)
	}
	if o.Rules != nil {
		out.Rules = append([]Rule(nil), (*o.Rules)...)
	}
	if o.Evaluation != nil {
		out.Evaluation = o.Evaluation
	}
	return &out
}

// ApplyConfigLocalOverlay merges .codecanary/config.local.yml (same directory as
// mainConfigPath) onto base when the file exists. The merged config is validated.
func ApplyConfigLocalOverlay(mainConfigPath string, base *ReviewConfig) (*ReviewConfig, error) {
	if base == nil {
		return nil, fmt.Errorf("base config is nil")
	}
	dir := filepath.Dir(mainConfigPath)
	localPath := filepath.Join(dir, "config.local.yml")
	data, err := os.ReadFile(localPath)
	if err != nil {
		if os.IsNotExist(err) {
			return base, nil
		}
		return nil, fmt.Errorf("reading %s: %w", localPath, err)
	}
	var overlay configLocalOverlayYAML
	if err := yaml.Unmarshal(data, &overlay); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", localPath, err)
	}
	merged := applyLocalConfigOverlay(base, &overlay)
	if err := merged.Validate(); err != nil {
		return nil, fmt.Errorf("merged config (%s + %s): %w", mainConfigPath, localPath, err)
	}
	Stderrf(ansiCyan, "Applying local overrides from %s\n", localPath)
	return merged, nil
}

// FindConfig looks for review config starting from the current directory and
// walking up the directory tree. It checks .codecanary/config.yml first, then
// falls back to the legacy .codecanary.yml with a deprecation warning.
//
// For local-only review runs (no PR), if .codecanary/config.local.yml exists next
// to the resolved config file, it is merged on top — see ApplyConfigLocalOverlay.
func FindConfig() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting working directory: %w", err)
	}

	for {
		// Prefer new location: .codecanary/config.yml
		newPath := filepath.Join(dir, ".codecanary", "config.yml")
		if _, err := os.Stat(newPath); err == nil {
			return newPath, nil
		}

		// Legacy fallback: .codecanary.yml
		legacyPath := filepath.Join(dir, ".codecanary.yml")
		if _, err := os.Stat(legacyPath); err == nil {
			Stderrf(ansiYellow, "Warning: .codecanary.yml is deprecated — move to .codecanary/config.yml\n")
			return legacyPath, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("no .codecanary/config.yml found")
}
