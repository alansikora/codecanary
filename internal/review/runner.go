package review

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/alansikora/codecanary/internal/credentials"
	"github.com/alansikora/codecanary/internal/telemetry"
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
	Version    string         // binary version (for telemetry)
	PR         *PRData        // pre-fetched PRData (used in local mode)
	Platform   ReviewPlatform // environment adapter (GitHub or local)
}

// allowedEnvPrefixes lists environment variable prefixes passed to the LLM subprocess.
var allowedEnvPrefixes = []string{
	"CODECANARY_",
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
	// Inject keychain credential if not already in env.
	if !present[credentials.EnvVar] {
		if val, _, err := credentials.Retrieve(); err == nil && val != "" {
			filtered = append(filtered, credentials.EnvVar+"="+val)
		}
	}
	return filtered
}

// providerResult holds the text output and usage metadata from an LLM call.
type providerResult struct {
	Text        string
	Usage       CallUsage
	ModelUsages []CallUsage // per-model breakdown from modelUsage map
	DurationMS  int
	Truncated   bool // true when the response hit the output token limit
}

// fixedThread holds the index and resolution reason for a fixed thread.
type fixedThread struct {
	Index  int
	Reason string // "code_change", "dismissed", "acknowledged", "rebutted", or "" for unknown
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
func trackUsage(tracker *UsageTracker, result *providerResult, phase string) {
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
	startTime := time.Now()
	platform := opts.Platform
	pr := opts.PR

	// 1. Resolve repo name — needed for GitHub API calls and telemetry.
	var detectRepoErr error
	if opts.Repo == "" {
		opts.Repo, detectRepoErr = DetectRepo()
	}

	// 2. Fetch PR data if not pre-fetched (GitHub mode).
	if pr == nil {
		if opts.Repo == "" {
			if detectRepoErr != nil {
				return fmt.Errorf("detecting repo: %w", detectRepoErr)
			}
			return fmt.Errorf("detecting repo: could not determine repository")
		}
		// Propagate resolved repo to the platform adapter.
		if gp, ok := platform.(*GithubPlatform); ok && gp.Repo == "" {
			gp.Repo = opts.Repo
		}

		fetched, err := FetchPR(opts.Repo, opts.PRNumber)
		if err != nil {
			return fmt.Errorf("fetching PR: %w", err)
		}
		pr = fetched

		// Skip setup PRs (workflow file being added for the first time).
		if isSetupPR(pr.Diff, pr.Files) {
			fmt.Fprintf(os.Stderr, "Setup PR detected — skipping review\n")
			if opts.Post {
				if err := PostComment(opts.Repo, opts.PRNumber,
					"## \U0001F425 CodeCanary\n\nSetup PR detected \u2014 skipping automated review. Future PRs will be reviewed automatically. \U0001F389"); err != nil {
					return fmt.Errorf("posting setup PR comment: %w", err)
				}
			}
			return nil
		}
	}

	// 3. Prepare shared review context.
	rctx, err := prepareReview(pr, opts.ConfigPath)
	if err != nil {
		return err
	}
	cfg := rctx.Config
	reviewMC := &ModelConfig{Provider: cfg.Provider, Model: cfg.ReviewModel, APIBase: cfg.APIBase, APIKeyEnv: cfg.APIKeyEnv}
	triageMC := &ModelConfig{Provider: cfg.Provider, Model: cfg.TriageModel, APIBase: cfg.APIBase, APIKeyEnv: cfg.APIKeyEnv}
	reviewProvider := NewProviderForRole(reviewMC, rctx.Env)
	triageProvider := NewProviderForRole(triageMC, rctx.Env)
	tracker := rctx.Tracker

	// 4. Load previous findings via the platform adapter.
	reviewThreads, previousSHA, startIndex := platform.LoadPreviousFindings()

	// Reply-only mode: bail early if there are no threads to evaluate.
	if opts.ReplyOnly && (len(reviewThreads) == 0 || previousSHA == "") {
		fmt.Fprintf(os.Stderr, "No previous review threads to evaluate\n")
		return nil
	}

	// 5. Triage & build prompt.
	var prompt string
	var fixed []fixedThread
	var stillOpenFindings []Finding
	isIncremental := len(reviewThreads) > 0 && previousSHA != ""

	if isIncremental {
		prompt, fixed, stillOpenFindings = runTriage(
			pr, cfg, rctx.ProjectDocs, triageProvider, tracker, platform,
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

	// 6. Dry run — print prompt and return.
	if opts.DryRun {
		if prompt != "" {
			fmt.Print(prompt)
		}
		return nil
	}

	// 7. Budget check & LLM call.
	var findings []Finding
	if !opts.ReplyOnly && prompt != "" {
		if err := CheckBudget(tracker, cfg.MaxBudgetUSD); err != nil {
			fmt.Fprintf(os.Stderr, "Skipping review call: %v\n", err)
			prompt = ""
		}
	}
	if !opts.ReplyOnly && prompt != "" {
		claudeOut, err := reviewProvider.Run(context.Background(), prompt, RunOpts{
			MaxBudgetUSD: cfg.MaxBudgetUSD,
			Timeout:      cfg.EffectiveTimeout(),
		})
		if err != nil {
			return err
		}
		trackUsage(tracker, claudeOut, "review")
		if claudeOut.Truncated {
			Stderrf(ansiYellow, "Warning: review response was truncated — findings may be incomplete\n")
		}

		findings, err = processFindings(claudeOut.Text, pr.Files, isIncremental)
		if err != nil {
			return err
		}
	}

	// 8. Build result.
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

	// 9. Publish results via the platform adapter.
	// In reply-only mode, per-thread ack replies are posted earlier;
	// skip top-level review comments, minimization, and all-clear posts.
	if !opts.ReplyOnly {
		if err := platform.Publish(result, pr, reviewThreads, fixed); err != nil {
			return err
		}
	}

	// 10. Save state for future incremental reviews.
	// Skip in reply-only mode to avoid overwriting previous findings with an empty slice.
	if !opts.DryRun && !opts.ReplyOnly {
		if err := platform.SaveState(result, stillOpenFindings, isIncremental); err != nil {
			return err
		}
	}

	// 11. Report usage.
	platform.ReportUsage(tracker)

	// 12. Anonymous telemetry (fire-and-forget).
	if !opts.DryRun && telemetry.Enabled() {
		calls := tracker.Calls()
		var totalIn, totalOut, totalCache int
		var totalCost float64
		for _, c := range calls {
			totalIn += c.InputTokens
			totalOut += c.OutputTokens
			totalCache += c.CacheReadTokens
			totalCost += c.CostUSD
		}
		bySeverity := make(map[string]int)
		for _, f := range result.Findings {
			bySeverity[f.Severity]++
		}
		platformName := "github"
		if _, ok := opts.Platform.(*LocalPlatform); ok {
			platformName = "local"
		}
		telemetry.SendReview(telemetry.ReviewEvent{
			Repo:              opts.Repo,
			Version:           opts.Version,
			Provider:          cfg.Provider,
			Platform:          platformName,
			IsIncremental:     isIncremental,
			NewFindings:       len(result.Findings),
			StillOpenFindings: len(result.StillOpen),
			BySeverity:        bySeverity,
			InputTokens:       totalIn,
			OutputTokens:      totalOut,
			CacheReadTokens:   totalCache,
			CostUSD:           totalCost,
			DurationMS:        time.Since(startTime).Milliseconds(),
		})
	}

	return nil
}

// runTriage handles the incremental review: classify previous threads, evaluate
// via LLM, handle resolutions, and build the incremental prompt.
// Returns the prompt, fixed threads, and still-open findings.
func runTriage(
	pr *PRData, cfg *ReviewConfig, projectDocs map[string]string,
	triageProvider ModelProvider, tracker *UsageTracker, platform ReviewPlatform,
	reviewThreads []ReviewThread, previousSHA string, startIndex int,
	opts RunOptions,
) (string, []fixedThread, []Finding) {
	Stderrf(ansiBold, "Re-evaluating %d unresolved thread(s) (base %s)...\n", len(reviewThreads), shortSHA(previousSHA))

	// Detect rebase: if previousSHA is not an ancestor of HEAD the branch was
	// force-pushed and git diff previousSHA..HEAD would include unrelated
	// base-branch changes, producing misleading reviews.
	ancestor, ancestorErr := isAncestor(previousSHA)
	if ancestorErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not check ancestry of %s: %v — treating as rebase\n", shortSHA(previousSHA), ancestorErr)
	}
	// Treat git failures the same as a confirmed rebase: skip the incremental
	// diff which could be misleading if the SHA exists but ancestry is unknown.
	rebased := !ancestor || ancestorErr != nil

	var incrementalDiff string
	var diffErr error

	if rebased && ancestorErr == nil {
		Stderrf(ansiYellow, "Rebase detected (base %s is no longer an ancestor of HEAD)\n", shortSHA(previousSHA))
	} else {
		// Compute incremental diff via the platform adapter.
		// In local modes this also includes uncommitted working-tree changes.
		incrementalDiff, diffErr = platform.GetIncrementalDiff(previousSHA, pr.Files)
		if diffErr != nil {
			fmt.Fprintf(os.Stderr, "Could not compute incremental diff, will use full PR diff for reevaluation\n")
		} else {
			allowed := make(map[string]bool, len(pr.Files))
			for _, f := range pr.Files {
				allowed[f] = true
			}
			incrementalDiff = ScopeDiffToFiles(incrementalDiff, allowed)
		}
	}

	// Phase 1: Go-driven triage — classify threads.
	reevalDiff := incrementalDiff
	if rebased || diffErr != nil {
		reevalDiff = pr.Diff
	}

	botLogin := platform.ExcludedAuthor(reviewThreads)
	triaged := ClassifyThreads(reviewThreads, reevalDiff, botLogin, pr.Files, pr.FileContents)

	var fixed []fixedThread

	if opts.DryRun {
		LogTriage(triaged)
		for _, t := range triaged {
			if t.Class == TriageSkip || t.Class == TriageFileRemovedFromPR {
				continue
			}
			fmt.Print("\n---\n\n")
			fmt.Print(BuildPerThreadPrompt(t, cfg))
		}
		fmt.Print("\n---\n\n")
	} else {
		LogTriage(triaged)

		// Pre-evaluation: auto-resolve file-removed threads (Go-only, no LLM).
		for _, t := range triaged {
			if t.Class == TriageFileRemovedFromPR {
				fixed = append(fixed, fixedThread{Index: t.Index, Reason: "file_removed"})
			}
		}
		if len(fixed) > 0 {
			platform.HandleResolutions(reviewThreads, fixed)
		}

		// LLM evaluation for remaining threads.
		needsEval := countNonSkipped(triaged)
		if needsEval > 0 {
			resolutions := EvaluateThreadsParallel(triaged, triageProvider, cfg, 3, tracker, cfg.MaxBudgetUSD)
			LogResolutions(triaged, resolutions)
			llmFixed := toFixedThreads(resolutions)
			if len(llmFixed) > 0 {
				platform.HandleResolutions(reviewThreads, llmFixed)
				fixed = append(fixed, llmFixed...)
			}
		} else if len(fixed) == 0 {
			fmt.Fprintf(os.Stderr, "No threads need re-evaluation — skipping Claude\n")
		}
	}

	if opts.ReplyOnly {
		return "", fixed, nil
	}

	// Phase 2: Build review prompt for new findings.
	// code_change and file_removed threads are truly resolved. Other reasons
	// (dismissed, acknowledged, rebutted) stay in unresolved so they
	// get re-triaged on future pushes.
	fixedSet := make(map[int]bool, len(fixed))
	for _, f := range fixed {
		if f.Index >= 0 && f.Index < len(reviewThreads) && isTrueResolution(f.Reason) {
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
	// Only include code_change resolutions — file_removed threads reference
	// files no longer in the PR and would confuse the model.
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
	if rebased || diffErr != nil {
		// No reliable incremental diff (rebase or error) — fall back to full
		// PR diff but pass known issues so the LLM avoids duplicating them.
		fbPlural := "s"
		if len(unresolved) == 1 {
			fbPlural = ""
		}
		Stderrf(ansiBold, "Falling back to full review (%d known issue%s excluded)...\n", len(unresolved), fbPlural)
		prompt = BuildIncrementalPrompt(pr.Diff, cfg, unresolved, opts.PRNumber, startIndex, pr.FileContents, pr.Files, resolvedCtx, projectDocs)
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

// loadReviewConfig loads the review config from the given path, or
// auto-detects via FindConfig() when configPath is empty.
func loadReviewConfig(configPath string) (*ReviewConfig, error) {
	if configPath == "" {
		found, err := FindConfig()
		if err != nil {
			return nil, fmt.Errorf("loading config: %w", err)
		}
		configPath = found
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	return cfg, nil
}

// isTrueResolution returns true for resolution reasons that fully close a thread.
// code_change and file_removed are true resolutions. Other reasons (dismissed,
// acknowledged, rebutted) keep the thread open for re-triage.
func isTrueResolution(reason string) bool {
	return reason == "code_change" || reason == "file_removed"
}

// shortSHA returns the first 8 characters of a SHA, or the full string if shorter.
func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}



