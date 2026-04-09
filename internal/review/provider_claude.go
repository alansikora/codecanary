package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/alansikora/codecanary/internal/credentials"
)

// validCLIModels is the set of allowed model values for the Claude CLI provider.
// Accepts both aliases (sonnet) and full model IDs (claude-sonnet-4-6).
var validCLIModels = map[string]bool{
	"haiku": true, "sonnet": true, "opus": true,
	"claude-haiku-4-5-20251001": true,
	"claude-sonnet-4-6":         true,
	"claude-sonnet-4-5":         true,
	"claude-opus-4-6":           true,
	"claude-opus-4-5":           true,
}

func init() {
	providers["claude"] = ProviderFactory{
		New:      newClaudeCLIProvider,
		Validate: validateClaude,
		// No pricing entries — the Claude CLI reports cost directly.
		SuggestedReviewModel: "sonnet",
		SuggestedTriageModel: "haiku",
		AppRequirement: &AppRequirement{
			Name:       "Claude",
			AppSlug:    "claude",
			InstallURL: "https://github.com/apps/claude/installations/new",
		},
		OAuthConfig: &OAuthConfig{
			ClientID:     "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
			AuthorizeURL: "https://claude.ai/oauth/authorize",
			TokenURL:     "https://platform.claude.com/v1/oauth/token",
			Scope:        "user:inference",
		},
	}
}

func validateClaude(mc *ModelConfig) error {
	if mc.Model != "" && !validCLIModels[mc.Model] {
		return fmt.Errorf("invalid model %q for claude provider (valid: haiku, sonnet, opus)", mc.Model)
	}
	return nil
}

// claudeCLIProvider implements ModelProvider using the Claude CLI binary.
// Requires the `claude` binary in PATH and an OAuth token.
type claudeCLIProvider struct {
	model      string
	env        []string
	extraArgs  []string // from ClaudeArgs; appended after all managed flags
	binaryPath string   // resolved Claude CLI binary path; never empty
}

// claudeOAuthEnvVar is the environment variable the Claude CLI reads for OAuth tokens.
const claudeOAuthEnvVar = "CLAUDE_CODE_OAUTH_TOKEN"

func newClaudeCLIProvider(mc *ModelConfig, env []string) ModelProvider {
	binaryPath := mc.ClaudePath
	if binaryPath == "" {
		binaryPath = "claude"
	}
	// Map CODECANARY_PROVIDER_SECRET → CLAUDE_CODE_OAUTH_TOKEN so the Claude CLI
	// can authenticate using the OAuth token obtained during `codecanary setup`.
	env = injectClaudeOAuthToken(env)
	return &claudeCLIProvider{
		model:      mc.Model,
		env:        env,
		extraArgs:  mc.ClaudeArgs,
		binaryPath: binaryPath,
	}
}

// injectClaudeOAuthToken copies CODECANARY_PROVIDER_SECRET into
// CLAUDE_CODE_OAUTH_TOKEN when the latter is not already set.
func injectClaudeOAuthToken(env []string) []string {
	var secret string
	hasOAuth := false
	for _, e := range env {
		key, val, _ := strings.Cut(e, "=")
		if key == claudeOAuthEnvVar {
			hasOAuth = true
			break
		}
		if key == credentials.EnvVar {
			secret = val
		}
	}
	if !hasOAuth && secret != "" {
		env = append(env, claudeOAuthEnvVar+"="+secret)
	}
	return env
}

func (p *claudeCLIProvider) Run(ctx context.Context, prompt string, opts RunOpts) (*providerResult, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// --tools "" disables all built-in tools (Bash, Read, Edit, etc.), making
	// the CLI a single-shot prompt-in/text-out call. See `claude --help`:
	//   --tools: Use "" to disable all tools, "default" to use all tools
	args := []string{"--print", "--output-format", "json", "--no-session-persistence", "--tools", ""}
	if p.model != "" {
		args = append(args, "--model", p.model)
	}
	if opts.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", opts.MaxBudgetUSD))
	}
	args = append(args, p.extraArgs...)
	cmd := exec.CommandContext(ctx, p.binaryPath, args...)
	cmd.Env = p.env
	cmd.Stdin = strings.NewReader(prompt)
	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("claude timed out after %s", timeout)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("claude failed: %s\n%s", string(exitErr.Stderr), string(output))
		}
		return nil, fmt.Errorf("running claude: %w", err)
	}

	var resp claudeJSONResponse
	if err := json.Unmarshal(output, &resp); err != nil {
		// Fallback: treat entire output as plain text (e.g. older CLI version).
		return &providerResult{Text: string(output)}, nil
	}

	if resp.IsError {
		return nil, fmt.Errorf("claude returned error: %s", resp.Result)
	}

	// Note: the Claude CLI JSON output does not expose stop_reason, so we
	// cannot detect truncation here. The CLI manages its own output limits.
	result := &providerResult{
		Text: resp.Result,
		Usage: CallUsage{
			Model:             resp.firstModel(),
			InputTokens:       resp.Usage.InputTokens,
			OutputTokens:      resp.Usage.OutputTokens,
			CacheReadTokens:   resp.Usage.CacheReadInputTokens,
			CacheCreateTokens: resp.Usage.CacheCreationInputTokens,
			CostUSD:           resp.CostUSD,
			DurationMS:        resp.DurationMS,
		},
		DurationMS: resp.DurationMS,
	}
	for model, mu := range resp.ModelUsage {
		result.ModelUsages = append(result.ModelUsages, CallUsage{
			Model:             model,
			InputTokens:       mu.InputTokens,
			OutputTokens:      mu.OutputTokens,
			CacheReadTokens:   mu.CacheReadInputTokens,
			CacheCreateTokens: mu.CacheCreationInputTokens,
			CostUSD:           mu.CostUSD,
		})
	}
	return result, nil
}
