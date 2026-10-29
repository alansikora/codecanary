package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/alansikora/codecanary/internal/review"
	"github.com/spf13/cobra"
)

// replyCmd posts a reply on an existing PR review-comment thread. The
// codecanary-fix skill uses this to record a rationale on every finding
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
codecanary-fix skill to record why a finding was skipped — the skill
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

		owner, name, pr, commentID, err := review.ParseReviewCommentURL(urlStr)
		if err != nil {
			return err
		}
		if err := review.ReplyToReviewComment(urlStr, body); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"✓ replied on %s/%s PR #%d comment %d\n", owner, name, pr, commentID)
		return nil
	},
}

func init() {
	replyCmd.Flags().String("url", "",
		"Comment URL to reply under (use the comment_url field from `codecanary findings --output json`)")
	replyCmd.Flags().String("body", "", "Reply body (markdown)")
	rootCmd.AddCommand(replyCmd)
}
