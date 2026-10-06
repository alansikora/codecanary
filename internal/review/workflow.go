package review

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Workflow-file helpers shared by `codecanary mode` (is a workflow wired
// up?) and the GitHub update notice (is that workflow older than the
// template this binary ships?).

// codecanaryWorkflow is a workflow file that uses the CodeCanary action.
type codecanaryWorkflow struct {
	Path    string // relative to the repo root (see relToRoot)
	Content string
}

// detectCodecanaryWorkflow scans .github/workflows/*.yml and *.yaml in
// the current repository for a step that uses the CodeCanary action.
// Returns the first matching file's path relative to the repo root.
func detectCodecanaryWorkflow() (string, bool) {
	wf, ok := findCodecanaryWorkflow()
	return wf.Path, ok
}

// findCodecanaryWorkflow is detectCodecanaryWorkflow plus the file's
// contents.
//
// The scan is rooted at `git rev-parse --show-toplevel` rather than
// the current working directory so calls from a subdirectory still
// find workflow files correctly — running `codecanary` from
// `repo/cmd/review/` must not silently miscategorise the mode.
// When not in a git repo, falls back to a cwd-relative scan so the
// detector keeps working in tests and non-git setups.
//
// Textual scan, not YAML parsing: the detection rule (a `uses:` line
// referencing the action repo) is stable, and a real parse would need
// to resolve matrix expansions and reusable workflows for no win.
// Commented-out lines are skipped.
func findCodecanaryWorkflow() (codecanaryWorkflow, bool) {
	root := gitRepoRoot()
	workflowsDir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(workflowsDir)
	if err != nil {
		return codecanaryWorkflow{}, false
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}
		path := filepath.Join(workflowsDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if workflowUsesCodecanary(string(data)) {
			return codecanaryWorkflow{Path: relToRoot(root, path), Content: string(data)}, true
		}
	}
	return codecanaryWorkflow{}, false
}

// relToRoot returns path relative to root so WorkflowPath stays stable
// in the JSON output regardless of the caller's cwd. When root is
// empty (non-git fallback), path is already cwd-relative and returned
// as-is. On any Rel() error, falls back to the absolute path rather
// than returning an empty string — a visible full path is better than
// a silently-empty one for downstream consumers.
func relToRoot(root, path string) string {
	if root == "" {
		return path
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}

// workflowUsesCodecanary returns true if the given workflow YAML text
// contains a non-commented `uses:` line referencing the CodeCanary
// action repository.
func workflowUsesCodecanary(yaml string) bool {
	for _, line := range strings.Split(yaml, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Strip a "- " list prefix if present, then match on "uses:".
		trimmed = strings.TrimPrefix(trimmed, "- ")
		trimmed = strings.TrimLeft(trimmed, " \t")
		if !strings.HasPrefix(trimmed, "uses:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(trimmed, "uses:"))
		// Strip optional surrounding quotes.
		value = strings.Trim(value, `"'`)
		if strings.HasPrefix(value, "alansikora/codecanary") {
			return true
		}
	}
	return false
}

// workflowTemplateMarker matches the template-version comment carried by
// the workflow template (internal/setup/codecanary.yml), e.g.
//
//	# codecanary-workflow: v2
//
// The marker is bumped whenever the template changes, so a consumer's
// copy can be compared by version instead of by content: customizations
// (extra steps, a different secret name, a pinned action ref) don't make
// a copy look outdated, and a copy that predates a template change does.
var workflowTemplateMarker = regexp.MustCompile(`(?m)^[ \t]*#[ \t]*codecanary-workflow:[ \t]*v(\d+)[ \t]*$`)

// WorkflowTemplateVersion returns the template version declared by the
// marker comment in a workflow file, or 0 when there is none — copies
// made before the marker existed count as older than every marked
// template.
func WorkflowTemplateVersion(workflow string) int {
	m := workflowTemplateMarker.FindStringSubmatch(workflow)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}
