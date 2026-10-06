package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alansikora/codecanary/internal/review"
	"github.com/spf13/cobra"
)

// checksOutput is the JSON envelope of `codecanary checks --output json`.
type checksOutput struct {
	PR   int    `json:"pr"`
	Repo string `json:"repo"`
	review.RequiredChecks
}

var checksCmd = &cobra.Command{
	Use:   "checks [pr-number]",
	Short: "Report a PR's required checks, other than CodeCanary's own",
	Long: `Report the state of a PR's required checks — pass, fail or pending —
leaving out CodeCanary's own check and commit status. With --watch, blocks
until they pass or one fails.

Used by the codecanary-fix skill in repos with review_on: ready, where a
fix is pushed to a draft and the PR is marked ready (which requests the
review) only once CI is green. A repo with no required checks reports pass.

PR number is auto-detected from the current branch when omitted. Exits
non-zero when a required check failed.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repoFlagSet := cmd.Flags().Changed("repo")
		repo, _ := cmd.Flags().GetString("repo")
		output, _ := cmd.Flags().GetString("output")
		watch, _ := cmd.Flags().GetBool("watch")
		timeoutMinutes, _ := cmd.Flags().GetInt("timeout")

		prNumber, err := resolveFindingsPR(args, repoFlagSet)
		if err != nil {
			return err
		}
		if repo == "" {
			detected, err := review.DetectRepo()
			if err != nil {
				return fmt.Errorf("could not detect repo (pass --repo owner/name): %w", err)
			}
			repo = detected
		}

		var rc review.RequiredChecks
		if watch {
			rc, err = review.WaitForRequiredChecks(repo, prNumber, time.Duration(timeoutMinutes)*time.Minute)
		} else {
			rc, err = review.FetchRequiredChecks(repo, prNumber)
		}
		if err != nil {
			return err
		}

		payload := checksOutput{PR: prNumber, Repo: repo, RequiredChecks: rc}
		if output == "json" {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(payload); err != nil {
				return err
			}
		} else {
			fmt.Printf("PR #%d required checks: %s\n", prNumber, rc.State)
			if len(rc.Failing) > 0 {
				fmt.Printf("  failing: %s\n", strings.Join(rc.Failing, ", "))
			}
			if len(rc.Pending) > 0 {
				fmt.Printf("  pending: %s\n", strings.Join(rc.Pending, ", "))
			}
		}
		if rc.State == "fail" {
			cmd.SilenceUsage = true
			return fmt.Errorf("required checks failed: %s", strings.Join(rc.Failing, ", "))
		}
		return nil
	},
}

func init() {
	checksCmd.Flags().StringP("repo", "r", "", "GitHub repo (owner/name); defaults to current repo")
	checksCmd.Flags().StringP("output", "o", "markdown", "Output format: markdown or json")
	checksCmd.Flags().Bool("watch", false, "Poll until the required checks pass or one fails")
	checksCmd.Flags().Int("timeout", 30, "Max minutes to wait when --watch is set (0 = no limit)")
	rootCmd.AddCommand(checksCmd)
}
