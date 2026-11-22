package review

import (
	"fmt"
	"io"
	"os"
	"maps"
	"slices"
	"strings"
)

// Pull-request policy: the config.yml keys that decide how a given pull
// request is reviewed, as opposed to how the provider is called. Tiers pick
// whether and with which model; intent turns on review against the PR's own
// stated intent; context_files adds rule files by path; status_context names
// the commit status.

// DefaultStatusContext is the commit-status context used when status_context
// is unset. Stable so teams can require it in branch protection.
const DefaultStatusContext = "CodeCanary / review"

// TierConfig is one entry of `tiers:`: what a PR carrying that label gets.
type TierConfig struct {
	// Review, when false, skips the review: the head gets a success status
	// "skipped: <tier>" and no comments. Default true.
	Review *bool `yaml:"review"`
	// ReviewModel overrides review_model for PRs in this tier.
	ReviewModel string `yaml:"review_model"`
}

// Reviews reports whether PRs in this tier are reviewed.
func (t TierConfig) Reviews() bool { return t.Review == nil || *t.Review }

// Tier is the tier a pull request resolved to.
type Tier struct {
	Name   string
	Config TierConfig
}

// SkippedDescription is the commit-status description for a tier that is
// not reviewed, e.g. "skipped: tier low" for "tier:low".
func (t Tier) SkippedDescription() string {
	return "skipped: " + strings.Join(strings.FieldsFunc(t.Name, func(r rune) bool {
		return r == ':' || r == '/' || r == ' '
	}), " ")
}

// ResolveTier returns the tier for a PR with the given labels, or nil when
// no tiers are configured. A PR with exactly one configured tier label gets
// that tier; none, or more than one, gets default_tier. It is never skipped
// for lacking a label.
func (c *ReviewConfig) ResolveTier(labels []string) *Tier {
	if c == nil || len(c.Tiers) == 0 {
		return nil
	}
	var matched []string
	for _, l := range labels {
		if _, ok := c.Tiers[l]; ok && !slices.Contains(matched, l) {
			matched = append(matched, l)
		}
	}
	name := c.DefaultTier
	if len(matched) == 1 {
		name = matched[0]
	}
	return &Tier{Name: name, Config: c.Tiers[name]}
}

// IsTierLabel reports whether label is one of the configured tiers.
func (c *ReviewConfig) IsTierLabel(label string) bool {
	if c == nil {
		return false
	}
	_, ok := c.Tiers[label]
	return ok
}

// EffectiveStatusContext returns status_context, defaulting to
// DefaultStatusContext.
func (c *ReviewConfig) EffectiveStatusContext() string {
	if c != nil && c.StatusContext != "" {
		return c.StatusContext
	}
	return DefaultStatusContext
}

// RepoStatusContext returns status_context from the repository's own
// .codecanary/config.yml (the one CI reads), for local commands that post or
// read the status: the local config under ~/.codecanary usually does not set
// it. DefaultStatusContext when there is no repo config; a repo config that
// fails to load is warned about on stderr, since falling back then can post
// a status under a different name than CI uses.
func RepoStatusContext() string {
	return repoStatusContext(os.Stderr)
}

func repoStatusContext(warn io.Writer) string {
	path, err := FindRepoConfig()
	if err != nil {
		return DefaultStatusContext
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		_, _ = fmt.Fprintf(warn, "Warning: could not load %s (%v) — using the status name %q; if the repo sets status_context, CI uses that name instead\n", path, err, DefaultStatusContext)
		return DefaultStatusContext
	}
	return cfg.EffectiveStatusContext()
}

// IntentConfig is the `intent:` block: review the diff against the PR body's
// intent section (see writeIntentSection).
type IntentConfig struct {
	Enabled bool `yaml:"enabled"`
	// Heading is the PR body's level-2 heading holding the intent
	// (default "Intent").
	Heading string `yaml:"heading"`
	// TestingHeading is the heading that must justify each changed test
	// expectation (default "Testing").
	TestingHeading string `yaml:"testing_heading"`
}

// IntentEnabled reports whether intent review is on.
func (c *ReviewConfig) IntentEnabled() bool {
	return c != nil && c.Intent != nil && c.Intent.Enabled
}

// IntentHeadings returns the intent and testing headings with defaults.
func (c *ReviewConfig) IntentHeadings() (intent, testing string) {
	intent, testing = "Intent", "Testing"
	if c != nil && c.Intent != nil {
		if c.Intent.Heading != "" {
			intent = c.Intent.Heading
		}
		if c.Intent.TestingHeading != "" {
			testing = c.Intent.TestingHeading
		}
	}
	return intent, testing
}

// ContextFilesRule is one entry of `context_files:`: when a changed file
// matches any of Paths, every file in Files is loaded as project
// documentation. A listed file that does not exist is a config error.
type ContextFilesRule struct {
	Paths []string `yaml:"paths"`
	Files []string `yaml:"files"`
}

// ContextFilesFor returns the context files the rules select for the changed
// files, deduplicated in config order.
func (c *ReviewConfig) ContextFilesFor(changed []string) []string {
	if c == nil {
		return nil
	}
	var out []string
	for _, r := range c.ContextFiles {
		if !slices.ContainsFunc(changed, func(f string) bool { return matchesAny(f, r.Paths) }) {
			continue
		}
		for _, f := range r.Files {
			if !slices.Contains(out, f) {
				out = append(out, f)
			}
		}
	}
	return out
}

// AllContextFiles returns every file any context_files rule lists.
func (c *ReviewConfig) AllContextFiles() []string {
	if c == nil {
		return nil
	}
	var out []string
	for _, r := range c.ContextFiles {
		for _, f := range r.Files {
			if !slices.Contains(out, f) {
				out = append(out, f)
			}
		}
	}
	return out
}

// validatePolicy checks tiers, intent and context_files.
func (c *ReviewConfig) validatePolicy() error {
	if c.StatusContext != "" && strings.TrimSpace(c.StatusContext) != c.StatusContext {
		return fmt.Errorf("status_context %q has leading or trailing spaces", c.StatusContext)
	}
	if len(c.Tiers) == 0 {
		if c.DefaultTier != "" {
			return fmt.Errorf("default_tier is set but no tiers are configured")
		}
	} else {
		if c.DefaultTier == "" {
			return fmt.Errorf("tiers: default_tier is required — a PR without exactly one tier label is reviewed as that tier")
		}
		if _, ok := c.Tiers[c.DefaultTier]; !ok {
			return fmt.Errorf("default_tier %q is not one of the configured tiers", c.DefaultTier)
		}
		for _, name := range slices.Sorted(maps.Keys(c.Tiers)) {
			t := c.Tiers[name]
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("tiers: empty tier label")
			}
			if t.ReviewModel == "" {
				continue
			}
			if !t.Reviews() {
				return fmt.Errorf("tiers.%s: review_model is set but review is false", name)
			}
			if pf, ok := providers[c.Provider]; ok && pf.Validate != nil {
				mc := &ModelConfig{Provider: c.Provider, Model: t.ReviewModel, APIBase: c.APIBase, APIKeyEnv: c.APIKeyEnv}
				if err := pf.Validate(mc); err != nil {
					return fmt.Errorf("tiers.%s.review_model: %w", name, err)
				}
			}
		}
	}
	for i, r := range c.ContextFiles {
		if len(r.Paths) == 0 || len(r.Files) == 0 {
			return fmt.Errorf("context_files[%d]: both paths and files are required", i)
		}
		for _, f := range r.Files {
			if f == "" || strings.HasPrefix(f, "/") || slices.Contains(strings.Split(f, "/"), "..") {
				return fmt.Errorf("context_files[%d]: %q must be a repository-relative path", i, f)
			}
		}
	}
	return nil
}
