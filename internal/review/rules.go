package review

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// maxRuleBytes caps the size of a single .claude/rules/*.md file included in
// the prompt. Rules are path-scoped, so this budget is intentionally separate
// from (and smaller than) the CLAUDE.md caps in docs.go.
const maxRuleBytes = 8192

// maxTotalRuleBytes caps the combined size of all rule files included.
const maxTotalRuleBytes = 32768

// truncationMarker is appended to a rule whose content exceeds maxRuleBytes.
const truncationMarker = "\n... (truncated)"

// claudeRule is the YAML frontmatter of a .claude/rules/*.md file — the subset
// of Claude Code's rule format CodeCanary uses for path scoping.
type claudeRule struct {
	Description string   `yaml:"description"`
	Paths       []string `yaml:"paths"`
}

// ReadClaudeRules discovers Claude Code path-scoped rule files under
// .claude/rules/*.md and returns a map of path → content for the rules that
// apply to the changed files. A rule applies when a changed file matches one
// of its `paths` globs; a rule with no `paths` frontmatter always applies
// (matching Claude Code semantics). Results share a byte budget independent of
// the CLAUDE.md caps. When prFiles is empty, only unscoped rules load.
func ReadClaudeRules(prFiles []string) map[string]string {
	root, err := os.Getwd()
	if err != nil {
		return map[string]string{}
	}
	return readClaudeRulesFrom(root, prFiles)
}

// readClaudeRulesFrom is the testable core of ReadClaudeRules; it resolves
// candidate paths relative to root, which must be an absolute path.
func readClaudeRulesFrom(root string, prFiles []string) map[string]string {
	rules := make(map[string]string)

	matches, err := filepath.Glob(filepath.Join(root, ".claude", "rules", "*.md"))
	if err != nil {
		return rules
	}
	// Sort for deterministic budget exhaustion — filepath.Glob's order is not
	// guaranteed across platforms, so which rules survive a full budget must
	// not depend on filesystem ordering.
	sort.Strings(matches)

	totalBytes := 0
	for _, abs := range matches {
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}

		meta, body := splitFrontmatter(string(data))

		// Scope: skip path-scoped rules that no changed file matches.
		// matchesAny (config.go) is the same full-path matcher used for
		// review.yml rule scoping, so both honor identical glob semantics.
		if len(meta.Paths) > 0 && !anyFileMatches(prFiles, meta.Paths) {
			continue
		}

		relPath, err := filepath.Rel(root, abs)
		if err != nil {
			relPath = abs
		}

		content := body
		if meta.Description != "" {
			content = meta.Description + "\n\n" + body
		}
		if len(content) > maxRuleBytes {
			// Reserve room for the marker so a truncated file is exactly
			// maxRuleBytes — keeps the total-budget accounting exact rather
			// than letting each truncated file overshoot by the marker length.
			content = content[:maxRuleBytes-len(truncationMarker)] + truncationMarker
			fmt.Fprintf(os.Stderr, "Rule %s exceeds %d bytes and was truncated for the review prompt\n", relPath, maxRuleBytes)
		}
		if totalBytes+len(content) > maxTotalRuleBytes {
			fmt.Fprintf(os.Stderr, "Rule budget (%d bytes) exhausted; skipping %s\n", maxTotalRuleBytes, relPath)
			continue
		}

		rules[relPath] = content
		totalBytes += len(content)
	}

	return rules
}

// anyFileMatches reports whether any of the files matches any of the patterns.
func anyFileMatches(files, patterns []string) bool {
	for _, f := range files {
		if matchesAny(f, patterns) {
			return true
		}
	}
	return false
}

// splitFrontmatter separates leading `---`-fenced YAML frontmatter from the
// document body. The closing fence must be an unindented line consisting
// solely of `---` (trailing whitespace/CR tolerated) — a `---` sequence inside
// a YAML value, an indented block scalar, or a body paragraph is not mistaken
// for the fence. When the content has no (or malformed/unterminated)
// frontmatter, meta is the zero value and body is the whole content.
func splitFrontmatter(content string) (meta claudeRule, body string) {
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return claudeRule{}, content
	}

	// Drop the opening fence line, then scan line by line for a closing fence.
	rest := content[strings.IndexByte(content, '\n')+1:]
	offset := 0
	frontEnd := -1
	bodyStart := len(rest)
	for offset < len(rest) {
		nl := strings.IndexByte(rest[offset:], '\n')
		var line string
		if nl == -1 {
			line = rest[offset:]
		} else {
			line = rest[offset : offset+nl]
		}
		// A closing fence is an unindented `---` line (trailing whitespace/CR
		// tolerated for CRLF files). Trimming *leading* whitespace too would
		// let an indented `---` inside a YAML block scalar end the frontmatter
		// prematurely.
		if strings.TrimRight(line, " \t\r") == "---" {
			frontEnd = offset
			if nl != -1 {
				bodyStart = offset + nl + 1
			}
			break
		}
		if nl == -1 {
			break
		}
		offset += nl + 1
	}
	if frontEnd == -1 {
		return claudeRule{}, content // unterminated — treat as body
	}

	if err := yaml.Unmarshal([]byte(rest[:frontEnd]), &meta); err != nil {
		return claudeRule{}, content // malformed — treat as body
	}
	return meta, strings.TrimLeft(rest[bodyStart:], "\n")
}
