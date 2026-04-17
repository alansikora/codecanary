package review

import (
	"fmt"
	"os"
	"strings"
)

// allResolved checks if all review threads have been resolved by code changes.
// Threads resolved by other reasons (dismissed, acknowledged, rebutted) are kept
// open and do not count as resolved.
func allResolved(threads []ReviewThread, fixed []fixedThread) bool {
	fixedSet := make(map[int]bool, len(fixed))
	for _, f := range fixed {
		if isTrueResolution(f.Reason) {
			fixedSet[f.Index] = true
		}
	}
	for i := range threads {
		if !fixedSet[i] {
			return false
		}
	}
	return true
}

// resolvedFindingIDs builds the set of finding IDs that are resolved.
// It combines threads already resolved on GitHub with threads just fixed by code changes.
// Threads resolved by other reasons (dismissed, acknowledged, rebutted) are not included
// since they are kept open for re-triage.
func resolvedFindingIDs(allThreads, unresolved []ReviewThread, fixed []fixedThread) map[string]bool {
	resolved := make(map[string]bool)
	for _, t := range allThreads {
		if t.Resolved {
			if id := FindingIDFromThread(t.Body); id != "" {
				resolved[id] = true
			}
		}
	}
	fixedSet := make(map[int]bool, len(fixed))
	for _, f := range fixed {
		if isTrueResolution(f.Reason) {
			fixedSet[f.Index] = true
		}
	}
	for i, t := range unresolved {
		if fixedSet[i] {
			if id := FindingIDFromThread(t.Body); id != "" {
				resolved[id] = true
			}
		}
	}
	return resolved
}

// minimizeFullyResolvedReviews minimizes reviews whose findings are all resolved.
func minimizeFullyResolvedReviews(repo string, prNumber int, resolvedIDs map[string]bool) {
	reviews, err := FindReviews(repo, prNumber)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not fetch reviews for minimization: %v\n", err)
		return
	}
	minimized := 0
	for _, rev := range reviews {
		allResolved := len(rev.FindingIDs) > 0
		for _, fid := range rev.FindingIDs {
			if !resolvedIDs[fid] {
				allResolved = false
				break
			}
		}
		if !allResolved {
			continue
		}
		if err := MinimizeComment(rev.NodeID); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not minimize review: %v\n", err)
		} else {
			minimized++
		}
	}
	if minimized > 0 {
		fmt.Fprintf(os.Stderr, "Minimized %d previous review(s)\n", minimized)
	}
}

// acknowledgmentMessage returns a reply body for non-code-change resolutions.
// Each message includes a hidden HTML marker for dedup detection.
func acknowledgmentMessage(reason string) string {
	marker := fmt.Sprintf("%s%s -->", ackMarkerPrefix, reason)
	switch reason {
	case "dismissed":
		return marker + "\nAuthor dismissed this finding. Keeping open \u2014 will re-check if related code changes."
	case "acknowledged":
		return marker + "\nAuthor acknowledged this finding. Keeping open \u2014 will re-check on future pushes."
	case "rebutted":
		return marker + "\nAuthor provided a technical rebuttal. Keeping open \u2014 will re-check if related code changes."
	default:
		return fmt.Sprintf("%sunknown -->", ackMarkerPrefix) + "\nFinding acknowledged. Keeping open \u2014 will re-check on future pushes."
	}
}

// hasAcknowledgmentReply checks if an acknowledgment reply already exists
// on the thread for the given reason, to avoid posting duplicate replies.
func hasAcknowledgmentReply(t ReviewThread, reason string) bool {
	newMarker := fmt.Sprintf("%s%s -->", ackMarkerPrefix, reason)
	oldMarker := fmt.Sprintf("%s%s -->", legacyAckPrefix, reason)
	for _, r := range t.Replies {
		if strings.Contains(r.Body, newMarker) || strings.Contains(r.Body, oldMarker) {
			return true
		}
	}
	return false
}

// GithubPlatform implements ReviewPlatform for GitHub Actions / PR mode.
type GithubPlatform struct {
	Repo         string
	PRNumber     int
	Post         bool   // whether to post review comments to GitHub
	DryRun       bool
	OutputFormat string // user-requested output format (may be empty)
}

func (g *GithubPlatform) LoadPreviousFindings() ([]ReviewThread, string, int) {
	allThreads, err := FetchReviewThreads(g.Repo, g.PRNumber)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not fetch review threads: %v\n", err)
		return nil, "", 0
	}
	startIndex := len(allThreads)
	var unresolved []ReviewThread
	for _, t := range allThreads {
		if !t.Resolved {
			unresolved = append(unresolved, t)
		}
	}
	previousSHA := FetchPreviousReviewSHA(g.Repo, g.PRNumber)
	return unresolved, previousSHA, startIndex
}

func (g *GithubPlatform) ExcludedAuthor(threads []ReviewThread) string {
	if len(threads) > 0 {
		if login := threads[0].Author; login != "" {
			return login
		}
		fmt.Fprintf(os.Stderr, "Warning: could not determine bot login from thread author\n")
	}
	return ""
}

func (g *GithubPlatform) HandleResolutions(threads []ReviewThread, fixed []fixedThread) {
	for _, f := range fixed {
		if f.Index < 0 || f.Index >= len(threads) {
			continue
		}
		t := threads[f.Index]
		label := threadLabel(t)
		if isTrueResolution(f.Reason) {
			// Post an explanatory comment for file_removed before resolving.
			if f.Reason == "file_removed" {
				msg := "File removed from PR — resolving."
				if err := ReplyToThread(t.ID, msg); err != nil {
					fmt.Fprintf(os.Stderr, "  ! %s — failed to post file-removed comment: %v\n", label, err)
				}
			}
			if err := ResolveThread(t.ID); err != nil {
				if strings.Contains(err.Error(), "Resource not accessible") {
					fmt.Fprintf(os.Stderr, "  ~ %s (auto-resolve unavailable: token lacks permission)\n", label)
				} else {
					fmt.Fprintf(os.Stderr, "  ! %s — resolved, but failed to update thread: %v\n", label, err)
				}
			}
		} else {
			if !hasAcknowledgmentReply(t, f.Reason) {
				msg := acknowledgmentMessage(f.Reason)
				if err := ReplyToThread(t.ID, msg); err != nil {
					fmt.Fprintf(os.Stderr, "  ! %s — failed to post acknowledgment: %v\n", label, err)
				}
			}
		}
	}
}

func (g *GithubPlatform) Publish(result *ReviewResult, pr *PRData, threads []ReviewThread, fixed []fixedThread) error {
	// Print output to stdout when not posting to GitHub.
	outputFormat := resolveOutputFormat(g.OutputFormat)
	if !g.Post {
		formatted, err := formatResult(result, outputFormat)
		if err != nil {
			return err
		}
		fmt.Print(formatted)
		// Usage table is printed by ReportUsage for terminal output.
		return nil
	}

	summary := computeReviewSummary(threads, fixed, result.Findings)

	// Decide edit-vs-post: if the latest CodeCanary review on the PR carries
	// the current HEAD SHA in its marker, this is either a reply-only run or
	// a duplicate synchronize webhook — refresh that review in place rather
	// than stacking another top-level comment.
	latest, latestErr := FetchLatestCodecanaryReview(g.Repo, g.PRNumber)
	if latestErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not fetch latest review for dedup: %v\n", latestErr)
	}
	if latest != nil && result.SHA != "" && latest.SHA == result.SHA {
		updated := replaceSummaryBlock(latest.Body, summary)
		if updated == latest.Body {
			Stderrf(ansiGreen, "Latest review already current for %s — no update needed\n", shortSHA(result.SHA))
			return nil
		}
		if err := UpdateReviewBody(g.Repo, g.PRNumber, latest.ID, updated); err != nil {
			return fmt.Errorf("updating review body: %w", err)
		}
		Stderrf(ansiGreen, "Updated latest review on PR #%d\n", g.PRNumber)
		return nil
	}

	// Minimize previous reviews before posting a fresh one.
	minimizeFailed := false
	if len(threads) > 0 {
		if nodeIDs, err := FindReviewNodeIDs(g.Repo, g.PRNumber); err == nil {
			if allResolved(threads, fixed) {
				for _, nodeID := range nodeIDs {
					if err := MinimizeComment(nodeID); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: could not minimize review: %v\n", err)
						minimizeFailed = true
					}
				}
				if len(nodeIDs) > 0 && !minimizeFailed {
					fmt.Fprintf(os.Stderr, "Minimized %d previous review(s)\n", len(nodeIDs))
				}
			} else {
				allThreads, err := FetchReviewThreads(g.Repo, g.PRNumber)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not fetch review threads for minimization: %v\n", err)
				} else {
					resolvedIDs := resolvedFindingIDs(allThreads, threads, fixed)
					minimizeFullyResolvedReviews(g.Repo, g.PRNumber, resolvedIDs)
				}
			}
		} else {
			fmt.Fprintf(os.Stderr, "Warning: could not fetch reviews for minimization: %v\n", err)
			minimizeFailed = true
		}
	}

	// POST path — pick the body shape that fits the cycle outcome. Every
	// branch emits a top-level review so each push lands a visible status
	// comment on the PR.
	switch {
	case len(result.Findings) > 0:
		if err := PostReview(g.Repo, g.PRNumber, result, pr.ValidationDiff(), result.SHA, summary); err != nil {
			return fmt.Errorf("posting review: %w", err)
		}
		Stderrf(ansiGreen, "Review posted to PR #%d\n", g.PRNumber)
	case len(threads) > 0 && allResolved(threads, fixed):
		if err := PostAllClearReview(g.Repo, g.PRNumber, result.SHA, minimizeFailed, summary); err != nil {
			return fmt.Errorf("posting all-clear review: %w", err)
		}
		Stderrf(ansiGreen, "All clear! No issues remaining.\n")
	case len(threads) > 0:
		if err := PostActivityReview(g.Repo, g.PRNumber, result.SHA, summary); err != nil {
			return fmt.Errorf("posting activity review: %w", err)
		}
		Stderrf(ansiGreen, "Posted activity summary to PR #%d\n", g.PRNumber)
	default:
		if err := PostCleanReview(g.Repo, g.PRNumber, result.SHA, summary); err != nil {
			return fmt.Errorf("posting review: %w", err)
		}
		Stderrf(ansiGreen, "Review posted to PR #%d\n", g.PRNumber)
	}

	return nil
}

func (g *GithubPlatform) SaveState(result *ReviewResult, stillOpen []Finding, isIncremental bool) error {
	// In CI mode (Post=true), don't save local state.
	// In LocalDetect mode (Post=false), save for future incremental reviews.
	if g.Post || g.DryRun {
		return nil
	}

	branch, err := currentBranch()
	if err != nil {
		return nil // non-fatal
	}

	allFindings := combineFindings(stillOpen, result.Findings)

	if err := SaveLocalState(branch, &LocalState{
		SHA:      result.SHA,
		Branch:   branch,
		Findings: allFindings,
	}); err != nil {
		Stderrf(ansiYellow, "Warning: could not save local state: %v\n", err)
	}
	return nil
}

func (g *GithubPlatform) GetIncrementalDiff(baseSHA string, prFiles []string) (string, error) {
	diff, err := GetIncrementalDiff(baseSHA)
	if err != nil {
		return "", err
	}

	// In CI mode, only committed changes matter.
	if g.Post {
		return diff, nil
	}

	// Local-detect mode: also include uncommitted changes scoped to PR files.
	return appendWorkingTreeDiff(diff, prFiles)
}

func (g *GithubPlatform) ReportUsage(tracker *UsageTracker) {
	report := tracker.Report(g.Repo, g.PRNumber)
	if len(report.Calls) > 0 {
		if err := WriteUsageEnv(report); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not write usage env: %v\n", err)
		}
	}

	// Also print usage table when output goes to terminal (local-detect mode).
	if !g.Post {
		outputFormat := resolveOutputFormat(g.OutputFormat)
		if outputFormat == "terminal" {
			fmt.Fprint(os.Stderr, FormatUsageTable(tracker, colorsEnabled()))
		}
	}
}
