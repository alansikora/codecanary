package review

import (
	"fmt"

	"github.com/alansikora/codecanary/internal/credentials"
)

func init() {
	providers["grok"] = ProviderFactory{
		New:      newGrokProvider,
		Validate: validateGrok,
		Pricing: []PricingEntry{
			// xAI uses two naming conventions: dot-separated for the flagship
			// family (grok-4.20-*) and hyphen-separated for the fast tier
			// (grok-4-1-fast-*). Both are correct per the xAI API docs.
			// More-specific substrings must come before the grok-4 catch-all.
			{"grok-4-1-fast", modelPricing{0.20, 0.50, 0.20, 0.05}},
			{"grok-4.20", modelPricing{2, 6, 2, 0.20}},
			{"grok-4", modelPricing{2, 6, 2, 0.20}},
		},
		MaxOutputTokens: []MaxTokensEntry{
			{"grok-4-1-fast", 131_072},
			{"grok-4.20", 131_072},
			{"grok-4", 131_072},
		},
		SuggestedReviewModel: "grok-4.20-0309-non-reasoning",
		SuggestedTriageModel: "grok-4-1-fast-non-reasoning",
	}
}

func validateGrok(mc *ModelConfig) error {
	if mc.APIBase != "" && !isValidURL(mc.APIBase) {
		return fmt.Errorf("invalid api_base %q: must be an HTTP(S) URL", mc.APIBase)
	}
	return nil
}

// newGrokProvider constructs the xAI Grok adapter. Grok uses the
// OpenAI-compatible chat completions format, so transport logic lives in
// openaiCompatProvider.
func newGrokProvider(mc *ModelConfig, env []string) ModelProvider {
	apiBase := "https://api.x.ai/v1"
	if mc.APIBase != "" {
		apiBase = mc.APIBase
	}
	keyEnv := credentials.EnvVar
	if mc.APIKeyEnv != "" {
		keyEnv = mc.APIKeyEnv
	}
	return &openaiCompatProvider{model: mc.Model, apiBase: apiBase, keyEnv: keyEnv, env: env}
}
