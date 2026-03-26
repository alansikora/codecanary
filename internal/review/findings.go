package review

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
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
}

// ReviewResult holds the complete output of a review run.
type ReviewResult struct {
	PRNumber int       `json:"pr_number"`
	Repo     string    `json:"repo"`
	Findings []Finding `json:"findings"`
	Summary  string    `json:"summary"`
	SHA      string    `json:"sha,omitempty"`
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

// dismissivePhrases are patterns that suggest a finding concludes the code is correct.
var dismissivePhrases = []string{
	"no action needed",
	"no action required",
	"no change needed",
	"no change required",
	"this is fine",
	"this is safe",
	"this is correct",
	"no bug here",
	"no issue here",
	"no real issue",
	"actually fine",
	"not a concern",
	"everything looks good",
	"the pattern is appropriate",
	"this is intentional",
	"correctly handled",
	"properly handled",
}

// actionableWords indicate the finding contains a real suggestion despite dismissive language.
var actionableWords = []string{
	"should", "consider", "must", "fix", "change", "remove", "replace",
	"update", "avoid", "instead", "recommend", "migrate", "refactor",
	"extract", "rename",
}

// IsPossiblyNonActionable returns true when the finding appears to conclude
// that the code is correct and no change is needed. It checks both the
// explicit Actionable field (set by Claude) and pattern-matches the description.
func (f *Finding) IsPossiblyNonActionable() bool {
	// If Claude explicitly marked it as not actionable, trust that.
	if f.Actionable != nil && !*f.Actionable {
		return true
	}

	desc := strings.ToLower(f.Description)

	// Check for dismissive phrases.
	hasDismissive := false
	for _, phrase := range dismissivePhrases {
		if strings.Contains(desc, phrase) {
			hasDismissive = true
			break
		}
	}
	if !hasDismissive {
		return false
	}

	// If dismissive language is present, check whether there's also actionable
	// language — if so, the finding likely has a real suggestion mixed in.
	text := desc + " " + strings.ToLower(f.Suggestion)
	for _, word := range actionableWords {
		if strings.Contains(text, word) {
			return false
		}
	}

	return true
}
