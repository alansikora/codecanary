package review

import (
	"context"
	"fmt"

	"github.com/alansikora/codecanary/internal/credentials"
)

func init() {
	providers["grok"] = ProviderFactory{
		New:      newGrokProvider,
		Validate: validateGrok,
		Pricing: []PricingEntry{
			// More-specific substrings first (grok-4-1-fast before grok-4).
			{"grok-4-1-fast", modelPricing{0.20, 0.50, 0.20, 0.05}},
			{"grok-4", modelPricing{2, 6, 2, 0.20}},
		},
		MaxOutputTokens: []MaxTokensEntry{
			{"grok-4-1-fast", 131_072},
			{"grok-4", 131_072},
		},
		SuggestedReviewModel: "grok-4.20-0309-non-reasoning",
		SuggestedTriageModel: "grok-4-1-fast-non-reasoning",
	}
}

func validateGrok(mc *ModelConfig) error {
	if mc.APIBase != "" {
		return fmt.Errorf("api_base is not supported by the grok provider")
	}
	return nil
}

// grokProvider implements ModelProvider for the xAI Grok API.
// Grok uses the OpenAI-compatible chat completions format.
type grokProvider struct {
	model  string
	keyEnv string
	env    []string
}

func newGrokProvider(mc *ModelConfig, env []string) ModelProvider {
	keyEnv := credentials.EnvVar
	if mc.APIKeyEnv != "" {
		keyEnv = mc.APIKeyEnv
	}
	return &grokProvider{model: mc.Model, keyEnv: keyEnv, env: env}
}

func (p *grokProvider) Run(ctx context.Context, prompt string, opts RunOpts) (*providerResult, error) {
	apiKey := lookupEnvVar(p.env, p.keyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("API key not found: set %s or run `codecanary setup local`", p.keyEnv)
	}

	chatResp, durationMS, truncated, err := doChat(ctx, "https://api.x.ai/v1", apiKey, p.model, prompt, opts.Timeout)
	if err != nil {
		return nil, err
	}

	return chatResultFromResponse(p.model, chatResp, durationMS, truncated), nil
}
