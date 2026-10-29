package cli

import (
	"fmt"
	"os"
	"strconv"

	"github.com/alansikora/codecanary/internal/review"
	"github.com/spf13/cobra"
)

var fixCmd = &cobra.Command{
	Use:   "fix <pr-number>",
	Short: "Apply or refute a PR's open findings and push the fix (CI)",
	Long: `Run one CI fix cycle on a pull request. The workflow's fix job runs it
after a review leaves findings open, when fix.enabled is set in
.codecanary/config.yml.

The fix model (Claude CLI, fix.model, default opus) gets the open findings
and file tools confined to the checkout — no shell. Per finding it applies
the minimal edit, refutes the finding with a reason (posted as a reply on
its thread), or leaves it open. The edits are committed once, authored by
the PR author with Claude as co-author, and pushed to the PR branch with
the CodeCanary app's token (GH_TOKEN), which triggers the next review.

Loop limits come from the PR's commits: at most fix.max_per_push fix
commits since the last commit that is not a fix, and fix.max_per_pr over
the PR's life. At a limit no model runs; one comment lists the open
findings.

The command never fails CI: provider, rate-limit and push failures post
one comment per head pointing at /codecanary-fix, and every error exits 0
with a workflow warning.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, _ := cmd.Flags().GetString("repo")
		configPath, _ := cmd.Flags().GetString("config")
		claudePath, _ := cmd.Flags().GetString("claude-path")
		reviewedSHA, _ := cmd.Flags().GetString("reviewed-sha")

		prNumber, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid PR number %q: %w", args[0], err)
		}
		if repo == "" {
			if repo, err = review.DetectRepo(); err != nil {
				return fmt.Errorf("could not detect repo (pass --repo owner/name): %w", err)
			}
		}

		err = review.RunFix(review.FixOptions{
			ConfigPath:  configPath,
			ClaudePath:  claudePath,
			ReviewedSHA: reviewedSHA,
			Platform:    &review.GithubPlatform{Repo: repo, PRNumber: prNumber},
		})
		if err != nil {
			// A workflow annotation, not a failed job: the fix is an
			// assistant, and CI stays the review's and the project's.
			fmt.Fprintf(os.Stderr, "::warning::CodeCanary fix did not run: %v\n", err)
		}
		return nil
	},
}

func init() {
	fixCmd.Flags().StringP("repo", "r", "", "GitHub repo (owner/name); defaults to current repo")
	fixCmd.Flags().StringP("config", "c", "", "Path to review config (auto-detected if empty)")
	fixCmd.Flags().String("claude-path", "", "Path to the Claude CLI binary (overrides config claude_path)")
	fixCmd.Flags().String("reviewed-sha", "", "Commit the triggering review ran on; the fix does nothing once the PR head has moved past it")
	rootCmd.AddCommand(fixCmd)
}
