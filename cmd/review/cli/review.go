package cli

import (
	"fmt"
	"strconv"

	"github.com/alansikora/codecanary/internal/review"
	"github.com/spf13/cobra"
)

var reviewCmd = &cobra.Command{
	Use:              "review <pr-number>",
	Short:            "Review a pull request",
	Args:             cobra.ArbitraryArgs,
	TraverseChildren: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		prNumber, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid PR number %q: %w", args[0], err)
		}

		repo, _ := cmd.Flags().GetString("repo")
		output, _ := cmd.Flags().GetString("output")
		post, _ := cmd.Flags().GetBool("post")
		configPath, _ := cmd.Flags().GetString("config")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		replyOnly, _ := cmd.Flags().GetBool("reply-only")

		return review.Run(review.RunOptions{
			Repo:       repo,
			PRNumber:   prNumber,
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
	reviewCmd.Flags().StringP("config", "c", ".codecanary.yml", "Path to review config")
	reviewCmd.Flags().Bool("reply-only", false, "Evaluate thread replies only, skip new findings")
	reviewCmd.PersistentFlags().Bool("dry-run", false, "Show prompt without running Claude")
	rootCmd.AddCommand(reviewCmd)
}
