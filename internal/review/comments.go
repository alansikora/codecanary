package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// runGhJSON runs `gh <args...>` capturing stdout and — on failure —
// surfacing gh's stderr in the returned error. Plain `exec.Output()`
// drops stderr, making gh auth / rate-limit failures opaque.
func runGhJSON(args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("%w: %s",
				err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, err
	}
	return out, nil
}

// BotLogin is the GitHub login of the codecanary review bot that posts
// findings on PRs. Comments from any other author are ignored when
// collecting findings.
const BotLogin = "codecanary-bot[bot]"

// reviewCheckName is the name of the GitHub check emitted by the codecanary
// action. WaitForReview polls for this check to reach COMPLETED.
const reviewCheckName = "review"

// PRReviewComment mirrors the subset of the GitHub review comment payload
// we care about. Fields follow the `gh api repos/.../pulls/N/comments`
// response shape.
type PRReviewComment struct {
	ID   int64 `json:"id"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	Body         string `json:"body"`
	Path         string `json:"path"`
	Line         int    `json:"line"`
	OriginalLine int    `json:"original_line"`
	CommitID     string `json:"commit_id"`
	CreatedAt    string `json:"created_at"`
	HTMLURL      string `json:"html_url"`
}

// PRFinding is a Finding augmented with the GitHub comment context it was
// posted from. Used by the `codecanary findings` command.
type PRFinding struct {
	Finding
	CommentURL string `json:"comment_url,omitempty"`
	CommitID   string `json:"commit_id,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
}

// FetchPRComments returns all line-anchored review comments on the given
// PR, oldest first. Uses gh API pagination (`--paginate`) which emits one
// JSON array per page concatenated back-to-back; we decode them as a
// stream rather than string-splicing, so comment bodies that legitimately
// contain a "][" sequence survive unscathed.
func FetchPRComments(repo string, prNumber int) ([]PRReviewComment, error) {
	owner, name, err := parseRepoSlug(repo)
	if err != nil {
		return nil, err
	}
	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/comments", owner, name, prNumber)
	out, err := runGhJSON("api", "--paginate", apiPath)
	if err != nil {
		return nil, fmt.Errorf("fetching PR comments: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	var all []PRReviewComment
	for {
		var page []PRReviewComment
		if err := dec.Decode(&page); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("parsing PR comments page: %w", err)
		}
		all = append(all, page...)
	}
	return all, nil
}

// ParseFindingMarkers filters to bot-authored comments and extracts the
// embedded `<!-- codecanary:finding {...} -->` JSON marker from each body.
// Comments without a finding marker are silently skipped.
func ParseFindingMarkers(comments []PRReviewComment) []PRFinding {
	var out []PRFinding
	for _, c := range comments {
		if c.User.Login != BotLogin {
			continue
		}
		idx := strings.Index(c.Body, findingMarkerPrefix)
		if idx < 0 {
			continue
		}
		start := idx + len(findingMarkerPrefix)
		endIdx := strings.Index(c.Body[start:], reviewMarkerSuffix)
		if endIdx < 0 {
			continue
		}
		jsonData := c.Body[start : start+endIdx]
		var f Finding
		if err := json.Unmarshal([]byte(jsonData), &f); err != nil {
			continue
		}
		out = append(out, PRFinding{
			Finding:    f,
			CommentURL: c.HTMLURL,
			CommitID:   c.CommitID,
			CreatedAt:  c.CreatedAt,
		})
	}
	return out
}

// ReviewStatus captures the state of the codecanary `review` check on a PR,
// along with the PR's current head SHA for convenience.
type ReviewStatus struct {
	Status     string `json:"status"`     // queued | in_progress | completed | ""
	Conclusion string `json:"conclusion"` // success | failure | cancelled | ""
	HeadSHA    string `json:"head_sha"`
}

// ghStatusRollup is the subset of `gh pr view --json ...` we parse.
type ghStatusRollup struct {
	HeadRefOid       string `json:"headRefOid"`
	StatusCheckRollup []struct {
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	} `json:"statusCheckRollup"`
}

// FetchReviewStatus queries the current state of the `review` check for
// the given PR. Returns a ReviewStatus with empty Status/Conclusion if
// the check isn't present (e.g. the action hasn't started yet).
func FetchReviewStatus(repo string, prNumber int) (ReviewStatus, error) {
	args := []string{"pr", "view", fmt.Sprintf("%d", prNumber),
		"--json", "headRefOid,statusCheckRollup"}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	out, err := runGhJSON(args...)
	if err != nil {
		return ReviewStatus{}, fmt.Errorf("gh pr view: %w", err)
	}
	var rollup ghStatusRollup
	if err := json.Unmarshal(out, &rollup); err != nil {
		return ReviewStatus{}, fmt.Errorf("parsing pr view: %w", err)
	}
	rs := ReviewStatus{HeadSHA: rollup.HeadRefOid}
	for _, c := range rollup.StatusCheckRollup {
		if strings.EqualFold(c.Name, reviewCheckName) {
			rs.Status = strings.ToLower(c.Status)
			rs.Conclusion = strings.ToLower(c.Conclusion)
			break
		}
	}
	return rs, nil
}

// WaitForReview polls until the `review` check reaches the COMPLETED state
// for the given PR. Progress is printed to stderr as a dot per poll so
// stdout stays clean for JSON consumption. If timeout is zero, waits
// indefinitely.
func WaitForReview(repo string, prNumber int, timeout time.Duration) (ReviewStatus, error) {
	return waitForReview(repo, prNumber, timeout, 15*time.Second, os.Stderr)
}

// waitForReview is the testable inner loop — injectable poll interval and
// progress sink so tests don't actually sleep or write to stderr.
func waitForReview(
	repo string,
	prNumber int,
	timeout, pollInterval time.Duration,
	progress io.Writer,
) (ReviewStatus, error) {
	deadline := time.Time{}
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	for {
		status, err := FetchReviewStatus(repo, prNumber)
		if err != nil {
			return status, err
		}
		if status.Status == "completed" {
			_, _ = fmt.Fprintln(progress)
			return status, nil
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return status, fmt.Errorf(
				"timed out after %s waiting for review check on PR #%d (last status=%q)",
				timeout, prNumber, status.Status)
		}
		_, _ = fmt.Fprint(progress, ".")
		time.Sleep(pollInterval)
	}
}
