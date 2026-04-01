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

// apiProvider implements ModelProvider using the OpenAI-compatible chat completions API.
// Works with OpenRouter, OpenAI, Azure, Ollama, and any compatible endpoint.
type apiProvider struct {
	apiBase string   // e.g. "https://openrouter.ai/api/v1"
	keyEnv  string   // env var name holding the API key
	env     []string // filtered environment
}

// chatRequest is the OpenAI chat completions request format.
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

// chatResponse is the OpenAI chat completions response format.
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
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

func (p *apiProvider) Run(ctx context.Context, prompt string, opts RunOpts) (*claudeResult, error) {
	apiKey := p.lookupEnv(p.keyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("API key not found: set %s environment variable", p.keyEnv)
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	reqBody := chatRequest{
		Model: opts.Model,
		Messages: []chatMessage{
			{Role: "user", Content: prompt},
		},
		MaxTokens: 16384,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	url := strings.TrimRight(p.apiBase, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("API request timed out after %s", timeout)
		}
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()
	durationMS := int(time.Since(start).Milliseconds())

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if chatResp.Error != nil {
		return nil, fmt.Errorf("API error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("API returned no choices")
	}

	text := chatResp.Choices[0].Message.Content

	usage := CallUsage{
		Model:      opts.Model,
		DurationMS: durationMS,
	}
	if chatResp.Usage != nil {
		usage.OutputTokens = chatResp.Usage.CompletionTokens
		if chatResp.Usage.PromptTokensDetails != nil && chatResp.Usage.PromptTokensDetails.CachedTokens > 0 {
			usage.CacheReadTokens = chatResp.Usage.PromptTokensDetails.CachedTokens
			usage.InputTokens = chatResp.Usage.PromptTokens - usage.CacheReadTokens
		} else {
			usage.InputTokens = chatResp.Usage.PromptTokens
		}
	}
	usage.CostUSD = estimateCost(usage)

	return &claudeResult{
		Text:  text,
		Usage: usage,
	}, nil
}

// lookupEnv finds a variable by name in the filtered environment.
func (p *apiProvider) lookupEnv(key string) string {
	for _, e := range p.env {
		k, v, ok := strings.Cut(e, "=")
		if ok && k == key {
			return v
		}
	}
	return ""
}
