package review

import (
	"context"
	"fmt"
)

func init() {
	providers["openrouter"] = ProviderFactory{
		New:      newOpenRouterProvider,
		Validate: validateOpenRouter,
		// No pricing entries — OpenRouter proxies other providers' models,
		// which are matched by substring from those providers' pricing tables.
		DefaultReviewModel: "anthropic/claude-sonnet-4-6",
		DefaultTriageModel: "anthropic/claude-haiku-4-5-20251001",
	}
}

func validateOpenRouter(cfg *ReviewConfig) error {
	if cfg.APIBase != "" {
		return fmt.Errorf("api_base is not supported by the openrouter provider")
	}
	return nil
}

// openrouterProvider implements ModelProvider for the OpenRouter API.
// OpenRouter uses the OpenAI-compatible chat completions format and
// supports automatic prompt caching with sticky provider routing.
type openrouterProvider struct {
	keyEnv string
	env    []string
}

func newOpenRouterProvider(cfg *ReviewConfig, env []string) ModelProvider {
	keyEnv := "OPENROUTER_API_KEY"
	if cfg.APIKeyEnv != "" {
		keyEnv = cfg.APIKeyEnv
	}
	return &openrouterProvider{keyEnv: keyEnv, env: env}
}

func (p *openrouterProvider) Run(ctx context.Context, prompt string, opts RunOpts) (*claudeResult, error) {
	apiKey := lookupEnvVar(p.env, p.keyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("API key not found: set %s environment variable", p.keyEnv)
	}

	chatResp, durationMS, err := doChat(ctx, "https://openrouter.ai/api/v1", apiKey, opts.Model, prompt, opts.Timeout)
	if err != nil {
		return nil, err
	}

	usage := CallUsage{
		Model:      opts.Model,
		DurationMS: durationMS,
	}
	if chatResp.Usage != nil {
		usage.InputTokens = chatResp.Usage.PromptTokens
		usage.OutputTokens = chatResp.Usage.CompletionTokens
		// OpenRouter reports cached tokens the same way as OpenAI.
		if chatResp.Usage.PromptTokensDetails != nil && chatResp.Usage.PromptTokensDetails.CachedTokens > 0 {
			usage.CacheReadTokens = chatResp.Usage.PromptTokensDetails.CachedTokens
			usage.InputTokens = max(0, chatResp.Usage.PromptTokens-usage.CacheReadTokens)
		}
	}
	usage.CostUSD = estimateCost(usage)

	text := ""
	if len(chatResp.Choices) > 0 {
		text = chatResp.Choices[0].Message.Content
	}

	return &claudeResult{
		Text:  text,
		Usage: usage,
	}, nil
}
