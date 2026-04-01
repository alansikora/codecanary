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
		local, _ := cmd.Flags().GetBool("local")
		baseBranch, _ := cmd.Flags().GetString("base-branch")

		// --local is incompatible with an explicit PR number.
		if local && len(args) > 0 {
			return fmt.Errorf("cannot use --local with a PR number")
		}

		// --local forces local mode, skipping PR detection entirely.
		if local {
			pr, err := review.FetchLocalDiff(baseBranch)
			if err != nil {
				return fmt.Errorf("local diff failed: %w", err)
			}
			review.Stderrf(review.ColorCyan, "Local mode — reviewing changes on %s\n", pr.HeadBranch)
			if post {
				review.Stderrf(review.ColorYellow, "Warning: --post ignored in local mode (no PR to post to)\n")
				post = false
			}
			if replyOnly {
				review.Stderrf(review.ColorYellow, "Warning: --reply-only ignored in local mode\n")
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
		}

		// Explicit PR number — GitHub mode.
		if len(args) > 0 {
			prNumber, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid PR number %q: %w", args[0], err)
			}
			if baseBranch != "" {
				review.Stderrf(review.ColorYellow, "Warning: --base-branch is ignored when reviewing a PR\n")
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
			review.Stderrf(review.ColorCyan, "Auto-detected PR #%d from current branch\n", prNumber)
			if baseBranch != "" {
				review.Stderrf(review.ColorYellow, "Warning: --base-branch is ignored when reviewing a PR\n")
			}
			return review.Run(review.RunOptions{
				Repo:        repo,
				PRNumber:    prNumber,
				ConfigPath:  configPath,
				Output:      output,
				Post:        post,
				DryRun:      dryRun,
				ReplyOnly:   replyOnly,
				LocalDetect: true,
			})
		}

		// No PR — local mode (auto-fallback).
		pr, err := review.FetchLocalDiff(baseBranch)
		if err != nil {
			return fmt.Errorf("no PR found and local diff failed: %w", err)
		}
		review.Stderrf(review.ColorCyan, "No PR found — reviewing local changes on %s\n", pr.HeadBranch)

		if post {
			review.Stderrf(review.ColorYellow, "Warning: --post ignored in local mode (no PR to post to)\n")
			post = false
		}
		if replyOnly {
			review.Stderrf(review.ColorYellow, "Warning: --reply-only ignored in local mode\n")
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
	reviewCmd.Flags().StringP("output", "o", "markdown", "Output format: markdown, terminal, or json; auto-upgrades to terminal when stdout is a TTY")
	reviewCmd.Flags().Bool("post", false, "Post findings as a PR comment")
	reviewCmd.Flags().StringP("config", "c", ".codecanary/config.yml", "Path to review config")
	reviewCmd.Flags().Bool("reply-only", false, "Evaluate thread replies only, skip new findings")
	reviewCmd.PersistentFlags().Bool("dry-run", false, "Show prompt without running Claude")
	reviewCmd.Flags().BoolP("local", "l", false, "Force local diff review, skip PR detection")
	reviewCmd.Flags().String("base-branch", "", "Base branch for local diff (default: auto-detect main/master)")
	rootCmd.AddCommand(reviewCmd)
}
