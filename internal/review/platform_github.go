package review

import (
	"fmt"
	"os"
	"strings"
)

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
		if f.Reason == "code_change" {
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
		if outputFormat == "terminal" {
			// Usage table is printed by ReportUsage.
		}
		return nil
	}

	// Minimize previous reviews before posting.
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

	// Post one of: new findings, all-clear, clean review, or nothing.
	if len(result.Findings) > 0 {
		if err := PostReview(g.Repo, g.PRNumber, result, pr.Diff, result.SHA); err != nil {
			return fmt.Errorf("posting review: %w", err)
		}
		Stderrf(ansiGreen, "Review posted to PR #%d\n", g.PRNumber)
	} else if len(threads) > 0 && allResolved(threads, fixed) {
		if err := PostAllClearReview(g.Repo, g.PRNumber, minimizeFailed); err != nil {
			return fmt.Errorf("posting all-clear review: %w", err)
		}
		Stderrf(ansiGreen, "All clear! No issues remaining.\n")
	} else if len(threads) > 0 {
		codeFixedSet := make(map[int]bool, len(fixed))
		for _, f := range fixed {
			if f.Index >= 0 && f.Index < len(threads) && f.Reason == "code_change" {
				codeFixedSet[f.Index] = true
			}
		}
		unresolvedCount := len(threads) - len(codeFixedSet)
		fmt.Fprintf(os.Stderr, "No new findings. %d previous thread(s) still unresolved.\n", unresolvedCount)
	} else {
		if err := PostCleanReview(g.Repo, g.PRNumber); err != nil {
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

	// Strip "still open" status before saving.
	var surviving []Finding
	for _, f := range stillOpen {
		sf := f
		sf.Status = ""
		surviving = append(surviving, sf)
	}
	allFindings := mergeFindings(surviving, result.Findings)

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
	wtDiff, err := workingTreeDiff(prFiles)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not compute working-tree diff: %v\n", err)
		return diff, nil
	}
	if wtDiff == "" {
		return diff, nil
	}

	if diff == "" {
		return wtDiff, nil
	}
	return diff + "\n" + wtDiff, nil
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
			fmt.Fprint(os.Stderr, FormatUsageTable(tracker.Calls(), colorsEnabled()))
		}
	}
}
