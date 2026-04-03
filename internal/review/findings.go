package review

import (
	"encoding/json"
	"fmt"
	"os"
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
//
// When the suggestion or description fields contain embedded markdown code
// fences (```bash, ```go, etc.), the non-greedy regex may match too early.
// A bracket-matching fallback handles this case.
func ParseFindings(output string) ([]Finding, error) {
	allMatches := jsonFenceRe.FindAllStringSubmatch(output, -1)

	// Try each regex match — the actual findings array may not be the first fence.
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

	// Fallback: when embedded ``` in string values cause the regex to match
	// too early, extract the JSON array by bracket-matching.
	if raw, ok := extractJSONArray(output); ok {
		var findings []Finding
		if err := json.Unmarshal([]byte(raw), &findings); err != nil {
			lastErr = err
		} else {
			return findings, nil
		}
	}

	if len(allMatches) == 0 && lastErr == nil {
		return nil, fmt.Errorf("no ```json code fence found in output:\n%s", output)
	}
	return nil, fmt.Errorf("parsing findings JSON: %w\nClaude output:\n%s", lastErr, output)
}

// extractJSONArray finds the first top-level JSON array in the output by
// tracking bracket depth and string boundaries. This handles cases where
// the content contains embedded triple backticks that confuse fence detection.
func extractJSONArray(output string) (string, bool) {
	// Only search within a ```json fence region to avoid matching unrelated arrays.
	fenceStart := strings.Index(output, "```json")
	if fenceStart < 0 {
		return "", false
	}
	// Skip past the ```json line.
	searchFrom := strings.Index(output[fenceStart:], "\n")
	if searchFrom < 0 {
		return "", false
	}
	searchFrom += fenceStart + 1

	start := strings.Index(output[searchFrom:], "[")
	if start < 0 {
		return "", false
	}
	start += searchFrom

	depth := 0
	inString := false
	escape := false
	for i := start; i < len(output); i++ {
		ch := output[i]
		if escape {
			escape = false
			continue
		}
		if ch == '\\' && inString {
			escape = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch ch {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return output[start : i+1], true
			}
		}
	}
	return "", false
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
