package review

import (
	"context"
	"fmt"
)

// openaiProvider implements ModelProvider for the OpenAI API.
// Supports automatic prompt caching (reported via prompt_tokens_details).
// Also works with any OpenAI-compatible endpoint by overriding api_base
// (e.g. Azure OpenAI, Ollama, vLLM).
type openaiProvider struct {
	apiBase string
	keyEnv  string
	env     []string
}

func newOpenAIProvider(cfg *ReviewConfig, env []string) ModelProvider {
	apiBase := "https://api.openai.com/v1"
	if cfg.APIBase != "" {
		apiBase = cfg.APIBase
	}
	keyEnv := "OPENAI_API_KEY"
	if cfg.APIKeyEnv != "" {
		keyEnv = cfg.APIKeyEnv
	}
	return &openaiProvider{apiBase: apiBase, keyEnv: keyEnv, env: env}
}

func (p *openaiProvider) Run(ctx context.Context, prompt string, opts RunOpts) (*claudeResult, error) {
	apiKey := lookupEnvVar(p.env, p.keyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("API key not found: set %s environment variable", p.keyEnv)
	}

	chatResp, durationMS, err := doChat(ctx, p.apiBase, apiKey, opts.Model, prompt, opts.Timeout)
	if err != nil {
		return nil, err
	}

	usage := CallUsage{
		Model:      opts.Model,
		DurationMS: durationMS,
	}
	if chatResp.Usage != nil {
		usage.OutputTokens = chatResp.Usage.CompletionTokens
		// OpenAI reports cached tokens in prompt_tokens_details.
		if chatResp.Usage.PromptTokensDetails != nil && chatResp.Usage.PromptTokensDetails.CachedTokens > 0 {
			usage.CacheReadTokens = chatResp.Usage.PromptTokensDetails.CachedTokens
			usage.InputTokens = chatResp.Usage.PromptTokens - usage.CacheReadTokens
		} else {
			usage.InputTokens = chatResp.Usage.PromptTokens
		}
	}
	usage.CostUSD = estimateCost(usage)

	return &claudeResult{
		Text:  chatResp.Choices[0].Message.Content,
		Usage: usage,
	}, nil
}
