package review

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidate_AdvisorModel_OpenAIRejected(t *testing.T) {
	cfg := &ReviewConfig{
		Provider:     "openai",
		ReviewModel:  "gpt-5",
		TriageModel:  "gpt-5-mini",
		AdvisorModel: "claude-opus-4-7",
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for advisor_model on openai provider")
	}
	if !strings.Contains(err.Error(), "anthropic and claude") {
		t.Errorf("error should mention supported providers, got: %v", err)
	}
}

func TestValidate_AdvisorModel_OpenRouterRejected(t *testing.T) {
	cfg := &ReviewConfig{
		Provider:     "openrouter",
		ReviewModel:  "anthropic/claude-sonnet-4-6",
		TriageModel:  "anthropic/claude-haiku-4-5",
		AdvisorModel: "claude-opus-4-7",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for advisor_model on openrouter provider")
	}
}

func TestValidate_AdvisorModel_AnthropicAccepted(t *testing.T) {
	cfg := &ReviewConfig{
		Provider:     "anthropic",
		ReviewModel:  "claude-sonnet-4-6",
		TriageModel:  "claude-haiku-4-5-20251001",
		AdvisorModel: "claude-opus-4-7",
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_AdvisorModel_ClaudeAliasAccepted(t *testing.T) {
	cfg := &ReviewConfig{
		Provider:     "claude",
		ReviewModel:  "sonnet",
		TriageModel:  "haiku",
		AdvisorModel: "opus",
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_AdvisorModel_UnknownCustomModelRejected(t *testing.T) {
	// Substring match must not accept arbitrary fork names that happen to
	// embed a known ID — see PR #161 finding 161-3.
	cfg := &ReviewConfig{
		Provider:     "anthropic",
		ReviewModel:  "claude-sonnet-4-6",
		TriageModel:  "claude-haiku-4-5-20251001",
		AdvisorModel: "opus-tuned-fork", // contains "opus" but is not a CLI alias and not a full ID
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for non-alias, non-full-ID advisor")
	}
}

func TestValidate_AdvisorModel_SonnetAliasRejectedAsAdvisor(t *testing.T) {
	// Only `opus` is a valid CLI advisor alias. `sonnet`/`haiku` must not pass.
	cfg := &ReviewConfig{
		Provider:     "claude",
		ReviewModel:  "sonnet",
		TriageModel:  "haiku",
		AdvisorModel: "sonnet",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for sonnet as advisor alias")
	}
}

func TestValidate_AdvisorModel_InvalidAdvisor(t *testing.T) {
	cfg := &ReviewConfig{
		Provider:     "anthropic",
		ReviewModel:  "claude-sonnet-4-6",
		TriageModel:  "claude-haiku-4-5-20251001",
		AdvisorModel: "claude-sonnet-4-6", // Sonnet is not a valid advisor
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for Sonnet as advisor")
	}
	if !strings.Contains(err.Error(), "supported advisor") {
		t.Errorf("error should mention invalid advisor, got: %v", err)
	}
}

func TestValidate_AdvisorModel_Empty_NoValidation(t *testing.T) {
	cfg := &ReviewConfig{
		Provider:    "openai", // advisor validation skipped entirely when unset
		ReviewModel: "gpt-5",
		TriageModel: "gpt-5-mini",
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error when advisor_model is empty: %v", err)
	}
}

// TestAnthropicProvider_AdvisorTool_Wiring spins up a fake Anthropic endpoint
// and verifies the provider sends the beta header, the advisor tool entry,
// and parses the usage.iterations[] breakdown into per-model ModelUsages.
// Also confirms server_tool_use and advisor_tool_result blocks are filtered
// out of the response text so they don't contaminate findings parsing.
func TestAnthropicProvider_AdvisorTool_Wiring(t *testing.T) {
	var gotBeta string
	var gotTools []anthropicTool
	var gotModel string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBeta = r.Header.Get("anthropic-beta")
		var body anthropicRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotTools = body.Tools
		gotModel = body.Model

		resp := map[string]any{
			"id":    "msg_test",
			"type":  "message",
			"role":  "assistant",
			"model": body.Model,
			"content": []map[string]any{
				{"type": "text", "text": "pre-advisor"},
				{"type": "server_tool_use", "id": "srvtoolu_1", "name": "advisor", "input": map[string]any{}},
				{"type": "advisor_tool_result", "tool_use_id": "srvtoolu_1"},
				{"type": "text", "text": "post-advisor"},
			},
			"stop_reason": "end_turn",
			"usage": map[string]any{
				"input_tokens":                100,
				"output_tokens":               200,
				"cache_read_input_tokens":     0,
				"cache_creation_input_tokens": 0,
				"iterations": []map[string]any{
					{"type": "message", "input_tokens": 100, "output_tokens": 50},
					{"type": "advisor_message", "model": "claude-opus-4-7", "input_tokens": 500, "output_tokens": 800},
					{"type": "message", "input_tokens": 600, "output_tokens": 150, "cache_read_input_tokens": 100},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := &anthropicProvider{
		model:        "claude-sonnet-4-6",
		advisorModel: "claude-opus-4-7",
		keyEnv:       "TEST_KEY",
		env:          []string{"TEST_KEY=sk-test"},
	}
	// Redirect the Anthropic URL by swapping the HTTP client. Since the
	// provider hard-codes the URL, run the request against the httptest
	// server by temporarily pointing DefaultTransport at its URL.
	prevURL := anthropicAPIURL
	anthropicAPIURL = server.URL
	defer func() { anthropicAPIURL = prevURL }()

	result, err := p.Run(t.Context(), "prompt", RunOpts{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if gotBeta != advisorBetaHeader {
		t.Errorf("beta header = %q, want %q", gotBeta, advisorBetaHeader)
	}
	if gotModel != "claude-sonnet-4-6" {
		t.Errorf("request model = %q, want claude-sonnet-4-6", gotModel)
	}
	if len(gotTools) != 1 || gotTools[0].Type != advisorToolType || gotTools[0].Name != advisorToolName || gotTools[0].Model != "claude-opus-4-7" {
		t.Errorf("advisor tool not wired correctly: %+v", gotTools)
	}
	if !strings.Contains(result.Text, "pre-advisor") || !strings.Contains(result.Text, "post-advisor") {
		t.Errorf("expected text from both text blocks, got %q", result.Text)
	}
	if strings.Contains(result.Text, "server_tool_use") || strings.Contains(result.Text, "advisor_tool_result") {
		t.Errorf("advisor metadata leaked into text: %q", result.Text)
	}
	if len(result.ModelUsages) != 3 {
		t.Fatalf("expected 3 ModelUsages iterations, got %d", len(result.ModelUsages))
	}
	var sawAdvisor bool
	for _, mu := range result.ModelUsages {
		if mu.Model == "claude-opus-4-7" {
			sawAdvisor = true
			if mu.InputTokens != 500 || mu.OutputTokens != 800 {
				t.Errorf("advisor usage wrong: %+v", mu)
			}
		}
	}
	if !sawAdvisor {
		t.Errorf("no advisor iteration captured in ModelUsages: %+v", result.ModelUsages)
	}
}

// TestAnthropicProvider_NoAdvisor_NoBetaHeader confirms we do not send the
// advisor beta header or the advisor tool when advisorModel is empty.
func TestAnthropicProvider_NoAdvisor_NoBetaHeader(t *testing.T) {
	var gotBeta string
	var gotTools []anthropicTool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBeta = r.Header.Get("anthropic-beta")
		var body anthropicRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotTools = body.Tools

		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "msg",
			"type":    "message",
			"role":    "assistant",
			"model":   "claude-sonnet-4-6",
			"content": []map[string]any{{"type": "text", "text": "ok"}},
			"usage":   map[string]any{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer server.Close()

	p := &anthropicProvider{
		model:  "claude-sonnet-4-6",
		keyEnv: "TEST_KEY",
		env:    []string{"TEST_KEY=sk-test"},
	}
	prevURL := anthropicAPIURL
	anthropicAPIURL = server.URL
	defer func() { anthropicAPIURL = prevURL }()

	if _, err := p.Run(t.Context(), "prompt", RunOpts{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotBeta != "" {
		t.Errorf("beta header should be empty when advisor disabled, got %q", gotBeta)
	}
	if len(gotTools) != 0 {
		t.Errorf("tools should be empty when advisor disabled, got %+v", gotTools)
	}
}

func TestClaudeProvider_AdvisorGateInjected(t *testing.T) {
	mc := &ModelConfig{
		Provider:     "claude",
		Model:        "sonnet",
		AdvisorModel: "opus",
	}
	p := newClaudeCLIProvider(mc, []string{"PATH=/usr/bin"}).(*claudeCLIProvider)
	var found bool
	for _, e := range p.env {
		if e == claudeAdvisorEnableEnvVar+"=1" {
			found = true
		}
	}
	if !found {
		t.Errorf("advisor gate env var not injected: %v", p.env)
	}
	if p.advisorModel != "opus" {
		t.Errorf("advisorModel not stored on provider: %q", p.advisorModel)
	}
}

func TestClaudeProvider_NoAdvisor_NoGate(t *testing.T) {
	mc := &ModelConfig{Provider: "claude", Model: "sonnet"}
	p := newClaudeCLIProvider(mc, []string{"PATH=/usr/bin"}).(*claudeCLIProvider)
	for _, e := range p.env {
		if strings.HasPrefix(e, claudeAdvisorEnableEnvVar+"=") {
			t.Errorf("advisor gate set without advisor_model: %q", e)
		}
	}
}

func TestHasFlag(t *testing.T) {
	cases := []struct {
		args []string
		flag string
		want bool
	}{
		{[]string{"--foo", "bar"}, "--foo", true},
		{[]string{"--foo=bar"}, "--foo", true},
		{[]string{"--foobar"}, "--foo", false},
		{[]string{}, "--foo", false},
		{[]string{"--settings", "{}"}, "--settings", true},
	}
	for _, c := range cases {
		if got := hasFlag(c.args, c.flag); got != c.want {
			t.Errorf("hasFlag(%v, %q) = %v, want %v", c.args, c.flag, got, c.want)
		}
	}
}
