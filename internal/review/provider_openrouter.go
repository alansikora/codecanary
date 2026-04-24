package review

import (
	"fmt"

	"github.com/alansikora/codecanary/internal/credentials"
)

func init() {
	providers["openrouter"] = ProviderFactory{
		New:      newOpenRouterProvider,
		Validate: validateOpenRouter,
		// No pricing or MaxOutputTokens entries — OpenRouter proxies other
		// providers' models, which are matched by substring from those
		// providers' tables.
		SuggestedReviewModel: "anthropic/claude-sonnet-4-6",
		SuggestedTriageModel: "anthropic/claude-haiku-4-5-20251001",
	}
}

func validateOpenRouter(mc *ModelConfig) error {
	if mc.APIBase != "" {
		return fmt.Errorf("api_base is not supported by the openrouter provider")
	}
	return nil
}

// newOpenRouterProvider constructs the OpenRouter adapter. OpenRouter uses the
// OpenAI-compatible chat completions format and supports automatic prompt
// caching with sticky provider routing. Transport logic lives in
// openaiCompatProvider; api_base is fixed because OpenRouter is only ever
// reached at its canonical URL.
func newOpenRouterProvider(mc *ModelConfig, env []string) ModelProvider {
	keyEnv := credentials.EnvVar
	if mc.APIKeyEnv != "" {
		keyEnv = mc.APIKeyEnv
	}
	return &openaiCompatProvider{
		model:   mc.Model,
		apiBase: "https://openrouter.ai/api/v1",
		keyEnv:  keyEnv,
		env:     env,
	}
}
