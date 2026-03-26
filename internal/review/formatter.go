package review

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// severityIcon returns the emoji icon for a severity level.
func severityIcon(severity string) string {
	switch strings.ToLower(severity) {
	case "critical":
		return "\U0001F534" // 🔴
	case "bug":
		return "\U0001F7E0" // 🟠
	case "warning":
		return "\U0001F7E1" // 🟡
	case "suggestion":
		return "\U0001F535" // 🔵
	case "nitpick":
		return "\u26AA" // ⚪
	default:
		return "\U0001F535" // 🔵
	}
}

// severityOrder returns a sort rank for a severity level (lower = more severe).
func severityOrder(severity string) int {
	switch strings.ToLower(severity) {
	case "critical":
		return 0
	case "bug":
		return 1
	case "warning":
		return 2
	case "suggestion":
		return 3
	case "nitpick":
		return 4
	default:
		return 5
	}
}

// sortFindings sorts findings by severity (most severe first), preserving
// relative order among findings of the same severity.
func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		return severityOrder(findings[i].Severity) < severityOrder(findings[j].Severity)
	})
}

// severityLabel returns a human-friendly plural-aware label for counts.
func severityLabel(sev string, n int) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, sev)
	}
	switch sev {
	case "warning":
		return fmt.Sprintf("%d warnings", n)
	case "nitpick":
		return fmt.Sprintf("%d nitpicks", n)
	default:
		return fmt.Sprintf("%d %ss", n, sev)
	}
}

// FormatMarkdown formats review findings as a markdown string suitable for a
// PR comment.
func FormatMarkdown(result *ReviewResult) string {
	var b strings.Builder

	// Sort findings by severity.
	sortFindings(result.Findings)

	fmt.Fprintf(&b, "## \U0001F425 CodeCanary \u2014 PR #%d\n\n", result.PRNumber)

	// Summary section.
	if result.Summary != "" {
		fmt.Fprintf(&b, "### Summary\n%s\n", result.Summary)
	} else {
		b.WriteString("### Summary\n")
		b.WriteString(buildSeveritySummary(result.Findings))
		b.WriteString("\n")
	}

	// Individual findings.
	for _, f := range result.Findings {
		b.WriteString("\n---\n\n")
		icon := severityIcon(f.Severity)
		fmt.Fprintf(&b, "### %s `%s` in `%s:%d`\n", icon, f.ID, f.File, f.Line)
		fmt.Fprintf(&b, "**%s**\n\n", f.Title)
		fmt.Fprintf(&b, "%s\n", f.Description)

		if f.Suggestion != "" {
			fmt.Fprintf(&b, "\n> **Suggestion**: %s\n", f.Suggestion)
		}

	}

	// Embed review data as hidden HTML comment for review data extraction.
	jsonData, err := json.Marshal(result)
	if err == nil {
		fmt.Fprintf(&b, "\n<!-- codecanary:review %s -->\n", string(jsonData))
	}

	return b.String()
}

// buildSeveritySummary builds a default summary line from severity counts.
func buildSeveritySummary(findings []Finding) string {
	counts := map[string]int{}
	for _, f := range findings {
		counts[strings.ToLower(f.Severity)]++
	}
	total := len(findings)

	// Ordered severity levels for consistent output.
	levels := []string{"critical", "bug", "warning", "suggestion", "nitpick"}
	var parts []string
	for _, sev := range levels {
		if n := counts[sev]; n > 0 {
			parts = append(parts, severityLabel(sev, n))
		}
	}
	return fmt.Sprintf("Found %d issues (%s)", total, strings.Join(parts, ", "))
}

// FormatReviewBody renders the summary body for a PR review, including hidden
// review data. Inline findings are posted as separate line comments.
func FormatReviewBody(result *ReviewResult, canInline func(Finding) bool) string {
	var b strings.Builder

	fmt.Fprintf(&b, "## \U0001F425 CodeCanary \u2014 PR #%d\n\n", result.PRNumber)

	// Summary section.
	if result.Summary != "" {
		fmt.Fprintf(&b, "### Summary\n%s\n", result.Summary)
	} else {
		b.WriteString("### Summary\n")
		b.WriteString(buildSeveritySummary(result.Findings))
		b.WriteString("\n")
	}

	// Check if there are any inline (line-level) comments.
	hasInline := false
	for _, f := range result.Findings {
		if canInline(f) {
			hasInline = true
			break
		}
	}
	if hasInline {
		b.WriteString("\n\U0001F4AC See inline comments for details.\n")
	}

	// Include findings that cannot be posted inline.
	for _, f := range result.Findings {
		if !canInline(f) {
			b.WriteString("\n---\n\n")
			icon := severityIcon(f.Severity)
			fmt.Fprintf(&b, "### %s **%s** \u2014 `%s`\n\n", icon, f.Severity, f.ID)
			fmt.Fprintf(&b, "**%s**\n\n", f.Title)
			fmt.Fprintf(&b, "%s\n", f.Description)
			if f.Suggestion != "" {
				fmt.Fprintf(&b, "\n> **Suggestion**: %s\n", f.Suggestion)
			}
		}
	}

	// Fix-all prompt in a collapsible section.
	if len(result.Findings) > 0 {
		b.WriteString("\n<details>\n<summary>\U0001F527 Fix all with AI</summary>\n\n")
		b.WriteString("Copy the prompt below and paste it into your AI coding tool:\n\n")
		prompt := buildFixAllPrompt(result.Findings)
		fence := codeFence(prompt)
		fmt.Fprintf(&b, "%s\n", fence)
		b.WriteString(prompt)
		fmt.Fprintf(&b, "%s\n\n", fence)
		b.WriteString("</details>\n")
	}

	// Embed review data as hidden HTML comment for review data extraction.
	jsonData, err := json.Marshal(result)
	if err == nil {
		fmt.Fprintf(&b, "\n<!-- codecanary:review %s -->\n", string(jsonData))
	}

	return b.String()
}

// buildFixAllPrompt constructs a copy-pasteable prompt that addresses all findings.
func buildFixAllPrompt(findings []Finding) string {
	var b strings.Builder

	b.WriteString("Fix the following code review findings. For each finding, apply the suggested fix or resolve the described issue.\n")

	for _, f := range findings {
		b.WriteString("\n---\n\n")
		fmt.Fprintf(&b, "## File: %s, Line: %d\n", f.File, f.Line)
		fmt.Fprintf(&b, "**Issue (%s):** %s\n", f.Severity, f.Title)
		fmt.Fprintf(&b, "%s\n", f.Description)
		if f.Suggestion != "" {
			fmt.Fprintf(&b, "Suggested fix: %s\n", f.Suggestion)
		}
	}

	return b.String()
}

// codeFence returns a backtick fence long enough to safely wrap content.
// It scans for the longest consecutive backtick run and returns one longer,
// with a minimum of 3.
func codeFence(content string) string {
	max := 0
	cur := 0
	for _, ch := range content {
		if ch == '`' {
			cur++
			if cur > max {
				max = cur
			}
		} else {
			cur = 0
		}
	}
	n := max + 1
	if n < 3 {
		n = 3
	}
	return strings.Repeat("`", n)
}

// FormatFindingComment renders a single finding as markdown for an inline PR
// review comment.
func FormatFindingComment(f *Finding) string {
	var b strings.Builder

	b.WriteString("<!-- codecanary:finding -->\n")
	icon := severityIcon(f.Severity)
	fmt.Fprintf(&b, "%s **%s** \u2014 `%s`\n\n", icon, f.Severity, f.ID)
	fmt.Fprintf(&b, "%s\n", f.Description)

	if f.Suggestion != "" {
		fmt.Fprintf(&b, "\n> **Suggestion**: %s\n", f.Suggestion)
	}

	return b.String()
}

// FormatJSON formats review findings as a JSON string.
func FormatJSON(result *ReviewResult) (string, error) {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling review result: %w", err)
	}
	return string(data), nil
}
