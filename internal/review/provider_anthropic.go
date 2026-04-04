package review

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alansikora/codecanary/internal/credentials"
)

func init() {
	providers["anthropic"] = ProviderFactory{
		New:      newAnthropicProvider,
		Validate: validateAnthropic,
		Pricing: []PricingEntry{
			// Opus 4.6 / 4.5
			{"claude-opus-4-6", modelPricing{5, 25, 6.25, 0.50}},
			{"claude-opus-4-5", modelPricing{5, 25, 6.25, 0.50}},
			// Opus 4.1 / 4
			{"claude-opus-4-1", modelPricing{15, 75, 18.75, 1.50}},
			{"claude-opus-4-", modelPricing{15, 75, 18.75, 1.50}},
			// Sonnet 4.6 / 4.5 / 4
			{"claude-sonnet-4", modelPricing{3, 15, 3.75, 0.30}},
			// Haiku 4.5
			{"claude-haiku-4-5", modelPricing{1, 5, 1.25, 0.10}},
			// Haiku 3.5
			{"claude-haiku-3-5", modelPricing{0.80, 4, 1.0, 0.08}},
			// Haiku 3
			{"claude-haiku-3", modelPricing{0.25, 1.25, 0.30, 0.03}},
		},
		MaxOutputTokens: []MaxTokensEntry{
			// Opus 4.6 / 4.5: 128k output
			{"claude-opus-4-6", 128_000},
			{"claude-opus-4-5", 128_000},
			// Opus 4.1 / 4: 32k output
			{"claude-opus-4-1", 32_000},
			{"claude-opus-4-", 32_000},
			// Sonnet 4.6 / 4.5 / 4: 64k output
			{"claude-sonnet-4", 64_000},
			// Haiku 4.5: 64k output
			{"claude-haiku-4-5", 64_000},
			// Haiku 3.5: 8k output
			{"claude-haiku-3-5", 8_192},
			// Haiku 3: 4k output
			{"claude-haiku-3", 4_096},
		},
		SuggestedReviewModel: "claude-sonnet-4-6",
		SuggestedTriageModel: "claude-haiku-4-5-20251001",
	}
}

func validateAnthropic(mc *ModelConfig) error {
	if mc.APIBase != "" {
		return fmt.Errorf("api_base is not supported by the anthropic provider")
	}
	return nil
}

// anthropicProvider implements ModelProvider using the native Anthropic Messages API.
// Supports prompt caching for significant cost savings on repeated calls.
type anthropicProvider struct {
	model  string   // model to use for every call
	keyEnv string   // env var name holding the API key
	env    []string // filtered environment
}

func newAnthropicProvider(mc *ModelConfig, env []string) ModelProvider {
	keyEnv := credentials.EnvVar
	if mc.APIKeyEnv != "" {
		keyEnv = mc.APIKeyEnv
	}
	return &anthropicProvider{model: mc.Model, keyEnv: keyEnv, env: env}
}

// anthropicRequest is the Anthropic /v1/messages request format.
type anthropicRequest struct {
	Model     string                  `json:"model"`
	MaxTokens int                     `json:"max_tokens"`
	System    []anthropicContentBlock `json:"system,omitempty"`
	Messages  []anthropicMessage      `json:"messages"`
}

type anthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []anthropicContentBlock
}

type anthropicContentBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicCacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// anthropicResponse is the Anthropic /v1/messages response format.
type anthropicResponse struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (p *anthropicProvider) Run(ctx context.Context, prompt string, opts RunOpts) (*providerResult, error) {
	apiKey := lookupEnvVar(p.env, p.keyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("API key not found: set %s or run `codecanary setup local`", p.keyEnv)
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Place cache_control on the content block so the prompt is cached.
	maxTokens := lookupMaxOutputTokens(p.model)
	reqBody := anthropicRequest{
		Model:     p.model,
		MaxTokens: maxTokens,
		Messages: []anthropicMessage{
			{
				Role: "user",
				Content: []anthropicContentBlock{
					{
						Type:         "text",
						Text:         prompt,
						CacheControl: &anthropicCacheControl{Type: "ephemeral"},
					},
				},
			},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("Anthropic API request timed out after %s", timeout) //nolint:staticcheck // proper noun
		}
		return nil, fmt.Errorf("Anthropic API request failed: %w", err) //nolint:staticcheck // proper noun
	}
	defer func() { _ = resp.Body.Close() }()
	durationMS := int(time.Since(start).Milliseconds())

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Anthropic API returned status %d: %s", resp.StatusCode, string(body)) //nolint:staticcheck // proper noun
	}

	var msgResp anthropicResponse
	if err := json.Unmarshal(body, &msgResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if msgResp.Error != nil {
		return nil, fmt.Errorf("Anthropic API error: %s", msgResp.Error.Message) //nolint:staticcheck // proper noun
	}

	truncated := msgResp.StopReason == "max_tokens"

	// Extract text from content blocks.
	var textParts []string
	for _, block := range msgResp.Content {
		if block.Type == "text" {
			textParts = append(textParts, block.Text)
		}
	}
	if len(textParts) == 0 {
		return nil, fmt.Errorf("Anthropic API returned no text content") //nolint:staticcheck // proper noun
	}

	usage := CallUsage{
		Model:             msgResp.Model,
		InputTokens:       msgResp.Usage.InputTokens,
		OutputTokens:      msgResp.Usage.OutputTokens,
		CacheReadTokens:   msgResp.Usage.CacheReadInputTokens,
		CacheCreateTokens: msgResp.Usage.CacheCreationInputTokens,
		DurationMS:        durationMS,
	}
	usage.CostUSD = estimateCost(usage)

	return &providerResult{
		Text:      strings.Join(textParts, ""),
		Usage:     usage,
		Truncated: truncated,
	}, nil
}

