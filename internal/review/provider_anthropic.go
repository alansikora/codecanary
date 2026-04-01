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

// anthropicProvider implements ModelProvider using the native Anthropic Messages API.
// Supports prompt caching for significant cost savings on repeated calls.
type anthropicProvider struct {
	keyEnv string   // env var name holding the API key
	env    []string // filtered environment
}

// anthropicRequest is the Anthropic /v1/messages request format.
type anthropicRequest struct {
	Model        string                   `json:"model"`
	MaxTokens    int                      `json:"max_tokens"`
	System       []anthropicContentBlock  `json:"system,omitempty"`
	Messages     []anthropicMessage       `json:"messages"`
	CacheControl *anthropicCacheControl   `json:"cache_control,omitempty"`
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

func (p *anthropicProvider) Run(ctx context.Context, prompt string, opts RunOpts) (*claudeResult, error) {
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

	// Use top-level cache_control for automatic caching of the entire prompt.
	reqBody := anthropicRequest{
		Model:     opts.Model,
		MaxTokens: 16384,
		CacheControl: &anthropicCacheControl{Type: "ephemeral"},
		Messages: []anthropicMessage{
			{Role: "user", Content: prompt},
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
			return nil, fmt.Errorf("Anthropic API request timed out after %s", timeout)
		}
		return nil, fmt.Errorf("Anthropic API request failed: %w", err)
	}
	defer resp.Body.Close()
	durationMS := int(time.Since(start).Milliseconds())

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Anthropic API returned status %d: %s", resp.StatusCode, string(body))
	}

	var msgResp anthropicResponse
	if err := json.Unmarshal(body, &msgResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if msgResp.Error != nil {
		return nil, fmt.Errorf("Anthropic API error: %s", msgResp.Error.Message)
	}

	// Extract text from content blocks.
	var textParts []string
	for _, block := range msgResp.Content {
		if block.Type == "text" {
			textParts = append(textParts, block.Text)
		}
	}
	if len(textParts) == 0 {
		return nil, fmt.Errorf("Anthropic API returned no text content")
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

	return &claudeResult{
		Text:  strings.Join(textParts, ""),
		Usage: usage,
	}, nil
}

// lookupEnv finds a variable by name in the filtered environment.
func (p *anthropicProvider) lookupEnv(key string) string {
	for _, e := range p.env {
		k, v, ok := strings.Cut(e, "=")
		if ok && k == key {
			return v
		}
	}
	return ""
}
