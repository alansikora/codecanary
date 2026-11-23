package review

// ReviewPlatform abstracts environment-specific operations in the review pipeline.
// GithubPlatform talks to the GitHub API; LocalPlatform reads/writes local state.
type ReviewPlatform interface {
	// LoadPreviousFindings returns unresolved review threads from the previous
	// review, the SHA that review was based on, and the start index for fix_ref
	// numbering. Returns (nil, "", 0) for first reviews.
	LoadPreviousFindings() (threads []ReviewThread, previousSHA string, startIndex int)

	// ExcludedAuthor returns the author login to exclude when checking for
	// new human replies during triage. On GitHub this is the review bot;
	// locally it returns "" (no author to exclude).
	ExcludedAuthor(threads []ReviewThread) string

	// HandleResolutions processes triage results. On GitHub this resolves
	// threads and posts acknowledgment replies. Locally this is a no-op
	// (the caller removes resolved findings from state).
	HandleResolutions(threads []ReviewThread, fixed []fixedThread)

	// Publish outputs the review results. On GitHub this posts review
	// comments. Locally this prints to stdout.
	Publish(result *ReviewResult, pr *PRData, threads []ReviewThread, fixed []fixedThread) error

	// SaveState persists findings for future incremental reviews.
	SaveState(result *ReviewResult, stillOpen []Finding, isIncremental bool) error

	// GetIncrementalDiff returns what changed on the PR since previousSHA
	// was reviewed (see IncrementalDiff; rebase-aware, so a rebase without
	// author changes yields ""). pr supplies the base branch and file set.
	// In CI this is committed changes only. Locally it also includes
	// uncommitted working-tree changes scoped to the PR files.
	GetIncrementalDiff(previousSHA string, pr *PRData) (string, error)

	// ReportUsage handles usage data (best-effort). On GitHub this writes
	// to GITHUB_ENV. Locally this prints a usage table to stderr.
	ReportUsage(tracker *UsageTracker)

	// SkipReview reports whether this run should not review at all under the
	// config, and settles what a skipped run still owes. On GitHub that is
	// review_on: ready on a push to a ready PR; the new HEAD still gets a
	// commit status, derived from the open threads, so a required check
	// doesn't block the PR. Locally it never skips.
	SkipReview(cfg *ReviewConfig, replyOnly bool) (bool, error)

	// ReportFailure tells the PR that a run failed (best-effort), so a
	// failed run never looks like a clean review or leaves a required check
	// waiting with no explanation. On GitHub it posts a PR notice; the
	// "error" commit status is set by Run through SetRunStatus. Locally it
	// does nothing: the error is already printed.
	ReportFailure(runErr error, replyOnly bool)

	// LoadRules loads the config and locates the other rule sources (project
	// docs, Claude rules, context_files). On GitHub they come from the PR's
	// base commit, so a PR cannot change the rules it is reviewed by; locally
	// from the working tree.
	LoadRules(pr *PRData, configPath string) (*RuleSources, error)

	// SetRunStatus reports the run's state for the commit it reviews:
	// "pending" while it runs, "error" when it could not finish, "success"
	// for a review the config skips. On GitHub this is the review's commit
	// status; locally a no-op. Best-effort: failures are logged.
	SetRunStatus(sha, state, desc string)

	// PublishGate reports a review that did not run because the PR is not
	// ready for one (e.g. no intent section): the blockers fail the status.
	// The head is not marked reviewed, so the next run reviews it in full.
	PublishGate(sha string, blockers []string) error

	// AnswerRejectedReplies answers, once, each author reply triage did not
	// accept (resolve_accepted_replies). On GitHub a reply on the thread;
	// locally a no-op. rejected carries the evaluator's rationale.
	AnswerRejectedReplies(threads []ReviewThread, rejected []fixedThread)
}

// combineFindings strips the Status field from stillOpen findings (so persisted
// state doesn't carry triage labels) and merges them with new findings.
func combineFindings(stillOpen, newFindings []Finding) []Finding {
	var surviving []Finding
	for _, f := range stillOpen {
		sf := f
		sf.Status = ""
		surviving = append(surviving, sf)
	}
	return mergeFindings(surviving, newFindings)
}
