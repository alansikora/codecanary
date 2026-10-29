package review

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// FixPlatform abstracts what one CI fix cycle needs from the environment the
// pull request lives in. GithubPlatform implements it (fix_github.go); the
// cycle itself (RunFix) stays platform-agnostic, like Run.
type FixPlatform interface {
	// LoadFixTarget returns the pull request the fix commits to.
	LoadFixTarget() (*FixTarget, error)
	// LoadOpenFindings returns the findings still open on the pull request:
	// not resolved, and not acknowledged by the bot after an author reply.
	LoadOpenFindings() ([]PRFinding, error)
	// LoadCommitMessages returns the pull request's commit messages, oldest
	// first. The fix limits are counted from them.
	LoadCommitMessages() ([]string, error)
	// OpenThreadsStatus returns the review commit status the open review
	// threads warrant, by the rule a review applies.
	OpenThreadsStatus() (state, desc string, err error)
	// SetReviewStatus sets the review commit status on sha.
	SetReviewStatus(sha, state, desc string) error
	// ReplyToFinding posts body in the finding's review thread.
	ReplyToFinding(f PRFinding, body string) error
	// PostNotice posts body as a pull request comment unless a comment
	// carrying key was posted before, so a notice is posted once per key.
	PostNotice(key, body string) error
	// Push pushes the checkout's HEAD to the pull request's head branch.
	Push(target *FixTarget) error
	// ReportUsage handles the fix's usage data, as for a review.
	ReportUsage(tracker *UsageTracker)
}

// FixTarget is the pull request a fix cycle commits to.
type FixTarget struct {
	Number     int
	HeadSHA    string
	HeadBranch string
	// SameRepo is false for a pull request from a fork, which the fix never
	// pushes to.
	SameRepo bool
	// Author is the pull request's author, who authors the fix commit;
	// Committer is the bot that makes it.
	Author    CommitIdentity
	Committer CommitIdentity
}

// CommitIdentity is a git author or committer.
type CommitIdentity struct {
	Name  string
	Email string
}

// FixOptions configures one CI fix cycle.
type FixOptions struct {
	ConfigPath string
	ClaudePath string // overrides config claude_path
	// ReviewedSHA is the commit the triggering review ran on. When the pull
	// request has moved past it, the cycle does nothing: the new head gets a
	// review of its own, and a fix of its own if that review finds anything.
	ReviewedSHA string
	Platform    FixPlatform
}

// Fix-cycle constants.
const (
	// fixEffort is the Claude CLI --effort level for the fix model.
	fixEffort = "medium"
	// defaultFixTimeout bounds the fix model when timeout_minutes is unset:
	// it reads, edits and re-reads files, so it runs longer than a review.
	defaultFixTimeout = 20 * time.Minute
	// fixTrailer marks a commit as a CodeCanary fix. Its value is the
	// commit whose review the fix answers.
	fixTrailer = "CodeCanary-Fix"
	// claudeCoAuthorTrailer credits the model that wrote the fix.
	claudeCoAuthorTrailer = "Co-authored-by: Claude <noreply@anthropic.com>"
)

// protectedWritePaths are checkout paths the fix must never change: git
// metadata, CI workflows (which run with secrets, and which an App token
// without the workflows permission can't push anyway), CodeCanary's own
// config, and Claude Code project configuration. The Claude provider denies
// edits to them (claudeWriteRules), and RunFix reverts any change to them
// before committing, so neither layer relies on the other.
var protectedWritePaths = []string{
	".git/**",
	".github/**",
	".codecanary/**",
	".codecanary.yml",
	".claude/**",
	".mcp.json",
}

// RunFix runs one CI fix cycle: it loads the open findings, stops when a
// loop limit is reached, otherwise has the fix model apply or refute each
// finding, replies on every refuted finding's thread, and commits the edits
// once and pushes them to the head branch (the push triggers the next review).
//
// Only a setup problem (config, checkout) is returned as an error. Model,
// rate-limit and push failures post one notice on the pull request and
// return nil: the fix job never fails CI.
func RunFix(opts FixOptions) error {
	cfg, err := LoadReviewConfig(opts.ConfigPath)
	if err != nil {
		return err
	}
	if !cfg.FixEnabled() {
		fmt.Fprintf(os.Stderr, "fix.enabled is not set in the config — nothing to do\n")
		return nil
	}
	platform := opts.Platform

	target, err := platform.LoadFixTarget()
	if err != nil {
		return fmt.Errorf("loading pull request: %w", err)
	}
	if !target.SameRepo {
		fmt.Fprintf(os.Stderr, "Pull request #%d comes from a fork — the fix never pushes to one\n", target.Number)
		return nil
	}
	if opts.ReviewedSHA != "" && target.HeadSHA != opts.ReviewedSHA {
		fmt.Fprintf(os.Stderr, "Pull request head moved from %s to %s since the review — the new head gets its own review\n",
			shortSHA(opts.ReviewedSHA), shortSHA(target.HeadSHA))
		return nil
	}
	if head, err := currentHEAD(); err != nil {
		return err
	} else if head != target.HeadSHA {
		return fmt.Errorf("checkout is at %s, but the pull request head is %s", shortSHA(head), shortSHA(target.HeadSHA))
	}
	if changed, err := changedPaths(); err != nil {
		return err
	} else if len(changed) > 0 {
		return fmt.Errorf("checkout has uncommitted changes before the fix ran: %s", strings.Join(changed, ", "))
	}

	findings, err := platform.LoadOpenFindings()
	if err != nil {
		return fmt.Errorf("loading findings: %w", err)
	}
	if len(findings) == 0 {
		fmt.Fprintf(os.Stderr, "No open findings on pull request #%d\n", target.Number)
		return nil
	}

	messages, err := platform.LoadCommitMessages()
	if err != nil {
		return fmt.Errorf("loading commits: %w", err)
	}
	maxPerPush, maxPerPR := cfg.EffectiveFixLimits()
	sincePush, total := countFixCommits(messages)
	if limit := fixLimitReached(sincePush, total, maxPerPush, maxPerPR); limit != nil {
		stopAtLimit(platform, target, findings, limit)
		return nil
	}

	claudePath := cfg.ClaudePath
	if opts.ClaudePath != "" {
		claudePath = opts.ClaudePath
	}
	if claudePath == "" {
		claudePath = "claude"
	}
	if _, err := exec.LookPath(claudePath); err != nil {
		return fmt.Errorf("claude binary %q not found: %w", claudePath, err)
	}
	provider := NewProviderForRole(&ModelConfig{
		Provider:       cfg.Provider,
		Model:          cfg.EffectiveFixModel(),
		ClaudePath:     claudePath,
		ClaudeFixTools: true,
		Effort:         fixEffort,
	}, resolveEnv())

	timeout := cfg.EffectiveTimeout()
	if timeout == 0 {
		timeout = defaultFixTimeout
	}
	Stderrf(ansiBold, "Fixing %d finding%s on pull request #%d with %s...\n",
		len(findings), pluralS(len(findings)), target.Number, cfg.EffectiveFixModel())
	result, err := provider.Run(context.Background(), BuildFixPrompt(findings, cfg), RunOpts{
		MaxBudgetUSD: cfg.MaxBudgetUSD,
		Timeout:      timeout,
	})
	if err != nil {
		discardChanges() // edits made before the failure (e.g. a timeout)
		postFixFailure(platform, target, "the fix model failed", err)
		return nil
	}
	tracker := &UsageTracker{}
	trackUsage(tracker, result, "fix")
	platform.ReportUsage(tracker)

	dispositions, err := ParseFixDispositions(result.Text, findings)
	if err != nil {
		discardChanges()
		postFixFailure(platform, target, "the fix model's answer could not be read", err)
		return nil
	}
	applied, refuted, skipped := splitDispositions(dispositions)
	for _, d := range skipped {
		fmt.Fprintf(os.Stderr, "  ~ %s left open: %s\n", d.FixRef, d.Reason)
	}

	// Refutations go first, so the review the push triggers already reads
	// them; they stand on their own if the push then fails.
	for _, d := range refuted {
		if err := platform.ReplyToFinding(d.Finding, fixReplyBody(d.Reason)); err != nil {
			fmt.Fprintf(os.Stderr, "  ! %s — could not post the refutation: %v\n", d.FixRef, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "  ✓ %s refuted: %s\n", d.FixRef, d.Reason)
	}

	if err := commitAndPush(platform, cfg, target, applied, opts.ReviewedSHA); err != nil {
		postFixFailure(platform, target, "the fix could not be pushed", err)
	}
	return nil
}

// commitAndPush commits the fix model's edits as one commit and pushes it.
// Edits to protectedWritePaths are reverted first. Edits made without any
// finding reported applied are discarded rather than committed unexplained.
// The discard is all-or-nothing: once one finding is applied, every remaining
// edit is committed, because a fix may touch files its finding doesn't name
// and edits can't be tied to findings reliably. The prompt keeps edits to
// applied findings. fix.format_command then formats the edited files.
func commitAndPush(platform FixPlatform, cfg *ReviewConfig, target *FixTarget, applied []FixDisposition, reviewedSHA string) error {
	reverted, err := revertProtectedChanges()
	if err != nil {
		return err
	}
	for _, p := range reverted {
		Stderrf(ansiYellow, "Warning: reverted the fix's change to %s — the fix may not change it\n", p)
	}
	changed, err := changedPaths()
	if err != nil {
		return err
	}
	switch {
	case len(changed) == 0 && len(applied) > 0:
		fmt.Fprintf(os.Stderr, "The fix model reported %d finding%s applied but left no changes — nothing to commit\n", len(applied), pluralS(len(applied)))
		return nil
	case len(changed) == 0:
		fmt.Fprintf(os.Stderr, "No finding applied — nothing to commit\n")
		return nil
	case len(applied) == 0:
		fmt.Fprintf(os.Stderr, "The fix model changed %s without reporting a finding applied — discarding the changes\n", strings.Join(changed, ", "))
		discardChanges()
		return nil
	}
	formatFixChanges(cfg)
	if reviewedSHA == "" {
		reviewedSHA = target.HeadSHA
	}
	if err := commitFix(target, buildFixCommitMessage(applied, reviewedSHA)); err != nil {
		return err
	}
	if err := platform.Push(target); err != nil {
		return err
	}
	Stderrf(ansiGreen, "Pushed a fix for %d finding%s to %s\n", len(applied), pluralS(len(applied)), target.HeadBranch)
	return nil
}

// fixLimit is a reached fix loop limit.
type fixLimit struct {
	Count     int  // fix commits counted against the limit
	SincePush bool // true: the per-push limit; false: the per-PR limit
}

// fixLimitReached reports which loop limit, if any, stops another fix. The
// per-push limit is checked first: it is the one a human push resets, so
// naming it tells the author what moves things again.
func fixLimitReached(sincePush, total, maxPerPush, maxPerPR int) *fixLimit {
	switch {
	case sincePush >= maxPerPush:
		return &fixLimit{Count: sincePush, SincePush: true}
	case total >= maxPerPR:
		return &fixLimit{Count: total}
	}
	return nil
}

// reason is the limit in the words of the stop notice.
func (l *fixLimit) reason() string {
	if l.SincePush {
		return fmt.Sprintf("stopped after %d fix%s since your last push", l.Count, pluralEs(l.Count))
	}
	return fmt.Sprintf("stopped after %d fix%s on this PR", l.Count, pluralEs(l.Count))
}

// stopAtLimit settles a cycle stopped by a loop limit, without a model call:
// one comment listing the open findings, and the review status on the head
// with the reason appended. Both are best-effort.
func stopAtLimit(platform FixPlatform, target *FixTarget, findings []PRFinding, limit *fixLimit) {
	Stderrf(ansiYellow, "CodeCanary fix %s — not running the fix model\n", limit.reason())
	body := buildStopNotice(findings, limit)
	if err := platform.PostNotice(fixNoticeKey("limit", target.HeadSHA), body); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not post the stop notice: %v\n", err)
	}
	state, desc, err := platform.OpenThreadsStatus()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not compute the review status: %v\n", err)
		return
	}
	desc += fmt.Sprintf("; stopped after %d fix%s", limit.Count, pluralEs(limit.Count))
	if err := platform.SetReviewStatus(target.HeadSHA, state, desc); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not set the %s status: %v\n", ReviewCommitStatusContext, err)
	}
}

// postFixFailure reports a cycle that could not finish: one comment per head
// pointing the author at the local fix loop. The error goes to the job log,
// with a one-line summary in the comment.
func postFixFailure(platform FixPlatform, target *FixTarget, what string, err error) {
	fmt.Fprintf(os.Stderr, "::warning::CodeCanary fix: %s: %v\n", what, err)
	body := buildFailureNotice(target.HeadSHA, what, err)
	if perr := platform.PostNotice(fixNoticeKey("failed", target.HeadSHA), body); perr != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not post the failure notice: %v\n", perr)
	}
}

// buildFailureNotice is the comment posted when the fix could not finish.
// FetchFixStop reads its reason back (failedNoticeReasonRe).
func buildFailureNotice(headSHA, what string, err error) string {
	return fmt.Sprintf("%s\n\nThe CodeCanary fix did not finish on `%s`: %s (%s).\n\nFix the open findings locally with `/codecanary-fix`.\n",
		fixNoticeTitle, shortSHA(headSHA), what, failureSummary(err))
}

// failureSummary is a one-line description of err for a PR comment: the
// provider's classification when there is one (rate limit, authentication),
// else the error's first line, shortened.
func failureSummary(err error) string {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return fmt.Sprintf("%s %s", pe.Provider, pe.Kind)
	}
	line, _, _ := strings.Cut(err.Error(), "\n")
	return truncateUTF8(line, 200)
}

// truncateUTF8 cuts s to at most n bytes plus "…", dropping a rune the cut
// would split, so the result stays valid UTF-8.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}

// pluralEs returns "es" unless n is 1 ("1 fix", "2 fixes").
func pluralEs(n int) string {
	if n == 1 {
		return ""
	}
	return "es"
}
