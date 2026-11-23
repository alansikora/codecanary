package cli

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/alansikora/codecanary/internal/review"
	"github.com/spf13/cobra"
)

// resolveCmd is the action's resolve step. GitHub only lets a token with
// contents: write resolve a review thread, and the review runs with one that
// cannot write contents, so the review reports the threads it settled and
// this command resolves them afterwards with a token of its own.
var resolveCmd = &cobra.Command{
	Use:   "resolve <pr-number>",
	Short: "Resolve codecanary review threads a review settled",
	Long: `Resolve review threads on a PR. Used by the GitHub Action after a
review: the review's token cannot resolve threads (GitHub requires
contents: write for that), so the review reports the threads it settled
and this command resolves them with a token minted for this step.

Only CodeCanary finding threads on the given PR are resolved; any other
ID is refused. Exits non-zero when a thread could not be resolved.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, _ := cmd.Flags().GetString("repo")
		list, _ := cmd.Flags().GetString("threads")
		prNumber, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid PR number: %s", args[0])
		}
		if repo == "" {
			return errors.New("--repo required (owner/name)")
		}
		ids := review.ParseThreadIDs(list)
		if len(ids) == 0 {
			return errors.New("--threads required (comma-separated review thread IDs)")
		}
		return review.ResolveFindingThreads(repo, prNumber, ids)
	},
}

func init() {
	resolveCmd.Flags().StringP("repo", "r", "", "Repository in owner/name format")
	resolveCmd.Flags().String("threads", "", "Comma-separated review thread IDs (the review step's resolve_threads output)")
	rootCmd.AddCommand(resolveCmd)
}
