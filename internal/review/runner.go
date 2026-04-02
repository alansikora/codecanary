package review

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/alansikora/codecanary/internal/credentials"
)

// RunOptions configures a review run.
type RunOptions struct {
	Repo       string
	PRNumber   int
	ConfigPath string
	Output     string // "markdown" or "json"
	Post       bool
	DryRun     bool
	ReplyOnly  bool           // evaluate thread replies only, skip new findings
	PR         *PRData        // pre-fetched PRData (used in local mode)
	Platform   ReviewPlatform // environment adapter (GitHub or local)
}

// allowedEnvPrefixes lists environment variable prefixes passed to the LLM subprocess.
var allowedEnvPrefixes = []string{
	"ANTHROPIC_",
	"CLAUDE_",
	"GITHUB_",
	"OPENAI_",
	"OPENROUTER_",
}

// allowedEnvKeys lists exact environment variable names passed to the Claude subprocess.
var allowedEnvKeys = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "SHELL": true,
	"LANG": true, "TERM": true, "CI": true, "TMPDIR": true,
}

// resolveEnv builds a filtered environment for the Claude subprocess,
// passing only variables needed for normal operation and CI.
// For known provider env vars not already present, it checks the OS keychain
// (set by `codecanary setup local`). Env vars always take priority.
func resolveEnv() []string {
	var filtered []string
	present := make(map[string]bool)
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		if allowedEnvKeys[key] {
			filtered = append(filtered, e)
			present[key] = true
			continue
		}
		for _, prefix := range allowedEnvPrefixes {
			if strings.HasPrefix(key, prefix) {
				filtered = append(filtered, e)
				present[key] = true
				break
			}
		}
	}
	// Inject keychain credentials for known providers if not already in env.
	for _, envVar := range credentials.KnownProviderEnvVars() {
		if !present[envVar] {
			if val, _, err := credentials.Retrieve(envVar); err == nil && val != "" {
				filtered = append(filtered, envVar+"="+val)
			}
		}
	}
	return filtered
}

// claudeResult holds the text output and usage metadata from an LLM call.
type claudeResult struct {
	Text        string
	Usage       CallUsage
	ModelUsages []CallUsage // per-model breakdown from modelUsage map
	DurationMS  int
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

// ── Shared review pipeline ──

// reviewContext holds shared resources prepared at the start of any review.
type reviewContext struct {
	Config      *ReviewConfig
	ProjectDocs map[string]string
	Env         []string
	Tracker     *UsageTracker
}

// prepareReview loads config, project docs, file contents, and resolves the
// Claude environment. Both the PR and local paths use this.
func prepareReview(pr *PRData, configPath string) (*reviewContext, error) {
	cfg, err := loadReviewConfig(configPath)
	if err != nil {
		return nil, err
	}

	projectDocs := ReadProjectDocs()
	if len(projectDocs) > 0 {
		fmt.Fprintf(os.Stderr, "Loaded %d project doc(s) for review context\n", len(projectDocs))
	}

	fileContents, skippedFiles := FetchFileContents(pr.Files, cfg.Ignore, cfg.EffectiveMaxFileSize(), cfg.EffectiveMaxTotalSize())
	pr.FileContents = fileContents
	if len(skippedFiles) > 0 {
		fmt.Fprintf(os.Stderr, "Skipped %d large/ignored files: %s\n", len(skippedFiles), strings.Join(skippedFiles, ", "))
	}

	return &reviewContext{
		Config:      cfg,
		ProjectDocs: projectDocs,
		Env:         resolveEnv(),
		Tracker:     &UsageTracker{},
	}, nil
}

// processFindings parses Claude's output, filters findings to diff files,
// removes non-actionable findings, and tags status for incremental reviews.
func processFindings(claudeText string, diffFiles []string, incremental bool) ([]Finding, error) {
	findings, err := ParseFindings(claudeText)
	if err != nil {
		return nil, fmt.Errorf("parsing findings: %w", err)
	}

	fileSet := make(map[string]bool, len(diffFiles))
	for _, f := range diffFiles {
		fileSet[f] = true
	}
	var filtered []Finding
	for _, f := range findings {
		if f.File == "" || fileSet[f.File] {
			filtered = append(filtered, f)
		} else {
			fmt.Fprintf(os.Stderr, "Dropped finding on file outside diff: %s\n", f.File)
		}
	}
	findings = FilterNonActionable(filtered)

	if incremental {
		for i := range findings {
			findings[i].Status = "new"
		}
	}

	if len(findings) == 0 {
		Stderrf(ansiGreen, "No new findings\n")
	} else {
		Stderrf(ansiYellow, "Found %d new findings\n", len(findings))
	}

	return findings, nil
}

// resolveOutputFormat determines the output format based on user preference and
// whether stdout is a TTY.
func resolveOutputFormat(pref string) string {
	if pref == "" {
		pref = "markdown"
	}
	if pref == "markdown" && stdoutIsTTY() {
		return "terminal"
	}
	return pref
}

// formatResult renders the ReviewResult in the given format.
func formatResult(result *ReviewResult, format string) (string, error) {
	switch format {
	case "json":
		return FormatJSON(result)
	case "terminal":
		return FormatTerminal(result), nil
	default:
		return FormatMarkdown(result), nil
	}
}

// trackUsage records the usage from a Claude call. It prefers per-model
// breakdowns from ModelUsages, falling back to the aggregate Usage.
func trackUsage(tracker *UsageTracker, result *claudeResult, phase string) {
	if len(result.ModelUsages) > 0 {
		for i := range result.ModelUsages {
			result.ModelUsages[i].Phase = phase
			result.ModelUsages[i].DurationMS = result.DurationMS
			tracker.Add(result.ModelUsages[i])
		}
	} else {
		usage := result.Usage
		usage.Phase = phase
		tracker.Add(usage)
	}
}

// currentHEAD returns the current HEAD SHA.
func currentHEAD() (string, error) {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("resolving HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Run orchestrates the full review flow using the platform adapter.
func Run(opts RunOptions) error {
	platform := opts.Platform
	pr := opts.PR

	// 1. Fetch PR data if not pre-fetched (GitHub mode).
	if pr == nil {
		repo := opts.Repo
		if repo == "" {
			detected, err := DetectRepo()
			if err != nil {
				return fmt.Errorf("detecting repo: %w", err)
			}
			repo = detected
			opts.Repo = repo
		}
		// Propagate resolved repo to the platform adapter.
		if gp, ok := platform.(*GithubPlatform); ok && gp.Repo == "" {
			gp.Repo = repo
		}

		fetched, err := FetchPR(repo, opts.PRNumber)
		if err != nil {
			return fmt.Errorf("fetching PR: %w", err)
		}
		pr = fetched

		// Skip setup PRs (workflow file being added for the first time).
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
	}

	// 2. Prepare shared review context.
	rctx, err := prepareReview(pr, opts.ConfigPath)
	if err != nil {
		return err
	}
	cfg := rctx.Config
	provider := NewProvider(cfg, rctx.Env)
	tracker := rctx.Tracker

	// 3. Load previous findings via the platform adapter.
	reviewThreads, previousSHA, startIndex := platform.LoadPreviousFindings()

	// Reply-only mode: bail early if there are no threads to evaluate.
	if opts.ReplyOnly && (len(reviewThreads) == 0 || previousSHA == "") {
		fmt.Fprintf(os.Stderr, "No previous review threads to evaluate\n")
		return nil
	}

	// 4. Triage & build prompt.
	var prompt string
	var fixed []fixedThread
	var stillOpenFindings []Finding
	isIncremental := len(reviewThreads) > 0 && previousSHA != ""

	if isIncremental {
		prompt, fixed, stillOpenFindings = runTriage(
			pr, cfg, rctx.ProjectDocs, provider, tracker, platform,
			reviewThreads, previousSHA, startIndex, opts,
		)
	} else {
		// First review — full diff.
		label := pr.HeadBranch
		if opts.PRNumber > 0 {
			label = fmt.Sprintf("PR #%d", opts.PRNumber)
		}
		Stderrf(ansiBold, "Reviewing %s...\n", label)
		prompt = BuildPrompt(pr, cfg, startIndex, rctx.ProjectDocs)
	}

	// 5. Dry run — print prompt and return.
	if opts.DryRun {
		if prompt != "" {
			fmt.Print(prompt)
		}
		return nil
	}

	// 6. Budget check & LLM call.
	var findings []Finding
	if !opts.ReplyOnly && prompt != "" {
		if err := CheckBudget(tracker, cfg.MaxBudgetUSD); err != nil {
			fmt.Fprintf(os.Stderr, "Skipping review call: %v\n", err)
			prompt = ""
		}
	}
	if !opts.ReplyOnly && prompt != "" {
		claudeOut, err := provider.Run(context.Background(), prompt, RunOpts{
			Model:        cfg.EffectiveReviewModel(),
			MaxBudgetUSD: cfg.MaxBudgetUSD,
			Timeout:      cfg.EffectiveTimeout(),
		})
		if err != nil {
			return err
		}
		trackUsage(tracker, claudeOut, "review")

		findings, err = processFindings(claudeOut.Text, pr.Files, isIncremental)
		if err != nil {
			return err
		}
	}

	// 7. Build result.
	headSHA, err := currentHEAD()
	if err != nil {
		return fmt.Errorf("resolving HEAD: %w", err)
	}
	result := &ReviewResult{
		PRNumber:  opts.PRNumber,
		Repo:      opts.Repo,
		Findings:  findings,
		StillOpen: stillOpenFindings,
		SHA:       headSHA,
	}

	// Handle early return for "no new changes" with no open findings.
	if prompt == "" && len(findings) == 0 && len(stillOpenFindings) == 0 && isIncremental {
		return nil
	}

	// 8. Publish results via the platform adapter.
	// In reply-only mode, per-thread ack replies are posted earlier;
	// skip top-level review comments, minimization, and all-clear posts.
	if !opts.ReplyOnly {
		if err := platform.Publish(result, pr, reviewThreads, fixed); err != nil {
			return err
		}
	}

	// 9. Save state for future incremental reviews.
	// Skip in reply-only mode to avoid overwriting previous findings with an empty slice.
	if !opts.DryRun && !opts.ReplyOnly {
		if err := platform.SaveState(result, stillOpenFindings, isIncremental); err != nil {
			return err
		}
	}

	// 10. Report usage.
	platform.ReportUsage(tracker)

	return nil
}

// runTriage handles the incremental review: classify previous threads, evaluate
// via LLM, handle resolutions, and build the incremental prompt.
// Returns the prompt, fixed threads, and still-open findings.
func runTriage(
	pr *PRData, cfg *ReviewConfig, projectDocs map[string]string,
	provider ModelProvider, tracker *UsageTracker, platform ReviewPlatform,
	reviewThreads []ReviewThread, previousSHA string, startIndex int,
	opts RunOptions,
) (string, []fixedThread, []Finding) {
	Stderrf(ansiBold, "Re-evaluating %d unresolved thread(s) (base %s)...\n", len(reviewThreads), shortSHA(previousSHA))

	// Compute incremental diff via the platform adapter.
	// In local modes this also includes uncommitted working-tree changes.
	incrementalDiff, diffErr := platform.GetIncrementalDiff(previousSHA, pr.Files)
	if diffErr != nil {
		fmt.Fprintf(os.Stderr, "Could not compute incremental diff, will use full PR diff for reevaluation\n")
	} else {
		allowed := make(map[string]bool, len(pr.Files))
		for _, f := range pr.Files {
			allowed[f] = true
		}
		incrementalDiff = ScopeDiffToFiles(incrementalDiff, allowed)
	}

	// Phase 1: Go-driven triage — classify threads.
	reevalDiff := incrementalDiff
	if diffErr != nil {
		reevalDiff = pr.Diff
	}

	botLogin := platform.ExcludedAuthor(reviewThreads)
	triaged := ClassifyThreads(reviewThreads, reevalDiff, botLogin)

	var fixed []fixedThread

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
			resolutions := EvaluateThreadsParallel(triaged, provider, cfg, 3, cfg.EffectiveTriageModel(), tracker, cfg.MaxBudgetUSD)
			LogResolutions(triaged, resolutions)
			fixed = toFixedThreads(resolutions)

			// Delegate resolution handling to the platform adapter.
			platform.HandleResolutions(reviewThreads, fixed)
		} else {
			fmt.Fprintf(os.Stderr, "No threads need re-evaluation — skipping Claude\n")
		}
	}

	if opts.ReplyOnly {
		return "", fixed, nil
	}

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
	var stillOpenFindings []Finding
	for i, t := range reviewThreads {
		if fixedSet[i] {
			continue
		}
		unresolved = append(unresolved, t)
		stillOpenFindings = append(stillOpenFindings, FindingFromThread(t))
	}

	// Build resolved context for the incremental review prompt (anti-ping-pong).
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

	resolved := len(fixedSet)
	if resolved > 0 {
		fmt.Fprintf(os.Stderr, "%d finding(s) resolved by code changes\n", resolved)
	}

	var prompt string
	if diffErr != nil {
		// No incremental diff — fall back to full review.
		fbPlural := "s"
		if len(unresolved) == 1 {
			fbPlural = ""
		}
		fmt.Fprintf(os.Stderr, "Falling back to full review (%d known issue%s excluded)...\n", len(unresolved), fbPlural)
		prompt = BuildPrompt(pr, cfg, startIndex, projectDocs)
	} else if strings.TrimSpace(incrementalDiff) == "" {
		// No new changes — return previous findings as still-open.
		Stderrf(ansiGreen, "No new changes since last review\n")
		return "", fixed, stillOpenFindings
	} else {
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
		Stderrf(ansiBold, "Reviewing incremental changes (%d known issue%s excluded)...\n", len(unresolved), plural)
		prompt = BuildIncrementalPrompt(incrementalDiff, cfg, unresolved, opts.PRNumber, startIndex, incContents, incFiles, resolvedCtx, projectDocs)
	}

	return prompt, fixed, stillOpenFindings
}

// loadReviewConfig loads the review config from the given path (or default).
// When the specified path does not exist, it falls back to FindConfig which
// discovers both the new (.codecanary/config.yml) and legacy (.codecanary.yml)
// locations. Returns an empty config if no config file is found anywhere.
func loadReviewConfig(configPath string) (*ReviewConfig, error) {
	if configPath == "" {
		configPath = ".codecanary/config.yml"
	}
	cfg, err := LoadConfig(configPath)
	if err == nil {
		return cfg, nil
	}

	// If the explicit path doesn't exist, try discovery.
	if errors.Is(err, os.ErrNotExist) || os.IsNotExist(err) {
		if found, findErr := FindConfig(); findErr == nil {
			cfg, err = LoadConfig(found)
			if err != nil {
				return nil, fmt.Errorf("loading config: %w", err)
			}
			return cfg, nil
		}
		// No config found anywhere.
		return nil, fmt.Errorf("no config file found — create .codecanary/config.yml (see https://github.com/alansikora/codecanary)")
	}

	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		// Path error (e.g. permission denied on missing dir) — try discovery.
		if found, findErr := FindConfig(); findErr == nil {
			cfg, err = LoadConfig(found)
			if err != nil {
				return nil, fmt.Errorf("loading config: %w", err)
			}
			return cfg, nil
		}
		return &ReviewConfig{}, nil
	}

	return nil, fmt.Errorf("loading config: %w", err)
}

// isAncestor checks if the given SHA is an ancestor of HEAD.
func isAncestor(sha string) bool {
	return exec.Command("git", "merge-base", "--is-ancestor", sha, "HEAD").Run() == nil
}

// shortSHA returns the first 8 characters of a SHA, or the full string if shorter.
func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
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

