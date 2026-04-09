package review

import (
	"fmt"
	"os"
)

// LocalPlatform implements ReviewPlatform for local-only reviews (no PR).
type LocalPlatform struct {
	Branch       string
	OutputFormat string // user-requested output format (may be empty)
}

func (l *LocalPlatform) LoadPreviousFindings() ([]ReviewThread, string, int) {
	state, err := LoadLocalState(l.Branch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not load local state: %v\n", err)
	}
	if state == nil || state.SHA == "" {
		return nil, "", 0
	}

	fmt.Fprintf(os.Stderr, "Found previous local review at %s (%d findings)\n", shortSHA(state.SHA), len(state.Findings))
	threads := findingsToKnownIssues(state.Findings)
	return threads, state.SHA, len(state.Findings)
}

func (l *LocalPlatform) ExcludedAuthor(_ []ReviewThread) string {
	return "" // no author to exclude in local mode
}

func (l *LocalPlatform) HandleResolutions(_ []ReviewThread, _ []fixedThread) {
	// No-op: resolved findings are removed from state by the caller.
}

func (l *LocalPlatform) Publish(result *ReviewResult, _ *PRData, _ []ReviewThread, _ []fixedThread) error {
	outputFormat := resolveOutputFormat(l.OutputFormat)
	formatted, err := formatResult(result, outputFormat)
	if err != nil {
		return err
	}
	fmt.Print(formatted)
	return nil
}

func (l *LocalPlatform) SaveState(result *ReviewResult, stillOpen []Finding, _ bool) error {
	allFindings := combineFindings(stillOpen, result.Findings)

	if err := SaveLocalState(l.Branch, &LocalState{
		SHA:      result.SHA,
		Branch:   l.Branch,
		Findings: allFindings,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save local state: %v\n", err)
	}
	return nil
}

func (l *LocalPlatform) GetIncrementalDiff(baseSHA string, prFiles []string) (string, error) {
	diff, err := GetIncrementalDiff(baseSHA)
	if err != nil {
		return "", err
	}

	// Always include uncommitted changes in local mode.
	return appendWorkingTreeDiff(diff, prFiles)
}

func (l *LocalPlatform) ReportUsage(tracker *UsageTracker) {
	outputFormat := resolveOutputFormat(l.OutputFormat)
	if outputFormat == "terminal" {
		fmt.Fprint(os.Stderr, FormatUsageTable(tracker, colorsEnabled()))
	}
}
