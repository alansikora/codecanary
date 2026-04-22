package review

import (
	"strings"
	"testing"
)

func TestClassifyProviderError_KindFromStatus(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		status   int
		message  string
		wantKind ErrorKind
	}{
		{"anthropic 429", "anthropic", 429, "rate limit hit", ErrorKindRateLimit},
		{"anthropic 401", "anthropic", 401, "bad key", ErrorKindAuth},
		{"anthropic 403", "anthropic", 403, "forbidden", ErrorKindAuth},
		{"anthropic 500", "anthropic", 500, "oops", ErrorKindServer},
		{"anthropic 502", "anthropic", 502, "bad gateway", ErrorKindServer},
		{"anthropic 400", "anthropic", 400, "bad request", ErrorKindUnknown},
		{"claude 429", "claude", 429, "", ErrorKindRateLimit},
		{"claude cli zero status rate-limit text", "claude", 0, "You've hit your limit · resets 8pm", ErrorKindRateLimit},
		{"claude cli zero status quota text", "claude", 0, "monthly quota exceeded", ErrorKindRateLimit},
		{"openai 429", "openai", 429, "", ErrorKindRateLimit},
		{"grok 401", "grok", 401, "", ErrorKindAuth},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pe := classifyProviderError(c.provider, c.status, c.message, "")
			if pe.Kind != c.wantKind {
				t.Fatalf("Kind = %v, want %v", pe.Kind, c.wantKind)
			}
			if pe.Provider != c.provider {
				t.Fatalf("Provider = %q, want %q", pe.Provider, c.provider)
			}
			if pe.Status != c.status {
				t.Fatalf("Status = %d, want %d", pe.Status, c.status)
			}
		})
	}
}

func TestProviderError_Error_AnthropicHasHint(t *testing.T) {
	pe := classifyProviderError("anthropic", 429, "rate_limit_error: slow down", `{"error":{"message":"rate_limit_error: slow down"}}`)
	got := pe.Error()
	if !strings.Contains(got, "anthropic rate limit (429)") {
		t.Errorf("missing header: %q", got)
	}
	if !strings.Contains(got, "rate_limit_error: slow down") {
		t.Errorf("missing upstream message: %q", got)
	}
	if !strings.Contains(got, "Hint:") {
		t.Errorf("expected registered hint, got: %q", got)
	}
	if strings.Contains(got, "No formatted error handler") {
		t.Errorf("should not render fallback banner when a hint is registered: %q", got)
	}
}

func TestProviderError_Error_ClaudeRateLimitHasHint(t *testing.T) {
	// Matches the real-world payload: api_error_status=429, message carries
	// the subscription-reset notice.
	pe := classifyProviderError("claude", 429, "You've hit your limit · resets 8pm (America/Sao_Paulo)", "")
	got := pe.Error()
	if !strings.Contains(got, "claude rate limit (429)") {
		t.Errorf("missing header: %q", got)
	}
	if !strings.Contains(got, "You've hit your limit") {
		t.Errorf("missing upstream message: %q", got)
	}
	if !strings.Contains(got, "Hint:") || !strings.Contains(got, "subscription") {
		t.Errorf("expected subscription hint, got: %q", got)
	}
}

func TestProviderError_Error_UnformattedProviderFallsBack(t *testing.T) {
	raw := `{"error":{"code":"rate_limit_exceeded","message":"too many requests"}}`
	pe := classifyProviderError("openrouter", 429, "too many requests", raw)
	got := pe.Error()
	if !strings.Contains(got, "openrouter rate limit (429)") {
		t.Errorf("missing header: %q", got)
	}
	if !strings.Contains(got, `No formatted error handler for "openrouter" provider`) {
		t.Errorf("expected no-formatter banner for unregistered provider, got: %q", got)
	}
	if !strings.Contains(got, raw) {
		t.Errorf("raw body should be preserved under the banner, got: %q", got)
	}
}

func TestProviderError_Error_UnknownKindStillReportsStatus(t *testing.T) {
	pe := classifyProviderError("anthropic", 418, "i am a teapot", "")
	got := pe.Error()
	if !strings.Contains(got, "anthropic error (418)") {
		t.Errorf("expected generic 'error' label with status, got: %q", got)
	}
}

func TestTryParseClaudeEnvelope_HandlesAPIErrorStatus(t *testing.T) {
	raw := []byte(`prefix noise {"type":"result","is_error":true,"api_error_status":429,"result":"You've hit your limit · resets 8pm (America/Sao_Paulo)"}`)
	resp, ok := tryParseClaudeEnvelope(raw)
	if !ok {
		t.Fatal("expected parse to succeed")
	}
	if !resp.IsError {
		t.Errorf("IsError = false, want true")
	}
	if resp.APIErrorStatus != 429 {
		t.Errorf("APIErrorStatus = %d, want 429", resp.APIErrorStatus)
	}
	if !strings.Contains(resp.Result, "hit your limit") {
		t.Errorf("Result missing rate-limit text: %q", resp.Result)
	}
}

func TestTryParseClaudeEnvelope_NotJSON(t *testing.T) {
	_, ok := tryParseClaudeEnvelope([]byte("plain text with no brace"))
	if ok {
		t.Fatal("expected parse to fail on non-JSON output")
	}
}
