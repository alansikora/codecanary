package cli

import (
	"errors"
	"fmt"

	"github.com/alansikora/codecanary/internal/review"
	"github.com/spf13/cobra"
)

// signoffCmd posts a GitHub commit status on the current HEAD based on the
// most recent local review for the current branch. Inspired by Rails 8.1's
// `gh signoff` (Basecamp): combined with a required status check in branch
// protection, this lets a team gate merges on "a human actually ran
// codecanary locally and it came back clean", without paying for a cloud
// review on every push.
var signoffCmd = &cobra.Command{
	Use:   "signoff",
	Short: "Post a GitHub commit status from the last local review",
	Long: `Post a GitHub commit status on HEAD reflecting the most recent local
codecanary review for the current branch.

Requires:
  - you ran 'codecanary review' on this branch
  - the working tree is clean and HEAD matches the reviewed SHA
  - 'gh' is installed and authenticated with repo:status scope

Combine with a required 'codecanary/review' check in branch protection to
block merges until a clean local review exists for the tip commit.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")

		slug, err := review.RepoSlug()
		if err != nil {
			return err
		}
		branch, err := review.CurrentBranch()
		if err != nil {
			return err
		}
		sha, err := review.HeadSHA()
		if err != nil {
			return err
		}

		state, err := review.LoadLocalState(branch)
		if err != nil {
			return fmt.Errorf("loading local state: %w", err)
		}
		if state == nil {
			return fmt.Errorf("no local review found for branch %q — run 'codecanary review' first", branch)
		}

		if !force {
			if state.SHA != sha {
				return fmt.Errorf(
					"local review was for %s but HEAD is %s — re-run 'codecanary review' (or pass --force)",
					shortSHA(state.SHA), shortSHA(sha))
			}
			dirty, err := review.WorkingTreeDirty()
			if err != nil {
				return fmt.Errorf("checking working tree: %w", err)
			}
			if dirty {
				return errors.New(
					"working tree has uncommitted changes — commit them, " +
						"run 'git stash --include-untracked' (plain 'git stash' leaves untracked files behind), " +
						"or pass --force if you know the dirty files are unrelated to the PR")
			}
		}

		unresolved := 0
		for _, f := range state.Findings {
			if f.Actionable != nil && !*f.Actionable {
				continue
			}
			unresolved++
		}

		statusState, desc := "success", "0 findings"
		if unresolved > 0 {
			statusState = "failure"
			suffix := "s"
			if unresolved == 1 {
				suffix = ""
			}
			desc = fmt.Sprintf("%d unresolved finding%s", unresolved, suffix)
		}

		if err := review.PostReviewCommitStatus(slug, sha, statusState, desc); err != nil {
			return fmt.Errorf("posting commit status: %w", err)
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"✓ %s = %s on %s@%s (%s)\n",
			review.ReviewCommitStatusContext, statusState, slug, shortSHA(sha), desc)
		return nil
	},
}

func init() {
	signoffCmd.Flags().Bool("force", false,
		"Sign off even if HEAD doesn't match the reviewed SHA or the tree is dirty")
	rootCmd.AddCommand(signoffCmd)
}
