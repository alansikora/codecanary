package review

import (
	"context"
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

// openrouterProvider implements ModelProvider for the OpenRouter API.
// OpenRouter uses the OpenAI-compatible chat completions format and
// supports automatic prompt caching with sticky provider routing.
type openrouterProvider struct {
	model  string
	keyEnv string
	env    []string
}

func newOpenRouterProvider(mc *ModelConfig, env []string) ModelProvider {
	keyEnv := credentials.EnvVar
	if mc.APIKeyEnv != "" {
		keyEnv = mc.APIKeyEnv
	}
	return &openrouterProvider{model: mc.Model, keyEnv: keyEnv, env: env}
}

func (p *openrouterProvider) Run(ctx context.Context, prompt string, opts RunOpts) (*providerResult, error) {
	apiKey := lookupEnvVar(p.env, p.keyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("API key not found: set %s or run `codecanary setup local`", p.keyEnv)
	}

	chatResp, durationMS, truncated, err := doChat(ctx, "https://openrouter.ai/api/v1", apiKey, p.model, prompt, opts.Timeout)
	if err != nil {
		return nil, err
	}

	return chatResultFromResponse(p.model, chatResp, durationMS, truncated), nil
}
