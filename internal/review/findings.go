package review

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
)

// Finding represents a single review issue found in the code.
type Finding struct {
	ID          string `json:"id"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Severity    string `json:"severity"` // One of: critical, bug, warning, suggestion, nitpick
	Title       string `json:"title"`
	Description string `json:"description"`
	Suggestion  string `json:"suggestion,omitempty"`
	FixRef      string `json:"fix_ref"`
	Actionable  *bool  `json:"actionable,omitempty"`
	Status      string `json:"status,omitempty"` // "new", "still open", or "" (first review)
}

// ReviewResult holds the complete output of a review run.
type ReviewResult struct {
	PRNumber  int       `json:"pr_number"`
	Repo      string    `json:"repo"`
	Findings  []Finding `json:"findings"`
	StillOpen []Finding `json:"still_open,omitempty"` // Unresolved findings from previous reviews
	Summary   string    `json:"summary"`
	SHA       string    `json:"sha,omitempty"`
}

// jsonFenceRe matches a ```json ... ``` code fence.
var jsonFenceRe = regexp.MustCompile("(?s)```json\\s*\n(.*?)\n```")

// ParseFindings extracts findings from Claude's output by looking for a JSON
// array inside a ```json code fence. It tries all matches in case an earlier
// fence contains non-JSON content (e.g. a code example).
func ParseFindings(output string) ([]Finding, error) {
	allMatches := jsonFenceRe.FindAllStringSubmatch(output, -1)
	if len(allMatches) == 0 {
		return nil, fmt.Errorf("no ```json code fence found in output:\n%s", output)
	}

	// Try each match — the actual findings array may not be the first fence.
	var lastErr error
	for _, matches := range allMatches {
		raw := matches[1]
		var findings []Finding
		if err := json.Unmarshal([]byte(raw), &findings); err != nil {
			lastErr = err
			continue
		}
		return findings, nil
	}

	return nil, fmt.Errorf("parsing findings JSON: %w\nClaude output:\n%s", lastErr, output)
}

// FilterNonActionable removes findings that Claude explicitly marked as not
// actionable (actionable: false). Findings without the field are kept.
func FilterNonActionable(findings []Finding) []Finding {
	var kept []Finding
	for _, f := range findings {
		if f.Actionable != nil && !*f.Actionable {
			fmt.Fprintf(os.Stderr, "Dropped non-actionable finding: %s (%s:%d)\n", f.ID, f.File, f.Line)
			continue
		}
		kept = append(kept, f)
	}
	return kept
}
