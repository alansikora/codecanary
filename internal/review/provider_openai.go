package review

import (
	"context"
	"fmt"
	"strings"
)

func init() {
	providers["openai"] = ProviderFactory{
		New:      newOpenAIProvider,
		Validate: validateOpenAI,
		Pricing: []PricingEntry{
			// GPT-5.4 family
			{"gpt-5.4-nano", modelPricing{0.20, 1.25, 0.20, 0.02}},
			{"gpt-5.4-mini", modelPricing{0.75, 4.50, 0.75, 0.075}},
			{"gpt-5.4", modelPricing{2.50, 15, 2.50, 0.25}},
			// GPT-4.1 family
			{"gpt-4.1-nano", modelPricing{0.05, 0.20, 0.05, 0.025}},
			{"gpt-4.1-mini", modelPricing{0.20, 0.80, 0.20, 0.10}},
			{"gpt-4.1", modelPricing{2, 8, 2, 0.50}},
			// GPT-4o family
			{"gpt-4o-mini", modelPricing{0.15, 0.60, 0.15, 0.075}},
			{"gpt-4o", modelPricing{2.50, 10, 2.50, 1.25}},
			// o-series reasoning
			{"o4-mini", modelPricing{0.55, 2.20, 0.55, 0.275}},
			{"o3-mini", modelPricing{0.55, 2.20, 0.55, 0.55}},
			{"o3", modelPricing{2, 8, 2, 0.50}},
			{"o1-mini", modelPricing{0.55, 2.20, 0.55, 0.55}},
			{"o1", modelPricing{15, 60, 15, 7.50}},
		},
		SuggestedReviewModel: "gpt-5.4",
		SuggestedTriageModel: "gpt-5.4-mini",
	}
}

func validateOpenAI(cfg *ReviewConfig) error {
	if cfg.APIBase != "" && !isValidURL(cfg.APIBase) {
		return fmt.Errorf("invalid api_base %q: must be an HTTP(S) URL", cfg.APIBase)
	}
	// Skip model checks when api_base is overridden — custom endpoints can serve any model.
	if cfg.APIBase == "" {
		for _, model := range []string{cfg.ReviewModel, cfg.TriageModel} {
			if model == "" {
				continue
			}
			if strings.Contains(model, "/") {
				return fmt.Errorf("model %q looks like an OpenRouter model (contains '/') — use provider: openrouter instead", model)
			}
			if strings.HasPrefix(model, "claude-") || model == "haiku" || model == "sonnet" || model == "opus" {
				return fmt.Errorf("model %q looks like an Anthropic model — use provider: anthropic, or provider: openrouter to mix models", model)
			}
		}
	}
	return nil
}

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
		return nil, fmt.Errorf("API key not found: set %s or run `codecanary setup local`", p.keyEnv)
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
			usage.InputTokens = max(0, chatResp.Usage.PromptTokens-usage.CacheReadTokens)
		} else {
			usage.InputTokens = chatResp.Usage.PromptTokens
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
