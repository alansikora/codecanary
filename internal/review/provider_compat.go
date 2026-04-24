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
)

// OpenAI-compatible chat completions types shared by the openai and
// openrouter adapters. Other providers that speak this format can
// reuse these types and the doChat helper.

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *chatUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

type chatUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

// doChat sends a chat completions request and returns the raw response plus
// a truncated flag. Individual provider adapters call this and then extract
// usage in their own way (e.g. OpenAI parses prompt_tokens_details for caching).
func doChat(ctx context.Context, apiBase, apiKey, model, prompt string, timeout time.Duration) (*chatResponse, int, bool, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	maxTokens := lookupMaxOutputTokens(model)
	reqBody := chatRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "user", Content: prompt},
		},
		MaxTokens: maxTokens,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, 0, false, fmt.Errorf("marshaling request: %w", err)
	}

	url := strings.TrimRight(apiBase, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, 0, false, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, 0, false, fmt.Errorf("request timed out after %s", timeout)
		}
		return nil, 0, false, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	durationMS := int(time.Since(start).Milliseconds())

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, false, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, 0, false, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return nil, 0, false, fmt.Errorf("parsing response: %w", err)
	}

	if chatResp.Error != nil {
		return nil, 0, false, fmt.Errorf("API error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return nil, 0, false, fmt.Errorf("API returned no choices")
	}

	truncated := chatResp.Choices[0].FinishReason == "length"

	return &chatResp, durationMS, truncated, nil
}

// openaiCompatProvider is the shared ModelProvider implementation for
// backends that speak the OpenAI chat completions format (openai, openrouter,
// grok, plus any OpenAI-compatible endpoint configured via api_base). Each
// provider file registers its own factory that constructs this struct with
// the right apiBase — the Run logic is identical, so keeping it here prevents
// every new OpenAI-compatible provider from copy-pasting HTTP boilerplate.
type openaiCompatProvider struct {
	model   string
	apiBase string
	keyEnv  string
	env     []string
}

func (p *openaiCompatProvider) Run(ctx context.Context, prompt string, opts RunOpts) (*providerResult, error) {
	apiKey := lookupEnvVar(p.env, p.keyEnv)
	if apiKey == "" {
		return nil, errMissingAPIKey(p.keyEnv)
	}

	chatResp, durationMS, truncated, err := doChat(ctx, p.apiBase, apiKey, p.model, prompt, opts.Timeout)
	if err != nil {
		return nil, err
	}

	return chatResultFromResponse(p.model, chatResp, durationMS, truncated), nil
}

// chatResultFromResponse builds a providerResult from a chat completions
// response. Both the openai and openrouter adapters call this after doChat.
func chatResultFromResponse(model string, chatResp *chatResponse, durationMS int, truncated bool) *providerResult {
	usage := CallUsage{
		Model:      model,
		DurationMS: durationMS,
	}
	if chatResp.Usage != nil {
		usage.OutputTokens = chatResp.Usage.CompletionTokens
		usage.InputTokens = chatResp.Usage.PromptTokens
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

	return &providerResult{
		Text:      text,
		Usage:     usage,
		Truncated: truncated,
	}
}
