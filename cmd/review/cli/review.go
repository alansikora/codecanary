package cli

import (
	"fmt"
	"os"
	"strconv"

	"github.com/alansikora/codecanary/internal/review"
	"github.com/spf13/cobra"
)

var reviewCmd = &cobra.Command{
	Use:              "review [pr-number]",
	Short:            "Review a pull request",
	Long:             "Review a pull request. If no PR number is given, detects the PR for the current branch. If no PR exists, reviews the local branch diff.",
	Args:             cobra.ArbitraryArgs,
	TraverseChildren: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, _ := cmd.Flags().GetString("repo")
		output, _ := cmd.Flags().GetString("output")
		post, _ := cmd.Flags().GetBool("post")
		configPath, _ := cmd.Flags().GetString("config")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		replyOnly, _ := cmd.Flags().GetBool("reply-only")

		// Explicit PR number — GitHub mode.
		if len(args) > 0 {
			prNumber, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid PR number %q: %w", args[0], err)
			}
			return review.Run(review.RunOptions{
				Repo:       repo,
				PRNumber:   prNumber,
				ConfigPath: configPath,
				Output:     output,
				Post:       post,
				DryRun:     dryRun,
				ReplyOnly:  replyOnly,
			})
		}

		// Try auto-detecting PR from current branch.
		if prNumber, err := review.DetectPRNumber(repo); err == nil {
			fmt.Fprintf(os.Stderr, "Auto-detected PR #%d from current branch\n", prNumber)
			return review.Run(review.RunOptions{
				Repo:       repo,
				PRNumber:   prNumber,
				ConfigPath: configPath,
				Output:     output,
				Post:       post,
				DryRun:     dryRun,
				ReplyOnly:  replyOnly,
			})
		}

		// No PR — local mode.
		pr, err := review.FetchLocalDiff()
		if err != nil {
			return fmt.Errorf("no PR found and local diff failed: %w", err)
		}
		fmt.Fprintf(os.Stderr, "No PR found — reviewing local changes on %s\n", pr.HeadBranch)

		if post {
			fmt.Fprintf(os.Stderr, "Warning: --post ignored in local mode (no PR to post to)\n")
			post = false
		}
		if replyOnly {
			fmt.Fprintf(os.Stderr, "Warning: --reply-only ignored in local mode\n")
			replyOnly = false
		}

		return review.Run(review.RunOptions{
			PR:         pr,
			Local:      true,
			ConfigPath: configPath,
			Output:     output,
			Post:       post,
			DryRun:     dryRun,
			ReplyOnly:  replyOnly,
		})
	},
}

func init() {
	reviewCmd.Flags().StringP("repo", "r", "", "GitHub repo (owner/name)")
	reviewCmd.Flags().StringP("output", "o", "markdown", "Output format: markdown or json")
	reviewCmd.Flags().Bool("post", false, "Post findings as a PR comment")
	reviewCmd.Flags().StringP("config", "c", ".codecanary/config.yml", "Path to review config")
	reviewCmd.Flags().Bool("reply-only", false, "Evaluate thread replies only, skip new findings")
	reviewCmd.PersistentFlags().Bool("dry-run", false, "Show prompt without running Claude")
	rootCmd.AddCommand(reviewCmd)
}
