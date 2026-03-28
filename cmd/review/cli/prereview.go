package cli

import (
	"github.com/alansikora/codecanary/internal/review"
	"github.com/spf13/cobra"
)

var preReviewCmd = &cobra.Command{
	Use:   "pre-review",
	Short: "Review local changes before opening a PR",
	Long:  "Review your local branch changes without a GitHub PR. Diffs the current branch against a base branch and runs CodeCanary locally — no CI triggered.",
	RunE: func(cmd *cobra.Command, args []string) error {
		baseBranch, err := cmd.Flags().GetString("base")
		if err != nil {
			return err
		}
		output, err := cmd.Flags().GetString("output")
		if err != nil {
			return err
		}
		configPath, err := cmd.Flags().GetString("config")
		if err != nil {
			return err
		}
		dryRun, err := cmd.Flags().GetBool("dry-run")
		if err != nil {
			return err
		}
		saveUsage, err := cmd.Flags().GetBool("save-usage")
		if err != nil {
			return err
		}
		applyFixes, err := cmd.Flags().GetBool("fix")
		if err != nil {
			return err
		}

		return review.RunLocal(review.RunLocalOptions{
			BaseBranch: baseBranch,
			ConfigPath: configPath,
			Output:     output,
			DryRun:     dryRun,
			SaveUsage:  saveUsage,
			ApplyFixes: applyFixes,
		})
	},
}

func init() {
	preReviewCmd.Flags().StringP("base", "b", "", "Base branch to diff against (default: origin/main if available, otherwise main)")
	preReviewCmd.Flags().StringP("output", "o", "markdown", "Output format: markdown or json")
	preReviewCmd.Flags().StringP("config", "c", ".codecanary.yml", "Path to review config")
	preReviewCmd.Flags().Bool("dry-run", false, "Show prompt without running Claude")
	preReviewCmd.Flags().Bool("save-usage", false, "Write codecanary-usage.json with token usage report")
	preReviewCmd.Flags().Bool("fix", false, "Automatically apply all findings via Claude after the review")
	rootCmd.AddCommand(preReviewCmd)
}
