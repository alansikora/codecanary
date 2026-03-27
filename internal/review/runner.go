package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// RunOptions configures a review run.
type RunOptions struct {
	Repo       string
	PRNumber   int
	ConfigPath string
	Output     string // "markdown" or "json"
	Post       bool
	DryRun     bool
	ReplyOnly  bool // evaluate thread replies only, skip new findings
}

// allowedEnvPrefixes lists environment variable prefixes passed to the Claude subprocess.
var allowedEnvPrefixes = []string{
	"ANTHROPIC_",
	"CLAUDE_",
	"GITHUB_",
}

// allowedEnvKeys lists exact environment variable names passed to the Claude subprocess.
var allowedEnvKeys = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "SHELL": true,
	"LANG": true, "TERM": true, "CI": true, "TMPDIR": true,
}

// resolveEnv builds a filtered environment for the Claude subprocess,
// passing only variables needed for normal operation and CI.
func resolveEnv() []string {
	var filtered []string
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		if allowedEnvKeys[key] {
			filtered = append(filtered, e)
			continue
		}
		for _, prefix := range allowedEnvPrefixes {
			if strings.HasPrefix(key, prefix) {
				filtered = append(filtered, e)
				break
			}
		}
	}
	return filtered
}

// claudeResult holds the text output and usage metadata from a Claude CLI call.
type claudeResult struct {
	Text  string
	Usage CallUsage
}

// runClaude executes Claude with the given prompt and environment.
// When model is non-empty, it is passed via --model (e.g. "haiku", "sonnet").
// When maxBudgetUSD is > 0, it is passed via --max-budget-usd.
// When timeout is 0, defaults to EffectiveTimeout() (5 minutes).
func runClaude(prompt string, env []string, model string, maxBudgetUSD float64, timeout time.Duration) (*claudeResult, error) {
	if timeout <= 0 {
		timeout = (&ReviewConfig{}).EffectiveTimeout()
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	args := []string{"--print", "--output-format", "json", "--no-session-persistence"}
	if model != "" {
		args = append(args, "--model", model)
	}
	if maxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", maxBudgetUSD))
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(prompt)
	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("claude timed out after %s", timeout)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("claude failed: %s\n%s", string(exitErr.Stderr), string(output))
		}
		return nil, fmt.Errorf("running claude: %w", err)
	}

	var resp claudeJSONResponse
	if err := json.Unmarshal(output, &resp); err != nil {
		// Fallback: treat entire output as plain text (e.g. older CLI version).
		return &claudeResult{Text: string(output)}, nil
	}

	if resp.IsError {
		return nil, fmt.Errorf("claude returned error: %s", resp.Result)
	}

	return &claudeResult{
		Text: resp.Result,
		Usage: CallUsage{
			Model:             resp.firstModel(),
			InputTokens:       resp.Usage.InputTokens,
			OutputTokens:      resp.Usage.OutputTokens,
			CacheReadTokens:   resp.Usage.CacheReadInputTokens,
			CacheCreateTokens: resp.Usage.CacheCreationInputTokens,
			CostUSD:           resp.CostUSD,
			DurationMS:        resp.DurationMS,
		},
	}, nil
}

// fixedThread holds the index and resolution reason for a fixed thread.
type fixedThread struct {
	Index  int
	Reason string // "code_change", "dismissed", "acknowledged", "rebutted", or "" for unknown
}

// allResolved checks if all review threads have been resolved by code changes.
// Threads resolved by other reasons (dismissed, acknowledged, rebutted) are kept
// open and do not count as resolved.
func allResolved(threads []ReviewThread, fixed []fixedThread) bool {
	fixedSet := make(map[int]bool, len(fixed))
	for _, f := range fixed {
		if f.Reason == "code_change" {
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
		if f.Reason == "code_change" {
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

// Run orchestrates the full review flow.
func Run(opts RunOptions) error {
	// 1. Detect repo if not provided.
	repo := opts.Repo
	if repo == "" {
		detected, err := DetectRepo()
		if err != nil {
			return fmt.Errorf("detecting repo: %w", err)
		}
		repo = detected
	}

	// 2. Fetch PR data.
	pr, err := FetchPR(repo, opts.PRNumber)
	if err != nil {
		return fmt.Errorf("fetching PR: %w", err)
	}

	// 2b. Skip setup PRs (workflow file being added for the first time).
	// Only skip if the PR exclusively contains setup files (workflow + config).
	if isSetupPR(pr.Diff, pr.Files) {
		fmt.Fprintf(os.Stderr, "Setup PR detected — skipping review\n")
		if opts.Post {
			if err := PostComment(repo, opts.PRNumber,
				"## \U0001F425 CodeCanary\n\nSetup PR detected \u2014 skipping automated review. Future PRs will be reviewed automatically. \U0001F389"); err != nil {
				return fmt.Errorf("posting setup PR comment: %w", err)
			}
		}
		return nil
	}

	// 3. Load review config.
	var cfg *ReviewConfig
	configPath := opts.ConfigPath
	if configPath == "" {
		configPath = ".codecanary.yml"
	}
	loaded, err := LoadConfig(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || os.IsNotExist(err) {
			cfg = &ReviewConfig{}
		} else {
			var pathErr *os.PathError
			if errors.As(err, &pathErr) {
				cfg = &ReviewConfig{}
			} else {
				return fmt.Errorf("loading config: %w", err)
			}
		}
	} else {
		cfg = loaded
	}

	// Fetch full file contents for context.
	fileContents, skippedFiles := FetchFileContents(pr.Files, cfg.Ignore, cfg.EffectiveMaxFileSize(), cfg.EffectiveMaxTotalSize())
	pr.FileContents = fileContents
	if len(skippedFiles) > 0 {
		fmt.Fprintf(os.Stderr, "Skipped %d large/ignored files: %s\n", len(skippedFiles), strings.Join(skippedFiles, ", "))
	}

	// Resolve Claude environment once for reuse.
	env := resolveEnv()

	// Usage tracker accumulates token/cost data across all Claude calls.
	tracker := &UsageTracker{}

	// Check for previous review threads.
	threads, _ := FetchReviewThreads(repo, opts.PRNumber)
	startIndex := len(threads) // continue fix_ref numbering after all existing threads
	var reviewThreads []ReviewThread
	for _, t := range threads {
		if !t.Resolved {
			reviewThreads = append(reviewThreads, t)
		}
	}
	previousSHA := FetchPreviousReviewSHA(repo, opts.PRNumber)

	// Reply-only mode: bail early if there are no threads to evaluate.
	if opts.ReplyOnly && (len(reviewThreads) == 0 || previousSHA == "") {
		fmt.Fprintf(os.Stderr, "No previous review threads to evaluate\n")
		return nil
	}

	var prompt string
	var fixed []fixedThread
	if len(reviewThreads) > 0 && previousSHA != "" {
		// Incremental review.
		fmt.Fprintf(os.Stderr, "Re-reviewing PR #%d (%d unresolved threads, base %s)\n", opts.PRNumber, len(reviewThreads), previousSHA[:8])
		incrementalDiff, diffErr := GetIncrementalDiff(previousSHA)
		if diffErr != nil {
			fmt.Fprintf(os.Stderr, "Could not compute incremental diff, will use full PR diff for reevaluation\n")
		} else {
			// Scope incremental diff to only PR files to exclude main-branch
			// changes pulled in by rebases.
			allowed := make(map[string]bool, len(pr.Files))
			for _, f := range pr.Files {
				allowed[f] = true
			}
			incrementalDiff = ScopeDiffToFiles(incrementalDiff, allowed)
		}

		// Phase 1: Go-driven triage — classify threads using GitHub's outdated
		// flag and presence of human replies. Only threads that need evaluation
		// will trigger a Claude call.
		reevalDiff := incrementalDiff
		if diffErr != nil {
			reevalDiff = pr.Diff
		}

		botLogin := ""
		if len(reviewThreads) > 0 {
			botLogin = reviewThreads[0].Author
		}
		if botLogin == "" {
			fmt.Fprintf(os.Stderr, "Warning: could not determine bot login from thread author\n")
		}
		triaged := ClassifyThreads(reviewThreads, reevalDiff, botLogin)

		if opts.DryRun {
			LogTriage(triaged)
			for _, t := range triaged {
				if t.Class == TriageSkip {
					continue
				}
				fmt.Print("\n---\n\n")
				fmt.Print(BuildPerThreadPrompt(t, cfg))
			}
			fmt.Print("\n---\n\n")
		} else {
			LogTriage(triaged)
			needsEval := countNonSkipped(triaged)
			if needsEval > 0 {
				resolutions := EvaluateThreadsParallel(triaged, env, cfg, 3, "haiku", tracker)
				LogResolutions(triaged, resolutions)
				fixed = toFixedThreads(resolutions)

				// Resolve or acknowledge threads on GitHub.
				for _, f := range fixed {
					if f.Index >= 0 && f.Index < len(reviewThreads) {
						t := reviewThreads[f.Index]
						label := threadLabel(t)
						if f.Reason == "code_change" {
							// Actually fixed — resolve on GitHub.
							if err := ResolveThread(t.ID); err != nil {
								if strings.Contains(err.Error(), "Resource not accessible") {
									fmt.Fprintf(os.Stderr, "  ~ %s (auto-resolve unavailable: token lacks permission)\n", label)
								} else {
									fmt.Fprintf(os.Stderr, "  ! %s — resolved, but failed to update thread: %v\n", label, err)
								}
							}
						} else {
							// Not fixed by code — acknowledge but keep open for re-triage.
							if !hasAcknowledgmentReply(t, f.Reason) {
								msg := acknowledgmentMessage(f.Reason)
								if err := ReplyToThread(t.ID, msg); err != nil {
									fmt.Fprintf(os.Stderr, "  ! %s — failed to post acknowledgment: %v\n", label, err)
								}
							}
						}
					}
				}
			} else {
				fmt.Fprintf(os.Stderr, "No threads need re-evaluation — skipping Claude\n")
			}
		}

		if !opts.ReplyOnly {
			// Phase 2: Build review prompt for new findings.
			// Only code_change threads are truly resolved. Other reasons
			// (dismissed, acknowledged, rebutted) stay in unresolved so they
			// get re-triaged on future pushes.
			fixedSet := make(map[int]bool, len(fixed))
			for _, f := range fixed {
				if f.Index >= 0 && f.Index < len(reviewThreads) && f.Reason == "code_change" {
					fixedSet[f.Index] = true
				}
			}
			var unresolved []ReviewThread
			for i, t := range reviewThreads {
				if !fixedSet[i] {
					unresolved = append(unresolved, t)
				}
			}

			// Build resolved context for the incremental review prompt (anti-ping-pong).
			// Only code_change threads are added — acknowledged/dismissed/rebutted
			// threads stay open and should be re-checked if related code changes.
			var resolvedCtx []ResolvedContext
			for _, f := range fixed {
				if f.Index >= 0 && f.Index < len(reviewThreads) && f.Reason == "code_change" {
					t := reviewThreads[f.Index]
					title := t.Body
					if nl := strings.Index(title, "\n"); nl >= 0 {
						title = title[:nl]
					}
					resolvedCtx = append(resolvedCtx, ResolvedContext{
						Path:   t.Path,
						Line:   t.Line,
						Title:  title,
						Reason: f.Reason,
					})
				}
			}

			if diffErr != nil {
				// No incremental diff available — fall back to full review.
				fbPlural := "s"
				if len(unresolved) == 1 {
					fbPlural = ""
				}
				fmt.Fprintf(os.Stderr, "Falling back to full review (%d known issue%s excluded)...\n", len(unresolved), fbPlural)
				prompt = BuildPrompt(pr, cfg, startIndex)
			} else {
				// Scope file contents to only files in the incremental diff to
				// prevent hallucinations about unrelated files from the full PR.
				incFiles := FilesFromDiff(incrementalDiff)
				incContents := make(map[string]string, len(incFiles))
				for _, f := range incFiles {
					if content, ok := pr.FileContents[f]; ok {
						incContents[f] = content
					}
				}
				plural := "s"
				if len(unresolved) == 1 {
					plural = ""
				}
				fmt.Fprintf(os.Stderr, "Reviewing new changes (%d known issue%s excluded)...\n", len(unresolved), plural)
				prompt = BuildIncrementalPrompt(incrementalDiff, cfg, unresolved, opts.PRNumber, startIndex, incContents, incFiles, resolvedCtx)
			}
		}
	} else {
		// First review — full PR diff.
		fmt.Fprintf(os.Stderr, "Reviewing PR #%d...\n", opts.PRNumber)
		prompt = BuildPrompt(pr, cfg, startIndex)
	}

	// 5. Dry run — print prompt and return.
	if opts.DryRun {
		fmt.Print(prompt)
		return nil
	}

	var findings []Finding
	if !opts.ReplyOnly {
		// 6. Run Claude.
		result, err := runClaude(prompt, env, "", cfg.MaxBudgetUSD, cfg.EffectiveTimeout())
		if err != nil {
			return err
		}
		usage := result.Usage
		usage.Phase = "review"
		tracker.Add(usage)

		// 7. Parse findings.
		findings, err = ParseFindings(result.Text)
		if err != nil {
			return fmt.Errorf("parsing findings: %w", err)
		}
		// Safety net: drop findings on files not in the PR.
		prFileSet := make(map[string]bool, len(pr.Files))
		for _, f := range pr.Files {
			prFileSet[f] = true
		}
		var filteredFindings []Finding
		for _, f := range findings {
			if f.File == "" || prFileSet[f.File] {
				filteredFindings = append(filteredFindings, f)
			} else {
				fmt.Fprintf(os.Stderr, "Dropped finding on non-PR file: %s\n", f.File)
			}
		}
		findings = filteredFindings
		findings = FilterNonActionable(findings)
	}

	if len(findings) == 0 {
		fmt.Fprintf(os.Stderr, "No new findings\n")
	} else {
		fmt.Fprintf(os.Stderr, "Found %d new findings\n", len(findings))
	}

	// Get current HEAD SHA for tracking.
	headSHA, _ := exec.Command("git", "rev-parse", "HEAD").Output()

	// 8. Build result.
	result := &ReviewResult{
		PRNumber: opts.PRNumber,
		Repo:     repo,
		Findings: findings,
		SHA:      strings.TrimSpace(string(headSHA)),
	}

	// 9. Format output.
	outputFormat := opts.Output
	if outputFormat == "" {
		outputFormat = "markdown"
	}

	var formatted string
	switch outputFormat {
	case "json":
		jsonOut, err := FormatJSON(result)
		if err != nil {
			return fmt.Errorf("formatting JSON: %w", err)
		}
		formatted = jsonOut
	default:
		formatted = FormatMarkdown(result)
	}

	// 10. Print to stdout (skip when posting to avoid noisy CI logs).
	if !opts.Post {
		fmt.Print(formatted)
	}

	// 11. Post review if requested.
	// In reply-only mode, per-thread ack replies are posted earlier;
	// skip top-level review comments, minimization, and all-clear posts.
	if opts.Post && !opts.ReplyOnly {
		// Step 5: Minimize ALL previous reviews where all inline
		// findings are resolved. This runs before posting so the new
		// message is always the final one on the timeline.
		minimizeFailed := false
		if len(reviewThreads) > 0 {
			if nodeIDs, err := FindReviewNodeIDs(repo, opts.PRNumber); err == nil {
				if allResolved(reviewThreads, fixed) {
					// All resolved — minimize every previous review.
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
					// Partially resolved — minimize only reviews whose findings are ALL resolved.
					resolvedIDs := resolvedFindingIDs(threads, reviewThreads, fixed)
					minimizeFullyResolvedReviews(repo, opts.PRNumber, resolvedIDs)
				}
			} else {
				fmt.Fprintf(os.Stderr, "Warning: could not fetch reviews for minimization: %v\n", err)
				minimizeFailed = true
			}
		}

		// Step 6: Post one of the following.
		if len(findings) > 0 {
			// New findings — post review with inline comments.
			if err := PostReview(repo, opts.PRNumber, result, pr.Diff, result.SHA); err != nil {
				return fmt.Errorf("posting review: %w", err)
			}
			fmt.Fprintf(os.Stderr, "Review posted to PR #%d\n", opts.PRNumber)
		} else if len(reviewThreads) > 0 && allResolved(reviewThreads, fixed) {
			// Re-review: all resolved — post all-clear.
			if err := PostAllClearReview(repo, opts.PRNumber, minimizeFailed); err != nil {
				return fmt.Errorf("posting all-clear review: %w", err)
			}
			fmt.Fprintf(os.Stderr, "All clear! No issues remaining.\n")
		} else if len(reviewThreads) > 0 {
			// Re-review: some still unresolved, no new findings — nothing to post.
			codeFixedSet := make(map[int]bool, len(fixed))
			for _, f := range fixed {
				if f.Index >= 0 && f.Index < len(reviewThreads) && f.Reason == "code_change" {
					codeFixedSet[f.Index] = true
				}
			}
			unresolvedCount := len(reviewThreads) - len(codeFixedSet)
			fmt.Fprintf(os.Stderr, "No new findings. %d previous thread(s) still unresolved.\n", unresolvedCount)
		} else {
			// First review, no issues.
			if err := PostCleanReview(repo, opts.PRNumber); err != nil {
				return fmt.Errorf("posting review: %w", err)
			}
			fmt.Fprintf(os.Stderr, "Review posted to PR #%d\n", opts.PRNumber)
		}
	}

	// Write usage report.
	if report := tracker.Report(repo, opts.PRNumber); len(report.Calls) > 0 {
		if err := WriteUsageFile(report); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not write usage report: %v\n", err)
		}
	}

	return nil
}

// acknowledgmentMessage returns a reply body for non-code-change resolutions.
// Each message includes a hidden HTML marker for dedup detection.
func acknowledgmentMessage(reason string) string {
	switch reason {
	case "dismissed":
		return "<!-- codecanary:ack:dismissed -->\nAuthor dismissed this finding. Keeping open \u2014 will re-check if related code changes."
	case "acknowledged":
		return "<!-- codecanary:ack:acknowledged -->\nAuthor acknowledged this finding. Keeping open \u2014 will re-check on future pushes."
	case "rebutted":
		return "<!-- codecanary:ack:rebutted -->\nAuthor provided a technical rebuttal. Keeping open \u2014 will re-check if related code changes."
	default:
		return "<!-- codecanary:ack:unknown -->\nFinding acknowledged. Keeping open \u2014 will re-check on future pushes."
	}
}

// hasAcknowledgmentReply checks if an acknowledgment reply already exists
// on the thread for the given reason, to avoid posting duplicate replies.
func hasAcknowledgmentReply(t ReviewThread, reason string) bool {
	newMarker := fmt.Sprintf("<!-- codecanary:ack:%s -->", reason)
	oldMarker := fmt.Sprintf("<!-- clanopy:ack:%s -->", reason)
	for _, r := range t.Replies {
		if strings.Contains(r.Body, newMarker) || strings.Contains(r.Body, oldMarker) {
			return true
		}
	}
	return false
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}
