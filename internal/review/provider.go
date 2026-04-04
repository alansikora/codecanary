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

// MaxTokensEntry maps a model name substring to its maximum output token limit.
// Matched the same way as PricingEntry — first substring match wins.
type MaxTokensEntry struct {
	Substring      string
	MaxOutputTokens int
}

// AppRequirement describes an external app (e.g. a GitHub App) that must be
// installed for a provider to work. Used by the setup wizard to prompt the user.
type AppRequirement struct {
	Name       string // Human-readable name, e.g. "Claude"
	InstallURL string // URL to install the app
}

// OAuthConfig holds the parameters needed to run an OAuth PKCE flow for a provider.
// Providers that use OAuth (instead of API keys) set this on their factory.
type OAuthConfig struct {
	ClientID     string
	AuthorizeURL string
	TokenURL     string
	Scope        string
}

// ProviderFactory holds everything needed to construct, validate, and
// price a provider. Each provider file registers one of these via init().
type ProviderFactory struct {
	New                  func(mc *ModelConfig, env []string) ModelProvider
	Validate             func(mc *ModelConfig) error
	Pricing              []PricingEntry
	MaxOutputTokens      []MaxTokensEntry
	SuggestedReviewModel string
	SuggestedTriageModel string
	AppRequirement       *AppRequirement
	OAuthConfig          *OAuthConfig
}

// defaultMaxOutputTokens is used when a model is not in any provider's
// MaxOutputTokens table. Conservative enough to work with most models.
const defaultMaxOutputTokens = 16384

// lookupMaxOutputTokens returns the maximum output token limit for a model.
// Searches each provider's MaxOutputTokens entries by substring match.
// Returns defaultMaxOutputTokens if the model is unknown.
func lookupMaxOutputTokens(model string) int {
	lower := strings.ToLower(model)
	for _, name := range providerNames() {
		pf := providers[name]
		for _, entry := range pf.MaxOutputTokens {
			if strings.Contains(lower, entry.Substring) {
				return entry.MaxOutputTokens
			}
		}
	}
	return defaultMaxOutputTokens
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

// NewProviderForRole constructs a ModelProvider for a specific role (review or triage).
// The model is stored in the provider at construction time — callers do not pass it per-call.
func NewProviderForRole(mc *ModelConfig, env []string) ModelProvider {
	if mc == nil {
		panic("NewProviderForRole called with nil ModelConfig")
	}
	pf, ok := providers[mc.Provider]
	if !ok {
		panic(fmt.Sprintf("unknown provider %q (should have been caught by config validation)", mc.Provider))
	}
	if pf.New == nil {
		panic(fmt.Sprintf("provider %q registered without a New constructor", mc.Provider))
	}
	return pf.New(mc, env)
}

// GetSuggestedReviewModel returns the suggested review model for a provider.
// Used by the setup wizard to pre-select the recommended option.
func GetSuggestedReviewModel(provider string) string {
	pf, ok := providers[provider]
	if !ok {
		return ""
	}
	return pf.SuggestedReviewModel
}

// GetSuggestedTriageModel returns the suggested triage model for a provider.
// Used by the setup wizard to pre-select the recommended option.
func GetSuggestedTriageModel(provider string) string {
	pf, ok := providers[provider]
	if !ok {
		return ""
	}
	return pf.SuggestedTriageModel
}

// GetAppRequirement returns the app requirement for a provider, or nil if none.
func GetAppRequirement(provider string) *AppRequirement {
	pf, ok := providers[provider]
	if !ok {
		return nil
	}
	return pf.AppRequirement
}

// GetOAuthConfig returns the OAuth config for a provider, or nil if it uses API keys.
func GetOAuthConfig(provider string) *OAuthConfig {
	pf, ok := providers[provider]
	if !ok {
		return nil
	}
	return pf.OAuthConfig
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
