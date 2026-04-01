package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// claudeCLIProvider implements ModelProvider using the Claude CLI binary.
type claudeCLIProvider struct {
	env []string
}

func (p *claudeCLIProvider) Run(ctx context.Context, prompt string, opts RunOpts) (*claudeResult, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = (&ReviewConfig{}).EffectiveTimeout()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{"--print", "--output-format", "json", "--no-session-persistence"}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", opts.MaxBudgetUSD))
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
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
		return &claudeResult{Text: string(output)}, nil
	}

	if resp.IsError {
		return nil, fmt.Errorf("claude returned error: %s", resp.Result)
	}

	result := &claudeResult{
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
