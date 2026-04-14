package setup

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// ValidateAPIKey makes a lightweight test call to verify the key works.
func ValidateAPIKey(provider, apiKey string) error {
	switch provider {
	case "anthropic":
		return validateAnthropic(apiKey)
	case "openai":
		return validateOpenAI(apiKey)
	case "grok":
		return validateGrokKey(apiKey)
	case "openrouter":
		return validateOpenRouter(apiKey)
	case "claude":
		return nil // Claude CLI uses its own auth; use CheckClaudeCLI() instead
	default:
		return fmt.Errorf("unknown provider: %s", provider)
	}
}

// CheckClaudeCLI verifies that the claude binary is available.
func CheckClaudeCLI() error {
	path, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude CLI not found in PATH — install it from https://docs.anthropic.com/en/docs/claude-code/overview")
	}
	_ = path
	return nil
}

// doValidationRequest executes a validation HTTP request with a shared timeout
// and checks for connection failure and invalid API key (401).
// On success the caller must close resp.Body.
func doValidationRequest(req *http.Request) (*http.Response, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}
	if resp.StatusCode == 401 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("invalid API key (401 Unauthorized)")
	}
	return resp, nil
}

func validateAnthropic(apiKey string) error {
	body := `{"model":"claude-haiku-4-5-20251001","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`
	req, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := doValidationRequest(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == 403 {
		return fmt.Errorf("API key does not have permission (403 Forbidden)")
	}
	// Any 2xx or 4xx that isn't auth-related means the key is valid.
	if resp.StatusCode >= 200 && resp.StatusCode < 500 {
		return nil
	}
	return fmt.Errorf("unexpected status %d from Anthropic API", resp.StatusCode)
}

func validateOpenAI(apiKey string) error {
	body := `{"model":"gpt-4o-mini","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`
	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := doValidationRequest(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 500 {
		return nil
	}
	return fmt.Errorf("unexpected status %d from OpenAI API", resp.StatusCode)
}

func validateGrokKey(apiKey string) error {
	body := `{"model":"grok-4-1-fast-non-reasoning","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`
	req, err := http.NewRequest("POST", "https://api.x.ai/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := doValidationRequest(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 500 {
		return nil
	}
	return fmt.Errorf("unexpected status %d from xAI API", resp.StatusCode)
}

func validateOpenRouter(apiKey string) error {
	// OpenRouter's /auth/key endpoint validates without making a model call.
	req, err := http.NewRequest("GET", "https://openrouter.ai/api/v1/auth/key", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := doValidationRequest(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return fmt.Errorf("unexpected status %d from OpenRouter API", resp.StatusCode)
	}

	var result struct {
		Data struct {
			Label string `json:"label"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil // response parsed means key worked
	}
	return nil
}
