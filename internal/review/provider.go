package review

import (
	"context"
	"fmt"
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
// To add a new provider:
//  1. Create provider_<name>.go implementing ModelProvider
//  2. Add a constructor to the providers map below
//  3. Add the name to validProviders in config.go
type ModelProvider interface {
	// Run sends a prompt and returns the result text plus usage metadata.
	Run(ctx context.Context, prompt string, opts RunOpts) (*claudeResult, error)
}

// providers maps provider names to their constructor functions.
var providers = map[string]func(cfg *ReviewConfig, env []string) ModelProvider{
	"anthropic":  newAnthropicProvider,
	"openai":     newOpenAIProvider,
	"openrouter": newOpenRouterProvider,
	"claude":     newClaudeCLIProvider,
}

// NewProvider constructs the appropriate ModelProvider based on config.
// The provider field is required — config validation rejects empty/unknown values.
// When cfg is nil (e.g. config generation), falls back to the Claude CLI provider.
func NewProvider(cfg *ReviewConfig, env []string) ModelProvider {
	if cfg == nil {
		return &claudeCLIProvider{env: env}
	}
	factory, ok := providers[cfg.Provider]
	if !ok {
		panic(fmt.Sprintf("unknown provider %q (should have been caught by config validation)", cfg.Provider))
	}
	return factory(cfg, env)
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
