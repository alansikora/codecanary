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

	// GetIncrementalDiff returns the diff since the given SHA.
	// In CI this is committed changes only. Locally it also includes
	// uncommitted working-tree changes scoped to the given file set.
	GetIncrementalDiff(baseSHA string, prFiles []string) (string, error)

	// ReportUsage handles usage data (best-effort). On GitHub this writes
	// to GITHUB_ENV. Locally this prints a usage table to stderr.
	ReportUsage(tracker *UsageTracker)
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
