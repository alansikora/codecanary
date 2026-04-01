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
	MaxFileSize  int               `yaml:"max_file_size"`  // per-file content limit in bytes (default 100KB)
	MaxTotalSize int               `yaml:"max_total_size"` // total file content limit in bytes (default 500KB)
	MaxBudgetUSD float64           `yaml:"max_budget_usd"`  // per-invocation spending limit in USD (default 0 = unlimited)
	TimeoutMins  int               `yaml:"timeout_minutes"` // per-invocation timeout in minutes (default 5)
	ReviewModel  string            `yaml:"review_model"`    // model for main review (default: sonnet)
	TriageModel  string            `yaml:"triage_model"`    // model for thread re-evaluation (default: haiku)
	Provider     string            `yaml:"provider"`        // "anthropic", "openai", "openrouter", or "claude"
	APIBase      string            `yaml:"api_base"`        // override base URL (openai provider only)
	APIKeyEnv    string            `yaml:"api_key_env"`     // env var name for API key (default depends on provider)
	Evaluation   *EvaluationConfig `yaml:"evaluation"`
}

// validCLIModels is the set of allowed model values for the Claude CLI provider.
var validCLIModels = map[string]bool{
	"haiku": true, "sonnet": true, "opus": true,
}

// EffectiveReviewModel returns the configured review model.
// Each provider has its own default when review_model is not set.
func (c *ReviewConfig) EffectiveReviewModel() string {
	if c != nil && c.ReviewModel != "" {
		return c.ReviewModel
	}
	if c != nil {
		switch c.Provider {
		case "claude":
			return "sonnet"
		case "openrouter":
			return "anthropic/claude-sonnet-4-6"
		case "openai":
			return "gpt-5.4"
		}
	}
	return "claude-sonnet-4-6" // anthropic
}

// EffectiveTriageModel returns the configured triage model.
// Each provider has its own default when triage_model is not set.
func (c *ReviewConfig) EffectiveTriageModel() string {
	if c != nil && c.TriageModel != "" {
		return c.TriageModel
	}
	if c != nil {
		switch c.Provider {
		case "claude":
			return "haiku"
		case "openrouter":
			return "anthropic/claude-haiku-4-5-20251001"
		case "openai":
			return "gpt-5.4-mini"
		}
	}
	return "claude-haiku-4-5-20251001" // anthropic
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
	switch c.Provider {
	case "anthropic", "openrouter":
		// Accept any model string.
		if c.APIBase != "" {
			return fmt.Errorf("api_base is not supported by the %s provider", c.Provider)
		}
	case "openai":
		// Accept any model string; api_base can override the endpoint.
		if c.APIBase != "" && !isValidURL(c.APIBase) {
			return fmt.Errorf("invalid api_base %q: must be an HTTP(S) URL", c.APIBase)
		}
	case "claude":
		// Claude CLI only accepts known shorthand model names.
		if c.ReviewModel != "" && !validCLIModels[c.ReviewModel] {
			return fmt.Errorf("invalid review_model %q for claude provider (valid: haiku, sonnet, opus)", c.ReviewModel)
		}
		if c.TriageModel != "" && !validCLIModels[c.TriageModel] {
			return fmt.Errorf("invalid triage_model %q for claude provider (valid: haiku, sonnet, opus)", c.TriageModel)
		}
	case "":
		return fmt.Errorf("provider is required (valid: anthropic, openai, openrouter, claude)")
	default:
		return fmt.Errorf("invalid provider %q (valid: anthropic, openai, openrouter, claude)", c.Provider)
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

// FindConfig looks for review config starting from the current directory and
// walking up the directory tree. It checks .codecanary/config.yml first, then
// falls back to the legacy .codecanary.yml with a deprecation warning.
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
