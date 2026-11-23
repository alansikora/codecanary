package review

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Rule sources are the files that decide how a pull request is reviewed:
// the CodeCanary config and policy files, the CLAUDE.md files and Claude
// rules loaded as project documentation, and the files context_files lists.
// In GitHub mode they are read from the PR's base branch, never its head, so
// a PR cannot change the rules it is reviewed by (see
// GithubPlatform.LoadRules). Locally they are read from the working tree.

// RuleSources is what ReviewPlatform.LoadRules returns.
type RuleSources struct {
	Config *ReviewConfig
	// Root is the directory holding the rule-source files at their
	// repository paths: the working tree locally, a copy of the base
	// branch's files in GitHub mode.
	Root string
	// BaseSHA is the commit the files were read from, "" when they come
	// from the working tree.
	BaseSHA string
	// Cleanup removes what LoadRules created (the copy of the base files);
	// nil when there is nothing to remove.
	Cleanup func()
}

// configFilePaths are the repository paths of CodeCanary's own config.
var configFilePaths = []string{
	".codecanary/config.yml",
	".codecanary/review.yml",
	".codecanary/review.local.yml",
	".codecanary.yml",
}

// isRuleSourcePath reports whether a repository path is a config file or a
// file the review may load as project documentation (CLAUDE.md at any depth,
// .claude/rules/*.md). context_files are checked separately.
func isRuleSourcePath(p string) bool {
	p = path.Clean(filepath.ToSlash(p))
	if slices.Contains(configFilePaths, p) || path.Base(p) == "CLAUDE.md" {
		return true
	}
	return path.Dir(p) == ".claude/rules" && strings.HasSuffix(p, ".md")
}

// changedRuleFiles returns the PR files that are rule sources under cfg.
func changedRuleFiles(prFiles []string, cfg *ReviewConfig) []string {
	ctx := cfg.AllContextFiles()
	var out []string
	for _, f := range prFiles {
		if isRuleSourcePath(f) || slices.Contains(ctx, f) {
			out = append(out, f)
		}
	}
	return out
}

// renderRulesChangedNote is the review-body note for a PR that edits its own
// rule sources: the review used the base branch's copies. "" when none.
func renderRulesChangedNote(files []string) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n> [!NOTE]\n> This PR changes files that set CodeCanary's review rules. They were reviewed as code, but this review used the base branch's copies; the changes apply once the PR merges:\n")
	for _, f := range files {
		fmt.Fprintf(&b, "> - `%s`\n", strings.ReplaceAll(f, "`", "'"))
	}
	return b.String()
}

// readContextFiles reads the context files the changed files and the tier
// select (tier may be nil), from root. A listed file that does not exist (or
// links outside the repository) is a config error: the rules it holds would
// otherwise go silently missing.
// Each file is cut to maxFileSize.
func readContextFiles(root string, cfg *ReviewConfig, changed []string, tier *Tier, maxFileSize int) (map[string]string, error) {
	out := make(map[string]string)
	for _, rel := range cfg.ContextFilesFor(changed, tier) {
		data, err := readRepoFile(root, rel)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("config error: context_files lists %q, which does not exist", rel)
			}
			return nil, fmt.Errorf("config error: reading context file %q: %w", rel, err)
		}
		content := string(data)
		if maxFileSize > 0 && len(content) > maxFileSize {
			Stderrf(ansiYellow, "Context file %s is over %d bytes — truncated\n", rel, maxFileSize)
			content = strings.ToValidUTF8(content[:maxFileSize], "") + "\n... (truncated)"
		}
		out[rel] = content
	}
	return out, nil
}
