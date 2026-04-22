package cli

import (
	"fmt"
	"strconv"

	"github.com/alansikora/codecanary/internal/review"
	"github.com/spf13/cobra"
)

var reviewCmd = &cobra.Command{
	Use:              "review [pr-number]",
	Short:            "Review a pull request",
	Long:             "Review a pull request. Without --post, always runs locally against the branch diff (uncommitted changes included) and persists state under ~/.codecanary/state/. With --post, fetches the PR from GitHub and posts findings as review comments — use a PR number or omit it to auto-detect from the current branch.",
	Args:             cobra.ArbitraryArgs,
	TraverseChildren: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, _ := cmd.Flags().GetString("repo")
		output, _ := cmd.Flags().GetString("output")
		post, _ := cmd.Flags().GetBool("post")
		configPath, _ := cmd.Flags().GetString("config")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		replyOnly, _ := cmd.Flags().GetBool("reply-only")
		claudePath, _ := cmd.Flags().GetString("claude-path")
		baseBranch, _ := cmd.Flags().GetString("base")

		// GitHub mode — only when posting. Requires a PR (explicit or auto-detected).
		if post {
			var prNumber int
			if len(args) > 0 {
				n, err := strconv.Atoi(args[0])
				if err != nil {
					return fmt.Errorf("invalid PR number %q: %w", args[0], err)
				}
				prNumber = n
			} else {
				n, err := review.DetectPRNumber(repo)
				if err != nil {
					return fmt.Errorf("--post requires a PR; could not auto-detect one for the current branch: %w", err)
				}
				review.Stderrf(review.ColorCyan, "Auto-detected PR #%d from current branch\n", n)
				prNumber = n
			}
			if baseBranch != "" {
				review.Stderrf(review.ColorYellow, "Warning: --base ignored in --post mode\n")
			}
			return review.Run(review.RunOptions{
				Repo:       repo,
				PRNumber:   prNumber,
				ConfigPath: configPath,
				Output:     output,
				Post:       true,
				DryRun:     dryRun,
				ReplyOnly:  replyOnly,
				ClaudePath: claudePath,
				Version:    Version,
				Platform: &review.GithubPlatform{
					Repo:     repo,
					PRNumber: prNumber,
					DryRun:   dryRun,
				},
			})
		}

		// Local mode — default. A PR number argument is ignored: local is local.
		if replyOnly {
			review.Stderrf(review.ColorYellow, "Warning: --reply-only ignored in local mode\n")
			replyOnly = false
		}

		pr, err := review.FetchLocalDiff(baseBranch)
		if err != nil {
			return fmt.Errorf("local diff failed: %w", err)
		}
		review.Stderrf(review.ColorCyan, "Reviewing local changes on %s\n", pr.HeadBranch)

		return review.Run(review.RunOptions{
			PR:         pr,
			ConfigPath: configPath,
			Output:     output,
			DryRun:     dryRun,
			ReplyOnly:  replyOnly,
			ClaudePath: claudePath,
			Version:    Version,
			Platform: &review.LocalPlatform{
				Branch:       pr.HeadBranch,
				OutputFormat: output,
			},
		})
	},
}

func init() {
	reviewCmd.Flags().StringP("repo", "r", "", "GitHub repo (owner/name) — only used with --post")
	reviewCmd.Flags().StringP("output", "o", "markdown", "Output format: markdown, terminal, or json; auto-upgrades to terminal when stdout is a TTY")
	reviewCmd.Flags().Bool("post", false, "Fetch the PR from GitHub and post findings as review comments (requires a PR; auto-detected if no number given)")
	reviewCmd.Flags().StringP("config", "c", "", "Path to review config (auto-detected if empty)")
	reviewCmd.Flags().Bool("reply-only", false, "Evaluate thread replies only, skip new findings (--post only)")
	reviewCmd.Flags().String("claude-path", "", "Path to the Claude CLI binary (overrides config claude_path)")
	reviewCmd.Flags().StringP("base", "b", "", "Base branch for local review (auto-detected if empty)")
	reviewCmd.PersistentFlags().Bool("dry-run", false, "Show prompt without running Claude")
	rootCmd.AddCommand(reviewCmd)
}
