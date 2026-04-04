package review

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// escapePromptTag neutralises any XML-like tag matching tagName in content,
// preventing adversarial repos from injecting fake prompt sections.
// Replaces every "<" immediately followed by tagName or /tagName with "&lt;"
// which covers all variants: opening, closing, self-closing, with or without
// attributes or whitespace. Only "<" needs escaping — a trailing ">" without
// a preceding "<tagName" is inert text and cannot form or close a tag.
func escapePromptTag(content, tagName string) string {
	content = strings.ReplaceAll(content, "</"+tagName, "&lt;/"+tagName)
	content = strings.ReplaceAll(content, "<"+tagName, "&lt;"+tagName)
	return content
}

// BuildPrompt constructs the review prompt from PR data and review config.
// startIndex is the number of existing findings across prior reviews so that
// fix_ref numbering continues from where the last review left off.
func BuildPrompt(pr *PRData, cfg *ReviewConfig, startIndex int, projectDocs map[string]string) string {
	var b strings.Builder

	b.WriteString("You are a code reviewer. Review the following pull request and report findings.\n")
	b.WriteString("You will be given the full contents of changed files for context, along with the diff. Only report issues that are directly related to the changes in the diff — do not flag pre-existing issues in unchanged code. Do not report a finding if your analysis concludes that the code is correct and no action is needed — only report findings that require the author to make a change or consider a specific alternative.\n")
	b.WriteString("Also consider whether the changes could cause side effects in other files that depend on or interact with the modified code (e.g. callers, importers, shared state). If you identify a potential side effect, anchor your finding to the relevant line in the diff and describe the affected downstream code in the description.\n\n")

	// PR / branch metadata.
	if pr.Number > 0 {
		fmt.Fprintf(&b, "## Pull Request #%d\n", pr.Number)
	} else {
		fmt.Fprintf(&b, "## Branch Review: %s\n", pr.HeadBranch)
	}
	fmt.Fprintf(&b, "<pr-title>%s</pr-title>\n", escapePromptTag(pr.Title, "pr-title"))
	fmt.Fprintf(&b, "**Author:** %s\n", pr.Author)
	if pr.Body != "" {
		safeBody := escapePromptTag(pr.Body, "pr-body")
		fmt.Fprintf(&b, "<pr-body>\n%s\n</pr-body>\n", safeBody)
	}
	b.WriteString("\n")

	// Context from config.
	if cfg != nil && cfg.Context != "" {
		fmt.Fprintf(&b, "## Additional Context\n%s\n\n", cfg.Context)
	}

	// Project documentation (CLAUDE.md files).
	writeProjectDocs(&b, projectDocs)

	// Review rules.
	if cfg != nil && len(cfg.Rules) > 0 {
		b.WriteString("## Review Rules\n")
		b.WriteString("Apply the following rules when reviewing:\n\n")
		for _, rule := range cfg.Rules {
			fmt.Fprintf(&b, "- **%s** (severity: %s): %s\n", rule.ID, rule.Severity, rule.Description)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("## Review Rules\nNo specific rules are defined. Perform a general code review covering correctness, security, performance, and maintainability.\n\n")
	}

	// Ignore patterns.
	if cfg != nil && len(cfg.Ignore) > 0 {
		b.WriteString("## Ignore Patterns\nDo NOT report findings for files matching these patterns:\n")
		for _, pat := range cfg.Ignore {
			fmt.Fprintf(&b, "- `%s`\n", pat)
		}
		b.WriteString("\n")
	}

	// Explicit allowlist of files in this diff.
	if len(pr.Files) > 0 {
		b.WriteString("## Files in This Diff\n")
		b.WriteString("The following files — and ONLY these files — are part of this diff. Every finding you report MUST reference one of these exact paths. Do NOT reference any file that is not in this list.\n\n")
		for _, f := range pr.Files {
			fmt.Fprintf(&b, "- `%s`\n", f)
		}
		b.WriteString("\n")
	}

	// Changed file contents for full context.
	writeFileContents(&b, pr.FileContents, pr.Files)

	// Diff.
	diffFence := codeFence(pr.Diff)
	fmt.Fprintf(&b, "## Diff\n%sdiff\n", diffFence)
	b.WriteString(pr.Diff)
	fmt.Fprintf(&b, "\n%s\n\n", diffFence)

	// Output format instructions.
	b.WriteString("## Output Format\n")
	b.WriteString("Return your findings as a JSON array inside a ```json code fence. Each finding must have these fields:\n\n")
	b.WriteString("- `id` (string): The rule ID that was violated, or a short kebab-case identifier for general findings.\n")
	b.WriteString("- `file` (string): The file path where the issue was found. **Must be one of the exact paths listed in \"Files in This Diff\" above.** If a file path does not appear in that list, do NOT reference it. If your finding relates to a file not in the diff (e.g. a downstream consequence), set `file` and `line` to the diff location that triggers the issue and mention the affected file in `description`.\n")
	b.WriteString("- `line` (int): The line number in the file. **Must be a line that was added or modified in the diff** (a `+` line in the diff hunk). If your finding is about a side effect on a distant line, set `line` to the diff line that *causes* the issue and describe the affected location in `description`.\n")
	b.WriteString("- `severity` (string): One of \"critical\", \"bug\", \"warning\", \"suggestion\", or \"nitpick\".\n")
	b.WriteString("  - \"critical\": Security vulnerabilities, data loss, crashes.\n")
	b.WriteString("  - \"bug\": Logic errors, incorrect behavior.\n")
	b.WriteString("  - \"warning\": Potential issues, performance problems, code smells.\n")
	b.WriteString("  - \"suggestion\": Better patterns, readability improvements.\n")
	b.WriteString("  - \"nitpick\": Minor style, naming, formatting.\n")
	b.WriteString("- `title` (string): A short title for the finding.\n")
	b.WriteString("- `description` (string): A detailed explanation of the issue.\n")
	b.WriteString("- `suggestion` (string, optional): A suggested fix or improvement. For suggestions about broader patterns, structural concerns, or improvements that go beyond the scope of the current PR, recommend that the author discuss with the team or open a separate PR — do not imply they should fix it in this PR.\n")
	first := startIndex + 1
	fixRefPrefix := fmt.Sprintf("%d", pr.Number)
	if pr.Number == 0 {
		fixRefPrefix = "local"
	}
	fmt.Fprintf(&b, "- `fix_ref` (string): A reference ID in the format `%s-<index>` where index starts at %d (e.g. `%s-%d`, `%s-%d`).\n", fixRefPrefix, first, fixRefPrefix, first, fixRefPrefix, first+1)
	b.WriteString("- `actionable` (boolean): Set to `false` if your analysis concludes the code is correct and no change is needed. Set to `true` if the finding requires the author to act. **Prefer returning an empty array over emitting findings with `actionable: false`.**\n")
	b.WriteString("\n**IMPORTANT — JSON escaping:** When your description or suggestion references code containing backslash sequences (e.g. `\\n`, `\\t`, `\\\"`), you MUST double-escape the backslash in the JSON string value. For example, to mention `fmt.Print(\"\\n\")` in a JSON string, write `fmt.Print(\"\\\\n\")`. A single `\\n` in JSON is a newline character, not the literal text `\\n`.\n")
	b.WriteString("\n**Do not include findings where your conclusion is that the code is correct or no action is needed.** If you evaluate something and determine it is fine, omit it entirely rather than reporting it. Specifically: if you begin analyzing a potential issue but then realize the code handles it correctly, do NOT emit a finding that walks through the concern and then concludes \"this is actually fine\" or \"no bug here\" — simply drop it. Every finding you emit must represent a real, actionable problem.\n")
	b.WriteString("\n**CRITICAL: Do NOT invent or hallucinate file paths, function names, or code that does not appear in the diff or the provided file contents. If a file or function is not shown above, do not reference it.**\n")
	b.WriteString("\nIf there are no findings, return an empty array: `[]`.\n")
	b.WriteString("\nExample:\n```json\n[\n  {\n    \"id\": \"rule-id\",\n    \"file\": \"src/main.go\",\n    \"line\": 42,\n    \"severity\": \"warning\",\n    \"title\": \"Short title\",\n    \"description\": \"Detailed description.\",\n    \"suggestion\": \"Consider doing X instead.\",\n")
	fmt.Fprintf(&b, "    \"fix_ref\": \"%s-%d\",\n    \"actionable\": true\n  }\n]\n```\n", fixRefPrefix, first)

	return b.String()
}

// ResolvedContext describes a finding that was resolved during triage, used to
// prevent the incremental review from re-raising the same or similar issues.
type ResolvedContext struct {
	Path   string
	Line   int
	Title  string // first line of the finding body
	Reason string // "code_change", "dismissed", "acknowledged", "rebutted"
}

// Deprecated: BuildReevaluatePrompt is replaced by per-thread evaluation in triage.go.
// Kept temporarily for reference; will be removed in a future release.
func BuildReevaluatePrompt(threads []ReviewThread, incrementalDiff string) string {
	var b strings.Builder

	b.WriteString("You are a code reviewer. You previously left findings on a pull request. The author has pushed new changes.\n\n")
	b.WriteString("## Previous Findings\n")
	b.WriteString("Here are the unresolved findings from previous reviews:\n\n")

	for i, t := range threads {
		fmt.Fprintf(&b, "- **thread-%d** at `%s:%d`\n", i, t.Path, t.Line)
		// Extract the first line of the body as the severity+rule summary.
		firstLine := t.Body
		if idx := strings.Index(t.Body, "\n"); idx >= 0 {
			firstLine = t.Body[:idx]
		}
		fmt.Fprintf(&b, "  %s\n", firstLine)
		for _, r := range t.Replies {
			normalizedBody := strings.ReplaceAll(r.Body, "\n", " ")
			fmt.Fprintf(&b, "  > **@%s** replied: %s\n", r.Author, normalizedBody)
		}
	}

	reEvalFence := codeFence(incrementalDiff)
	fmt.Fprintf(&b, "\n## Changes Since Last Review\n%sdiff\n", reEvalFence)
	b.WriteString(incrementalDiff)
	fmt.Fprintf(&b, "\n%s\n\n", reEvalFence)

	b.WriteString("## Task\n")
	b.WriteString("Determine which of the previous findings should be resolved.\n\n")
	b.WriteString("A finding should be resolved if ANY of the following apply:\n")
	b.WriteString("1. **Fixed by code changes** — the new diff addresses the issue.\n")
	b.WriteString("2. **Dismissed by the author** — a human reply explicitly asks the reviewer to dismiss, ignore, or skip the finding (e.g. \"dismiss this\", \"you can safely dismiss\", \"please ignore\", \"skip this one\"). The author is exercising their authority to close the thread.\n")
	b.WriteString("3. **Acknowledged by the author** — a human reply indicates the finding is intentional, accepted as-is, or will be addressed separately (e.g. \"that's fine\", \"intentional\", \"will fix in a future PR\", \"tracked in issue #N\").\n")
	b.WriteString("4. **Rebutted by the author** — a human reply provides a concrete technical explanation showing the finding is not applicable, the concern is mitigated, or the tradeoff is justified in this context (e.g. the behaviour cannot occur due to framework semantics, the impact is negligible because of how the system is configured, or a project convention makes the approach intentional). A vague disagreement like \"I don't think so\" does NOT qualify — the reply must cite specific technical details, framework behaviour, or project constraints.\n\n")
	b.WriteString("A reply that merely asks a question or expresses disagreement without substantive technical reasoning should NOT count.\n\n")
	b.WriteString("Return a JSON array of objects for findings that should be resolved inside a ```json code fence.\n")
	b.WriteString("Each object must have `thread` (the thread ID) and `reason` (one of `code_change`, `dismissed`, `acknowledged`, or `rebutted`).\n")
	b.WriteString("If none should be resolved, return an empty array: `[]`.\n\n")
	b.WriteString("Example:\n```json\n[{\"thread\": \"thread-0\", \"reason\": \"code_change\"}, {\"thread\": \"thread-1\", \"reason\": \"dismissed\"}, {\"thread\": \"thread-2\", \"reason\": \"rebutted\"}]\n```\n")

	return b.String()
}

// BuildIncrementalPrompt reviews only new code, avoiding duplicate reports.
// startIndex is the number of existing findings so fix_ref numbering continues.
// resolved provides context about recently resolved findings to prevent ping-ponging.
func BuildIncrementalPrompt(diff string, cfg *ReviewConfig, knownIssues []ReviewThread, prNumber int, startIndex int, fileContents map[string]string, files []string, resolved []ResolvedContext, projectDocs map[string]string) string {
	var b strings.Builder

	b.WriteString("You are a code reviewer. Review ONLY the following incremental changes and report NEW findings.\n")
	b.WriteString("You will be given the full contents of changed files for context, along with the diff. Only report issues that are directly related to the changes in the diff — do not flag pre-existing issues in unchanged code. Do not report a finding if your analysis concludes that the code is correct and no action is needed — only report findings that require the author to make a change or consider a specific alternative.\n")
	b.WriteString("Also consider whether the changes could cause side effects in other files that depend on or interact with the modified code (e.g. callers, importers, shared state). If you identify a potential side effect, anchor your finding to the relevant line in the diff and describe the affected downstream code in the description.\n\n")

	// Explicit allowlist of files in this diff.
	if len(files) > 0 {
		b.WriteString("## Files in This Diff\n")
		b.WriteString("The following files — and ONLY these files — are part of this incremental diff. Every finding you report MUST reference one of these exact paths. Do NOT reference any file that is not in this list.\n\n")
		for _, f := range files {
			fmt.Fprintf(&b, "- `%s`\n", f)
		}
		b.WriteString("\n")
	}

	// Context from config.
	if cfg != nil && cfg.Context != "" {
		fmt.Fprintf(&b, "## Additional Context\n%s\n\n", cfg.Context)
	}

	// Project documentation (CLAUDE.md files).
	writeProjectDocs(&b, projectDocs)

	// Review rules.
	if cfg != nil && len(cfg.Rules) > 0 {
		b.WriteString("## Review Rules\n")
		b.WriteString("Apply the following rules when reviewing:\n\n")
		for _, rule := range cfg.Rules {
			fmt.Fprintf(&b, "- **%s** (severity: %s): %s\n", rule.ID, rule.Severity, rule.Description)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("## Review Rules\nNo specific rules are defined. Perform a general code review covering correctness, security, performance, and maintainability.\n\n")
	}

	// Ignore patterns.
	if cfg != nil && len(cfg.Ignore) > 0 {
		b.WriteString("## Ignore Patterns\nDo NOT report findings for files matching these patterns:\n")
		for _, pat := range cfg.Ignore {
			fmt.Fprintf(&b, "- `%s`\n", pat)
		}
		b.WriteString("\n")
	}

	// Known issues to avoid duplicating.
	if len(knownIssues) > 0 {
		b.WriteString("## Known Issues (DO NOT DUPLICATE)\n")
		b.WriteString("These issues are already reported and unresolved. Do NOT report them again:\n\n")
		for _, t := range knownIssues {
			fmt.Fprintf(&b, "- `%s:%d`\n", t.Path, t.Line)
		}
		b.WriteString("\n")
	}

	// Recently resolved issues — anti-ping-pong context.
	if len(resolved) > 0 {
		b.WriteString("## Recently Resolved Issues\n")
		b.WriteString("These issues from previous reviews were addressed in this push. Do NOT re-raise them or similar variants.\nIf you find a genuinely new issue in the same area, explain why it is distinct.\n\n")
		for _, r := range resolved {
			reasonLabel := r.Reason
			switch r.Reason {
			case "code_change":
				reasonLabel = "fixed by code change"
			case "dismissed":
				reasonLabel = "dismissed by author"
			case "acknowledged":
				reasonLabel = "acknowledged by author"
			case "rebutted":
				reasonLabel = "rebutted by author"
			}
			if r.Title != "" {
				fmt.Fprintf(&b, "- `%s:%d` (%s) — %s\n", r.Path, r.Line, r.Title, reasonLabel)
			} else {
				fmt.Fprintf(&b, "- `%s:%d` — %s\n", r.Path, r.Line, reasonLabel)
			}
		}
		b.WriteString("\n")
	}

	// Changed file contents for full context.
	writeFileContents(&b, fileContents, files)

	// Incremental diff.
	incDiffFence := codeFence(diff)
	fmt.Fprintf(&b, "## Incremental Diff\n%sdiff\n", incDiffFence)
	b.WriteString(diff)
	fmt.Fprintf(&b, "\n%s\n\n", incDiffFence)

	// Output format instructions.
	b.WriteString("## Output Format\n")
	b.WriteString("Return your findings as a JSON array inside a ```json code fence. Each finding must have these fields:\n\n")
	b.WriteString("- `id` (string): The rule ID that was violated, or a short kebab-case identifier for general findings.\n")
	b.WriteString("- `file` (string): The file path where the issue was found. **Must be one of the exact paths listed in \"Files in This Diff\" above.** If a file path does not appear in that list, do NOT reference it. If your finding relates to a downstream file not in the diff, set `file` and `line` to the diff location that triggers the issue and mention the affected file in `description`.\n")
	b.WriteString("- `line` (int): The line number in the file. **Must be a line that was added or modified in the diff** (a `+` line in the diff hunk). If your finding is about a side effect on a distant line, set `line` to the diff line that *causes* the issue and describe the affected location in `description`.\n")
	b.WriteString("- `severity` (string): One of \"critical\", \"bug\", \"warning\", \"suggestion\", or \"nitpick\".\n")
	b.WriteString("  - \"critical\": Security vulnerabilities, data loss, crashes.\n")
	b.WriteString("  - \"bug\": Logic errors, incorrect behavior.\n")
	b.WriteString("  - \"warning\": Potential issues, performance problems, code smells.\n")
	b.WriteString("  - \"suggestion\": Better patterns, readability improvements.\n")
	b.WriteString("  - \"nitpick\": Minor style, naming, formatting.\n")
	b.WriteString("- `title` (string): A short title for the finding.\n")
	b.WriteString("- `description` (string): A detailed explanation of the issue.\n")
	b.WriteString("- `suggestion` (string, optional): A suggested fix or improvement. For suggestions about broader patterns, structural concerns, or improvements that go beyond the scope of the current PR, recommend that the author discuss with the team or open a separate PR — do not imply they should fix it in this PR.\n")
	first := startIndex + 1
	fixRefPrefix := fmt.Sprintf("%d", prNumber)
	if prNumber == 0 {
		fixRefPrefix = "local"
	}
	fmt.Fprintf(&b, "- `fix_ref` (string): A reference ID in the format `%s-<index>` where index starts at %d (e.g. `%s-%d`, `%s-%d`).\n", fixRefPrefix, first, fixRefPrefix, first, fixRefPrefix, first+1)
	b.WriteString("- `actionable` (boolean): Set to `false` if your analysis concludes the code is correct and no change is needed. Set to `true` if the finding requires the author to act. **Prefer returning an empty array over emitting findings with `actionable: false`.**\n")
	b.WriteString("\n**IMPORTANT — JSON escaping:** When your description or suggestion references code containing backslash sequences (e.g. `\\n`, `\\t`, `\\\"`), you MUST double-escape the backslash in the JSON string value. For example, to mention `fmt.Print(\"\\n\")` in a JSON string, write `fmt.Print(\"\\\\n\")`. A single `\\n` in JSON is a newline character, not the literal text `\\n`.\n")
	b.WriteString("\n**Do not include findings where your conclusion is that the code is correct or no action is needed.** If you evaluate something and determine it is fine, omit it entirely rather than reporting it. Specifically: if you begin analyzing a potential issue but then realize the code handles it correctly, do NOT emit a finding that walks through the concern and then concludes \"this is actually fine\" or \"no bug here\" — simply drop it. Every finding you emit must represent a real, actionable problem.\n")
	b.WriteString("\n**CRITICAL: Do NOT invent or hallucinate file paths, function names, or code that does not appear in the diff or the provided file contents. If a file or function is not shown above, do not reference it.**\n")
	b.WriteString("\nOnly report NEW issues found in the incremental diff. If there are no new findings, return an empty array: `[]`.\n")
	b.WriteString("\nExample:\n```json\n[\n  {\n    \"id\": \"rule-id\",\n    \"file\": \"src/main.go\",\n    \"line\": 42,\n    \"severity\": \"warning\",\n    \"title\": \"Short title\",\n    \"description\": \"Detailed description.\",\n    \"suggestion\": \"Consider doing X instead.\",\n")
	fmt.Fprintf(&b, "    \"fix_ref\": \"%s-%d\",\n    \"actionable\": true\n  }\n]\n```\n", fixRefPrefix, first)

	return b.String()
}

// writeProjectDocs adds a "Project Documentation" section to the prompt builder
// if any CLAUDE.md files are provided.
func writeProjectDocs(b *strings.Builder, docs map[string]string) {
	if len(docs) == 0 {
		return
	}
	b.WriteString("## Project Documentation\n")
	b.WriteString("The following project documentation describes conventions and standards for this codebase. Use these to inform your review — flag violations of these conventions when relevant.\n\n")
	for _, path := range slices.Sorted(maps.Keys(docs)) {
		safe := escapePromptTag(docs[path], "project-doc")
		fmt.Fprintf(b, "<project-doc path=%q>\n%s\n</project-doc>\n\n", path, safe)
	}
}

// writeFileContents adds a "Changed File Contents" section to the prompt builder.
func writeFileContents(b *strings.Builder, fileContents map[string]string, files []string) {
	if len(fileContents) == 0 {
		return
	}

	b.WriteString("## Changed File Contents\n")
	b.WriteString("Below are the full contents of changed files. Use these to understand surrounding code, types, imports, and control flow. Do NOT report findings on unchanged code — only flag issues directly related to changes in the diff.\n\n")

	for _, path := range files {
		content, ok := fileContents[path]
		if !ok {
			continue
		}
		fence := codeFence(content)
		fmt.Fprintf(b, "### `%s`\n", path)
		fmt.Fprintf(b, "%s\n", fence)
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			fmt.Fprintf(b, "%d: %s\n", i+1, line)
		}
		fmt.Fprintf(b, "%s\n\n", fence)
	}
}
