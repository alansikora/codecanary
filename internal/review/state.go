package review

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LocalState holds the persisted state from a previous local review.
type LocalState struct {
	SHA        string    `json:"sha"`
	Branch     string    `json:"branch"`
	Findings   []Finding `json:"findings"`
	ReviewedAt string    `json:"reviewed_at"`
}

// LoadLocalState reads the state file for the given branch.
// Returns nil, nil if no state file exists.
func LoadLocalState(branch string) (*LocalState, error) {
	path := stateFilePath(branch)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading local state: %w", err)
	}

	var state LocalState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parsing local state: %w", err)
	}
	return &state, nil
}

// SaveLocalState writes the state file for the given branch.
func SaveLocalState(branch string, state *LocalState) error {
	path := stateFilePath(branch)

	// Ensure the .codecanary/.state/ directory exists.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}

	state.ReviewedAt = time.Now().UTC().Format(time.RFC3339)

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling local state: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing local state: %w", err)
	}
	return nil
}

// stateFilePath returns the path to .codecanary/.state/<sanitized-branch>.json.
// Branch name slashes are replaced with dashes for filesystem safety.
func stateFilePath(branch string) string {
	safe := strings.ReplaceAll(branch, "/", "-")
	return filepath.Join(".codecanary", ".state", safe+".json")
}

// mergeFindings appends new findings to existing ones, deduplicating by
// finding ID and file:line to prevent state file bloat across repeated runs.
func mergeFindings(existing, new []Finding) []Finding {
	seen := make(map[string]bool, len(existing))
	for _, f := range existing {
		key := f.ID + "\x00" + f.File + "\x00" + fmt.Sprintf("%d", f.Line)
		seen[key] = true
	}
	merged := make([]Finding, len(existing))
	copy(merged, existing)
	for _, f := range new {
		key := f.ID + "\x00" + f.File + "\x00" + fmt.Sprintf("%d", f.Line)
		if !seen[key] {
			merged = append(merged, f)
			seen[key] = true
		}
	}
	return merged
}

// findingsToKnownIssues converts saved findings to the ReviewThread shape
// expected by BuildIncrementalPrompt's knownIssues parameter.
func findingsToKnownIssues(findings []Finding) []ReviewThread {
	var threads []ReviewThread
	for _, f := range findings {
		threads = append(threads, ReviewThread{
			Path: f.File,
			Line: f.Line,
			Body: fmt.Sprintf("**%s** (%s): %s", f.Title, f.Severity, f.Description),
		})
	}
	return threads
}
