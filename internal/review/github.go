package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// runGH runs a gh command and returns stdout; on failure, the returned error
// includes gh's stderr so upstream API messages surface in logs instead of just
// "exit status 1".
func runGH(label string, args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		return nil, fmt.Errorf("%s: %s: %w", label, msg, err)
	}
	return out, nil
}

// parseRepoSlug splits a "owner/name" repository slug into its two parts.
func parseRepoSlug(repo string) (owner, name string, err error) {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid repo format %q, expected owner/name", repo)
	}
	return parts[0], parts[1], nil
}

// MaxFindingProximity is the maximum number of lines a finding may be from the
// nearest changed line in the PR diff. Findings beyond this distance are dropped
// (runner.go) or demoted from inline to body (PostReview). This enforces review
// scope — keeping findings anchored to the PR's actual changes — and catches
// hallucinated line numbers. A single constant ensures both checks stay in sync.
const MaxFindingProximity = 20

// HTML comment markers for embedding and detecting review data.
// Dual prefixes support both current (codecanary) and legacy (clanopy) markers.
var reviewMarkerPrefixes = []string{"<!-- codecanary:review ", "<!-- clanopy:review "}

const (
	reviewMarkerSuffix = " -->"
	findingMarkerPrefix = "<!-- codecanary:finding "
	ackMarkerPrefix     = "<!-- codecanary:ack:"
	legacyAckPrefix     = "<!-- clanopy:ack:"
)

// PRData holds PR metadata and diff.
type PRData struct {
	Number       int
	Title        string
	Body         string
	Author       string
	BaseBranch   string
	HeadBranch   string
	Diff         string
	FullDiff     string            // unfiltered diff for finding validation (set by prepareReview)
	Files        []string
	FileContents map[string]string // path -> full file content
}

// ValidationDiff returns the unfiltered diff for finding validation. When
// files were skipped during prepareReview, FullDiff holds the original diff
// while Diff is filtered for the LLM prompt.
func (pr *PRData) ValidationDiff() string {
	if pr.FullDiff != "" {
		return pr.FullDiff
	}
	return pr.Diff
}

// ghPRView is the JSON shape returned by gh pr view.
type ghPRView struct {
	Title       string `json:"title"`
	Body        string `json:"body"`
	Author      struct {
		Login string `json:"login"`
	} `json:"author"`
	BaseRefName string `json:"baseRefName"`
	HeadRefName string `json:"headRefName"`
	Files       []struct {
		Path string `json:"path"`
	} `json:"files"`
}

// FetchPR gets PR metadata and diff using the gh CLI.
func FetchPR(repo string, number int) (*PRData, error) {
	numStr := fmt.Sprintf("%d", number)

	// Fetch PR metadata as JSON.
	viewOut, err := runGH("gh pr view", "pr", "view", numStr,
		"--repo", repo,
		"--json", "title,body,author,baseRefName,headRefName,files",
	)
	if err != nil {
		return nil, err
	}

	var view ghPRView
	if err := json.Unmarshal(viewOut, &view); err != nil {
		return nil, fmt.Errorf("parsing gh pr view output: %w", err)
	}

	// Fetch the diff. This hits GitHub's pull request diff API
	// (GET /repos/{owner}/{repo}/pulls/{n} with Accept: application/vnd.github.v3.diff).
	diffOut, err := runGH("gh pr diff", "pr", "diff", numStr, "--repo", repo)
	if err != nil {
		return nil, fmt.Errorf("%w (GitHub pull request diff API; if this looks like a transient 5xx/timeout, retrying the job may help)", err)
	}

	files := make([]string, len(view.Files))
	for i, f := range view.Files {
		files[i] = f.Path
	}

	return &PRData{
		Number:     number,
		Title:      view.Title,
		Body:       view.Body,
		Author:     view.Author.Login,
		BaseBranch: view.BaseRefName,
		HeadBranch: view.HeadRefName,
		Diff:       string(diffOut),
		Files:      files,
	}, nil
}

// PostComment posts a review comment on a PR.
func PostComment(repo string, number int, body string) error {
	numStr := fmt.Sprintf("%d", number)
	cmd := exec.Command("gh", "pr", "comment", numStr,
		"--repo", repo,
		"--body", body,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh pr comment: %w\n%s", err, string(out))
	}
	return nil
}

// reviewPayload is the JSON structure for the GitHub PR review API.
type reviewPayload struct {
	Event    string          `json:"event"`
	Body     string          `json:"body"`
	Comments []reviewComment `json:"comments"`
	CommitID string          `json:"commit_id,omitempty"`
}

// reviewComment is a single inline comment in a PR review.
type reviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Body string `json:"body"`
}

// countDiffLines counts the number of added and removed lines in a unified diff.
func countDiffLines(diff string) (added, removed int) {
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			continue
		}
		if strings.HasPrefix(line, "+") {
			added++
		} else if strings.HasPrefix(line, "-") {
			removed++
		}
	}
	return
}

// diffLineMap maps each file to its sorted list of valid line numbers from the diff.
type diffLineMap map[string][]int

// parseDiffLines extracts valid line numbers per file from a unified diff.
func parseDiffLines(diff string) diffLineMap {
	valid := make(diffLineMap)
	var currentFile string
	var lineNum int

	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+++ b/") {
			currentFile = line[6:]
			continue
		}
		if strings.HasPrefix(line, "@@ ") {
			// Parse hunk header: @@ -old,count +new,count @@
			if idx := strings.Index(line, "+"); idx >= 0 {
				rest := line[idx+1:]
				if comma := strings.IndexAny(rest, ", "); comma >= 0 {
					rest = rest[:comma]
				}
				_, _ = fmt.Sscanf(rest, "%d", &lineNum)
			}
			continue
		}
		if currentFile == "" || lineNum == 0 {
			continue
		}
		if strings.HasPrefix(line, "-") {
			continue
		}
		if strings.HasPrefix(line, "+") {
			valid[currentFile] = append(valid[currentFile], lineNum)
			lineNum++
			continue
		}
		if strings.HasPrefix(line, " ") {
			lineNum++
			continue
		}
	}
	return valid
}

// nearestLine returns the closest valid diff line for a file:line pair.
// Returns the line itself if valid, the nearest valid line in that file,
// or 0 if the file is not in the diff at all.
func (d diffLineMap) nearestLine(file string, line int) int {
	lines, ok := d[file]
	if !ok || len(lines) == 0 {
		return 0
	}
	best := lines[0]
	bestDist := abs(line - best)
	for _, l := range lines[1:] {
		dist := abs(line - l)
		if dist < bestDist {
			best = l
			bestDist = dist
		}
	}
	return best
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// PostReview posts a PR review with inline comments using the GitHub API.
// Findings with file and line information become inline comments; others are
// included in the review body. The summary block is appended to the body so
// the status dashboard appears on every CodeCanary top-level review.
func PostReview(repo string, prNumber int, result *ReviewResult, diff string, commitSHA string, summary ReviewSummary) error {
	// Sort findings by severity before formatting.
	sortFindings(result.Findings)

	// Parse the diff to find valid line positions for inline comments.
	validLines := parseDiffLines(diff)

	// A finding can be inlined if its file is in the diff and the nearest
	// valid line is within a reasonable distance. Without a bound, findings
	// about code far from the diff get silently snapped to unrelated lines.
	canInline := func(f Finding) bool {
		if f.File == "" || f.Line <= 0 {
			return false
		}
		nearest := validLines.nearestLine(f.File, f.Line)
		return nearest > 0 && abs(f.Line-nearest) <= MaxFindingProximity
	}

	comments := make([]reviewComment, 0)
	for _, f := range result.Findings {
		if canInline(f) {
			comments = append(comments, reviewComment{
				Path: f.File,
				Line: validLines.nearestLine(f.File, f.Line),
				Body: FormatFindingComment(&f),
			})
		}
	}

	body := withSummary(FormatReviewBody(result, canInline), summary)

	payload := reviewPayload{
		Event:    "COMMENT",
		Body:     body,
		Comments: comments,
		CommitID: commitSHA,
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling review payload: %w", err)
	}

	owner, name, err := parseRepoSlug(repo)
	if err != nil {
		return err
	}

	// No fallback — if this fails, the apiError carries stderr and response
	// body so the caller surfaces full diagnostics for debugging.
	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, name, prNumber)
	_, err = ghAPIPOST(apiPath, payloadJSON)
	return err
}

// ReviewCommitStatusContext is the commit-status context the review bot
// writes when a review run completes. Using a stable string lets teams
// require it in branch protection — "CodeCanary / review must be green
// before merge." Matches the context used by `codecanary signoff`, so
// both the bot (on PRs with the workflow) and a local signoff can
// satisfy the same required check.
const ReviewCommitStatusContext = "CodeCanary / review"

// PostReviewCommitStatus POSTs a commit status on the given SHA summarising
// the review outcome. state is "success" when all findings are resolved or
// handled by the author, "failure" otherwise. Description is shown on the
// PR checks list, so keep it short and human-readable. Errors are returned
// so the caller can log them — a failed status post should not abort the
// larger Publish flow, since the review itself has already been posted.
func PostReviewCommitStatus(repo, sha, state, description string) error {
	if sha == "" {
		return fmt.Errorf("commit status requires a SHA")
	}
	owner, name, err := parseRepoSlug(repo)
	if err != nil {
		return err
	}
	payload := map[string]string{
		"state":       state,
		"context":     ReviewCommitStatusContext,
		"description": description,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling commit status payload: %w", err)
	}
	apiPath := fmt.Sprintf("repos/%s/%s/statuses/%s", owner, name, sha)
	_, err = ghAPIPOST(apiPath, payloadJSON)
	return err
}

// FetchReviewFromPR extracts cached review data from a PR review's hidden HTML tag.
func FetchReviewFromPR(repo string, prNumber int) (*ReviewResult, error) {
	owner, name, err := parseRepoSlug(repo)
	if err != nil {
		return nil, err
	}

	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, name, prNumber)
	out, err := exec.Command("gh", "api", apiPath).Output()
	if err != nil {
		return nil, fmt.Errorf("fetching PR reviews: %w", err)
	}

	var reviews []ghReview
	if err := json.Unmarshal(out, &reviews); err != nil {
		return nil, fmt.Errorf("parsing PR reviews: %w", err)
	}

	prefixes := reviewMarkerPrefixes
	const suffix = " -->"

	// Search from most recent to oldest.
	for i := len(reviews) - 1; i >= 0; i-- {
		body := reviews[i].Body
		for _, prefix := range prefixes {
			idx := strings.Index(body, prefix)
			if idx < 0 {
				continue
			}
			start := idx + len(prefix)
			endIdx := strings.Index(body[start:], suffix)
			if endIdx < 0 {
				continue
			}
			jsonData := body[start : start+endIdx]
			var result ReviewResult
			if err := json.Unmarshal([]byte(jsonData), &result); err != nil {
				continue
			}
			return &result, nil
		}
	}

	return nil, fmt.Errorf("no review data found in PR #%d reviews", prNumber)
}

// FetchFindingFromPR searches all reviews on a PR for a specific fix_ref.
// Unlike FetchReviewFromPR (which returns the latest review), this searches every
// review so that fix_ref links from older review rounds still resolve correctly.
func FetchFindingFromPR(repo string, prNumber int, fixRef string) (*Finding, error) {
	owner, name, err := parseRepoSlug(repo)
	if err != nil {
		return nil, err
	}

	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, name, prNumber)
	out, err := exec.Command("gh", "api", apiPath).Output()
	if err != nil {
		return nil, fmt.Errorf("fetching PR reviews: %w", err)
	}

	var reviews []ghReview
	if err := json.Unmarshal(out, &reviews); err != nil {
		return nil, fmt.Errorf("parsing PR reviews: %w", err)
	}

	prefixes := reviewMarkerPrefixes
	const suffix = " -->"

	for _, rev := range reviews {
		for _, prefix := range prefixes {
			idx := strings.Index(rev.Body, prefix)
			if idx < 0 {
				continue
			}
			start := idx + len(prefix)
			endIdx := strings.Index(rev.Body[start:], suffix)
			if endIdx < 0 {
				continue
			}
			var result ReviewResult
			if err := json.Unmarshal([]byte(rev.Body[start:start+endIdx]), &result); err != nil {
				continue
			}
			for i := range result.Findings {
				if result.Findings[i].FixRef == fixRef {
					return &result.Findings[i], nil
				}
			}
		}
	}

	return nil, fmt.Errorf("fix_ref %q not found in any review on PR #%d", fixRef, prNumber)
}

// DetectRepo gets owner/name from the current git remote.
func DetectRepo() (string, error) {
	out, err := exec.Command("gh", "repo", "view",
		"--json", "nameWithOwner",
		"--jq", ".nameWithOwner",
	).Output()
	if err != nil {
		return "", fmt.Errorf("gh repo view: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// DetectPRNumber detects the PR number for the current branch using gh.
// If repo is non-empty, it is passed as --repo to scope the lookup.
func DetectPRNumber(repo string) (int, error) {
	args := []string{"pr", "view", "--json", "number", "--jq", ".number"}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	out, err := exec.Command("gh", args...).Output()
	if err != nil {
		return 0, fmt.Errorf("no open pull request found for the current branch")
	}
	num, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("unexpected PR number from gh: %w", err)
	}
	return num, nil
}

// ThreadReply represents a reply to a review thread (i.e. any comment after the first).
type ThreadReply struct {
	Author string
	Body   string
}

// ReviewThread represents a review thread from a PR.
type ReviewThread struct {
	ID       string
	Path     string
	Line     int
	Body     string
	Author   string // login of the first comment author (the bot for review threads)
	Outdated bool   // true if GitHub marked the comment position as outdated (code changed)
	Resolved bool
	Replies  []ThreadReply
}

// graphQLThreadsResponse is the JSON shape returned by the review threads query.
type graphQLThreadsResponse struct {
	Data struct {
		Repository struct {
			PullRequest struct {
				ReviewThreads struct {
					Nodes []struct {
						ID         string `json:"id"`
						IsResolved bool   `json:"isResolved"`
						Comments struct {
							Nodes []struct {
								Body         string `json:"body"`
								Path         string `json:"path"`
								Line         int    `json:"line"`
								OriginalLine int    `json:"originalLine"`
								Outdated     bool   `json:"outdated"`
								Author       struct {
									Login string `json:"login"`
								} `json:"author"`
							} `json:"nodes"`
						} `json:"comments"`
					} `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
}

// FetchReviewThreads gets all review threads from a PR via GraphQL.
func FetchReviewThreads(repo string, prNumber int) ([]ReviewThread, error) {
	owner, name, err := parseRepoSlug(repo)
	if err != nil {
		return nil, err
	}

	query := `query($owner:String!,$name:String!,$pr:Int!){
  repository(owner:$owner,name:$name){
    pullRequest(number:$pr){
      reviewThreads(first:100){
        nodes{
          id
          isResolved
          comments(first:100){
            nodes{body path line originalLine outdated author{login}}
          }
        }
      }
    }
  }
}`

	cmd := exec.Command("gh", "api", "graphql",
		"-f", "query="+query,
		"-f", fmt.Sprintf("owner=%s", owner),
		"-f", fmt.Sprintf("name=%s", name),
		"-F", fmt.Sprintf("pr=%d", prNumber),
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh api graphql: %w", err)
	}

	var resp graphQLThreadsResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parsing graphql response: %w", err)
	}

	var threads []ReviewThread
	for _, node := range resp.Data.Repository.PullRequest.ReviewThreads.Nodes {
		if len(node.Comments.Nodes) == 0 {
			continue
		}
		comment := node.Comments.Nodes[0]

		// Filter to review threads only (new marker + legacy markers for backward compat).
		if !strings.Contains(comment.Body, findingMarkerPrefix) &&
			!strings.Contains(comment.Body, "codecanary fix") &&
			!strings.Contains(comment.Body, "clanopy fix") {
			continue
		}

		var replies []ThreadReply
		for _, c := range node.Comments.Nodes[1:] {
			replies = append(replies, ThreadReply{
				Author: c.Author.Login,
				Body:   c.Body,
			})
		}

		line := comment.Line
		if comment.Outdated && line == 0 && comment.OriginalLine > 0 {
			line = comment.OriginalLine
		}

		threads = append(threads, ReviewThread{
			ID:       node.ID,
			Path:     comment.Path,
			Line:     line,
			Body:     comment.Body,
			Author:   comment.Author.Login,
			Outdated: comment.Outdated,
			Resolved: node.IsResolved,
			Replies:  replies,
		})
	}

	return threads, nil
}

// ResolveThread resolves a review thread via GraphQL mutation.
func ResolveThread(threadID string) error {
	cmd := exec.Command("gh", "api", "graphql",
		"-f", "query=mutation($threadId:ID!){resolveReviewThread(input:{threadId:$threadId}){thread{isResolved}}}",
		"-f", fmt.Sprintf("threadId=%s", threadID),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh api graphql resolve: %w\n%s", err, string(out))
	}
	return nil
}

// ReplyToThread posts a reply on a review thread via GraphQL.
func ReplyToThread(threadID, body string) error {
	cmd := exec.Command("gh", "api", "graphql",
		"-f", "query=mutation($threadId:ID!,$body:String!){addPullRequestReviewThreadReply(input:{pullRequestReviewThreadId:$threadId,body:$body}){comment{id}}}",
		"-f", fmt.Sprintf("threadId=%s", threadID),
		"-f", fmt.Sprintf("body=%s", body),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh api graphql reply: %w\n%s", err, string(out))
	}
	return nil
}

// ghReview is the JSON shape for a PR review from the REST API.
type ghReview struct {
	ID     int64  `json:"id"`
	NodeID string `json:"node_id"`
	Body   string `json:"body"`
}

// LatestCodecanaryReview captures the identity and body of the most recent
// CodeCanary top-level review, together with the commit SHA embedded in its
// hidden marker. Returned by FetchLatestCodecanaryReview for the
// "edit if same SHA, otherwise post new" publish decision.
type LatestCodecanaryReview struct {
	ID   int64
	SHA  string
	Body string
}

// FetchLatestCodecanaryReview returns the most recent top-level review on
// the PR that carries a CodeCanary marker, along with the commit SHA
// embedded in that marker. Returns (nil, nil) when no matching review
// exists. A non-nil error is returned only for transport/parsing failures.
func FetchLatestCodecanaryReview(repo string, prNumber int) (*LatestCodecanaryReview, error) {
	owner, name, err := parseRepoSlug(repo)
	if err != nil {
		return nil, err
	}

	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, name, prNumber)
	out, err := exec.Command("gh", "api", apiPath).Output()
	if err != nil {
		return nil, fmt.Errorf("fetching PR reviews: %w", err)
	}

	var reviews []ghReview
	if err := json.Unmarshal(out, &reviews); err != nil {
		return nil, fmt.Errorf("parsing PR reviews: %w", err)
	}

	// Walk newest → oldest so we return the latest CodeCanary review.
	for i := len(reviews) - 1; i >= 0; i-- {
		rev := reviews[i]
		for _, prefix := range reviewMarkerPrefixes {
			idx := strings.Index(rev.Body, prefix)
			if idx < 0 {
				continue
			}
			start := idx + len(prefix)
			endIdx := strings.Index(rev.Body[start:], reviewMarkerSuffix)
			if endIdx < 0 {
				continue
			}
			jsonData := rev.Body[start : start+endIdx]
			var payload struct {
				SHA string `json:"sha"`
			}
			// Ignore unmarshal errors so old reviews that embed a richer
			// ReviewResult (also containing a "sha" field) still match.
			_ = json.Unmarshal([]byte(jsonData), &payload)
			return &LatestCodecanaryReview{
				ID:   rev.ID,
				SHA:  payload.SHA,
				Body: rev.Body,
			}, nil
		}
	}

	return nil, nil
}

// UpdateReviewBody replaces the body of an existing PR review via the
// GitHub REST API. Used when a reply-only run (or a duplicate synchronize
// webhook on the same HEAD) needs to refresh the latest CodeCanary review's
// counts instead of posting a new top-level comment.
func UpdateReviewBody(repo string, prNumber int, reviewID int64, body string) error {
	owner, name, err := parseRepoSlug(repo)
	if err != nil {
		return err
	}

	payload, err := json.Marshal(struct {
		Body string `json:"body"`
	}{Body: body})
	if err != nil {
		return fmt.Errorf("marshaling update payload: %w", err)
	}

	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews/%d", owner, name, prNumber, reviewID)
	_, err = ghAPIRequest("PUT", apiPath, payload)
	return err
}

// ghAPIRequest is a generic gh api wrapper that preserves the temp-file
// pattern used by ghAPIPOST (avoids stdin pipe truncation on large bodies).
func ghAPIRequest(method, apiPath string, payloadJSON []byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "codecanary-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	tmpFile, err := os.CreateTemp(dir, "payload.json")
	if err != nil {
		return nil, fmt.Errorf("creating temp file: %w", err)
	}
	if _, err := tmpFile.Write(payloadJSON); err != nil {
		_ = tmpFile.Close()
		return nil, fmt.Errorf("writing payload to temp file: %w", err)
	}
	_ = tmpFile.Close()

	cmd := exec.Command("gh", "api", apiPath, "--method", method, "--input", tmpFile.Name())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), &apiError{Err: err, Stderr: stderr.String(), Response: stdout.String()}
	}
	return stdout.Bytes(), nil
}

// FetchPreviousReviewSHA gets the SHA from the last review's hidden data.
func FetchPreviousReviewSHA(repo string, prNumber int) string {
	owner, name, err := parseRepoSlug(repo)
	if err != nil {
		return ""
	}

	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, name, prNumber)
	out, err := exec.Command("gh", "api", apiPath).Output()
	if err != nil {
		return ""
	}

	var reviews []ghReview
	if err := json.Unmarshal(out, &reviews); err != nil {
		return ""
	}

	prefixes := reviewMarkerPrefixes
	const suffix = " -->"

	// Search from most recent to oldest.
	for i := len(reviews) - 1; i >= 0; i-- {
		body := reviews[i].Body
		for _, prefix := range prefixes {
			idx := strings.Index(body, prefix)
			if idx < 0 {
				continue
			}
			start := idx + len(prefix)
			endIdx := strings.Index(body[start:], suffix)
			if endIdx < 0 {
				continue
			}
			jsonData := body[start : start+endIdx]
			var result ReviewResult
			if err := json.Unmarshal([]byte(jsonData), &result); err != nil {
				continue
			}
			if result.SHA != "" {
				return result.SHA
			}
		}
	}

	return ""
}

// FilesFromDiff extracts the list of file paths touched in a unified diff.
func FilesFromDiff(diff string) []string {
	var files []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+++ b/") {
			path := strings.TrimRight(line[6:], "\r")
			if !seen[path] {
				seen[path] = true
				files = append(files, path)
			}
		}
	}
	return files
}

// ScopeDiffToFiles filters a unified diff to only include hunks for files in
// the allowed set. This prevents rebase noise (main-branch changes) from
// leaking into incremental reviews.
func ScopeDiffToFiles(diff string, allowedFiles map[string]bool) string {
	if len(allowedFiles) == 0 {
		return diff
	}

	lines := strings.Split(diff, "\n")
	var result []string
	blockStart := -1
	blockAllowed := false

	for i := 0; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "diff --git") {
			// Flush previous block if allowed.
			if blockStart >= 0 && blockAllowed {
				result = append(result, lines[blockStart:i]...)
			}
			blockStart = i
			blockAllowed = false

			// Look ahead for +++ b/<path> to determine if this block is allowed.
			for j := i + 1; j < len(lines) && !strings.HasPrefix(lines[j], "diff --git"); j++ {
				if strings.HasPrefix(lines[j], "+++ b/") {
					path := strings.TrimRight(lines[j][6:], "\r")
					if allowedFiles[path] {
						blockAllowed = true
					}
					break
				}
			}
			continue
		}
	}

	// Flush last block.
	if blockStart >= 0 && blockAllowed {
		result = append(result, lines[blockStart:]...)
	}

	if len(result) == 0 {
		return ""
	}
	return strings.Join(result, "\n")
}

// validSHA matches a full-length lowercase hex Git SHA.
var validSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// GetIncrementalDiff gets the diff since a given SHA.
func GetIncrementalDiff(baseSHA string) (string, error) {
	if !validSHA.MatchString(baseSHA) {
		return "", fmt.Errorf("invalid SHA format: %q", baseSHA)
	}
	out, err := exec.Command("git", "diff", baseSHA+"..HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	return string(out), nil
}

// PostCleanReview posts a review when the first review finds no issues. The
// commitSHA is embedded in a hidden marker so future runs treat it as the
// baseline for incremental reviews, avoiding a redundant full re-review on the
// next push.
func PostCleanReview(repo string, prNumber int, commitSHA string, summary ReviewSummary) error {
	return postSimpleReview(repo, prNumber, buildCleanReviewBody(commitSHA, summary))
}

// PostAllClearReview posts a review when all previous findings have been
// resolved. If minimizeFailed is true, a note is appended warning about
// visible old reviews. The commitSHA is embedded in a hidden marker so future
// runs treat it as the baseline for incremental reviews; without it, the next
// push would fall back to reviewing the entire PR again.
func PostAllClearReview(repo string, prNumber int, commitSHA string, minimizeFailed bool, summary ReviewSummary) error {
	return postSimpleReview(repo, prNumber, buildAllClearReviewBody(commitSHA, minimizeFailed, summary))
}

// PostActivityReview posts a review when no new findings were raised but
// there is cycle activity worth surfacing (dismissals, acknowledgments,
// rebuttals, still-open threads). This keeps every commit push producing a
// visible top-level status comment instead of silently logging.
func PostActivityReview(repo string, prNumber int, commitSHA string, summary ReviewSummary) error {
	return postSimpleReview(repo, prNumber, buildActivityReviewBody(commitSHA, summary))
}

// buildCleanReviewBody renders the full Markdown body posted by
// PostCleanReview. Split out from the poster so tests can assert the exact
// string that lands on GitHub without having to mock gh.
func buildCleanReviewBody(commitSHA string, summary ReviewSummary) string {
	return withSummary("CodeCanary reviewed this PR \u2014 no issues found.", summary) + embedBaselineMarker(commitSHA)
}

// buildAllClearReviewBody renders the full Markdown body posted by
// PostAllClearReview. Split out for the same reason as buildCleanReviewBody.
func buildAllClearReviewBody(commitSHA string, minimizeFailed bool, summary ReviewSummary) string {
	body := "## \U0001F425 CodeCanary\n\n\u2705 All previous findings have been addressed. No new issues found. \u2728"
	if minimizeFailed {
		body += "\n\n> \u26A0\uFE0F Some previous review comments could not be minimized and may still be visible."
	}
	return withSummary(body, summary) + embedBaselineMarker(commitSHA)
}

// buildActivityReviewBody renders the body for a commit push that raised no
// new findings but has cycle activity (dismissals/acknowledgments/rebuttals
// or still-open threads carried forward).
func buildActivityReviewBody(commitSHA string, summary ReviewSummary) string {
	body := "## \U0001F425 CodeCanary\n\nReviewed this push \u2014 no new issues found."
	return withSummary(body, summary) + embedBaselineMarker(commitSHA)
}

// withSummary appends the status summary block to a review body. The block
// is skipped when the summary has no non-zero counts, so existing clean/
// all-clear bodies render identically when nothing happened.
func withSummary(body string, summary ReviewSummary) string {
	block := renderSummaryBlock(summary)
	if block == "" {
		return body
	}
	return body + block
}

// embedBaselineMarker returns a hidden HTML comment containing the commitSHA
// so FetchPreviousReviewSHA can use this review as the incremental baseline.
// Returns an empty string if commitSHA is empty (local mode, dry run). The
// marker carries only the SHA — FetchPreviousReviewSHA is the sole reader and
// it only needs that field.
func embedBaselineMarker(commitSHA string) string {
	if commitSHA == "" {
		return ""
	}
	data, err := json.Marshal(struct {
		SHA string `json:"sha"`
	}{SHA: commitSHA})
	if err != nil {
		return ""
	}
	return fmt.Sprintf("\n%s%s%s\n", reviewMarkerPrefixes[0], string(data), reviewMarkerSuffix)
}

func postSimpleReview(repo string, prNumber int, body string) error {
	owner, repoName, err := parseRepoSlug(repo)
	if err != nil {
		return err
	}

	payload := reviewPayload{
		Event:    "COMMENT",
		Body:     body,
		Comments: make([]reviewComment, 0),
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling payload: %w", err)
	}

	_, err = ghAPIPOST(fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, repoName, prNumber), payloadJSON)
	return err
}

// apiError is returned by ghAPIPOST so callers can inspect the stderr output
// from gh (which contains the HTTP status line) separately from the response body.
type apiError struct {
	Err      error
	Stderr   string
	Response string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("gh api: %v\nstderr: %s\nresponse: %s", e.Err, e.Stderr, e.Response)
}

func (e *apiError) Unwrap() error { return e.Err }

// ghAPIPOST sends a JSON payload to the GitHub API via gh, using a temp file
// to avoid stdin pipe issues that can cause "unexpected end of JSON input"
// errors on large payloads.
func ghAPIPOST(apiPath string, payloadJSON []byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "codecanary-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	tmpFile, err := os.CreateTemp(dir, "payload.json")
	if err != nil {
		return nil, fmt.Errorf("creating temp file: %w", err)
	}

	if _, err := tmpFile.Write(payloadJSON); err != nil {
		_ = tmpFile.Close()
		return nil, fmt.Errorf("writing payload to temp file: %w", err)
	}
	_ = tmpFile.Close()

	cmd := exec.Command("gh", "api", apiPath, "--method", "POST", "--input", tmpFile.Name())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), &apiError{Err: err, Stderr: stderr.String(), Response: stdout.String()}
	}
	return stdout.Bytes(), nil
}

// FindReviewNodeIDs returns the node_ids of all reviews on a PR.
func FindReviewNodeIDs(repo string, prNumber int) ([]string, error) {
	owner, name, err := parseRepoSlug(repo)
	if err != nil {
		return nil, err
	}

	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, name, prNumber)
	out, err := exec.Command("gh", "api", apiPath).Output()
	if err != nil {
		return nil, fmt.Errorf("fetching PR reviews: %w", err)
	}

	var reviews []ghReview
	if err := json.Unmarshal(out, &reviews); err != nil {
		return nil, fmt.Errorf("parsing PR reviews: %w", err)
	}

	prefixes := reviewMarkerPrefixes

	var nodeIDs []string
	for _, rev := range reviews {
		for _, prefix := range prefixes {
			if strings.Contains(rev.Body, prefix) {
				nodeIDs = append(nodeIDs, rev.NodeID)
				break
			}
		}
	}

	return nodeIDs, nil
}

// threadHeaderLine returns the first non-empty, non-HTML-comment line from
// a thread body. This is the header line containing severity and finding ID.
func threadHeaderLine(body string) string {
	for _, line := range strings.SplitN(body, "\n", 5) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "<!--") {
			continue
		}
		return line
	}
	return ""
}

// FindingIDFromThread extracts the finding ID from a thread body.
// Thread bodies follow the format: 🟠 **bug** — `finding-id`
func FindingIDFromThread(body string) string {
	firstLine := threadHeaderLine(body)
	// The separator is " — `" (space, em-dash U+2014, space, backtick).
	marker := " \u2014 `"
	start := strings.Index(firstLine, marker)
	if start < 0 {
		return ""
	}
	start += len(marker)
	end := strings.Index(firstLine[start:], "`")
	if end < 0 {
		return ""
	}
	return firstLine[start : start+end]
}

// severityFromThreadBody extracts the severity string from a thread body.
// Thread bodies follow the format: {icon} **severity** — `id`
var threadSeverityRe = regexp.MustCompile(`\*\*(\w+)\*\*`)

func severityFromThreadBody(body string) string {
	firstLine := threadHeaderLine(body)
	if m := threadSeverityRe.FindStringSubmatch(firstLine); len(m) > 1 {
		return strings.ToLower(m[1])
	}
	return "warning"
}

// findingFromEmbeddedJSON tries to parse a Finding from the JSON embedded in the
// codecanary:finding HTML comment marker. Returns the finding and true if successful.
func findingFromEmbeddedJSON(body string) (Finding, bool) {
	prefix := findingMarkerPrefix
	suffix := reviewMarkerSuffix
	start := strings.Index(body, prefix)
	if start < 0 {
		return Finding{}, false
	}
	start += len(prefix)
	end := strings.Index(body[start:], suffix)
	if end < 0 {
		return Finding{}, false
	}
	raw := body[start : start+end]
	if len(raw) == 0 || raw[0] != '{' {
		return Finding{}, false
	}
	var f Finding
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		return Finding{}, false
	}
	return f, true
}

// parseThreadBody extracts the description and suggestion from a thread comment
// body. This is the fallback parser for older comments that don't embed JSON.
func parseThreadBody(body string) (description, suggestion string) {
	lines := strings.Split(body, "\n")

	// Skip leading HTML markers and the header line (icon **sev** — `id`).
	contentStart := 0
	pastHeader := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "<!--") {
			continue
		}
		if !pastHeader {
			// First non-empty non-marker line is the header — skip it.
			pastHeader = true
			contentStart = i + 1
			continue
		}
		contentStart = i
		break
	}

	if contentStart >= len(lines) {
		return "", ""
	}

	// Split content into description and suggestion.
	var descLines []string
	var suggLines []string
	inSuggestion := false
	suggestionPrefix := "> **Suggestion**: "

	for _, line := range lines[contentStart:] {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, suggestionPrefix) {
			inSuggestion = true
			suggLines = append(suggLines, strings.TrimPrefix(trimmed, suggestionPrefix))
			continue
		}
		if inSuggestion {
			// Continuation of suggestion (blockquote lines).
			if strings.HasPrefix(trimmed, "> ") {
				suggLines = append(suggLines, strings.TrimPrefix(trimmed, "> "))
			} else if trimmed == ">" {
				suggLines = append(suggLines, "")
			} else {
				suggLines = append(suggLines, line)
			}
			continue
		}
		descLines = append(descLines, line)
	}

	description = strings.TrimSpace(strings.Join(descLines, "\n"))
	suggestion = strings.TrimSpace(strings.Join(suggLines, "\n"))
	return description, suggestion
}

// FindingFromThread extracts a Finding from a ReviewThread. It first tries to
// parse the embedded JSON (new format), then falls back to body parsing.
func FindingFromThread(t ReviewThread) Finding {
	// Try embedded JSON first (lossless roundtrip).
	if f, ok := findingFromEmbeddedJSON(t.Body); ok {
		f.File = t.Path
		f.Line = t.Line
		f.Status = "still open"
		return f
	}

	// Fallback: parse the markdown body.
	desc, suggestion := parseThreadBody(t.Body)
	return Finding{
		ID:          FindingIDFromThread(t.Body),
		File:        t.Path,
		Line:        t.Line,
		Severity:    severityFromThreadBody(t.Body),
		Title:       firstSentence(desc),
		Description: desc,
		Suggestion:  suggestion,
		Status:      "still open",
	}
}

// firstSentence returns the first sentence (or first line) of text as a title.
func firstSentence(text string) string {
	if text == "" {
		return ""
	}
	// Use first line.
	line := text
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		line = text[:idx]
	}
	line = strings.TrimSpace(line)
	// Truncate to 120 runes if very long.
	runes := []rune(line)
	if len(runes) > 120 {
		return string(runes[:117]) + "..."
	}
	return line
}

// ReviewInfo represents a review with its node ID and finding IDs.
type ReviewInfo struct {
	NodeID     string
	FindingIDs []string
}

// FindReviews returns reviews with their parsed finding IDs.
func FindReviews(repo string, prNumber int) ([]ReviewInfo, error) {
	owner, name, err := parseRepoSlug(repo)
	if err != nil {
		return nil, err
	}

	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, name, prNumber)
	out, err := exec.Command("gh", "api", apiPath).Output()
	if err != nil {
		return nil, fmt.Errorf("fetching PR reviews: %w", err)
	}

	var reviews []ghReview
	if err := json.Unmarshal(out, &reviews); err != nil {
		return nil, fmt.Errorf("parsing PR reviews: %w", err)
	}

	prefixes := reviewMarkerPrefixes
	const suffix = " -->"

	var result []ReviewInfo
	for _, rev := range reviews {
		for _, prefix := range prefixes {
			idx := strings.Index(rev.Body, prefix)
			if idx < 0 {
				continue
			}
			start := idx + len(prefix)
			endIdx := strings.Index(rev.Body[start:], suffix)
			if endIdx < 0 {
				continue
			}
			jsonData := rev.Body[start : start+endIdx]
			var rr ReviewResult
			if err := json.Unmarshal([]byte(jsonData), &rr); err != nil {
				continue
			}
			var ids []string
			for _, f := range rr.Findings {
				ids = append(ids, f.ID)
			}
			result = append(result, ReviewInfo{
				NodeID:     rev.NodeID,
				FindingIDs: ids,
			})
			break
		}
	}

	return result, nil
}

// MinimizeComment hides a comment on GitHub using the minimizeComment GraphQL mutation.
func MinimizeComment(nodeID string) error {
	cmd := exec.Command("gh", "api", "graphql",
		"-f", "query=mutation($id:ID!){minimizeComment(input:{subjectId:$id,classifier:RESOLVED}){minimizedComment{isMinimized}}}",
		"-F", "id="+nodeID,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh api graphql minimize: %w\n%s", err, string(out))
	}
	return nil
}

// FetchFileContents reads the full contents of changed files from disk.
// It skips files that are too large, binary, deleted, or match ignore patterns.
// Returns a map of path->content and a list of skipped file paths.
func FetchFileContents(files []string, ignorePatterns []string, maxPerFile, maxTotal int) (map[string]string, []string) {
	contents := make(map[string]string)
	var skipped []string
	totalSize := 0

	for _, path := range files {
		// Check ignore patterns.
		if matchesIgnore(path, ignorePatterns) {
			skipped = append(skipped, path)
			continue
		}

		data, err := os.ReadFile(path)
		if err != nil {
			// File may have been deleted in this PR — skip gracefully.
			continue
		}

		// Skip binary files (null bytes in first 512 bytes).
		peek := data
		if len(peek) > 512 {
			peek = peek[:512]
		}
		if bytes.ContainsRune(peek, 0) {
			skipped = append(skipped, path)
			continue
		}

		size := len(data)

		// Skip files exceeding per-file limit.
		if size > maxPerFile {
			skipped = append(skipped, path)
			continue
		}

		// Stop if total budget would be exceeded.
		if totalSize+size > maxTotal {
			skipped = append(skipped, path)
			continue
		}

		contents[path] = string(data)
		totalSize += size
	}

	return contents, skipped
}

// isSetupPR detects whether this is the initial setup PR.
// Returns true only when a new workflow file referencing codecanary is added AND
// the PR contains no other files beyond expected setup artifacts (workflow +
// config), so that PRs bundling real code changes are never silently skipped.
func isSetupPR(diff string, files []string) bool {
	// All files must be known setup paths.
	for _, f := range files {
		if !isSetupFile(f) {
			return false
		}
	}

	// At least one newly added workflow file must reference codecanary.
	lines := strings.Split(diff, "\n")
	for i := 0; i < len(lines)-1; i++ {
		if lines[i] != "--- /dev/null" {
			continue
		}
		plusLine := lines[i+1]
		if !strings.HasPrefix(plusLine, "+++ b/.github/workflows/") {
			continue
		}
		for j := i + 2; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "--- ") || strings.HasPrefix(lines[j], "diff --git") {
				break
			}
			if strings.HasPrefix(lines[j], "+") && (strings.Contains(lines[j], "codecanary") || strings.Contains(lines[j], "clanopy")) {
				return true
			}
		}
	}
	return false
}

// isSetupFile returns true if the file path is a known setup artifact.
func isSetupFile(path string) bool {
	return strings.HasPrefix(path, ".github/workflows/") ||
		strings.HasPrefix(path, ".codecanary/") || path == ".codecanary.yml" ||
		strings.HasPrefix(path, ".clanopy/")
}

// matchesIgnore checks if a path matches any of the ignore glob patterns.
// Uses doublestar to support ** recursive globs (e.g. "dist/**", "src/**/*.test.*").
func matchesIgnore(path string, patterns []string) bool {
	for _, pat := range patterns {
		if matched, _ := doublestar.Match(pat, path); matched {
			return true
		}
		// Also try matching against just the filename.
		if matched, _ := doublestar.Match(pat, filepath.Base(path)); matched {
			return true
		}
	}
	return false
}
