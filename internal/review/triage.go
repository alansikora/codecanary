package review

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// ThreadClassification is the triage result for a single unresolved thread.
type ThreadClassification int

const (
	TriageSkip              ThreadClassification = iota // no code changes at all
	TriageCodeChanged                                   // diff touches finding location (outdated)
	TriageHasReply                                      // thread has human replies
	TriageCodeChangedReply                              // both code changed AND has replies
	TriageCrossFileChange                               // diff has changes but NOT in this thread's file
	TriageFileRemovedFromPR                             // file no longer in the PR
	TriagePreviouslyAcked                               // bot already ack'd a deferral; no new human reply since
	TriagePreviouslyFixed                               // an earlier run judged it fixed by a code change; resolve it
)

// TriagedThread pairs a ReviewThread with its classification and context.
type TriagedThread struct {
	Thread         ReviewThread
	Index          int // original index in the unresolved slice
	Class          ThreadClassification
	FileDiff       string    // file-scoped diff (level 1) or full diff (cross-file)
	FullDiff       string    // full PR diff for widened-scope fallback; empty when not applicable
	FileSnippet    string    // windowed file content around finding + diff hunks
	BotLogin       string    // login of the review bot, for filtering replies
	PriorAckReason string    // for TriagePreviouslyAcked: the previously-recorded reason (acknowledged/rebutted/dismissed)
	Intent         *PRIntent // for an intent-* finding under intent review: the PR's intent as it reads now (see applyIntent)
}

// ThreadResolution is the result of a per-thread Claude evaluation.
type ThreadResolution struct {
	Index     int
	Resolved  bool
	Reason    string // "code_change", "acknowledged", "rebutted", "dismissed"
	Rationale string // one-sentence narrative from the evaluator, surfaced in the incremental review to prevent ping-ponging
	Error     error
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

// lineRange represents an inclusive range of 1-based line numbers.
type lineRange struct{ start, end int }

// parseHunkNewRanges extracts the new-file line ranges from unified diff hunk headers.
// Each @@ -X,Y +N,M @@ header yields a range [N, N+M-1].
func parseHunkNewRanges(diffText string) []lineRange {
	var ranges []lineRange
	for _, line := range strings.Split(diffText, "\n") {
		if !strings.HasPrefix(line, "@@ ") {
			continue
		}
		// Find +N or +N,M in the hunk header.
		idx := strings.Index(line, "+")
		if idx < 0 {
			continue
		}
		rest := line[idx+1:]
		// Trim everything after the space/comma/@@ that ends the range spec.
		if sp := strings.IndexAny(rest, " @"); sp >= 0 {
			rest = rest[:sp]
		}
		parts := strings.SplitN(rest, ",", 2)
		start, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		count := 1
		if len(parts) == 2 {
			if c, err := strconv.Atoi(parts[1]); err == nil {
				if c == 0 {
					continue // pure deletion hunk — no new lines
				}
				if c > 0 {
					count = c
				}
			}
		}
		ranges = append(ranges, lineRange{start: start, end: start + count - 1})
	}
	return ranges
}

// mergeRanges merges overlapping or adjacent line ranges, sorted by start.
func mergeRanges(ranges []lineRange) []lineRange {
	if len(ranges) == 0 {
		return nil
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
	merged := []lineRange{ranges[0]}
	for _, r := range ranges[1:] {
		last := &merged[len(merged)-1]
		if r.start <= last.end+1 {
			if r.end > last.end {
				last.end = r.end
			}
		} else {
			merged = append(merged, r)
		}
	}
	return merged
}

// ExtractFileSnippet extracts a windowed snippet from file content centered around
// the finding line and expanded to cover diff hunk ranges. Returns an empty string
// if content is empty. findingLine is 1-based. diffText is the file-scoped diff
// (used to parse hunk ranges; may be empty for cross-file cases). maxLines caps the
// total snippet length.
func ExtractFileSnippet(content string, findingLine int, diffText string, maxLines int) string {
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	// Build interesting ranges: finding line ± 50, each hunk range ± 30.
	const findingPad = 50
	const hunkPad = 30

	var ranges []lineRange
	if findingLine > 0 {
		ranges = append(ranges, lineRange{
			start: max(1, findingLine-findingPad),
			end:   min(totalLines, findingLine+findingPad),
		})
	}
	for _, hr := range parseHunkNewRanges(diffText) {
		ranges = append(ranges, lineRange{
			start: max(1, hr.start-hunkPad),
			end:   min(totalLines, hr.end+hunkPad),
		})
	}
	if len(ranges) == 0 {
		// Fallback: center on line 1 if nothing else.
		ranges = append(ranges, lineRange{start: 1, end: min(totalLines, maxLines)})
	}

	merged := mergeRanges(ranges)

	// If total exceeds maxLines, prioritize the range containing the finding line,
	// then include other ranges in order until the budget is exhausted.
	total := 0
	for _, r := range merged {
		total += r.end - r.start + 1
	}
	if total > maxLines {
		// Find which merged range contains the finding line.
		// When findingLine is invalid (<= 0), default to the first range.
		findingIdx := 0
		if findingLine > 0 {
			for i, r := range merged {
				if findingLine >= r.start && findingLine <= r.end {
					findingIdx = i
					break
				}
			}
		}
		// Start with the finding range, then add others.
		budget := maxLines
		kept := make([]bool, len(merged))
		kept[findingIdx] = true
		size := merged[findingIdx].end - merged[findingIdx].start + 1
		if size > budget && findingLine > 0 {
			// Truncate the finding range around the finding line.
			half := budget / 2
			merged[findingIdx] = lineRange{
				start: max(1, findingLine-half),
				end:   min(totalLines, findingLine+half),
			}
			size = merged[findingIdx].end - merged[findingIdx].start + 1
		} else if size > budget {
			// No valid finding line — just take the first maxLines of the range.
			merged[findingIdx] = lineRange{
				start: merged[findingIdx].start,
				end:   min(totalLines, merged[findingIdx].start+budget-1),
			}
			size = merged[findingIdx].end - merged[findingIdx].start + 1
		}
		budget -= size
		for i, r := range merged {
			if kept[i] {
				continue
			}
			rSize := r.end - r.start + 1
			if rSize <= budget {
				kept[i] = true
				budget -= rSize
			}
		}
		var trimmed []lineRange
		for i, r := range merged {
			if kept[i] {
				trimmed = append(trimmed, r)
			}
		}
		merged = trimmed
	}

	// Format with line numbers, inserting omission markers between gaps.
	var b strings.Builder
	for i, r := range merged {
		if i > 0 {
			gap := r.start - merged[i-1].end - 1
			fmt.Fprintf(&b, "... (%d lines omitted) ...\n", gap)
		}
		for ln := r.start; ln <= r.end && ln <= totalLines; ln++ {
			fmt.Fprintf(&b, "%d: %s\n", ln, lines[ln-1])
		}
	}
	return b.String()
}

// ClassifyThreads triages unresolved threads using GitHub's outdated flag and reply presence.
//
// Two diffs serve different purposes:
//   - activityDiff: the incremental diff (changes since last review). Used to decide whether
//     to skip evaluation — when empty, non-outdated threads with no replies are TriageSkip.
//   - contextDiff: the full PR diff (all changes). Used to determine the correct classification
//     (TriageCodeChanged vs TriageCrossFileChange) and to extract FileDiff/FileSnippet for the
//     evaluation prompt. This ensures fixes from earlier pushes are visible to the evaluator.
//
// prFiles is the current set of files in the PR; threads on files no longer in the PR
// are classified as TriageFileRemovedFromPR and auto-resolved without an LLM call.
// fileContents provides current file contents for building context snippets in triage prompts.
func ClassifyThreads(threads []ReviewThread, activityDiff, contextDiff, botLogin string, prFiles []string, fileContents map[string]string) []TriagedThread {
	prFileSet := make(map[string]bool, len(prFiles))
	for _, f := range prFiles {
		prFileSet[f] = true
	}

	result := make([]TriagedThread, len(threads))

	for i, t := range threads {
		// File no longer in the PR — auto-resolve without LLM.
		// When prFiles is empty (e.g. upstream fetch returned no file list),
		// we skip this check to avoid incorrectly resolving all threads.
		if len(prFileSet) > 0 && !prFileSet[t.Path] {
			result[i] = TriagedThread{
				Thread: t,
				Index:  i,
				Class:  TriageFileRemovedFromPR,
			}
			continue
		}

		// An earlier run judged the finding fixed by a code change but
		// could not resolve the thread (the action's resolve step failed).
		// Its verdict stands: resolve it now instead of judging it again,
		// which on the same head would skip it (no new changes) and leave it
		// blocking, or, after a reply, judge only the reply.
		if hasFixedReply(t, botLogin) {
			result[i] = TriagedThread{Thread: t, Index: i, Class: TriagePreviouslyFixed, BotLogin: botLogin}
			continue
		}

		hasReply := hasNewHumanReply(t, botLogin)
		outdated := t.Outdated
		deleted := fileDeletedInDiff(contextDiff, t.Path)

		// Sticky-ack: when the bot already recorded a deferral and the
		// author has not added a fresh reply since, preserve the prior
		// classification instead of rerunning triage. This prevents the
		// "Acknowledged by author: 2" → "Still unresolved: 2" regression
		// that happened on subsequent pushes when no new signal was present.
		var priorAck string
		if !hasReply {
			priorAck = parsePriorAckReason(t, botLogin)
		}

		var class ThreadClassification
		// replyCrossFile: the author replied and pushed, but not to this
		// file. The evaluator needs the whole PR diff to see the fix.
		replyCrossFile := false
		switch {
		case priorAck != "":
			class = TriagePreviouslyAcked
		case deleted:
			// File was deleted — evaluate with full diff so Claude can check
			// whether the code moved to a replacement file with the fix applied.
			class = TriageCrossFileChange
		case outdated && hasReply:
			class = TriageCodeChangedReply
		case outdated:
			class = TriageCodeChanged
		case hasReply && activityDiff != "":
			// The author replied and code changed since the last review. A
			// reply saying "fixed in <commit>" is then checkable against the
			// diff, even when the fix is in another file than the finding;
			// judged as reply-only, a real fix could never resolve it.
			class = TriageCodeChangedReply
			replyCrossFile = !fileInDiff(activityDiff, t.Path)
		case hasReply:
			class = TriageHasReply
		default:
			// No GitHub outdated flag, no replies. Use the incremental diff to
			// decide whether there is new activity worth evaluating.
			if activityDiff == "" {
				// No changes since last review — nothing new to evaluate.
				class = TriageSkip
			} else if fileInDiff(contextDiff, t.Path) {
				// File was changed in the PR — classify as code-changed so the
				// evaluator sees the file-scoped diff (which may include fixes
				// from earlier pushes that the incremental diff missed).
				class = TriageCodeChanged
			} else {
				// Code changed in other files — evaluate in case the fix is cross-file.
				class = TriageCrossFileChange
			}
		}

		// Use file-scoped diff for same-file evaluations to reduce noise.
		// The full PR diff can drown out the relevant fix with changes from
		// unrelated files, causing false "not resolved" verdicts. Cross-file
		// evaluations keep the full diff since the fix is in a different file.
		var fileDiff, fullDiff, fileSnippet string
		switch {
		case replyCrossFile, class == TriageCrossFileChange:
			fileDiff = contextDiff
		case class == TriageCodeChanged, class == TriageCodeChangedReply:
			fileDiff, fullDiff, fileSnippet = codeChangeContext(t, contextDiff, fileContents)
		}

		if content, ok := fileContents[t.Path]; ok {
			switch {
			case replyCrossFile, class == TriageCrossFileChange:
				// Show finding's file context even though the diff is in other files.
				fileSnippet = ExtractFileSnippet(content, t.Line, "", 200)
			case class == TriageHasReply:
				// Show current file state so the LLM can judge whether the author's
				// rebuttal is technically accurate given the code as it stands.
				fileSnippet = ExtractFileSnippet(content, t.Line, "", 200)
			}
		}

		result[i] = TriagedThread{
			Thread:         t,
			Index:          i,
			Class:          class,
			FileDiff:       fileDiff,
			FullDiff:       fullDiff,
			FileSnippet:    fileSnippet,
			BotLogin:       botLogin,
			PriorAckReason: priorAck,
		}
	}

	return result
}

// codeChangeContext is what a code-change evaluation of t sees: the
// file-scoped diff (the full diff is kept for the widened-scope fallback),
// and a windowed snippet of the file as it is now. The snippet's range comes
// from the file-scoped diff, so hunk line numbers from other files don't pull
// in wrong sections.
func codeChangeContext(t ReviewThread, contextDiff string, fileContents map[string]string) (fileDiff, fullDiff, snippet string) {
	fileDiff = ExtractFileDiff(contextDiff, t.Path)
	if content, ok := fileContents[t.Path]; ok {
		snippet = ExtractFileSnippet(content, t.Line, fileDiff, 300)
	}
	return fileDiff, contextDiff, snippet
}

// applyIntent gives each intent-* thread the PR's intent as it reads now, so
// its evaluation sees it. When the intent changed since the finding was
// raised (its IntentHash differs), the thread is re-checked even if no code
// near it changed or it has only a reply: editing the PR body can settle it,
// like a code change. intent is nil when intent review is off.
func applyIntent(triaged []TriagedThread, intent *PRIntent, contextDiff string, fileContents map[string]string) {
	if intent == nil {
		return
	}
	hash := intent.Hash()
	for i := range triaged {
		t := &triaged[i]
		f := FindingFromThread(t.Thread)
		if !isIntentFinding(f.ID) {
			continue
		}
		t.Intent = intent
		if f.IntentHash == hash {
			continue
		}
		switch t.Class {
		case TriageSkip:
			t.Class = TriageCodeChanged
		case TriageHasReply:
			t.Class = TriageCodeChangedReply
		default:
			continue
		}
		t.FileDiff, t.FullDiff, t.FileSnippet = codeChangeContext(t.Thread, contextDiff, fileContents)
	}
}

// hasFixedReply reports whether the bot replied on t with fixedMarker. Only
// the bot's own replies count: anyone else could type the marker.
func hasFixedReply(t ReviewThread, botLogin string) bool {
	if botLogin == "" {
		return false
	}
	for _, r := range t.Replies {
		if r.Author == botLogin && strings.Contains(r.Body, fixedMarker) {
			return true
		}
	}
	return false
}

// parsePriorAckReason returns the reason recorded in the most recent
// codecanary ack reply on the thread (acknowledged/rebutted/dismissed),
// or "" if no ack reply is present. Handles both the current marker
// (<!-- codecanary:ack:<reason> -->) and the legacy clanopy form.
func parsePriorAckReason(t ReviewThread, botLogin string) string {
	var latest string
	for _, r := range t.Replies {
		if r.Author != botLogin {
			continue
		}
		if reason := extractAckReason(r.Body); reason != "" {
			latest = reason
		}
	}
	return latest
}

// extractAckReason pulls the reason value out of a codecanary ack marker.
// Returns "" when the body has no marker. Unknown reasons are returned
// as-is so callers can decide how to handle them; in practice the bot
// only writes "acknowledged", "rebutted", "dismissed", or "unknown".
func extractAckReason(body string) string {
	for _, prefix := range []string{ackMarkerPrefix, legacyAckPrefix} {
		idx := strings.Index(body, prefix)
		if idx < 0 {
			continue
		}
		rest := body[idx+len(prefix):]
		end := strings.Index(rest, " -->")
		if end < 0 {
			end = strings.Index(rest, "-->")
			if end < 0 {
				continue
			}
		}
		return strings.TrimSpace(rest[:end])
	}
	return ""
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

// isAuthorReply reports whether a reply answers the finding on the author's
// behalf: one from anyone but the bot, or a refutation the CI fix posted as
// the bot (isFixReply). The bot's own acknowledgments are not.
func isAuthorReply(r ThreadReply, botLogin string) bool {
	return r.Author != botLogin || isFixReply(r.Body)
}

// hasHumanReply checks if a thread has at least one author reply.
func hasHumanReply(t ReviewThread, botLogin string) bool {
	for _, r := range t.Replies {
		if isAuthorReply(r, botLogin) {
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
		if r.Author == botLogin && (isAckReply(r.Body) || strings.Contains(r.Body, rejectedReplyMarker)) {
			lastAckIdx = i
		}
	}
	if lastAckIdx == -1 {
		// No ack reply exists — fall back to standard check.
		return hasHumanReply(t, botLogin)
	}
	// Check for human replies after the last ack.
	for _, r := range t.Replies[lastAckIdx+1:] {
		if isAuthorReply(r, botLogin) {
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

	writeFinding(&b, t)

	writeFileSnippet(&b, t.FileSnippet)

	b.WriteString("## Code Changes\n```diff\n")
	b.WriteString(t.FileDiff)
	b.WriteString("\n```\n\n")

	if ctx := evalContext(cfg, "code_change"); ctx != "" {
		fmt.Fprintf(&b, "## Additional Context\n%s\n\n", ctx)
	}

	b.WriteString("## Task\n")
	b.WriteString("Determine whether the issue you raised has been resolved.\n\n")
	b.WriteString("**Start with the current file content** (if provided). Read the code around the finding location and determine whether the issue still exists. If the problematic code has been fixed, removed, or restructured so the finding no longer applies, the answer is YES — regardless of which specific diff line produced the fix.\n\n")
	b.WriteString("If file content is not available, examine the diff for evidence that the issue was addressed.\n")
	b.WriteString("- A change that fixes the root cause, removes the problematic code, or meaningfully changes the code so the finding no longer applies → YES.\n")
	b.WriteString("- A change to nearby or adjacent code counts IF it effectively resolves the concern (e.g. fixing the logic, adding the missing check, refactoring the problematic pattern).\n")
	b.WriteString("- A structural change also counts — for example, if code was moved before a guard condition, control flow was reordered, or the code was refactored so the finding no longer applies.\n")
	b.WriteString("- Answer NO only if the concern is still present in the current code (when file content is provided) or if the diff does not address the finding.\n\n")
	writeCodeChangeResolutionFormat(&b)

	return b.String()
}

func buildReplyPrompt(t TriagedThread, cfg *ReviewConfig) string {
	var b strings.Builder

	b.WriteString("You are a code reviewer. You previously raised a finding on a pull request. The author replied.\n\n")

	writeFinding(&b, t)
	writeReplies(&b, t.Thread, t.BotLogin)
	writeFileSnippet(&b, t.FileSnippet)

	if ctx := evalContext(cfg, "reply"); ctx != "" {
		fmt.Fprintf(&b, "## Additional Context\n%s\n\n", ctx)
	}

	b.WriteString("## Task\n")
	b.WriteString("Does the author's reply resolve the finding?\n")
	b.WriteString("- **Dismissed**: Reply explicitly asks the reviewer to dismiss, ignore, or skip the finding (e.g. \"dismiss this\", \"you can safely dismiss\", \"please ignore\", \"skip this one\"). The author is exercising their authority to close the thread without further justification.\n")
	b.WriteString("- **Acknowledged**: Reply indicates the finding is intentional, accepted, or tracked elsewhere (e.g. \"intentional\", \"will fix in a future PR\", \"tracked in issue #N\").\n")
	b.WriteString("- **Rebutted**: Reply provides concrete technical reasoning showing the finding is not applicable. Vague disagreement (\"I don't think so\") does NOT qualify — the reply must cite specific technical details, framework behavior, or project constraints.\n")
	b.WriteString("- **Not resolved**: Reply is a question, vague disagreement, or does not address the finding. A reply that only says the code was fixed (e.g. \"fixed\", \"done\") is also not resolved here: no code change at the finding is shown.\n\n")
	writeReplyResolutionFormat(&b, cfg != nil && cfg.ResolveAcceptedReplies)

	return b.String()
}

func buildCodeChangeReplyPrompt(t TriagedThread, cfg *ReviewConfig) string {
	var b strings.Builder

	b.WriteString("You are a code reviewer. You previously raised a finding on a pull request. The author pushed new code AND replied.\n\n")

	writeFinding(&b, t)
	writeReplies(&b, t.Thread, t.BotLogin)

	writeFileSnippet(&b, t.FileSnippet)

	b.WriteString("## Code Changes\n```diff\n")
	b.WriteString(t.FileDiff)
	b.WriteString("\n```\n\n")

	if ctx := evalContext(cfg, "code_change"); ctx != "" {
		fmt.Fprintf(&b, "## Additional Context (Code Changes)\n%s\n\n", ctx)
	}
	if ctx := evalContext(cfg, "reply"); ctx != "" {
		fmt.Fprintf(&b, "## Additional Context (Replies)\n%s\n\n", ctx)
	}

	b.WriteString("## Task\n")
	b.WriteString("Is the finding resolved? It may be resolved by the code change, the reply, or both.\n\n")
	b.WriteString("**Start with the current file content** (if provided). If the issue no longer exists in the current code — the root cause is fixed, the code was removed, or the code was restructured — it is resolved by code change regardless of which diff line produced the fix — or which file: the fix may be in a different file from the finding, as the author's reply may say. A reply claiming a fix the code and diff don't show is not a code change.\n\n")
	b.WriteString("If the code still shows the issue, evaluate the reply:\n")
	b.WriteString("- **Dismissed**: Reply explicitly asks the reviewer to dismiss, ignore, or skip the finding (e.g. \"dismiss this\", \"you can safely dismiss\", \"please ignore\", \"skip this one\"). The author is exercising their authority to close the thread without further justification.\n")
	b.WriteString("- **Acknowledged**: Reply indicates the finding is intentional, accepted, or tracked elsewhere (e.g. \"intentional\", \"will fix in a future PR\", \"tracked in issue #N\").\n")
	b.WriteString("- **Rebutted**: Reply provides concrete technical reasoning showing the finding is not applicable. Vague disagreement (\"I don't think so\") does NOT qualify — the reply must cite specific technical details, framework behavior, or project constraints.\n")
	b.WriteString("- **Not resolved**: The issue is still in the code, and the reply is a question, vague disagreement, or does not address the finding.\n\n")
	writeResolutionFormat(&b, cfg != nil && cfg.ResolveAcceptedReplies)

	return b.String()
}

func buildCrossFilePrompt(t TriagedThread, cfg *ReviewConfig) string {
	var b strings.Builder

	b.WriteString("You are a code reviewer. You previously raised a finding on a pull request. The author pushed new code, but the changes are in DIFFERENT files from where you left your finding.\n\n")

	writeFinding(&b, t)

	writeFileSnippet(&b, t.FileSnippet)

	b.WriteString("## All Code Changes\n```diff\n")
	b.WriteString(t.FileDiff)
	b.WriteString("\n```\n\n")

	if ctx := evalContext(cfg, "code_change"); ctx != "" {
		fmt.Fprintf(&b, "## Additional Context\n%s\n\n", ctx)
	}

	b.WriteString("## Task\n")
	b.WriteString("Determine whether the issue you raised has been resolved, even though the changes are in different files from where you left your finding.\n")
	b.WriteString("**Start with the current file content** (if provided). If the issue no longer exists in the current code, the answer is YES.\n\n")
	b.WriteString("Otherwise, examine the code changes for evidence of a cross-file fix.\n")
	writeCrossFileCriteria(&b)
	b.WriteString("- Answer NO if none of the changes in this diff are related to the finding, or if file context is provided and the concern is still present in the current code.\n\n")
	writeCodeChangeResolutionFormat(&b)

	return b.String()
}

// buildWidenedScopePrompt is the level-2 fallback prompt used when the file-scoped
// evaluation (level 1) found no fix. It sends the full PR diff so the evaluator can
// detect cross-file fixes that the narrower scope missed.
func buildWidenedScopePrompt(t TriagedThread, cfg *ReviewConfig) string {
	var b strings.Builder

	b.WriteString("You are a code reviewer. You previously raised a finding on a pull request. A focused check of the finding's file did not find a fix. Now examine ALL code changes across the PR — the fix may be in a different file.\n\n")

	writeFinding(&b, t)

	writeFileSnippet(&b, t.FileSnippet)

	b.WriteString("## All Code Changes (full PR)\n```diff\n")
	b.WriteString(t.FullDiff)
	b.WriteString("\n```\n\n")

	if ctx := evalContext(cfg, "code_change"); ctx != "" {
		fmt.Fprintf(&b, "## Additional Context\n%s\n\n", ctx)
	}

	b.WriteString("## Task\n")
	b.WriteString("The finding's file was already checked and the issue appears unresolved there. Examine the full PR diff to determine if a change in another file resolves the concern.\n\n")
	writeCrossFileCriteria(&b)
	b.WriteString("- Answer NO if none of the changes in the diff are related to the finding.\n\n")
	writeCodeChangeResolutionFormat(&b)

	return b.String()
}

// writeCrossFileCriteria writes the shared YES-criteria bullets for cross-file evaluations.
// Used by both buildCrossFilePrompt and buildWidenedScopePrompt.
func writeCrossFileCriteria(b *strings.Builder) {
	b.WriteString("- Answer YES if a change in another file effectively resolves the concern (e.g. fixing the caller instead of the callee, adding validation in a different layer, removing the code path that triggers the issue).\n")
	b.WriteString("- A structural change also counts — for example, if code was moved, control flow was reordered, or the code was refactored so the finding no longer applies.\n")
}

// writeFileSnippet adds the current file content section to the prompt when available.
func writeFileSnippet(b *strings.Builder, snippet string) {
	if snippet == "" {
		return
	}
	b.WriteString("## Current File Content (around finding)\n")
	b.WriteString("This shows the file as it exists NOW (after the changes). Use it to understand the final code structure and control flow.\n\n~~~\n")
	b.WriteString(snippet)
	b.WriteString("~~~\n\n")
}

// writeFinding writes the finding section to the prompt, followed by the
// PR's intent for an intent-* finding (see applyIntent).
func writeFinding(b *strings.Builder, t TriagedThread) {
	b.WriteString("## Finding\n")
	fmt.Fprintf(b, "File: `%s:%d`\n", t.Thread.Path, t.Thread.Line)
	b.WriteString(t.Thread.Body)
	b.WriteString("\n\n")
	writeTriageIntent(b, t.Intent)
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
		if isAuthorReply(r, botLogin) {
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
// Used for prompts with both a code change and author replies (TriageCodeChangedReply).
// explainRejection (resolve_accepted_replies) asks for the reason a reply
// does not settle the finding; it is posted to the author once.
func writeResolutionFormat(b *strings.Builder, explainRejection bool) {
	b.WriteString("Return a JSON object inside a ```json code fence:\n")
	b.WriteString("- If resolved: `{\"resolved\": true, \"reason\": \"<reason>\", \"rationale\": \"<one sentence>\"}` — `reason` is one of `\"code_change\"`, `\"dismissed\"`, `\"acknowledged\"`, `\"rebutted\"`. `rationale` must be a single sentence naming the specific change, reply, or reasoning that resolved the finding (e.g. \"Removed the three `original_error_*` fields from the service logger\").\n")
	writeNotResolvedFormat(b, explainRejection)
}

// writeNotResolvedFormat writes the "not resolved" answer shared by the
// prompts that evaluate an author reply. With explainRejection it asks for
// the sentence posted to the author.
func writeNotResolvedFormat(b *strings.Builder, explainRejection bool) {
	if explainRejection {
		b.WriteString("- If NOT resolved: `{\"resolved\": false, \"rationale\": \"<one sentence>\"}` — `rationale` tells the author, in one sentence addressed to them, what the reply leaves unanswered. It is posted on the thread.\n")
		return
	}
	b.WriteString("- If NOT resolved: `{\"resolved\": false}`\n")
}

// writeReplyResolutionFormat writes a restricted JSON response format for
// reply-only evaluations (TriageHasReply). It leaves out code_change: the
// prompt shows no code change, and validateResolutionReason rejects that
// reason for this class. explainRejection is as in writeResolutionFormat.
func writeReplyResolutionFormat(b *strings.Builder, explainRejection bool) {
	b.WriteString("Return a JSON object inside a ```json code fence:\n")
	b.WriteString("- If resolved: `{\"resolved\": true, \"reason\": \"<reason>\", \"rationale\": \"<one sentence>\"}` — `reason` is one of `\"dismissed\"`, `\"acknowledged\"`, `\"rebutted\"`. `rationale` must be a single sentence naming the reply or reasoning that resolved the finding (e.g. \"The author says the retry limit is intentional and documented in the API spec\").\n")
	writeNotResolvedFormat(b, explainRejection)
}

// writeCodeChangeResolutionFormat writes a restricted JSON response format
// for code-change-only evaluations (no author reply). Only allows code_change
// as a resolution reason since there is no author reply to acknowledge/dismiss/rebut.
func writeCodeChangeResolutionFormat(b *strings.Builder) {
	b.WriteString("Return a JSON object inside a ```json code fence:\n")
	b.WriteString("- If resolved: `{\"resolved\": true, \"reason\": \"code_change\", \"rationale\": \"<one sentence>\"}` — `rationale` must be a single sentence naming the specific change that resolved the finding (e.g. \"Removed the three `original_error_*` fields from the service logger\").\n")
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
func EvaluateThreadsParallel(triaged []TriagedThread, provider ModelProvider, cfg *ReviewConfig, maxConcurrent int, tracker *UsageTracker, maxBudgetUSD float64) []ThreadResolution {
	results := make([]ThreadResolution, len(triaged))

	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup

	for i, t := range triaged {
		if t.Class == TriageSkip || t.Class == TriageFileRemovedFromPR || t.Class == TriagePreviouslyFixed {
			results[i] = ThreadResolution{Index: t.Index, Resolved: false}
			continue
		}
		if t.Class == TriagePreviouslyAcked {
			// Sticky: carry the prior ack reason forward without burning
			// triage tokens. computeReviewSummary will route it to the
			// matching bucket (Dismissed/Acknowledged/Rebutted), so the
			// commit status check stays green across pushes that don't
			// touch the deferred finding.
			results[i] = ThreadResolution{
				Index:    t.Index,
				Resolved: true,
				Reason:   t.PriorAckReason,
			}
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

			// Level 1: file-scoped evaluation.
			prompt := BuildPerThreadPrompt(tt, cfg)
			result, err := provider.Run(context.Background(), prompt, RunOpts{})
			if err != nil {
				results[idx] = ThreadResolution{Index: tt.Index, Error: err}
				return
			}
			trackUsage(tracker, result, "triage")
			res := parseThreadResolution(result.Text, tt.Index)
			res = validateResolutionReason(res, tt.Class)

			// Level 2: widen to full PR diff if file-scoped check was inconclusive.
			// Only for TriageCodeChanged — TriageCodeChangedReply has author replies
			// that buildWidenedScopePrompt doesn't include, so widening would drop
			// the reply context and prevent dismissed/acknowledged/rebutted resolutions.
			if !res.Resolved && tt.FullDiff != "" && tt.Class == TriageCodeChanged {
				if err := CheckBudget(tracker, maxBudgetUSD); err == nil {
					fmt.Fprintf(os.Stderr, "  [widen]    %s — checking full PR diff\n", threadLabel(tt.Thread))
					prompt2 := buildWidenedScopePrompt(tt, cfg)
					result2, err := provider.Run(context.Background(), prompt2, RunOpts{})
					if err == nil {
						trackUsage(tracker, result2, "triage")
						res2 := parseThreadResolution(result2.Text, tt.Index)
						res2 = validateResolutionReason(res2, tt.Class)
						if res2.Resolved {
							res = res2
						}
					}
				}
			}

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
			Resolved  bool   `json:"resolved"`
			Reason    string `json:"reason"`
			Rationale string `json:"rationale"`
		}
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			continue
		}
		return ThreadResolution{
			Index:     index,
			Resolved:  resp.Resolved,
			Reason:    resp.Reason,
			Rationale: strings.TrimSpace(resp.Rationale),
		}
	}

	// If parsing fails, treat as unresolved (conservative).
	return ThreadResolution{Index: index, Resolved: false}
}

// replyOnlyCodeChangeRationale replaces the evaluator's rationale when a
// reply-only thread is answered "code_change".
const replyOnlyCodeChangeRationale = "The code at this finding has not changed since the last review, so a reply saying it was fixed does not settle it."

// validateResolutionReason enforces two rules the evaluator cannot override:
//   - code-change-only classifications (no author reply) resolve only with
//     reason "code_change"; "acknowledged"/"dismissed"/"rebutted" become
//     unresolved.
//   - a reply-only classification (the code at the finding did not change,
//     and the prompt shows no diff) never resolves with "code_change",
//     whatever the reply claims.
func validateResolutionReason(res ThreadResolution, class ThreadClassification) ThreadResolution {
	if !res.Resolved {
		return res
	}
	switch {
	case (class == TriageCodeChanged || class == TriageCrossFileChange) && res.Reason != "code_change":
		res.Resolved = false
		res.Reason = ""
	case class == TriageHasReply && res.Reason == "code_change":
		res.Resolved = false
		res.Reason = ""
		// The evaluator's rationale argued for resolving. With
		// resolve_accepted_replies the rationale of a rejected reply is
		// posted to the author, so say why the reply was not accepted.
		res.Rationale = replyOnlyCodeChangeRationale
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
		case TriageFileRemovedFromPR:
			fmt.Fprintf(os.Stderr, "  [resolve]  %s — file removed from PR\n", label)
		case TriagePreviouslyAcked:
			fmt.Fprintf(os.Stderr, "  [sticky]   %s — prior ack (%s) carried forward\n", label, t.PriorAckReason)
		case TriagePreviouslyFixed:
			fmt.Fprintf(os.Stderr, "  [resolve]  %s — judged fixed by an earlier run; the thread was not resolved then\n", label)
		}
	}

	skipped := 0
	autoResolved := 0
	needsEval := 0
	for _, t := range triaged {
		switch t.Class {
		case TriageSkip:
			skipped++
		case TriageFileRemovedFromPR, TriagePreviouslyAcked, TriagePreviouslyFixed:
			autoResolved++
		default:
			needsEval++
		}
	}
	fmt.Fprintf(os.Stderr, "\nTriage result: %d skipped, %d auto-resolved, %d need evaluation\n", skipped, autoResolved, needsEval)
}

// LogResolutions prints structured evaluation results to stderr.
func LogResolutions(triaged []TriagedThread, resolutions []ThreadResolution) {
	fmt.Fprintf(os.Stderr, "\n")
	for i, r := range resolutions {
		switch triaged[i].Class {
		case TriageSkip, TriageFileRemovedFromPR, TriagePreviouslyAcked, TriagePreviouslyFixed:
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

// countNonSkipped returns the number of triaged threads that EvaluateThreadsParallel
// must process. TriagePreviouslyAcked is included even though it short-circuits
// without an LLM call: the function still has to run to emit the carried-forward
// fixedThread, which is what feeds the summary's Acknowledged/Rebutted/Dismissed
// buckets. Excluding it here was the bug behind the "Still unresolved: 2" stuck
// status on already-acked threads.
func countNonSkipped(triaged []TriagedThread) int {
	n := 0
	for _, t := range triaged {
		switch t.Class {
		case TriageSkip, TriageFileRemovedFromPR, TriagePreviouslyFixed:
			continue
		default:
			n++
		}
	}
	return n
}

// rejectedReplies returns the threads whose author reply triage evaluated
// and did not accept, with the evaluator's rationale. Evaluations that
// failed are left out: nothing was decided.
func rejectedReplies(triaged []TriagedThread, resolutions []ThreadResolution) []fixedThread {
	var out []fixedThread
	for i, r := range resolutions {
		if i >= len(triaged) || r.Resolved || r.Error != nil {
			continue
		}
		switch triaged[i].Class {
		case TriageHasReply, TriageCodeChangedReply:
			out = append(out, fixedThread{Index: r.Index, Rationale: r.Rationale})
		}
	}
	return out
}

// toFixedThreads converts thread resolutions to the fixedThread type used by downstream code.
func toFixedThreads(resolutions []ThreadResolution) []fixedThread {
	var result []fixedThread
	for _, r := range resolutions {
		if r.Resolved {
			result = append(result, fixedThread{Index: r.Index, Reason: r.Reason, Rationale: r.Rationale})
		}
	}
	return result
}
