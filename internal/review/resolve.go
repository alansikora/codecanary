package review

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// resolveThreadsOutput is the step output through which a review hands the
// threads its token could not resolve to the action's resolve step
// (GithubPlatform.resolveOrDefer writes it, `codecanary resolve` reads it).
const resolveThreadsOutput = "resolve_threads"

// resolveStatusOutput carries the review status posted with deferred threads
// (a ResolveStatus as JSON), for ReportUnresolvedThreads.
const resolveStatusOutput = "resolve_status"

// resolveErrorOutput is the resolve step's one-line reason, written when a
// thread stays open, for ReportUnresolvedThreads.
const resolveErrorOutput = "resolve_error"

// ResolveStatus is the review commit status a run posted.
type ResolveStatus struct {
	SHA         string `json:"sha"`
	Context     string `json:"context"`
	Description string `json:"description"`
}

// commitStatusDescriptionLimit is GitHub's cap on a commit status
// description.
const commitStatusDescriptionLimit = 140

// isResolvePermissionError reports whether err is GitHub refusing
// resolveReviewThread for lack of permission, as opposed to the call failing.
func isResolvePermissionError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "Resource not accessible")
}

// ParseThreadIDs splits the comma-separated `resolve_threads` output into
// thread IDs, dropping blanks and duplicates.
func ParseThreadIDs(list string) []string {
	var ids []string
	seen := make(map[string]bool)
	for _, id := range strings.Split(list, ",") {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

// ResolveFindingThreads resolves the given review threads on the PR. It is
// the action's resolve step: it runs after the review, with a token that can
// resolve threads, and takes nothing from the review but thread IDs. An ID is
// resolved only when it is one of this PR's CodeCanary finding threads, so
// whatever wrote the list cannot close anyone else's conversation. The error
// names every thread left open.
func ResolveFindingThreads(repo string, prNumber int, ids []string) error {
	threads, err := FetchReviewThreads(repo, prNumber)
	if err != nil {
		return fmt.Errorf("fetching review threads: %w", err)
	}
	err = resolveFindingThreads(threads, ids, ResolveThread)
	if err != nil {
		if werr := writeGithubOutput(resolveErrorOutput, err.Error()); werr != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not write the %s output: %v\n", resolveErrorOutput, werr)
		}
	}
	return err
}

// ReportUnresolvedThreads runs when the action's resolve step could not
// resolve the threads a review settled: the resolve token was refused, or
// the resolve itself failed. The review already posted its status, often
// success, and the job is about to end red for a reason that status doesn't
// show. This sets the status to error, keeping the review's counts and
// adding why the threads stay open. It runs with the review's token, which
// can post statuses; the resolve token can't.
//
// statusJSON is the review's `resolve_status` output. tokenErr is what the
// token service said, resolveErr the resolve step's `resolve_error`; either
// may be empty.
func ReportUnresolvedThreads(repo string, ids []string, statusJSON, tokenErr, resolveErr string) error {
	var st ResolveStatus
	if err := json.Unmarshal([]byte(statusJSON), &st); err != nil || st.SHA == "" {
		return errors.New("--status must be the review step's resolve_status output")
	}
	desc := unresolvedStatusDescription(st.Description, len(ids), tokenErr, resolveErr)
	if err := PostReviewCommitStatus(repo, st.Context, st.SHA, "error", desc); err != nil {
		return fmt.Errorf("posting the %s status: %w", st.Context, err)
	}
	Stderrf(ansiYellow, "Posted %s = error on %s (%s)\n", st.Context, shortSHA(st.SHA), desc)
	return nil
}

// unresolvedStatusDescription keeps the review's own description and adds
// why its resolved threads are still open, within GitHub's length cap.
func unresolvedStatusDescription(reviewDesc string, n int, tokenErr, resolveErr string) string {
	var why string
	switch {
	case strings.TrimSpace(tokenErr) != "":
		why = "resolve token refused: " + strings.TrimSpace(tokenErr)
	case strings.TrimSpace(resolveErr) != "":
		why = strings.TrimSpace(resolveErr)
	default:
		why = "the resolve step failed; see the workflow log"
	}
	threads := "thread"
	if n != 1 {
		threads = "threads"
	}
	msg := fmt.Sprintf("%d resolved %s still open (%s)", n, threads, why)
	if reviewDesc != "" {
		msg = reviewDesc + "; " + msg
	}
	msg = strings.Join(strings.Fields(msg), " ")
	if len(msg) > commitStatusDescriptionLimit {
		msg = truncateUTF8(msg, commitStatusDescriptionLimit-len("…"))
	}
	return msg
}

func resolveFindingThreads(threads []ReviewThread, ids []string, resolve func(threadID string) error) error {
	byID := make(map[string]ReviewThread, len(threads))
	for _, t := range threads {
		byID[t.ID] = t
	}
	var failed []string
	for _, id := range ids {
		t, ok := byID[id]
		if !ok {
			fmt.Fprintf(os.Stderr, "  ! %s — not a CodeCanary finding thread on this PR; left alone\n", id)
			failed = append(failed, id)
			continue
		}
		label := threadLabel(t)
		if t.Resolved {
			fmt.Fprintf(os.Stderr, "  = %s (already resolved)\n", label)
			continue
		}
		if err := resolve(id); err != nil {
			fmt.Fprintf(os.Stderr, "  ! %s — could not resolve the thread: %v\n", label, err)
			failed = append(failed, label)
			continue
		}
		fmt.Fprintf(os.Stderr, "  ✓ %s (resolved)\n", label)
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d thread(s) could not be resolved and stay open on the PR: %s",
			len(failed), len(ids), strings.Join(failed, "; "))
	}
	return nil
}
