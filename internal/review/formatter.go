package review

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
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

// FormatTerminal formats review findings with ANSI colors for terminal display.
func FormatTerminal(result *ReviewResult) string {
	var b strings.Builder
	colors := colorsEnabled()

	sortFindings(result.Findings)
	sortFindings(result.StillOpen)

	// Header.
	b.WriteString("\n")
	if result.PRNumber > 0 {
		fmt.Fprintf(&b, "  %s — PR #%d\n\n", applyStyle(colors, ansiBold, "CodeCanary"), result.PRNumber)
	} else {
		fmt.Fprintf(&b, "  %s — Local Review\n\n", applyStyle(colors, ansiBold, "CodeCanary"))
	}

	// Summary.
	if result.Summary != "" {
		fmt.Fprintf(&b, "  %s\n\n", result.Summary)
	} else if len(result.Findings) > 0 || len(result.StillOpen) > 0 {
		fmt.Fprintf(&b, "  %s\n\n", buildTerminalSummary(result.Findings, result.StillOpen, colors))
	} else {
		fmt.Fprintf(&b, "  %s\n\n", applyStyle(colors, ansiGreen, "No issues found"))
	}

	// Separator.
	sep := terminalSeparator()
	b.WriteString(sep)
	b.WriteString("\n")

	// New findings.
	for _, f := range result.Findings {
		writeTerminalFinding(&b, &f, colors)
		b.WriteString(sep)
		b.WriteString("\n")
	}

	// Still-open findings from previous reviews.
	for _, f := range result.StillOpen {
		writeTerminalFinding(&b, &f, colors)
		b.WriteString(sep)
		b.WriteString("\n")
	}

	return b.String()
}

// buildTerminalSummary builds a summary with severity counts and status breakdown.
func buildTerminalSummary(findings, stillOpen []Finding, colors bool) string {
	total := len(findings) + len(stillOpen)
	counts := map[string]int{}
	for _, f := range findings {
		counts[strings.ToLower(f.Severity)]++
	}
	for _, f := range stillOpen {
		counts[strings.ToLower(f.Severity)]++
	}

	levels := []string{"critical", "bug", "warning", "suggestion", "nitpick"}
	var parts []string
	for _, sev := range levels {
		if n := counts[sev]; n > 0 {
			label := severityLabel(sev, n)
			parts = append(parts, applyStyle(colors, severityColor(sev), label))
		}
	}
	summary := fmt.Sprintf("Found %d issues (%s)", total, strings.Join(parts, ", "))

	// Add status breakdown when there are both new and still-open findings.
	if len(findings) > 0 && len(stillOpen) > 0 {
		newTag := applyStyle(colors, ansiGreen, fmt.Sprintf("%d new", len(findings)))
		openTag := applyStyle(colors, ansiYellow, fmt.Sprintf("%d still open", len(stillOpen)))
		summary += fmt.Sprintf(" — %s, %s", newTag, openTag)
	} else if len(stillOpen) > 0 && len(findings) == 0 {
		openTag := applyStyle(colors, ansiYellow, fmt.Sprintf("%d still open", len(stillOpen)))
		summary += fmt.Sprintf(" — %s", openTag)
	}

	return summary
}

// applyStyle wraps text in an ANSI style code if colors are enabled.
func applyStyle(colors bool, style, text string) string {
	if !colors {
		return text
	}
	return style + text + ansiReset
}

// terminalSeparator returns a horizontal rule sized to the terminal.
func terminalSeparator() string {
	w := terminalWidth(60)
	// Leave 2-char indent, cap at reasonable width.
	lineW := w - 4
	if lineW < 20 {
		lineW = 20
	}
	if lineW > 80 {
		lineW = 80
	}
	return "  " + strings.Repeat("━", lineW)
}

// buildColorSeveritySummary builds a summary with colorized severity counts.
func buildColorSeveritySummary(findings []Finding, colors bool) string {
	counts := map[string]int{}
	for _, f := range findings {
		counts[strings.ToLower(f.Severity)]++
	}
	total := len(findings)

	levels := []string{"critical", "bug", "warning", "suggestion", "nitpick"}
	var parts []string
	for _, sev := range levels {
		if n := counts[sev]; n > 0 {
			label := severityLabel(sev, n)
			parts = append(parts, applyStyle(colors, severityColor(sev), label))
		}
	}
	return fmt.Sprintf("Found %d issues (%s)", total, strings.Join(parts, ", "))
}

// statusTag returns a colored status label for terminal display.
func statusTag(status string, colors bool) string {
	switch status {
	case "new":
		return applyStyle(colors, ansiGreen, "new")
	case "still open":
		return applyStyle(colors, ansiYellow, "still open")
	default:
		return ""
	}
}

// writeTerminalFinding writes a single finding block with ANSI formatting.
func writeTerminalFinding(b *strings.Builder, f *Finding, colors bool) {
	b.WriteString("\n")

	// Finding header: ● severity  finding-id  [status]
	dot := "●"
	if colors {
		dot = severityDot(f.Severity)
	}
	sevLabel := applyStyle(colors, severityColor(f.Severity), f.Severity)
	findingID := applyStyle(colors, ansiCyan, f.ID)
	header := fmt.Sprintf("  %s %s  %s", dot, sevLabel, findingID)
	if tag := statusTag(f.Status, colors); tag != "" {
		header += "  " + tag
	}
	fmt.Fprintf(b, "%s\n", header)

	// File location.
	if f.File != "" {
		loc := fmt.Sprintf("%s:%d", f.File, f.Line)
		fmt.Fprintf(b, "  %s\n", applyStyle(colors, ansiDim, loc))
	}

	// Title.
	title := stripInlineMarkdown(f.Title, colors)
	fmt.Fprintf(b, "  %s\n", applyStyle(colors, ansiBold, title))
	b.WriteString("\n")

	// Description.
	writeFormattedText(b, f.Description, colors)
	b.WriteString("\n")

	// Suggestion.
	if f.Suggestion != "" {
		fmt.Fprintf(b, "  %s\n", applyStyle(colors, ansiBold, "Suggestion:"))
		writeFormattedText(b, f.Suggestion, colors)
		b.WriteString("\n")
	}
}

// writeFormattedText renders text with code blocks extracted and box-drawn.
func writeFormattedText(b *strings.Builder, text string, colors bool) {
	segments := splitCodeBlocks(text)
	for _, seg := range segments {
		if seg.isCode {
			writeCodeBlock(b, seg.content, colors)
		} else {
			// Wrap plain text lines with 2-space indent.
			plain := stripInlineMarkdown(strings.TrimSpace(seg.content), colors)
			for _, line := range strings.Split(plain, "\n") {
				fmt.Fprintf(b, "  %s\n", line)
			}
		}
	}
}

// textSegment represents a chunk of text that is either plain or a code block.
type textSegment struct {
	content string
	isCode  bool
}

// codeBlockRe matches fenced code blocks (```lang\n...\n```).
var codeBlockRe = regexp.MustCompile("(?s)```[a-zA-Z]*\n(.*?)```")

// splitCodeBlocks splits text into alternating plain text and code segments.
func splitCodeBlocks(text string) []textSegment {
	var segments []textSegment
	matches := codeBlockRe.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return []textSegment{{content: text, isCode: false}}
	}

	prev := 0
	for _, m := range matches {
		// m[0]:m[1] is the full match, m[2]:m[3] is the capture group (code content).
		if m[0] > prev {
			segments = append(segments, textSegment{content: text[prev:m[0]], isCode: false})
		}
		segments = append(segments, textSegment{content: text[m[2]:m[3]], isCode: true})
		prev = m[1]
	}
	if prev < len(text) {
		segments = append(segments, textSegment{content: text[prev:], isCode: false})
	}
	return segments
}

// writeCodeBlock renders a code block with box-drawing borders.
func writeCodeBlock(b *strings.Builder, code string, colors bool) {
	code = strings.TrimRight(code, "\n")
	lines := strings.Split(code, "\n")

	// Determine box width from longest line (rune count, not byte count).
	maxLen := 40
	for _, line := range lines {
		if w := utf8.RuneCountInString(line); w > maxLen {
			maxLen = w
		}
	}
	boxW := maxLen + 2 // padding
	if boxW > 78 {
		boxW = 78
	}

	border := applyStyle(colors, ansiDim, "┌"+strings.Repeat("─", boxW))
	fmt.Fprintf(b, "  %s\n", border)
	for _, line := range lines {
		pipe := applyStyle(colors, ansiDim, "│")
		fmt.Fprintf(b, "  %s %s\n", pipe, line)
	}
	border = applyStyle(colors, ansiDim, "└"+strings.Repeat("─", boxW))
	fmt.Fprintf(b, "  %s\n", border)
}

// inlineBoldRe matches **bold** markdown.
var inlineBoldRe = regexp.MustCompile(`\*\*(.+?)\*\*`)

// inlineCodeRe matches `code` markdown.
var inlineCodeRe = regexp.MustCompile("`([^`]+)`")

// stripInlineMarkdown converts inline markdown to ANSI styling or plain text.
func stripInlineMarkdown(text string, colors bool) string {
	// Replace **bold** with ANSI bold or plain text.
	text = inlineBoldRe.ReplaceAllStringFunc(text, func(m string) string {
		inner := inlineBoldRe.FindStringSubmatch(m)[1]
		return applyStyle(colors, ansiBold, inner)
	})
	// Replace `code` with ANSI cyan or plain text.
	text = inlineCodeRe.ReplaceAllStringFunc(text, func(m string) string {
		inner := inlineCodeRe.FindStringSubmatch(m)[1]
		return applyStyle(colors, ansiCyan, inner)
	})
	return text
}

// FormatJSON formats review findings as a JSON string.
func FormatJSON(result *ReviewResult) (string, error) {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling review result: %w", err)
	}
	return string(data), nil
}
