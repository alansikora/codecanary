package review

import (
	"fmt"
	"strings"
)

// ReviewSummary holds per-cycle counts used to render the status block that
// appears at the top of every CodeCanary top-level review body.
type ReviewSummary struct {
	NewFindings    int // new findings raised this cycle
	ResolvedByCode int // previous findings resolved by code changes
	FileRemoved    int // previous findings whose file was removed from the PR
	Dismissed      int
	Acknowledged   int
	Rebutted       int
	StillOpen      int // threads still open with no classification this cycle
	// Blocking counts the new and still-open findings at or above
	// blockingSeverity. Only these fail the commit status.
	Blocking int
}

// hasContent reports whether any bucket has a non-zero count.
func (s ReviewSummary) hasContent() bool {
	return s.NewFindings+s.ResolvedByCode+s.FileRemoved+
		s.Dismissed+s.Acknowledged+s.Rebutted+s.StillOpen > 0
}

// Status block markers used to replace the block in place when a reply-only
// run (or duplicate synchronize webhook) edits the latest CodeCanary review.
const (
	statusBlockOpen  = "<!-- codecanary:status -->"
	statusBlockClose = "<!-- /codecanary:status -->"
)

// renderSummaryBlock returns a Markdown block listing non-zero counts.
// Returns an empty string when all counts are zero. Callers append the
// result to the review body. The block is wrapped in HTML-comment markers
// so replaceSummaryBlock can find and update it on subsequent edits.
func renderSummaryBlock(s ReviewSummary) string {
	if !s.hasContent() {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s\n### Status\n\n", statusBlockOpen)
	row := func(label string, n int) {
		if n == 0 {
			return
		}
		fmt.Fprintf(&b, "- %s: **%d**\n", label, n)
	}
	row("New findings", s.NewFindings)
	row("Resolved by code", s.ResolvedByCode)
	row("File removed", s.FileRemoved)
	row("Dismissed by author", s.Dismissed)
	row("Acknowledged by author", s.Acknowledged)
	row("Rebutted by author", s.Rebutted)
	row("Still unresolved", s.StillOpen)
	fmt.Fprintf(&b, "%s\n", statusBlockClose)
	return b.String()
}

// replaceSummaryBlock replaces an existing status block in body with the
// block rendered from summary. If no status block exists, the new block is
// appended. When summary is empty, any existing block is stripped.
func replaceSummaryBlock(body string, summary ReviewSummary) string {
	newBlock := renderSummaryBlock(summary)
	open := strings.Index(body, statusBlockOpen)
	if open < 0 {
		return body + newBlock
	}
	// Trim one leading newline so we don't accumulate blank lines on
	// repeated edits (renderSummaryBlock prepends "\n").
	start := open
	if start > 0 && body[start-1] == '\n' {
		start--
	}
	closeIdx := strings.Index(body[open:], statusBlockClose)
	if closeIdx < 0 {
		// Malformed marker — replace from the open tag to end of body.
		return body[:start] + newBlock
	}
	end := open + closeIdx + len(statusBlockClose)
	// Consume a trailing newline from the old block so we don't double up.
	if end < len(body) && body[end] == '\n' {
		end++
	}
	return body[:start] + newBlock + body[end:]
}

// computeReviewSummary builds a ReviewSummary from the fixed thread
// resolutions, the full prior thread list, and the new findings emitted
// this cycle. The StillOpen bucket captures threads that were not
// classified by this cycle (no code fix, no author activity).
func computeReviewSummary(threads []ReviewThread, fixed []fixedThread, newFindings []Finding) ReviewSummary {
	s := ReviewSummary{NewFindings: len(newFindings)}
	for _, f := range newFindings {
		if isBlocking(f.Severity) {
			s.Blocking++
		}
	}
	fixedSet := make(map[int]bool, len(fixed))
	for _, f := range fixed {
		if f.Index < 0 || f.Index >= len(threads) {
			continue
		}
		fixedSet[f.Index] = true
		switch f.Reason {
		case "code_change":
			s.ResolvedByCode++
		case "file_removed":
			s.FileRemoved++
		case "dismissed":
			s.Dismissed++
		case "acknowledged":
			s.Acknowledged++
		case "rebutted":
			s.Rebutted++
		}
	}
	s.StillOpen = len(threads) - len(fixedSet)
	for i, t := range threads {
		if !fixedSet[i] && isBlocking(FindingFromThread(t).Severity) {
			s.Blocking++
		}
	}
	return s
}

// commitStatusFromSummary maps a ReviewSummary to the (state, description)
// pair sent to the commit status API. Pulled out so the mapping is
// unit-testable without network access.
//
// An "unresolved" count combines new findings this cycle with threads that
// were already open and remain unclassified. Only the blocking ones fail the
// required check: suggestions and nitpicks stay visible on the PR but don't
// make the author push again. Everything classified by triage (resolved by
// code, file removed, dismissed, acknowledged, rebutted) counts as handled.
func commitStatusFromSummary(summary ReviewSummary) (state, desc string) {
	if summary.Blocking > 0 {
		return "failure", fmt.Sprintf("%d unresolved blocking finding%s", summary.Blocking, pluralS(summary.Blocking))
	}
	if unresolved := summary.NewFindings + summary.StillOpen; unresolved > 0 {
		return "success", fmt.Sprintf("%d non-blocking finding%s open", unresolved, pluralS(unresolved))
	}
	if summary.ResolvedByCode+summary.FileRemoved+summary.Dismissed+summary.Acknowledged+summary.Rebutted > 0 {
		return "success", "all findings resolved"
	}
	return "success", "no findings"
}

// CommitStatusForFindings maps the open findings of a local review to the
// (state, description) pair for the commit status, by the same rule the bot
// applies. Findings marked not actionable are left out.
func CommitStatusForFindings(findings []Finding) (state, desc string) {
	var s ReviewSummary
	for _, f := range findings {
		if f.Actionable != nil && !*f.Actionable {
			continue
		}
		s.StillOpen++
		if isBlocking(f.Severity) {
			s.Blocking++
		}
	}
	return commitStatusFromSummary(s)
}

// pluralS returns "s" unless n is 1.
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
