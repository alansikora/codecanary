package review

import (
	"fmt"

	"github.com/alansikora/codecanary/internal/credentials"
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
		// Entries matched by substring — more-specific names (e.g. gpt-5.4-mini)
		// must come before less-specific ones (e.g. gpt-5.4).
		MaxOutputTokens: []MaxTokensEntry{
			// GPT-5.4 family: 128k output
			{"gpt-5.4-nano", 128_000},
			{"gpt-5.4-mini", 128_000},
			{"gpt-5.4", 128_000},
			// GPT-4.1 family: 32k output
			{"gpt-4.1-nano", 32_768},
			{"gpt-4.1-mini", 32_768},
			{"gpt-4.1", 32_768},
			// GPT-4o family: 16k output
			{"gpt-4o-mini", 16_384},
			{"gpt-4o", 16_384},
			// o-series reasoning — more-specific names must precede less-specific
			// (o1-mini before o1, o3-mini before o3).
			{"o4-mini", 100_000},
			{"o3-mini", 100_000},
			{"o3", 100_000},
			{"o1-mini", 65_536},
			{"o1", 100_000},
		},
		SuggestedReviewModel: "gpt-5.4",
		SuggestedTriageModel: "gpt-5.4-mini",
	}
}

func validateOpenAI(mc *ModelConfig) error {
	if mc.APIBase != "" && !isValidURL(mc.APIBase) {
		return fmt.Errorf("invalid api_base %q: must be an HTTP(S) URL", mc.APIBase)
	}
	return nil
}

// newOpenAIProvider constructs the OpenAI adapter. OpenAI supports automatic
// prompt caching (reported via prompt_tokens_details) and works with any
// OpenAI-compatible endpoint by overriding api_base (e.g. Azure OpenAI,
// Ollama, vLLM). The transport logic lives in openaiCompatProvider.
func newOpenAIProvider(mc *ModelConfig, env []string) ModelProvider {
	apiBase := "https://api.openai.com/v1"
	if mc.APIBase != "" {
		apiBase = mc.APIBase
	}
	keyEnv := credentials.EnvVar
	if mc.APIKeyEnv != "" {
		keyEnv = mc.APIKeyEnv
	}
	return &openaiCompatProvider{model: mc.Model, apiBase: apiBase, keyEnv: keyEnv, env: env}
}
