package review

import (
	"context"
	"time"
)

// RunOpts configures a single model invocation.
type RunOpts struct {
	Model        string
	MaxBudgetUSD float64
	Timeout      time.Duration
}

// ModelProvider is the interface for running prompts against an LLM.
type ModelProvider interface {
	// Run sends a prompt and returns the result text plus usage metadata.
	Run(ctx context.Context, prompt string, opts RunOpts) (*claudeResult, error)
}

// NewProvider constructs the appropriate ModelProvider based on config.
//   - "anthropic": native Anthropic Messages API with prompt caching
//   - "api": OpenAI-compatible HTTP provider (OpenRouter, OpenAI, Ollama, etc.)
//   - "claude" or "" (default): Claude CLI
func NewProvider(cfg *ReviewConfig, env []string) ModelProvider {
	provider := ""
	if cfg != nil {
		provider = cfg.Provider
	}
	switch provider {
	case "anthropic":
		keyEnv := cfg.APIKeyEnv
		if keyEnv == "" {
			keyEnv = "ANTHROPIC_API_KEY"
		}
		return &anthropicProvider{
			keyEnv: keyEnv,
			env:    env,
		}
	case "api":
		apiBase := cfg.APIBase
		if apiBase == "" {
			apiBase = "https://openrouter.ai/api/v1"
		}
		keyEnv := cfg.APIKeyEnv
		if keyEnv == "" {
			keyEnv = "OPENROUTER_API_KEY"
		}
		return &apiProvider{
			apiBase: apiBase,
			keyEnv:  keyEnv,
			env:     env,
		}
	default:
		return &claudeCLIProvider{env: env}
	}
}
