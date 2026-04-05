package review

import (
	"encoding/json"
	"errors"
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
	LinesAdded        int         `json:"lines_added"`
	LinesRemoved      int         `json:"lines_removed"`
	FilesChanged      int         `json:"files_changed"`
	Calls             []CallUsage `json:"calls"`
	TotalInputTokens  int         `json:"total_input_tokens"`
	TotalOutputTokens int         `json:"total_output_tokens"`
	TotalCostUSD      float64     `json:"total_cost_usd"`
}

// UsageTracker accumulates usage across multiple Claude calls (thread-safe).
type UsageTracker struct {
	mu    sync.Mutex
	calls []CallUsage

	// PR size — set via SetPRSize before ReportUsage.
	linesAdded   int
	linesRemoved int
	filesChanged int
}

// SetPRSize records the PR's line and file counts (thread-safe).
func (u *UsageTracker) SetPRSize(linesAdded, linesRemoved, filesChanged int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.linesAdded = linesAdded
	u.linesRemoved = linesRemoved
	u.filesChanged = filesChanged
}

// PRSize returns the PR's line and file counts (thread-safe).
func (u *UsageTracker) PRSize() (linesAdded, linesRemoved, filesChanged int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.linesAdded, u.linesRemoved, u.filesChanged
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

// TotalCost returns the sum of CostUSD across all recorded calls.
func (u *UsageTracker) TotalCost() float64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	var total float64
	for _, c := range u.calls {
		total += c.CostUSD
	}
	return total
}

// BudgetExceededError is returned when accumulated cost exceeds the budget limit.
type BudgetExceededError struct {
	Spent float64
	Limit float64
}

func (e *BudgetExceededError) Error() string {
	return fmt.Sprintf("budget exceeded: $%.4f spent of $%.4f limit", e.Spent, e.Limit)
}

// CheckBudget returns a BudgetExceededError if the tracker's total cost exceeds
// the given limit. A limit of 0 means unlimited (no check performed).
func CheckBudget(tracker *UsageTracker, limit float64) error {
	if limit <= 0 {
		return nil
	}
	spent := tracker.TotalCost()
	if spent > limit {
		return &BudgetExceededError{Spent: spent, Limit: limit}
	}
	return nil
}

// isBudgetError checks whether an error is a BudgetExceededError.
func isBudgetError(err error) bool {
	var budgetErr *BudgetExceededError
	return errors.As(err, &budgetErr)
}

// Report builds a UsageReport from accumulated calls.
func (u *UsageTracker) Report(repo string, prNumber int) *UsageReport {
	u.mu.Lock()
	defer u.mu.Unlock()

	r := &UsageReport{
		PR:           fmt.Sprintf("%s#%d", repo, prNumber),
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		LinesAdded:   u.linesAdded,
		LinesRemoved: u.linesRemoved,
		FilesChanged: u.filesChanged,
		Calls:        u.calls,
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
// GitHub Actions. Also writes a markdown summary to $GITHUB_STEP_SUMMARY
// when that env var is set.
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
	defer func() { _ = f.Close() }()

	if _, err := fmt.Fprintf(f, "CODECANARY_USAGE=%s\n", data); err != nil {
		return fmt.Errorf("writing to GITHUB_ENV: %w", err)
	}

	if err := writeStepSummary(report); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to write GitHub Step Summary: %v\n", err)
	}

	return nil
}

// formatInt formats an integer with thousands separators (e.g. 1234567 -> "1,234,567").
func formatInt(n int) string {
	s := fmt.Sprintf("%d", n)
	if n < 0 {
		s = s[1:]
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	if n < 0 {
		return "-" + string(result)
	}
	return string(result)
}

// writeStepSummary appends a markdown usage table to $GITHUB_STEP_SUMMARY.
// No-op if $GITHUB_STEP_SUMMARY is not set or if the report has no calls.
func writeStepSummary(report *UsageReport) error {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return nil
	}
	if len(report.Calls) == 0 {
		return nil
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening GITHUB_STEP_SUMMARY: %w", err)
	}
	defer func() { _ = f.Close() }()

	fmt.Fprintln(f, "## CodeCanary Usage")
	fmt.Fprintln(f, "")
	fmt.Fprintln(f, "| Phase | Model | Input tokens | Output tokens | Cost |")
	fmt.Fprintln(f, "|-------|-------|-------------|---------------|------|")
	for _, c := range report.Calls {
		fmt.Fprintf(f, "| %s | %s | %s | %s | $%.4f |\n",
			c.Phase, c.Model,
			formatInt(c.InputTokens), formatInt(c.OutputTokens),
			c.CostUSD)
	}
	fmt.Fprintf(f, "| **Total** | | **%s** | **%s** | **$%.4f** |\n",
		formatInt(report.TotalInputTokens), formatInt(report.TotalOutputTokens),
		report.TotalCostUSD)

	return nil
}

// PrintUsageSummary prints a human-readable table and JSON to stdout.
func PrintUsageSummary(report *UsageReport) {
	fmt.Printf("\n── Usage (%s) ──\n", report.PR)
	fmt.Printf("  PR size: +%d/-%d lines across %d files\n",
		report.LinesAdded, report.LinesRemoved, report.FilesChanged)
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
