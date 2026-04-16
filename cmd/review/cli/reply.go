package cli

import (
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// replyCmd posts a reply on an existing PR review-comment thread. The
// codecanary-review skill uses this to record a rationale on every finding
// it skips, so the deferral shows up inline on the PR alongside the
// original bot comment instead of vanishing silently.
//
// The skill passes the finding's `comment_url` straight through — we parse
// owner/repo/PR/comment-id out of it, so the caller never has to carry
// those pieces separately.
var replyCmd = &cobra.Command{
	Use:   "reply",
	Short: "Post a reply on a codecanary review comment thread",
	Long: `Post a reply on an existing review comment thread. Used by the
codecanary-review skill to record why a finding was skipped — the skill
passes the comment_url straight from the findings JSON, which is all the
context needed to address the right thread.

The URL is the form GitHub returns for inline review comments, e.g.
  https://github.com/OWNER/REPO/pull/123#discussion_r456789

Any URL with a ?trailing=param or #discussion_r<id> fragment is accepted.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		urlStr, _ := cmd.Flags().GetString("url")
		body, _ := cmd.Flags().GetString("body")
		if urlStr == "" {
			return errors.New("--url required (the comment_url from `codecanary findings --output json`)")
		}
		if strings.TrimSpace(body) == "" {
			return errors.New("--body required and must be non-empty")
		}

		owner, name, pr, commentID, err := parseReviewCommentURL(urlStr)
		if err != nil {
			return err
		}

		apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/comments/%d/replies", owner, name, pr, commentID)
		// Use -X POST explicitly rather than relying on gh's body-implies-POST
		// heuristic so a body that happens to be empty-looking doesn't flip
		// the verb underneath us.
		c := exec.Command("gh", "api", "-X", "POST", apiPath, "-f", "body="+body)
		out, err := c.CombinedOutput()
		if err != nil {
			return fmt.Errorf("posting reply via gh api: %w\n%s",
				err, strings.TrimSpace(string(out)))
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"✓ replied on %s/%s PR #%d comment %d\n", owner, name, pr, commentID)
		return nil
	},
}

// parseReviewCommentURL extracts the owner, repo name, PR number, and
// underlying review-comment ID from a GitHub inline-comment URL like:
//
//	https://github.com/alansikora/codecanary/pull/123#discussion_r1234567
//
// The fragment can also be `discussion_r<id>` inside a longer suffix —
// anything the regex matches, we accept.
var discussionCommentRE = regexp.MustCompile(`discussion_r(\d+)`)

func parseReviewCommentURL(raw string) (owner, name string, pr int, commentID int64, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("parsing URL %q: %w", raw, err)
	}
	if u.Host != "github.com" && !strings.HasSuffix(u.Host, ".github.com") {
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

func init() {
	replyCmd.Flags().String("url", "",
		"Comment URL to reply under (use the comment_url field from `codecanary findings --output json`)")
	replyCmd.Flags().String("body", "", "Reply body (markdown)")
	rootCmd.AddCommand(replyCmd)
}
