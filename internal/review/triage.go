package review

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

// ThreadClassification is the triage result for a single unresolved thread.
type ThreadClassification int

const (
	TriageSkip             ThreadClassification = iota // no code changes at all
	TriageCodeChanged                                  // diff touches finding location (outdated)
	TriageHasReply                                     // thread has human replies
	TriageCodeChangedReply                             // both code changed AND has replies
	TriageCrossFileChange                              // diff has changes but NOT in this thread's file
)

// TriagedThread pairs a ReviewThread with its classification and context.
type TriagedThread struct {
	Thread   ReviewThread
	Index    int                  // original index in the unresolved slice
	Class    ThreadClassification
	FileDiff string               // diff hunks for this file only (context for Claude)
	BotLogin string               // login of the review bot, for filtering replies
}

// ThreadResolution is the result of a per-thread Claude evaluation.
type ThreadResolution struct {
	Index    int
	Resolved bool
	Reason   string // "code_change", "acknowledged", "rebutted", "dismissed"
	Error    error
}

// ExtractFileDiff extracts all diff hunks for a specific file from a unified diff.
func ExtractFileDiff(fullDiff, filePath string) string {
	lines := strings.Split(fullDiff, "\n")
	var result []string
	capturing := false

	for i := 0; i < len(lines); i++ {
		// Detect start of a new file in the diff.
		if strings.HasPrefix(lines[i], "diff --git") {
			if capturing {
				// We were capturing — new file starts, stop.
				break
			}
			// Search ahead for the "+++ b/<path>" header line.
			// The offset varies (index line, mode lines, etc.) so scan
			// forward instead of using a fixed offset.
			for j := i + 1; j < len(lines) && !strings.HasPrefix(lines[j], "diff --git"); j++ {
				if strings.HasPrefix(lines[j], "+++ b/"+filePath) {
					capturing = true
					result = append(result, lines[i])
					break
				}
			}
			continue
		}
		if capturing {
			result = append(result, lines[i])
		}
	}

	return strings.Join(result, "\n")
}

// ClassifyThreads triages unresolved threads using GitHub's outdated flag and reply presence.
func ClassifyThreads(threads []ReviewThread, fullDiff, botLogin string) []TriagedThread {
	result := make([]TriagedThread, len(threads))

	for i, t := range threads {
		hasReply := hasNewHumanReply(t, botLogin)
		outdated := t.Outdated
		deleted := fileDeletedInDiff(fullDiff, t.Path)

		var class ThreadClassification
		switch {
		case deleted:
			// File was deleted — evaluate with full diff so Claude can check
			// whether the code moved to a replacement file with the fix applied.
			class = TriageCrossFileChange
		case outdated && hasReply:
			class = TriageCodeChangedReply
		case outdated:
			class = TriageCodeChanged
		case hasReply:
			class = TriageHasReply
		default:
			// Even if GitHub didn't mark the thread as outdated, check if the
			// diff touches the same file. A fix may change nearby lines without
			// affecting the exact commented range.
			if fileInDiff(fullDiff, t.Path) {
				class = TriageCodeChanged
			} else if fullDiff != "" {
				// Code changed in other files — evaluate in case the fix is cross-file.
				class = TriageCrossFileChange
			} else {
				class = TriageSkip
			}
		}

		var fileDiff string
		switch class {
		case TriageCodeChanged, TriageCodeChangedReply:
			fileDiff = ExtractFileDiff(fullDiff, t.Path)
		case TriageCrossFileChange:
			fileDiff = fullDiff
		}

		result[i] = TriagedThread{
			Thread:   t,
			Index:    i,
			Class:    class,
			FileDiff: fileDiff,
			BotLogin: botLogin,
		}
	}

	return result
}

// fileInDiff checks if the diff contains changes to the given file path.
func fileInDiff(diff, path string) bool {
	target := "+++ b/" + path
	return strings.Contains(diff, target+"\n") || strings.Contains(diff, target+"\t") || strings.HasSuffix(diff, target)
}

// fileDeletedInDiff checks if the diff shows the given file was deleted.
// A deleted file has "--- a/<path>" followed by "+++ /dev/null".
func fileDeletedInDiff(diff, path string) bool {
	marker := "--- a/" + path
	idx := strings.Index(diff, marker)
	if idx < 0 {
		return false
	}
	// Ensure full path match (not a prefix of a longer filename).
	rest := diff[idx+len(marker):]
	if len(rest) > 0 && rest[0] != '\n' && rest[0] != '\r' {
		return false
	}
	nl := strings.Index(rest, "\n")
	if nl < 0 {
		return false
	}
	nextLine := ""
	rest = rest[nl+1:]
	if eol := strings.Index(rest, "\n"); eol >= 0 {
		nextLine = rest[:eol]
	} else {
		nextLine = rest
	}
	return nextLine == "+++ /dev/null"
}

// hasHumanReply checks if a thread has at least one reply from a non-bot author.
func hasHumanReply(t ReviewThread, botLogin string) bool {
	for _, r := range t.Replies {
		if r.Author != botLogin {
			return true
		}
	}
	return false
}

// isAckReply checks if a reply body contains an acknowledgment marker.
func isAckReply(body string) bool {
	return strings.Contains(body, ackMarkerPrefix) || strings.Contains(body, legacyAckPrefix)
}

// hasNewHumanReply checks if a thread has a human reply AFTER the last
// ack reply. If no ack reply exists, it falls back to hasHumanReply behavior.
// Replies are in chronological order.
func hasNewHumanReply(t ReviewThread, botLogin string) bool {
	lastAckIdx := -1
	for i, r := range t.Replies {
		if r.Author == botLogin && isAckReply(r.Body) {
			lastAckIdx = i
		}
	}
	if lastAckIdx == -1 {
		// No ack reply exists — fall back to standard check.
		return hasHumanReply(t, botLogin)
	}
	// Check for human replies after the last ack.
	for _, r := range t.Replies[lastAckIdx+1:] {
		if r.Author != botLogin {
			return true
		}
	}
	return false
}

// BuildPerThreadPrompt dispatches to the appropriate prompt builder based on classification.
func BuildPerThreadPrompt(t TriagedThread, cfg *ReviewConfig) string {
	switch t.Class {
	case TriageCodeChanged:
		return buildCodeChangePrompt(t, cfg)
	case TriageHasReply:
		return buildReplyPrompt(t, cfg)
	case TriageCodeChangedReply:
		return buildCodeChangeReplyPrompt(t, cfg)
	case TriageCrossFileChange:
		return buildCrossFilePrompt(t, cfg)
	default:
		return "" // TriageSkip — should not be called
	}
}

func buildCodeChangePrompt(t TriagedThread, cfg *ReviewConfig) string {
	var b strings.Builder

	b.WriteString("You are a code reviewer. You previously raised a finding on a pull request. The author pushed new code.\n\n")

	writeFinding(&b, t.Thread)

	b.WriteString("## Code Changes in This File\n```diff\n")
	b.WriteString(t.FileDiff)
	b.WriteString("\n```\n\n")

	if ctx := evalContext(cfg, "code_change"); ctx != "" {
		fmt.Fprintf(&b, "## Additional Context\n%s\n\n", ctx)
	}

	b.WriteString("## Task\n")
	b.WriteString("Does the code change address the issue you raised?\n")
	b.WriteString("- Answer YES if the change fixes the root cause, removes the problematic code, or meaningfully changes the code so the finding no longer applies.\n")
	b.WriteString("- A change to nearby or adjacent code counts IF it effectively resolves the concern (e.g. fixing the logic, adding the missing check, refactoring the problematic pattern).\n")
	b.WriteString("- Answer NO if the finding's concern is still present in the changed code.\n\n")
	writeCodeChangeResolutionFormat(&b)

	return b.String()
}

func buildReplyPrompt(t TriagedThread, cfg *ReviewConfig) string {
	var b strings.Builder

	b.WriteString("You are a code reviewer. You previously raised a finding on a pull request. The author replied.\n\n")

	writeFinding(&b, t.Thread)
	writeReplies(&b, t.Thread, t.BotLogin)

	if ctx := evalContext(cfg, "reply"); ctx != "" {
		fmt.Fprintf(&b, "## Additional Context\n%s\n\n", ctx)
	}

	b.WriteString("## Task\n")
	b.WriteString("Does the author's reply resolve the finding?\n")
	b.WriteString("- **Dismissed**: Reply explicitly asks the reviewer to dismiss, ignore, or skip the finding (e.g. \"dismiss this\", \"you can safely dismiss\", \"please ignore\", \"skip this one\"). The author is exercising their authority to close the thread without further justification.\n")
	b.WriteString("- **Acknowledged**: Reply indicates the finding is intentional, accepted, or tracked elsewhere (e.g. \"intentional\", \"will fix in a future PR\", \"tracked in issue #N\").\n")
	b.WriteString("- **Rebutted**: Reply provides concrete technical reasoning showing the finding is not applicable. Vague disagreement (\"I don't think so\") does NOT qualify — the reply must cite specific technical details, framework behavior, or project constraints.\n")
	b.WriteString("- **Not resolved**: Reply is a question, vague disagreement, or does not address the finding.\n\n")
	writeResolutionFormat(&b)

	return b.String()
}

func buildCodeChangeReplyPrompt(t TriagedThread, cfg *ReviewConfig) string {
	var b strings.Builder

	b.WriteString("You are a code reviewer. You previously raised a finding on a pull request. The author pushed new code AND replied.\n\n")

	writeFinding(&b, t.Thread)
	writeReplies(&b, t.Thread, t.BotLogin)

	b.WriteString("## Code Changes in This File\n```diff\n")
	b.WriteString(t.FileDiff)
	b.WriteString("\n```\n\n")

	if ctx := evalContext(cfg, "code_change"); ctx != "" {
		fmt.Fprintf(&b, "## Additional Context (Code Changes)\n%s\n\n", ctx)
	}
	if ctx := evalContext(cfg, "reply"); ctx != "" {
		fmt.Fprintf(&b, "## Additional Context (Replies)\n%s\n\n", ctx)
	}

	b.WriteString("## Task\n")
	b.WriteString("Is the finding resolved? It may be resolved by the code change, the reply, or both.\n")
	b.WriteString("Evaluate both the code change and the reply.\n\n")
	b.WriteString("- **Fixed by code change**: The new diff addresses the issue you raised — the root cause is fixed, removed, or the code is changed in a way that makes the finding no longer applicable.\n")
	b.WriteString("- **Dismissed**: Reply explicitly asks the reviewer to dismiss, ignore, or skip the finding (e.g. \"dismiss this\", \"you can safely dismiss\", \"please ignore\", \"skip this one\"). The author is exercising their authority to close the thread without further justification.\n")
	b.WriteString("- **Acknowledged**: Reply indicates the finding is intentional, accepted, or tracked elsewhere (e.g. \"intentional\", \"will fix in a future PR\", \"tracked in issue #N\").\n")
	b.WriteString("- **Rebutted**: Reply provides concrete technical reasoning showing the finding is not applicable. Vague disagreement (\"I don't think so\") does NOT qualify — the reply must cite specific technical details, framework behavior, or project constraints.\n")
	b.WriteString("- **Not resolved**: Reply is a question, vague disagreement, or does not address the finding.\n\n")
	writeResolutionFormat(&b)

	return b.String()
}

func buildCrossFilePrompt(t TriagedThread, cfg *ReviewConfig) string {
	var b strings.Builder

	b.WriteString("You are a code reviewer. You previously raised a finding on a pull request. The author pushed new code, but the changes are in DIFFERENT files from where you left your finding.\n\n")

	writeFinding(&b, t.Thread)

	b.WriteString("## All Code Changes\n```diff\n")
	b.WriteString(t.FileDiff)
	b.WriteString("\n```\n\n")

	if ctx := evalContext(cfg, "code_change"); ctx != "" {
		fmt.Fprintf(&b, "## Additional Context\n%s\n\n", ctx)
	}

	b.WriteString("## Task\n")
	b.WriteString("Does any change in this diff address the issue you raised, even though the changes are in different files?\n")
	b.WriteString("- Answer YES if a change in another file effectively resolves the concern (e.g. fixing the caller instead of the callee, adding validation in a different layer, removing the code path that triggers the issue).\n")
	b.WriteString("- Answer NO if none of the changes are related to the finding.\n\n")
	writeCodeChangeResolutionFormat(&b)

	return b.String()
}

// writeFinding writes the finding section to the prompt.
func writeFinding(b *strings.Builder, t ReviewThread) {
	b.WriteString("## Finding\n")
	fmt.Fprintf(b, "File: `%s:%d`\n", t.Path, t.Line)
	b.WriteString(t.Body)
	b.WriteString("\n\n")
}

// writeReplies writes the author replies section to the prompt.
// Bot replies are filtered out so the bot's own acknowledgment messages
// don't leak into the Claude prompt and bias evaluation.
func writeReplies(b *strings.Builder, t ReviewThread, botLogin string) {
	if len(t.Replies) == 0 {
		return
	}
	// Filter out bot replies using explicit botLogin, consistent with
	// hasHumanReply and hasNewHumanReply.
	var humanReplies []ThreadReply
	for _, r := range t.Replies {
		if r.Author != botLogin {
			humanReplies = append(humanReplies, r)
		}
	}
	if len(humanReplies) == 0 {
		return
	}
	b.WriteString("## Author Replies\n")
	for _, r := range humanReplies {
		normalizedBody := strings.ReplaceAll(r.Body, "\n", " ")
		fmt.Fprintf(b, "> **@%s**: %s\n", r.Author, normalizedBody)
	}
	b.WriteString("\n")
}

// writeResolutionFormat writes the expected JSON response format with all reason options.
// Used for prompts where author replies are present (TriageHasReply, TriageCodeChangedReply).
func writeResolutionFormat(b *strings.Builder) {
	b.WriteString("Return a JSON object inside a ```json code fence:\n")
	b.WriteString("- If resolved: `{\"resolved\": true, \"reason\": \"code_change\"}` or `{\"resolved\": true, \"reason\": \"dismissed\"}` or `{\"resolved\": true, \"reason\": \"acknowledged\"}` or `{\"resolved\": true, \"reason\": \"rebutted\"}`\n")
	b.WriteString("- If NOT resolved: `{\"resolved\": false}`\n")
}

// writeCodeChangeResolutionFormat writes a restricted JSON response format
// for code-change-only evaluations (no author reply). Only allows code_change
// as a resolution reason since there is no author reply to acknowledge/dismiss/rebut.
func writeCodeChangeResolutionFormat(b *strings.Builder) {
	b.WriteString("Return a JSON object inside a ```json code fence:\n")
	b.WriteString("- If resolved: `{\"resolved\": true, \"reason\": \"code_change\"}`\n")
	b.WriteString("- If NOT resolved: `{\"resolved\": false}`\n")
}

// evalContext returns the evaluation context string for a given type from config.
func evalContext(cfg *ReviewConfig, evalType string) string {
	if cfg == nil || cfg.Evaluation == nil {
		return ""
	}
	switch evalType {
	case "code_change":
		return cfg.Evaluation.CodeChange.Context
	case "reply":
		return cfg.Evaluation.Reply.Context
	}
	return ""
}

// EvaluateThreadsParallel runs the LLM in parallel for threads that need evaluation.
// When maxBudgetUSD > 0, new goroutines are not launched once the budget is exceeded
// (already-running goroutines are allowed to finish).
func EvaluateThreadsParallel(triaged []TriagedThread, provider ModelProvider, cfg *ReviewConfig, maxConcurrent int, model string, tracker *UsageTracker, maxBudgetUSD float64) []ThreadResolution {
	results := make([]ThreadResolution, len(triaged))

	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup

	for i, t := range triaged {
		if t.Class == TriageSkip {
			results[i] = ThreadResolution{Index: t.Index, Resolved: false}
			continue
		}
		// Soft budget cap: skip remaining evaluations if budget is exceeded.
		if err := CheckBudget(tracker, maxBudgetUSD); err != nil {
			results[i] = ThreadResolution{Index: t.Index, Error: err}
			continue
		}
		wg.Add(1)
		go func(idx int, tt TriagedThread) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			prompt := BuildPerThreadPrompt(tt, cfg)
			result, err := provider.Run(context.Background(), prompt, RunOpts{Model: model})
			if err != nil {
				results[idx] = ThreadResolution{Index: tt.Index, Error: err}
				return
			}
			trackUsage(tracker, result, "triage")
			res := parseThreadResolution(result.Text, tt.Index)
			res = validateResolutionReason(res, tt.Class)
			results[idx] = res
		}(i, t)
	}

	wg.Wait()
	return results
}

// parseThreadResolution parses Claude's JSON response for a single thread evaluation.
func parseThreadResolution(output string, index int) ThreadResolution {
	allMatches := jsonFenceRe.FindAllStringSubmatch(output, -1)
	for _, matches := range allMatches {
		raw := matches[1]

		var resp struct {
			Resolved bool   `json:"resolved"`
			Reason   string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			continue
		}
		return ThreadResolution{
			Index:    index,
			Resolved: resp.Resolved,
			Reason:   resp.Reason,
		}
	}

	// If parsing fails, treat as unresolved (conservative).
	return ThreadResolution{Index: index, Resolved: false}
}

// validateResolutionReason enforces that code-change-only classifications
// (no author reply) can only resolve with reason "code_change". If Claude
// returns "acknowledged"/"dismissed"/"rebutted", treat it as unresolved.
func validateResolutionReason(res ThreadResolution, class ThreadClassification) ThreadResolution {
	if res.Resolved && res.Reason != "code_change" &&
		(class == TriageCodeChanged || class == TriageCrossFileChange) {
		res.Resolved = false
		res.Reason = ""
	}
	return res
}

// LogTriage prints structured triage results to stderr.
func LogTriage(triaged []TriagedThread) {
	fmt.Fprintf(os.Stderr, "Re-evaluating %d unresolved thread(s)...\n\n", len(triaged))

	for _, t := range triaged {
		label := threadLabel(t.Thread)
		switch t.Class {
		case TriageSkip:
			fmt.Fprintf(os.Stderr, "  [skip]     %s — no code changes, no human replies\n", label)
		case TriageCodeChanged:
			fmt.Fprintf(os.Stderr, "  [evaluate] %s — code changes detected\n", label)
		case TriageHasReply:
			fmt.Fprintf(os.Stderr, "  [evaluate] %s — human reply detected\n", label)
		case TriageCodeChangedReply:
			fmt.Fprintf(os.Stderr, "  [evaluate] %s — code changes + human reply detected\n", label)
		case TriageCrossFileChange:
			fmt.Fprintf(os.Stderr, "  [evaluate] %s — cross-file changes detected\n", label)
		}
	}

	skipped := 0
	needsEval := 0
	for _, t := range triaged {
		if t.Class == TriageSkip {
			skipped++
		} else {
			needsEval++
		}
	}
	fmt.Fprintf(os.Stderr, "\nTriage result: %d skipped, %d need evaluation\n", skipped, needsEval)
}

// LogResolutions prints structured evaluation results to stderr.
func LogResolutions(triaged []TriagedThread, resolutions []ThreadResolution) {
	fmt.Fprintf(os.Stderr, "\n")
	for i, r := range resolutions {
		if triaged[i].Class == TriageSkip {
			continue
		}
		label := threadLabel(triaged[i].Thread)
		if r.Error != nil {
			if isBudgetError(r.Error) {
				fmt.Fprintf(os.Stderr, "  [skip]     %s — %v\n", label, r.Error)
			} else {
				fmt.Fprintf(os.Stderr, "  [error]    %s — evaluation failed: %v\n", label, r.Error)
			}
		} else if r.Resolved {
			switch r.Reason {
			case "code_change":
				fmt.Fprintf(os.Stderr, "  [resolved] %s — fixed by code change\n", label)
			case "dismissed":
				fmt.Fprintf(os.Stderr, "  [ack]      %s — dismissed by author (keeping open)\n", label)
			case "acknowledged":
				fmt.Fprintf(os.Stderr, "  [ack]      %s — acknowledged by author (keeping open)\n", label)
			case "rebutted":
				fmt.Fprintf(os.Stderr, "  [ack]      %s — rebutted by author (keeping open)\n", label)
			default:
				fmt.Fprintf(os.Stderr, "  [resolved] %s — resolved\n", label)
			}
		} else {
			fmt.Fprintf(os.Stderr, "  [open]     %s — not resolved\n", label)
		}
	}
}

// countNonSkipped returns the number of triaged threads that need evaluation.
func countNonSkipped(triaged []TriagedThread) int {
	n := 0
	for _, t := range triaged {
		if t.Class != TriageSkip {
			n++
		}
	}
	return n
}

// toFixedThreads converts thread resolutions to the fixedThread type used by downstream code.
func toFixedThreads(resolutions []ThreadResolution) []fixedThread {
	var result []fixedThread
	for _, r := range resolutions {
		if r.Resolved {
			result = append(result, fixedThread{Index: r.Index, Reason: r.Reason})
		}
	}
	return result
}
