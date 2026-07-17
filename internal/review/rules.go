package review

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// maxRuleBytes caps the size of a single .claude/rules/*.md file included in
// the prompt. Rules are path-scoped, so this budget is intentionally separate
// from (and smaller than) the CLAUDE.md caps in docs.go.
const maxRuleBytes = 8192

// maxTotalRuleBytes caps the combined size of all rule files included.
const maxTotalRuleBytes = 32768

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
			content = content[:maxRuleBytes] + "\n... (truncated)"
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
// document body. When the content has no (or malformed) frontmatter, meta is
// the zero value and body is the whole content.
func splitFrontmatter(content string) (meta claudeRule, body string) {
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return claudeRule{}, content
	}

	// Drop the opening fence, then find the closing one.
	rest := content[strings.IndexByte(content, '\n')+1:]
	end := strings.Index(rest, "\n---")
	if end == -1 {
		return claudeRule{}, content // unterminated — treat as body
	}

	front := rest[:end]
	body = rest[end+len("\n---"):]
	if nl := strings.IndexByte(body, '\n'); nl != -1 {
		body = body[nl+1:] // skip to the end of the closing fence line
	} else {
		body = ""
	}

	if err := yaml.Unmarshal([]byte(front), &meta); err != nil {
		return claudeRule{}, content // malformed — treat as body
	}
	return meta, strings.TrimLeft(body, "\n")
}
