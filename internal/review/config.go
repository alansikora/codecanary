package review

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
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
	AdvisorModel string            `yaml:"advisor_model"`   // optional advisor model for mid-generation strategic guidance (anthropic & claude providers only)
	Provider     string            `yaml:"provider"`        // "anthropic", "openai", "openrouter", or "claude"
	APIBase      string            `yaml:"api_base"`        // override base URL (openai provider only)
	APIKeyEnv    string            `yaml:"api_key_env"`     // env var name for API key (default depends on provider)
	ClaudeArgs   []string          `yaml:"claude_args"`     // extra args passed to the Claude CLI binary (claude provider only)
	ClaudePath   string            `yaml:"claude_path"`     // path to Claude CLI binary (default: "claude")
	Evaluation   *EvaluationConfig `yaml:"evaluation"`
}

// ModelConfig holds the provider and model settings needed to construct a
// single ModelProvider instance. Used internally to build review and triage
// providers from the flat ReviewConfig fields.
type ModelConfig struct {
	Provider     string
	Model        string
	AdvisorModel string // optional advisor model; when set, enables the server-side advisor tool (anthropic & claude providers only)
	APIBase      string
	APIKeyEnv    string
	ClaudeArgs   []string // forwarded to claudeCLIProvider; ignored by other providers
	ClaudePath   string   // forwarded to claudeCLIProvider; empty means "claude"
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

// EffectiveTimeout returns the per-invocation timeout. Returns 0 when
// timeout_minutes is not explicitly set, letting the provider choose its own
// default (e.g. 10m for the Claude CLI, 5m for direct API calls).
func (c *ReviewConfig) EffectiveTimeout() time.Duration {
	if c != nil && c.TimeoutMins > 0 {
		return time.Duration(c.TimeoutMins) * time.Minute
	}
	return 0
}

type Rule struct {
	ID           string   `yaml:"id"`
	Description  string   `yaml:"description"`
	Severity     string   `yaml:"severity"` // One of: critical, bug, warning, suggestion, nitpick
	Paths        []string `yaml:"paths"`
	ExcludePaths []string `yaml:"exclude_paths"`
}

// AppliesToFiles reports whether the rule should be enforced on a review
// covering the given file set. A rule with no Paths applies to any file. A
// rule with Paths applies when at least one file matches any include glob
// and no file-narrowing is needed — ExcludePaths only filter out files that
// would otherwise match. The check short-circuits on the first matching
// file, so the cost is O(files × patterns) in the worst case and usually
// much less. Returns true only when at least one included file survives the
// exclude filter.
//
// An empty file list is treated as "unknown" and returns true — matching the
// defensive behavior of FilterRules. Silently dropping path-scoped rules in
// the unknown case could cause a soft-fail that's hard to debug.
func (r Rule) AppliesToFiles(files []string) bool {
	if len(r.Paths) == 0 {
		return true
	}
	if len(files) == 0 {
		return true
	}
	for _, f := range files {
		if !matchesAny(f, r.Paths) {
			continue
		}
		if matchesAny(f, r.ExcludePaths) {
			continue
		}
		return true
	}
	return false
}

// FilterRules returns the rules applicable to the given file set. Preserves
// input order so severity-sorted configs render predictably.
func FilterRules(rules []Rule, files []string) []Rule {
	if len(files) == 0 {
		return rules
	}
	out := make([]Rule, 0, len(rules))
	for _, r := range rules {
		if r.AppliesToFiles(files) {
			out = append(out, r)
		}
	}
	return out
}

// matchesAny reports whether path matches any of the glob patterns.
// Patterns must be written in full-path form using doublestar globs — e.g.
// `**/*.rb` to match any `.rb` file at any depth, or `app/**/*.rb` to scope
// to a subtree. Bare-filename patterns like `*.rb` only match files at the
// repo root; users who want "any .rb file" should write `**/*.rb`.
//
// Intentionally stricter than matchesIgnore (github.go): rule path-scoping
// demands unambiguous matching, since a basename fallback can silently
// expand a path-scoped pattern to unrelated directories.
func matchesAny(path string, patterns []string) bool {
	for _, pat := range patterns {
		if matched, _ := doublestar.Match(pat, path); matched {
			return true
		}
	}
	return false
}

// validSeverities is the set of allowed severity values for rules,
// derived from the canonical severityLevels slice in formatter.go.
var validSeverities = func() map[string]bool {
	m := make(map[string]bool, len(severityLevels))
	for _, s := range severityLevels {
		m[s] = true
	}
	return m
}()

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
	if c.AdvisorModel != "" {
		if c.Provider != "anthropic" && c.Provider != "claude" {
			return fmt.Errorf("advisor_model is only supported by the anthropic and claude providers, not %q", c.Provider)
		}
		if err := validateAdvisorPairing(c.ReviewModel, c.AdvisorModel); err != nil {
			return err
		}
	}
	if c.Provider == "claude" {
		for _, arg := range c.ClaudeArgs {
			if !strings.HasPrefix(arg, "-") {
				return fmt.Errorf("claude_args: %q is not a flag; use --flag=value form to avoid positional argument injection", arg)
			}
			name := arg
			if i := strings.IndexByte(arg, '='); i >= 0 {
				name = arg[:i]
			}
			if claudeReservedArgs[name] {
				return fmt.Errorf("claude_args: %q is managed by codecanary and cannot be overridden", arg)
			}
		}
	} else if len(c.ClaudeArgs) > 0 || c.ClaudePath != "" {
		Stderrf(ansiYellow, "Warning: claude_args and claude_path are ignored for provider %q\n", c.Provider)
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

// claudeReservedArgs are flags codecanary always controls; users cannot override them via claude_args.
var claudeReservedArgs = map[string]bool{
	"--print":                  true,
	"--output-format":          true,
	"--no-session-persistence": true,
	"--model":                  true,
	"--max-budget-usd":         true,
	"--tools":                  true,
}

// safeSlugSegment matches valid owner/repo name characters (GitHub-compatible).
var safeSlugSegment = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$`)

// repoSlug returns "owner/repo" derived from the git remote origin URL
// of the current working directory. Supports HTTPS, SSH, and SCP-style URLs.
func repoSlug() (string, error) {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", fmt.Errorf("could not detect git remote: %w (are you in a git repo with an origin remote?)", err)
	}
	url := strings.TrimSpace(string(out))

	// SCP-style (no ://): git@github.com:owner/repo.git
	if !strings.Contains(url, "://") {
		if i := strings.Index(url, ":"); i >= 0 {
			url = url[i+1:]
		}
	} else {
		// HTTPS/SSH: strip scheme + host, keep path
		url = url[strings.Index(url, "://")+3:]
		if k := strings.Index(url, "/"); k >= 0 {
			url = url[k+1:]
		}
	}
	url = strings.TrimSuffix(url, ".git")

	parts := strings.SplitN(url, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("could not parse owner/repo from remote origin")
	}
	owner, repo := parts[0], parts[1]
	if !safeSlugSegment.MatchString(owner) || !safeSlugSegment.MatchString(repo) {
		return "", fmt.Errorf("unsafe characters in repo slug %q/%q", owner, repo)
	}
	return owner + "/" + repo, nil
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

// findPolicyFile looks for a review policy file by name. It first checks
// the directory of the given config path (covers the case where config
// is in .codecanary/), then walks up from cwd looking for
// .codecanary/<filename> (covers the case where config is in ~/.codecanary/).
// Returns nil (no error) if the file is not found.
func findPolicyFile(configPath, filename string) (*ReviewPolicy, error) {
	// Try adjacent to the config file first.
	adjacent := filepath.Join(filepath.Dir(configPath), filename)
	if policy, err := loadReviewPolicyFile(adjacent); policy != nil || err != nil {
		return policy, err
	}

	// Walk up from cwd to find .codecanary/<filename> in the repo.
	dir, err := os.Getwd()
	if err != nil {
		return nil, nil
	}
	for {
		policyPath := filepath.Join(dir, ".codecanary", filename)
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

// mergeLocalPolicy appends the local policy fields onto the base policy.
// Context is concatenated (newline-separated), Rules and Ignore are appended.
// If base is nil, the local policy is used as-is and vice versa.
func mergeLocalPolicy(base, local *ReviewPolicy) *ReviewPolicy {
	if local == nil {
		return base
	}
	if base == nil {
		return local
	}
	merged := &ReviewPolicy{}
	merged.Context = base.Context
	if local.Context != "" {
		if merged.Context != "" {
			merged.Context = strings.TrimRight(merged.Context, "\n") + "\n" + local.Context
		} else {
			merged.Context = local.Context
		}
	}

	merged.Rules = make([]Rule, 0, len(base.Rules)+len(local.Rules))
	merged.Rules = append(merged.Rules, base.Rules...)
	merged.Rules = append(merged.Rules, local.Rules...)

	merged.Ignore = make([]string, 0, len(base.Ignore)+len(local.Ignore))
	merged.Ignore = append(merged.Ignore, base.Ignore...)
	merged.Ignore = append(merged.Ignore, local.Ignore...)

	return merged
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
		return nil, fmt.Errorf("parsing %s: %w", filepath.Base(path), err)
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

	// Merge optional review policy files (rules, context, ignore) from repo.
	// review.local.yml fields are appended on top of review.yml.
	policy, err := findPolicyFile(path, "review.yml")
	if err != nil {
		return nil, err
	}
	localPolicy, err := findPolicyFile(path, "review.local.yml")
	if err != nil {
		return nil, err
	}

	merged := mergeLocalPolicy(policy, localPolicy)
	if merged != nil {
		cfg.Rules = merged.Rules
		cfg.Context = merged.Context
		cfg.Ignore = merged.Ignore
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
// ~/.codecanary/repos/<owner>/<repo>/config.yml, falling back to the
// legacy global ~/.codecanary/config.yml with a deprecation warning.
func FindConfig() (string, error) {
	if os.Getenv("GITHUB_ACTIONS") != "" {
		return FindRepoConfig()
	}

	localPath, err := LocalConfigPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(localPath); err == nil {
		return localPath, nil
	}

	// Fall back to legacy global config.
	// localPath is ~/.codecanary/repos/<owner>/<repo>/config.yml;
	// walk up from repos/<owner>/<repo>/ to ~/.codecanary/.
	legacyPath := filepath.Join(filepath.Dir(localPath), "..", "..", "..", "config.yml")
	if _, err := os.Stat(legacyPath); err == nil {
		Stderrf(ansiYellow, "Warning: using legacy global config at %s\n", legacyPath)
		Stderrf(ansiYellow, "  Run `codecanary setup local` to create a per-repo config at %s\n", localPath)
		return legacyPath, nil
	}

	return "", fmt.Errorf("no local config found at %s — run `codecanary setup local`", localPath)
}
