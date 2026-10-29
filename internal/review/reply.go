package review

import (
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// ReplyToReviewComment posts body as a reply in the review thread that
// commentURL (a finding's comment_url) starts. Used by `codecanary reply`
// and by the CI fix to record why a finding was not applied.
func ReplyToReviewComment(commentURL, body string) error {
	owner, name, pr, commentID, err := ParseReviewCommentURL(commentURL)
	if err != nil {
		return err
	}
	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/comments/%d/replies", owner, name, pr, commentID)
	// Use -X POST explicitly rather than relying on gh's body-implies-POST
	// heuristic so a body that happens to be empty-looking doesn't flip
	// the verb underneath us. -f, not -F: -F reads "@path" values from a
	// file and converts "true"/numbers, and the body is free text (in the
	// CI fix, written by a model reading untrusted code).
	c := exec.Command("gh", "api", "-X", "POST", apiPath, "-f", "body="+body)
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("posting reply via gh api: %w\n%s",
			err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ParseReviewCommentURL extracts the owner, repo name, PR number, and
// underlying review-comment ID from a GitHub inline-comment URL like:
//
//	https://github.com/alansikora/codecanary/pull/123#discussion_r1234567
//
// The fragment can also be `discussion_r<id>` inside a longer suffix —
// anything the regex matches, we accept.
var discussionCommentRE = regexp.MustCompile(`discussion_r(\d+)`)

func ParseReviewCommentURL(raw string) (owner, name string, pr int, commentID int64, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("parsing URL %q: %w", raw, err)
	}
	if u.Host != "github.com" {
		return "", "", 0, 0, fmt.Errorf("URL host %q is not github.com: %s", u.Host, raw)
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segs) < 4 || segs[2] != "pull" {
		return "", "", 0, 0,
			fmt.Errorf("URL path %q does not look like a PR review comment (expected /owner/repo/pull/N): %s",
				u.Path, raw)
	}
	owner = segs[0]
	name = segs[1]
	pr, err = strconv.Atoi(segs[3])
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("parsing PR number from %q: %w", segs[3], err)
	}
	m := discussionCommentRE.FindStringSubmatch(u.Fragment)
	if m == nil {
		return "", "", 0, 0,
			fmt.Errorf("URL fragment %q does not contain a discussion_r<id> comment reference: %s",
				u.Fragment, raw)
	}
	commentID, err = strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("parsing comment id %q: %w", m[1], err)
	}
	return owner, name, pr, commentID, nil
}
