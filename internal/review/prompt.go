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

// escapeAllTags replaces every "<" and ">" in content with their HTML entities,
// fully neutralising any XML-like tag injection. Both characters are escaped so
// that LLM parsers that resolve "&lt;" back to "<" still cannot match a closing
// ">" to form a complete tag. Use this for untrusted user content (PR title,
// body, thread replies) where no structural tags should survive.
func escapeAllTags(content string) string {
	content = strings.ReplaceAll(content, "<", "&lt;")
	content = strings.ReplaceAll(content, ">", "&gt;")
	return content
}

// writeFencedBlock writes content inside a dynamic-length code fence with the
// given language specifier, preventing content containing backtick runs from
// breaking out.
func writeFencedBlock(b *strings.Builder, lang, content string) {
	fence := codeFence(content)
	fmt.Fprintf(b, "%s%s\n", fence, lang)
	b.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		b.WriteString("\n")
	}
	fmt.Fprintf(b, "%s\n", fence)
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
	fmt.Fprintf(&b, "<pr-title>%s</pr-title>\n", escapeAllTags(pr.Title))
	fmt.Fprintf(&b, "**Author:** %s\n", pr.Author)
	if pr.Body != "" {
		fmt.Fprintf(&b, "<pr-body>\n%s\n</pr-body>\n", escapeAllTags(pr.Body))
	}
	b.WriteString("\n")

	// Context from config.
	if cfg != nil && cfg.Context != "" {
		fmt.Fprintf(&b, "## Additional Context\n%s\n\n", cfg.Context)
	}

	// Project documentation (CLAUDE.md files).
	writeProjectDocs(&b, projectDocs)

	// Review rules — filter to rules whose path scope matches at least one
	// PR file. Emitting irrelevant rules dilutes LLM attention and wastes
	// context (see `review.yml` rules scoped to paths not in this diff).
	writeRulesSection(&b, cfg, pr.Files)

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
	b.WriteString("## Diff\n")
	writeFencedBlock(&b, "diff", pr.Diff)
	b.WriteString("\n")

	// Lifecycle considerations — flag changes that behave differently after merge.
	writeLifecycleSection(&b)

	// PR description cross-check — flag claims in the body contradicted by the diff.
	if pr.Body != "" {
		writePRBodyCrossCheckSection(&b)
	}

	// Output format instructions.
	b.WriteString("## Output Format\n")
	b.WriteString("Return your findings as a JSON array inside a ```json code fence. Each finding must have these fields:\n\n")
	b.WriteString("- `id` (string): The rule ID that was violated, or a short kebab-case identifier for general findings.\n")
	b.WriteString("- `file` (string): The file path where the issue was found. **Must be one of the exact paths listed in \"Files in This Diff\" above.** If a file path does not appear in that list, do NOT reference it. If your finding relates to a file not in the diff (e.g. a downstream consequence), set `file` and `line` to the diff location that triggers the issue and mention the affected file in `description`.\n")
	b.WriteString("- `line` (int): The line number in the file. **Must be a line that was added or modified in the diff** (a `+` line in the diff hunk). If your finding is about a side effect on a distant line, set `line` to the diff line that *causes* the issue and describe the affected location in `description`.\n")
	b.WriteString("- `severity` (string): One of \"critical\", \"bug\", \"warning\", \"suggestion\", or \"nitpick\".\n")
	b.WriteString("  - \"critical\": Security vulnerabilities, data loss, crashes.\n")
	b.WriteString("  - \"bug\": A logic error that causes incorrect runtime behavior for real inputs. Missing test coverage, unused parameters, typos in identifiers that happen to compile, or \"what if a future caller…\" concerns do NOT qualify — use \"suggestion\" or \"nitpick\" for those. If you cannot name the concrete input and the concrete wrong output, it is not a bug.\n")
	b.WriteString("  - \"warning\": Potential issues, performance problems, code smells.\n")
	b.WriteString("  - \"suggestion\": Better patterns, readability improvements.\n")
	b.WriteString("  - \"nitpick\": Minor style, naming, formatting.\n")
	b.WriteString("- `title` (string): A short title for the finding.\n")
	b.WriteString("- `description` (string): A concise explanation of the issue — 2-3 sentences max. State what is wrong and why it matters. Do not repeat the code or walk through the logic step by step.\n")
	b.WriteString("- `suggestion` (string, optional): A concise suggested fix — 1-2 sentences of prose, then a code block if helpful. Do not explain what the code block does. For suggestions about broader patterns or improvements beyond the current PR scope, recommend opening a separate PR — do not imply they should fix it here.\n")
	first := startIndex + 1
	fixRefPrefix := fmt.Sprintf("%d", pr.Number)
	if pr.Number == 0 {
		fixRefPrefix = "local"
	}
	fmt.Fprintf(&b, "- `fix_ref` (string): A reference ID in the format `%s-<index>` where index starts at %d (e.g. `%s-%d`, `%s-%d`).\n", fixRefPrefix, first, fixRefPrefix, first, fixRefPrefix, first+1)
	b.WriteString("- `actionable` (boolean): Set to `false` if your analysis concludes the code is correct and no change is needed. Set to `true` if the finding requires the author to act. **Prefer returning an empty array over emitting findings with `actionable: false`.**\n")
	b.WriteString("\n**IMPORTANT — JSON escaping:** When your description or suggestion references code containing backslash sequences (e.g. `\\n`, `\\t`, `\\\"`), you MUST double-escape the backslash in the JSON string value. For example, to mention `fmt.Print(\"\\n\")` in a JSON string, write `fmt.Print(\"\\\\n\")`. A single `\\n` in JSON is a newline character, not the literal text `\\n`.\n")
	b.WriteString("\n**Do not include findings where your conclusion is that the code is correct or no action is needed.** If you evaluate something and determine it is fine, omit it entirely rather than reporting it. Specifically: if you begin analyzing a potential issue but then realize the code handles it correctly, do NOT emit a finding that walks through the concern and then concludes \"this is actually fine\" or \"no bug here\" — simply drop it. Every finding you emit must represent a real, actionable problem.\n")
	b.WriteString("\n**Check against project documentation before emitting.** The \"Project Documentation\" section above defines conventions for this codebase (e.g. \"don't add error handling for scenarios that can't happen\", \"keep the core engine agnostic\"). Before emitting a finding, verify it does not contradict those conventions. If your suggested fix would violate a project-doc rule, drop the finding — the author has already made that tradeoff deliberately.\n")
	b.WriteString("\n**Label uncertainty from external behavior.** If your finding's validity depends on the behavior of a third-party API, webhook payload shape, framework internal, or other system you cannot verify from the diff, file contents, and project docs above, you MUST (a) cap severity at \"suggestion\" and (b) state the assumption in `description` (e.g. \"Assumes `github.event.pull_request.number` is unset on `pull_request_review_comment` events — verify against GitHub's webhook docs before acting.\"). A finding that asserts external behavior as fact without this label is a false-positive risk.\n")
	b.WriteString("\n**CRITICAL: Do NOT invent or hallucinate file paths, function names, or code that does not appear in the diff or the provided file contents. If a file or function is not shown above, do not reference it.**\n")
	b.WriteString("\nIf there are no findings, return an empty array: `[]`.\n")
	b.WriteString("\nExample:\n```json\n[\n  {\n    \"id\": \"rule-id\",\n    \"file\": \"src/main.go\",\n    \"line\": 42,\n    \"severity\": \"warning\",\n    \"title\": \"Short title\",\n    \"description\": \"The value is used after the error check, so a non-nil error silently proceeds with stale data.\",\n    \"suggestion\": \"Return early on error.\\n\\n```go\\nif err != nil {\\n    return err\\n}\\n```\",\n")
	fmt.Fprintf(&b, "    \"fix_ref\": \"%s-%d\",\n    \"actionable\": true\n  }\n]\n```\n", fixRefPrefix, first)

	return b.String()
}

// ResolvedContext describes a finding that was resolved during triage, used to
// prevent the incremental review from re-raising the same or similar issues.
//
// Description and Suggestion carry the full narrative of the original finding so
// the incremental reviewer can recognize cascading implementations of the fix
// (e.g. test updates that drop assertions for code that was just removed).
// Rationale is the evaluator's one-sentence explanation of why the finding was
// resolved, which links the prior guidance to the current diff.
type ResolvedContext struct {
	Path        string
	Line        int
	Title       string
	Description string
	Suggestion  string
	Reason      string // "code_change", "dismissed", "acknowledged", "rebutted"
	Rationale   string
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

	b.WriteString("\n## Changes Since Last Review\n")
	writeFencedBlock(&b, "diff", incrementalDiff)
	b.WriteString("\n")

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
// prBody is the PR description (may be empty); when non-empty it is included so
// the cross-check section can flag contradictions between the description and
// the incremental diff.
func BuildIncrementalPrompt(diff string, cfg *ReviewConfig, knownIssues []ReviewThread, prNumber int, startIndex int, fileContents map[string]string, files []string, resolved []ResolvedContext, projectDocs map[string]string, prBody string) string {
	var b strings.Builder

	b.WriteString("You are a code reviewer. Review ONLY the following incremental changes and report NEW findings.\n")
	b.WriteString("You will be given the full contents of changed files for context, along with the diff. Only report issues that are directly related to the changes in the diff — do not flag pre-existing issues in unchanged code. Do not report a finding if your analysis concludes that the code is correct and no action is needed — only report findings that require the author to make a change or consider a specific alternative.\n")
	b.WriteString("Also consider whether the changes could cause side effects in other files that depend on or interact with the modified code (e.g. callers, importers, shared state). If you identify a potential side effect, anchor your finding to the relevant line in the diff and describe the affected downstream code in the description.\n\n")

	// PR description (when available) — needed for the cross-check section
	// below so the LLM can flag contradictions between the description and
	// the incremental diff. Emit the same header BuildPrompt uses so the body
	// arrives labelled rather than as raw text after the preamble; without it
	// the LLM has no framing for what the block represents and can misattribute
	// the claims it makes.
	if prBody != "" {
		if prNumber > 0 {
			fmt.Fprintf(&b, "## Pull Request #%d\n", prNumber)
		} else {
			b.WriteString("## Pull Request\n")
		}
		fmt.Fprintf(&b, "<pr-body>\n%s\n</pr-body>\n\n", escapeAllTags(prBody))
	}

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

	// Review rules — filter to rules whose path scope matches at least one
	// incremental-diff file, for the same reason as BuildPrompt.
	writeRulesSection(&b, cfg, files)

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
		b.WriteString("These findings from previous reviews were addressed in this push. The entries below include the original description, the suggestion you gave, and the evaluator's rationale for marking it resolved.\n\n")
		b.WriteString("**Ping-pong guard — READ CAREFULLY:**\n")
		b.WriteString("- Do NOT re-raise these findings or close variants of them.\n")
		b.WriteString("- If a change in the incremental diff appears to *implement* the suggestion of a resolved finding (for example: removing code the previous cycle asked you to remove, dropping fields that were flagged as redundant, deleting assertions for behavior that was intentionally removed), do NOT flag it — doing so contradicts your own prior guidance.\n")
		b.WriteString("- Cascading changes count: a resolved finding in a source file often requires matching edits in tests, callers, documentation, or cleanup of now-dead code. Treat those edits as part of the same fix even when they land in files or lines the original finding did not point at.\n")
		b.WriteString("- Only flag a new issue in the same area if it is genuinely distinct — and when you do, open your `description` by explaining why it is NOT the resolved finding resurfacing.\n\n")
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
			// All four narrative fields below originate from prior LLM output
			// (stored findings or embedded JSON in PR comments) and must be
			// treated as untrusted — an earlier cycle could have emitted text
			// containing XML-like tags or markdown headers that would otherwise
			// corrupt the structure of this prompt. Neutralize with
			// escapeAllTags, matching the handling of other untrusted strings.
			title := r.Title
			if title == "" {
				title = "(no title)"
			}
			fmt.Fprintf(&b, "### `%s:%d` — %s\n", r.Path, r.Line, escapeAllTags(title))
			fmt.Fprintf(&b, "**Status:** %s\n", reasonLabel)
			if r.Rationale != "" {
				fmt.Fprintf(&b, "**Evaluator rationale:** %s\n", escapeAllTags(r.Rationale))
			}
			if r.Description != "" {
				fmt.Fprintf(&b, "\n**Original description:**\n%s\n", escapeAllTags(r.Description))
			}
			if r.Suggestion != "" {
				fmt.Fprintf(&b, "\n**Suggestion you gave:**\n%s\n", escapeAllTags(r.Suggestion))
			}
			b.WriteString("\n")
		}
	}

	// Changed file contents for full context.
	writeFileContents(&b, fileContents, files)

	// Incremental diff.
	b.WriteString("## Incremental Diff\n")
	writeFencedBlock(&b, "diff", diff)
	b.WriteString("\n")

	// Lifecycle considerations — flag changes that behave differently after merge.
	writeLifecycleSection(&b)

	// PR description cross-check — flag claims in the body contradicted by the diff.
	if prBody != "" {
		writePRBodyCrossCheckSection(&b)
	}

	// Output format instructions.
	b.WriteString("## Output Format\n")
	b.WriteString("Return your findings as a JSON array inside a ```json code fence. Each finding must have these fields:\n\n")
	b.WriteString("- `id` (string): The rule ID that was violated, or a short kebab-case identifier for general findings.\n")
	b.WriteString("- `file` (string): The file path where the issue was found. **Must be one of the exact paths listed in \"Files in This Diff\" above.** If a file path does not appear in that list, do NOT reference it. If your finding relates to a downstream file not in the diff, set `file` and `line` to the diff location that triggers the issue and mention the affected file in `description`.\n")
	b.WriteString("- `line` (int): The line number in the file. **Must be a line that was added or modified in the diff** (a `+` line in the diff hunk). If your finding is about a side effect on a distant line, set `line` to the diff line that *causes* the issue and describe the affected location in `description`.\n")
	b.WriteString("- `severity` (string): One of \"critical\", \"bug\", \"warning\", \"suggestion\", or \"nitpick\".\n")
	b.WriteString("  - \"critical\": Security vulnerabilities, data loss, crashes.\n")
	b.WriteString("  - \"bug\": A logic error that causes incorrect runtime behavior for real inputs. Missing test coverage, unused parameters, typos in identifiers that happen to compile, or \"what if a future caller…\" concerns do NOT qualify — use \"suggestion\" or \"nitpick\" for those. If you cannot name the concrete input and the concrete wrong output, it is not a bug.\n")
	b.WriteString("  - \"warning\": Potential issues, performance problems, code smells.\n")
	b.WriteString("  - \"suggestion\": Better patterns, readability improvements.\n")
	b.WriteString("  - \"nitpick\": Minor style, naming, formatting.\n")
	b.WriteString("- `title` (string): A short title for the finding.\n")
	b.WriteString("- `description` (string): A concise explanation of the issue — 2-3 sentences max. State what is wrong and why it matters. Do not repeat the code or walk through the logic step by step.\n")
	b.WriteString("- `suggestion` (string, optional): A concise suggested fix — 1-2 sentences of prose, then a code block if helpful. Do not explain what the code block does. For suggestions about broader patterns or improvements beyond the current PR scope, recommend opening a separate PR — do not imply they should fix it here.\n")
	first := startIndex + 1
	fixRefPrefix := fmt.Sprintf("%d", prNumber)
	if prNumber == 0 {
		fixRefPrefix = "local"
	}
	fmt.Fprintf(&b, "- `fix_ref` (string): A reference ID in the format `%s-<index>` where index starts at %d (e.g. `%s-%d`, `%s-%d`).\n", fixRefPrefix, first, fixRefPrefix, first, fixRefPrefix, first+1)
	b.WriteString("- `actionable` (boolean): Set to `false` if your analysis concludes the code is correct and no change is needed. Set to `true` if the finding requires the author to act. **Prefer returning an empty array over emitting findings with `actionable: false`.**\n")
	b.WriteString("\n**IMPORTANT — JSON escaping:** When your description or suggestion references code containing backslash sequences (e.g. `\\n`, `\\t`, `\\\"`), you MUST double-escape the backslash in the JSON string value. For example, to mention `fmt.Print(\"\\n\")` in a JSON string, write `fmt.Print(\"\\\\n\")`. A single `\\n` in JSON is a newline character, not the literal text `\\n`.\n")
	b.WriteString("\n**Do not include findings where your conclusion is that the code is correct or no action is needed.** If you evaluate something and determine it is fine, omit it entirely rather than reporting it. Specifically: if you begin analyzing a potential issue but then realize the code handles it correctly, do NOT emit a finding that walks through the concern and then concludes \"this is actually fine\" or \"no bug here\" — simply drop it. Every finding you emit must represent a real, actionable problem.\n")
	b.WriteString("\n**Check against project documentation before emitting.** The \"Project Documentation\" section above defines conventions for this codebase (e.g. \"don't add error handling for scenarios that can't happen\", \"keep the core engine agnostic\"). Before emitting a finding, verify it does not contradict those conventions. If your suggested fix would violate a project-doc rule, drop the finding — the author has already made that tradeoff deliberately.\n")
	b.WriteString("\n**Label uncertainty from external behavior.** If your finding's validity depends on the behavior of a third-party API, webhook payload shape, framework internal, or other system you cannot verify from the diff, file contents, and project docs above, you MUST (a) cap severity at \"suggestion\" and (b) state the assumption in `description` (e.g. \"Assumes `github.event.pull_request.number` is unset on `pull_request_review_comment` events — verify against GitHub's webhook docs before acting.\"). A finding that asserts external behavior as fact without this label is a false-positive risk.\n")
	b.WriteString("\n**CRITICAL: Do NOT invent or hallucinate file paths, function names, or code that does not appear in the diff or the provided file contents. If a file or function is not shown above, do not reference it.**\n")
	b.WriteString("\nOnly report NEW issues found in the incremental diff. If there are no new findings, return an empty array: `[]`.\n")
	b.WriteString("\nExample:\n```json\n[\n  {\n    \"id\": \"rule-id\",\n    \"file\": \"src/main.go\",\n    \"line\": 42,\n    \"severity\": \"warning\",\n    \"title\": \"Short title\",\n    \"description\": \"The value is used after the error check, so a non-nil error silently proceeds with stale data.\",\n    \"suggestion\": \"Return early on error.\\n\\n```go\\nif err != nil {\\n    return err\\n}\\n```\",\n")
	fmt.Fprintf(&b, "    \"fix_ref\": \"%s-%d\",\n    \"actionable\": true\n  }\n]\n```\n", fixRefPrefix, first)

	return b.String()
}

// writeRulesSection renders the "## Review Rules" block, scoped to rules
// applicable to the given file set. Centralised so BuildPrompt and
// BuildIncrementalPrompt stay in lockstep on rule scoping and on the
// fallback message when no rules apply.
func writeRulesSection(b *strings.Builder, cfg *ReviewConfig, files []string) {
	var rules []Rule
	if cfg != nil {
		rules = FilterRules(cfg.Rules, files)
	}
	switch {
	case len(rules) > 0:
		b.WriteString("## Review Rules\n")
		b.WriteString("Apply the following rules when reviewing:\n\n")
		for _, rule := range rules {
			fmt.Fprintf(b, "- **%s** (severity: %s): %s\n", rule.ID, rule.Severity, rule.Description)
		}
		b.WriteString("\n")
	case cfg != nil && len(cfg.Rules) > 0:
		// Rules are configured, but none match the files in this diff.
		b.WriteString("## Review Rules\nNo rules from the project configuration apply to the files in this diff. Perform a general code review covering correctness, security, performance, and maintainability.\n\n")
	default:
		b.WriteString("## Review Rules\nNo specific rules are defined. Perform a general code review covering correctness, security, performance, and maintainability.\n\n")
	}
}

// writeLifecycleSection adds a "Lifecycle Considerations" section that asks
// the reviewer to flag changes that behave differently between the PR branch
// (where the review runs) and the default branch (where the code lives after
// merge). These bugs are easy to miss in a hunk-only review because the code
// looks correct in isolation — the defect is the gap between PR-branch and
// post-merge state.
//
// All findings emitted from this section must still anchor `file` and `line`
// to a diff line; the existing file allowlist + line-proximity validators in
// runner.go enforce that. The section only widens the LLM's attention, not
// the validator's tolerance.
func writeLifecycleSection(b *strings.Builder) {
	b.WriteString("## Lifecycle Considerations\n")
	b.WriteString("Some changes behave differently between the PR branch (where this review runs) and the default branch (where the code lives after merge). Scan the diff for places where this asymmetry could cause a real bug post-merge:\n\n")
	b.WriteString("- A trigger, branch filter, or workflow condition is hard-coded to the PR's source branch (e.g. `branches: [feature/foo]`, `if: github.ref == 'refs/heads/feature/foo'`, `if: github.head_ref == ...`). These typically need to be removed or generalized before merge.\n")
	b.WriteString("- A concurrency group, lock key, cache key, or identifier is keyed on a value that is unique-per-branch on the PR but shared post-merge (e.g. `${{ github.ref }}` resolves to many distinct refs across PRs but to a single value on the default branch). After merge, dispatches that should run in parallel will silently cancel each other.\n")
	b.WriteString("- Code paths or values gated on the PR being open or unmerged (e.g. test-only flags, debug switches, sample data, scaffolding) that won't behave the same way on the default branch.\n")
	b.WriteString("- Documentation, comments, or examples that describe temporary scaffolding the diff also adds — both need to be removed before merge, and missing one leaves a stale reference.\n\n")
	b.WriteString("For each lifecycle issue, anchor your finding's `file` and `line` to the offending diff line and explain the post-merge consequence in `description`. Do NOT report concerns in unchanged code — only in lines added or modified by this diff.\n\n")
}

// writePRBodyCrossCheckSection adds a "PR Description Cross-Check" section
// asking the reviewer to flag specific factual claims in the PR body that the
// diff contradicts. Should only be called when a PR body is present in the
// prompt — otherwise the section has nothing to cross-check against.
//
// As with the lifecycle section, findings emitted here must still anchor
// `file` and `line` to a diff line; runner.go's validators enforce that.
func writePRBodyCrossCheckSection(b *strings.Builder) {
	b.WriteString("## PR Description Cross-Check\n")
	b.WriteString("The PR description in `<pr-body>` above often makes specific factual claims about the diff: cost caps, behavior guarantees, feature lists, performance numbers, things the PR explicitly does or does not do. Before completing the review, scan the diff for places where the code directly contradicts a specific factual claim in the description:\n\n")
	b.WriteString("- A documented cost cap, rate limit, or numeric guarantee that the diff exceeds (e.g. body says \"~$2-4/run\", code uses a setting that produces $5-10).\n")
	b.WriteString("- A \"this PR does NOT do X\" or \"this is dispatch-only\" claim where X actually appears in the diff.\n")
	b.WriteString("- A behavior described in the body (e.g. \"fails closed on missing config\") that the diff doesn't implement.\n")
	b.WriteString("- A list of inputs, outputs, or supported cases that omits something the diff adds, or includes something the diff removes.\n\n")
	b.WriteString("For each contradiction, anchor your finding's `file` and `line` to the offending diff line and quote the contradicting text from the PR body in `description`. Do NOT flag style differences, tense mismatches, missing tests in the description, or things you wish the description said — only direct factual contradictions where the body and the code disagree on what the code does.\n\n")
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
		safePath := strings.ReplaceAll(path, "`", "'")
		fmt.Fprintf(b, "### `%s`\n", safePath)
		var numbered strings.Builder
		lines := strings.Split(content, "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		for i, line := range lines {
			fmt.Fprintf(&numbered, "%d: %s\n", i+1, line)
		}
		writeFencedBlock(b, "", numbered.String())
		b.WriteString("\n")
	}
}
