package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func report() *UsageReport {
	return &UsageReport{
		PR: "owner/repo#1",
		Calls: []CallUsage{
			{Phase: "review", Model: "sonnet", InputTokens: 1234567, OutputTokens: 890, CostUSD: 0.1234},
		},
		TotalInputTokens:  1234567,
		TotalOutputTokens: 890,
		TotalCostUSD:      0.1234,
	}
}

// The step summary must not be gated on GITHUB_ENV: the two variables travel
// together under GitHub Actions, but anywhere only the summary path is set
// (custom runners, test harnesses) the table would silently never be written.
func TestWriteUsageEnv_WritesStepSummaryWithoutGithubEnv(t *testing.T) {
	summary := filepath.Join(t.TempDir(), "summary.md")
	if err := os.WriteFile(summary, nil, 0644); err != nil {
		t.Fatalf("seeding summary file: %v", err)
	}
	t.Setenv("GITHUB_STEP_SUMMARY", summary)
	t.Setenv("GITHUB_ENV", "")

	if err := WriteUsageEnv(report()); err != nil {
		t.Fatalf("WriteUsageEnv: %v", err)
	}

	got, err := os.ReadFile(summary)
	if err != nil {
		t.Fatalf("reading summary: %v", err)
	}
	for _, want := range []string{"## CodeCanary Usage", "1,234,567", "$0.1234"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("step summary missing %q; got:\n%s", want, got)
		}
	}
}

func TestWriteStepSummary_NoopWhenUnset(t *testing.T) {
	t.Setenv("GITHUB_STEP_SUMMARY", "")
	if err := writeStepSummary(report()); err != nil {
		t.Errorf("writeStepSummary should be a no-op when unset: %v", err)
	}
}

func TestWriteStepSummary_NoopWithoutCalls(t *testing.T) {
	summary := filepath.Join(t.TempDir(), "summary.md")
	if err := os.WriteFile(summary, nil, 0644); err != nil {
		t.Fatalf("seeding summary file: %v", err)
	}
	t.Setenv("GITHUB_STEP_SUMMARY", summary)

	if err := writeStepSummary(&UsageReport{PR: "owner/repo#1"}); err != nil {
		t.Fatalf("writeStepSummary: %v", err)
	}
	got, err := os.ReadFile(summary)
	if err != nil {
		t.Fatalf("reading summary: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no summary for a report with no calls, got:\n%s", got)
	}
}
