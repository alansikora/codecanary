package review

import (
	"fmt"
	"os"
	"strings"
)

// resolveThreadsOutput is the step output through which a review hands the
// threads its token could not resolve to the action's resolve step
// (GithubPlatform.resolveOrDefer writes it, `codecanary resolve` reads it).
const resolveThreadsOutput = "resolve_threads"

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
	return resolveFindingThreads(threads, ids, ResolveThread)
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
