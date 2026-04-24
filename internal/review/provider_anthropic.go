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
			// Opus 4.7
			{"claude-opus-4-7", modelPricing{15, 75, 18.75, 1.50}},
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
			// Opus 4.7 / 4.6 / 4.5: 128k output
			{"claude-opus-4-7", 128_000},
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

// anthropicAPIURL is the Messages endpoint. Exposed as a variable so tests
// can point the provider at an httptest server.
var anthropicAPIURL = "https://api.anthropic.com/v1/messages"

// anthropicProvider implements ModelProvider using the native Anthropic Messages API.
// Supports prompt caching for significant cost savings on repeated calls.
type anthropicProvider struct {
	model        string   // executor model used for every call
	advisorModel string   // optional advisor model — enables the server-side advisor tool when non-empty
	keyEnv       string   // env var name holding the API key
	env          []string // filtered environment
}

func newAnthropicProvider(mc *ModelConfig, env []string) ModelProvider {
	keyEnv := credentials.EnvVar
	if mc.APIKeyEnv != "" {
		keyEnv = mc.APIKeyEnv
	}
	return &anthropicProvider{model: mc.Model, advisorModel: mc.AdvisorModel, keyEnv: keyEnv, env: env}
}

// anthropicRequest is the Anthropic /v1/messages request format.
type anthropicRequest struct {
	Model     string                  `json:"model"`
	MaxTokens int                     `json:"max_tokens"`
	System    []anthropicContentBlock `json:"system,omitempty"`
	Messages  []anthropicMessage      `json:"messages"`
	Tools     []anthropicTool         `json:"tools,omitempty"`
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

// anthropicTool represents a tool entry in the request's tools array. Only the
// advisor server-side tool is populated today; fields are kept optional so we
// can extend to additional tool shapes without breaking the wire format.
type anthropicTool struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Model string `json:"model,omitempty"`
}

// anthropicResponse is the Anthropic /v1/messages response format.
type anthropicResponse struct {
	ID      string                  `json:"id"`
	Type    string                  `json:"type"`
	Role    string                  `json:"role"`
	Content []anthropicResponseBlock `json:"content"`
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens              int                   `json:"input_tokens"`
		OutputTokens             int                   `json:"output_tokens"`
		CacheCreationInputTokens int                   `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int                   `json:"cache_read_input_tokens"`
		Iterations               []anthropicIteration  `json:"iterations"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// anthropicResponseBlock handles the assistant's content array. Only text
// blocks carry user-visible output; server_tool_use and advisor_tool_result
// blocks are metadata from the advisor tool and must be filtered out.
type anthropicResponseBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// anthropicIteration is a single sub-inference entry in usage.iterations[].
// Executor iterations have type "message"; advisor sub-inferences have
// type "advisor_message" with the advisor model ID attached.
type anthropicIteration struct {
	Type                     string `json:"type"`
	Model                    string `json:"model"`
	InputTokens              int    `json:"input_tokens"`
	OutputTokens             int    `json:"output_tokens"`
	CacheCreationInputTokens int    `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int    `json:"cache_read_input_tokens"`
}

func (p *anthropicProvider) Run(ctx context.Context, prompt string, opts RunOpts) (*providerResult, error) {
	apiKey := lookupEnvVar(p.env, p.keyEnv)
	if apiKey == "" {
		return nil, errMissingAPIKey(p.keyEnv)
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
	if p.advisorModel != "" {
		reqBody.Tools = []anthropicTool{{
			Type:  advisorToolType,
			Name:  advisorToolName,
			Model: p.advisorModel,
		}}
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", anthropicAPIURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	if p.advisorModel != "" {
		req.Header.Set("anthropic-beta", advisorBetaHeader)
	}

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
		// Try to decode the structured {error:{type,message}} envelope so we
		// can surface the upstream message; fall back to the raw body when
		// the body is empty or not JSON.
		var errResp anthropicResponse
		msg := ""
		if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error != nil {
			msg = errResp.Error.Message
		}
		return nil, classifyProviderError("anthropic", resp.StatusCode, msg, string(body))
	}

	var msgResp anthropicResponse
	if err := json.Unmarshal(body, &msgResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if msgResp.Error != nil {
		return nil, classifyProviderError("anthropic", resp.StatusCode, msgResp.Error.Message, string(body))
	}

	truncated := msgResp.StopReason == "max_tokens"

	// Extract text from content blocks. server_tool_use and
	// advisor_tool_result blocks are advisor-tool metadata and never user
	// output; filter them out so advisor calls do not contaminate the
	// parsed findings JSON.
	var textParts []string
	for _, block := range msgResp.Content {
		if block.Type == "text" {
			textParts = append(textParts, block.Text)
		}
	}
	if len(textParts) == 0 {
		return nil, fmt.Errorf("Anthropic API returned no text content") //nolint:staticcheck // proper noun
	}

	result := &providerResult{
		Text:       strings.Join(textParts, ""),
		DurationMS: durationMS,
		Truncated:  truncated,
	}

	// When the advisor tool ran, usage.iterations[] carries a per-sub-inference
	// breakdown. Split it so executor tokens bill at the executor's rate and
	// advisor tokens bill at the advisor's rate. For plain (non-advisor) calls
	// the iterations array is empty; fall back to the top-level usage totals.
	if len(msgResp.Usage.Iterations) > 0 {
		for _, it := range msgResp.Usage.Iterations {
			model := it.Model
			if model == "" {
				model = msgResp.Model
			}
			u := CallUsage{
				Model:             model,
				InputTokens:       it.InputTokens,
				OutputTokens:      it.OutputTokens,
				CacheReadTokens:   it.CacheReadInputTokens,
				CacheCreateTokens: it.CacheCreationInputTokens,
				DurationMS:        durationMS,
			}
			u.CostUSD = estimateCost(u)
			result.ModelUsages = append(result.ModelUsages, u)
		}
		// Aggregate Usage reflects the executor-visible totals reported by the
		// API so existing dashboards that read result.Usage still work.
		agg := CallUsage{
			Model:             msgResp.Model,
			InputTokens:       msgResp.Usage.InputTokens,
			OutputTokens:      msgResp.Usage.OutputTokens,
			CacheReadTokens:   msgResp.Usage.CacheReadInputTokens,
			CacheCreateTokens: msgResp.Usage.CacheCreationInputTokens,
			DurationMS:        durationMS,
		}
		var total float64
		for _, mu := range result.ModelUsages {
			total += mu.CostUSD
		}
		agg.CostUSD = total
		result.Usage = agg
		return result, nil
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
	result.Usage = usage
	return result, nil
}
