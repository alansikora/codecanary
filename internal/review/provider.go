package review

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// RunOpts configures a single model invocation.
type RunOpts struct {
	Model        string
	MaxBudgetUSD float64
	Timeout      time.Duration
}

// ModelProvider is the interface for running prompts against an LLM.
// Each provider adapter implements this interface in its own file.
//
// To add a new provider, create provider_<name>.go and register a
// ProviderFactory in the providers map via an init() function.
type ModelProvider interface {
	// Run sends a prompt and returns the result text plus usage metadata.
	Run(ctx context.Context, prompt string, opts RunOpts) (*claudeResult, error)
}

// PricingEntry maps a model name substring to its pricing.
// Entries are matched in slice order (first match wins), so more specific
// substrings must come before less specific ones (e.g. "claude-opus-4-6"
// before "claude-opus-4-").
type PricingEntry struct {
	Substring string
	Pricing   modelPricing
}

// ProviderFactory holds everything needed to construct, validate, and
// price a provider. Each provider file registers one of these via init().
type ProviderFactory struct {
	New                func(cfg *ReviewConfig, env []string) ModelProvider
	Validate           func(cfg *ReviewConfig) error
	Pricing            []PricingEntry
	SuggestedReviewModel string
	SuggestedTriageModel string
}

// providers maps provider names to their factories.
// Populated by init() functions in each provider_*.go file.
var providers = map[string]ProviderFactory{}

// providerNames returns registered provider names in sorted order.
func providerNames() []string {
	names := make([]string, 0, len(providers))
	for k := range providers {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// NewProvider constructs the appropriate ModelProvider based on config.
// The provider field is required — config validation rejects empty/unknown values.
func NewProvider(cfg *ReviewConfig, env []string) ModelProvider {
	if cfg == nil {
		panic("NewProvider called with nil config")
	}
	pf, ok := providers[cfg.Provider]
	if !ok {
		panic(fmt.Sprintf("unknown provider %q (should have been caught by config validation)", cfg.Provider))
	}
	if pf.New == nil {
		panic(fmt.Sprintf("provider %q registered without a New constructor", cfg.Provider))
	}
	return pf.New(cfg, env)
}

// lookupEnvVar finds a variable by name in the filtered environment.
func lookupEnvVar(env []string, key string) string {
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if ok && k == key {
			return v
		}
	}
	return ""
}
