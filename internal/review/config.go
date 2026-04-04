package review

import (
	"fmt"
	"os"
	"os/exec"
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
	Rules        []Rule            `yaml:"-"`
	Context      string            `yaml:"-"`
	Ignore       []string          `yaml:"-"`
	MaxFileSize  int               `yaml:"max_file_size"`  // per-file content limit in bytes (default 100KB)
	MaxTotalSize int               `yaml:"max_total_size"` // total file content limit in bytes (default 500KB)
	MaxBudgetUSD float64           `yaml:"max_budget_usd"`  // per-invocation spending limit in USD (default 0 = unlimited)
	TimeoutMins  int               `yaml:"timeout_minutes"` // per-invocation timeout in minutes (default 5)
	ReviewModel  string            `yaml:"review_model"`    // model for main review (required)
	TriageModel  string            `yaml:"triage_model"`    // model for thread re-evaluation (required)
	Provider     string            `yaml:"provider"`        // "anthropic", "openai", "openrouter", or "claude"
	APIBase      string            `yaml:"api_base"`        // override base URL (openai provider only)
	APIKeyEnv    string            `yaml:"api_key_env"`     // env var name for API key (default depends on provider)
	Evaluation   *EvaluationConfig `yaml:"evaluation"`
}

// ModelConfig holds the provider and model settings needed to construct a
// single ModelProvider instance. Used internally to build review and triage
// providers from the flat ReviewConfig fields.
type ModelConfig struct {
	Provider  string
	Model     string
	APIBase   string
	APIKeyEnv string
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
	if c.ReviewModel == "" {
		return fmt.Errorf("review_model is required — set the model used for code review")
	}
	if c.TriageModel == "" {
		return fmt.Errorf("triage_model is required — set the model used for thread re-evaluation")
	}
	pf, ok := providers[c.Provider]
	if !ok {
		return fmt.Errorf("invalid provider %q (valid: %s)", c.Provider, strings.Join(providerNames(), ", "))
	}
	if pf.Validate != nil {
		mc := &ModelConfig{Provider: c.Provider, Model: c.ReviewModel, APIBase: c.APIBase, APIKeyEnv: c.APIKeyEnv}
		if err := pf.Validate(mc); err != nil {
			return err
		}
		// Also validate the triage model.
		triageMC := &ModelConfig{Provider: c.Provider, Model: c.TriageModel, APIBase: c.APIBase, APIKeyEnv: c.APIKeyEnv}
		if err := pf.Validate(triageMC); err != nil {
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

// ReviewPolicy holds review-behavior fields that can live in a separate
// review.yml file alongside config.yml. When review.yml exists, its
// values override the corresponding fields in config.yml.
type ReviewPolicy struct {
	Rules   []Rule   `yaml:"rules"`
	Context string   `yaml:"context"`
	Ignore  []string `yaml:"ignore"`
}

// repoSlug returns "owner/repo" derived from the git remote origin URL
// of the current working directory. Supports HTTPS, SSH, and SCP-style URLs.
func repoSlug() (string, error) {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", fmt.Errorf("could not detect git remote: %w (are you in a git repo with an origin remote?)", err)
	}
	url := strings.TrimSpace(string(out))

	// SCP-style: git@github.com:owner/repo.git
	if i := strings.Index(url, ":"); i >= 0 && !strings.Contains(url[:i], "/") {
		url = url[i+1:]
	} else {
		// HTTPS/SSH: strip scheme + host, keep path
		if j := strings.Index(url, "://"); j >= 0 {
			url = url[j+3:]
		}
		if k := strings.Index(url, "/"); k >= 0 {
			url = url[k+1:]
		}
	}
	url = strings.TrimSuffix(url, ".git")

	parts := strings.SplitN(url, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("could not parse owner/repo from remote origin")
	}
	return parts[0] + "/" + parts[1], nil
}

// LocalConfigPath returns the path to the per-repo local config at
// ~/.codecanary/repos/<owner>/<repo>/config.yml. Each repo gets its
// own config so different repos can use different providers/models.
func LocalConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine home directory: %w", err)
	}
	slug, err := repoSlug()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codecanary", "repos", slug, "config.yml"), nil
}

// findReviewPolicy looks for review.yml in the repo. It first checks
// the directory of the given config path (covers the case where config
// is in .codecanary/), then walks up from cwd looking for
// .codecanary/review.yml (covers the case where config is in ~/.codecanary/).
// Returns nil (no error) if no review.yml is found.
func findReviewPolicy(configPath string) (*ReviewPolicy, error) {
	// Try adjacent to the config file first.
	adjacent := filepath.Join(filepath.Dir(configPath), "review.yml")
	if policy, err := loadReviewPolicyFile(adjacent); policy != nil || err != nil {
		return policy, err
	}

	// Walk up from cwd to find .codecanary/review.yml in the repo.
	dir, err := os.Getwd()
	if err != nil {
		return nil, nil
	}
	for {
		policyPath := filepath.Join(dir, ".codecanary", "review.yml")
		if policyPath != adjacent { // avoid re-checking
			if policy, err := loadReviewPolicyFile(policyPath); policy != nil || err != nil {
				return policy, err
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil, nil
}

func loadReviewPolicyFile(path string) (*ReviewPolicy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading review policy: %w", err)
	}
	var policy ReviewPolicy
	if err := yaml.Unmarshal(data, &policy); err != nil {
		return nil, fmt.Errorf("parsing review.yml: %w", err)
	}
	return &policy, nil
}

// LoadConfig reads and parses a review config YAML file from the given path.
// It also discovers the project's review.yml by walking up from cwd; if
// present, its rules, context, and ignore fields override config.yml.
func LoadConfig(path string) (*ReviewConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg ReviewConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	// Merge optional review.yml (rules, context, ignore) from repo.
	policy, err := findReviewPolicy(path)
	if err != nil {
		return nil, err
	}
	if policy != nil {
		cfg.Rules = policy.Rules
		cfg.Context = policy.Context
		cfg.Ignore = policy.Ignore
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// FindRepoConfig walks up from the current directory looking for the
// repo-level config at .codecanary/config.yml (or legacy .codecanary.yml).
func FindRepoConfig() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting working directory: %w", err)
	}

	for {
		newPath := filepath.Join(dir, ".codecanary", "config.yml")
		if _, err := os.Stat(newPath); err == nil {
			return newPath, nil
		}

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

// FindConfig returns the config path for the current environment.
// In GitHub Actions it returns the repo-level .codecanary/config.yml.
// Otherwise it returns the per-repo local config at
// ~/.codecanary/repos/<owner>/<repo>/config.yml.
func FindConfig() (string, error) {
	if os.Getenv("GITHUB_ACTIONS") != "" {
		return FindRepoConfig()
	}

	localPath, err := LocalConfigPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(localPath); err != nil {
		return "", fmt.Errorf("no local config found at %s — run `codecanary setup local`", localPath)
	}
	return localPath, nil
}
