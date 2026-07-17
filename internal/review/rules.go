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
// from (and smaller than) the CLAUDE.md caps.
const maxRuleBytes = 8192

// maxTotalRuleBytes caps the combined size of all rule files included.
const maxTotalRuleBytes = 32768

// claudeRule is the YAML frontmatter of a .claude/rules/*.md file. Mirrors the
// subset of Claude Code's rule format CodeCanary uses for scoping.
type claudeRule struct {
	Description string   `yaml:"description"`
	Paths       []string `yaml:"paths"`
}

// ReadClaudeRules discovers Claude Code path-scoped rule files under
// .claude/rules/*.md and returns a map of path → content for the rules that
// apply to the current change. A rule applies when any changed file matches
// one of its `paths:` globs; a rule with no `paths:` frontmatter always
// applies (matching Claude Code semantics). Results share a byte budget
// independent of the CLAUDE.md caps.
func ReadClaudeRules(changedFiles []string) map[string]string {
	matches, err := filepath.Glob(filepath.Join(".claude", "rules", "*.md"))
	if err != nil {
		return nil
	}

	rules := make(map[string]string)
	totalBytes := 0
	for _, p := range matches {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}

		meta, body := splitFrontmatter(string(data))

		// Scope: skip rules whose paths don't match any changed file.
		if len(meta.Paths) > 0 && !anyFileMatches(changedFiles, meta.Paths) {
			continue
		}

		content := body
		if meta.Description != "" {
			content = meta.Description + "\n\n" + body
		}
		if len(content) > maxRuleBytes {
			content = content[:maxRuleBytes] + "\n... (truncated)"
			fmt.Fprintf(os.Stderr, "Rule %s exceeds %d bytes and was truncated for the review prompt\n", p, maxRuleBytes)
		}
		if totalBytes+len(content) > maxTotalRuleBytes {
			fmt.Fprintf(os.Stderr, "Rule budget (%d bytes) exhausted; skipping %s\n", maxTotalRuleBytes, p)
			continue
		}

		rules[p] = content
		totalBytes += len(content)
	}

	return rules
}

// anyFileMatches reports whether any of the files matches any of the patterns.
func anyFileMatches(files, patterns []string) bool {
	for _, f := range files {
		if matchesAnyGlob(f, patterns) {
			return true
		}
	}
	return false
}

// splitFrontmatter separates leading `---`-fenced YAML frontmatter from the
// document body. If the content has no frontmatter, meta is the zero value and
// body is the whole content.
func splitFrontmatter(content string) (meta claudeRule, body string) {
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return claudeRule{}, content
	}

	// Drop the opening fence and find the closing one.
	rest := content[strings.IndexByte(content, '\n')+1:]
	end := strings.Index(rest, "\n---")
	if end == -1 {
		// Unterminated frontmatter — treat the whole thing as body.
		return claudeRule{}, content
	}

	front := rest[:end]
	body = rest[end+len("\n---"):]
	// Skip to the end of the closing fence line.
	if nl := strings.IndexByte(body, '\n'); nl != -1 {
		body = body[nl+1:]
	} else {
		body = ""
	}

	if err := yaml.Unmarshal([]byte(front), &meta); err != nil {
		// Malformed frontmatter — fall back to treating everything as body.
		return claudeRule{}, content
	}
	return meta, strings.TrimLeft(body, "\n")
}
