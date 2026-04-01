package review

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// CallUsage captures token usage and cost for a single Claude CLI invocation.
type CallUsage struct {
	Phase       string  `json:"phase"`
	Model       string  `json:"model"`
	InputTokens int     `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	CacheReadTokens   int `json:"cache_read_tokens,omitempty"`
	CacheCreateTokens int `json:"cache_creation_tokens,omitempty"`
	CostUSD     float64 `json:"cost_usd"`
	DurationMS  int     `json:"duration_ms"`
}

// UsageReport is the top-level structure written to the usage JSON file.
type UsageReport struct {
	PR                string      `json:"pr"`
	Timestamp         string      `json:"timestamp"`
	Calls             []CallUsage `json:"calls"`
	TotalInputTokens  int         `json:"total_input_tokens"`
	TotalOutputTokens int         `json:"total_output_tokens"`
	TotalCostUSD      float64     `json:"total_cost_usd"`
}

// UsageTracker accumulates usage across multiple Claude calls (thread-safe).
type UsageTracker struct {
	mu    sync.Mutex
	calls []CallUsage
}

// Add records a single Claude call's usage.
func (u *UsageTracker) Add(call CallUsage) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, call)
}

// Calls returns a copy of the accumulated call usages.
func (u *UsageTracker) Calls() []CallUsage {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]CallUsage, len(u.calls))
	copy(out, u.calls)
	return out
}

// Report builds a UsageReport from accumulated calls.
func (u *UsageTracker) Report(repo string, prNumber int) *UsageReport {
	u.mu.Lock()
	defer u.mu.Unlock()

	r := &UsageReport{
		PR:        fmt.Sprintf("%s#%d", repo, prNumber),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Calls:     u.calls,
	}
	for _, c := range u.calls {
		r.TotalInputTokens += c.InputTokens
		r.TotalOutputTokens += c.OutputTokens
		r.TotalCostUSD += c.CostUSD
	}
	return r
}

// WriteUsageEnv writes the usage report as a CODECANARY_USAGE env var
// to $GITHUB_ENV so subsequent workflow steps can read it. No-op outside
// GitHub Actions.
func WriteUsageEnv(report *UsageReport) error {
	path := os.Getenv("GITHUB_ENV")
	if path == "" {
		return nil
	}

	data, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshaling usage report: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening GITHUB_ENV: %w", err)
	}
	defer f.Close()

	if _, err := fmt.Fprintf(f, "CODECANARY_USAGE=%s\n", data); err != nil {
		return fmt.Errorf("writing to GITHUB_ENV: %w", err)
	}

	return nil
}

// PrintUsageSummary prints a human-readable table and JSON to stdout.
func PrintUsageSummary(report *UsageReport) {
	fmt.Printf("\n── Usage (%s) ──\n", report.PR)
	for _, c := range report.Calls {
		fmt.Printf("  %-8s  %-25s  %6d in / %6d out  $%.4f  %dms\n",
			c.Phase, c.Model, c.InputTokens, c.OutputTokens, c.CostUSD, c.DurationMS)
	}
	fmt.Printf("  %-34s  %6d in / %6d out  $%.4f\n",
		"TOTAL", report.TotalInputTokens, report.TotalOutputTokens, report.TotalCostUSD)

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return
	}
	fmt.Printf("\n%s\n", data)
}

// claudeJSONResponse represents the JSON output from `claude --print --output-format json`.
type claudeJSONResponse struct {
	Result     string  `json:"result"`
	IsError    bool    `json:"is_error"`
	CostUSD    float64 `json:"total_cost_usd"`
	DurationMS int     `json:"duration_ms"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	ModelUsage map[string]struct {
		InputTokens              int     `json:"inputTokens"`
		OutputTokens             int     `json:"outputTokens"`
		CacheReadInputTokens     int     `json:"cacheReadInputTokens"`
		CacheCreationInputTokens int     `json:"cacheCreationInputTokens"`
		CostUSD                  float64 `json:"costUSD"`
	} `json:"modelUsage"`
}

// firstModel extracts the model name from the modelUsage map.
func (r *claudeJSONResponse) firstModel() string {
	for k := range r.ModelUsage {
		return k
	}
	return ""
}
