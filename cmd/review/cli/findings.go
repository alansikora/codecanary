package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/alansikora/codecanary/internal/review"
	"github.com/spf13/cobra"
)

// findingsOutput is the JSON envelope emitted by `codecanary findings --output json`.
// Keeps the shape stable across CLI versions so the codecanary-loop skill
// (and any other automation) can rely on it.
type findingsOutput struct {
	PR           int                `json:"pr"`
	Repo         string             `json:"repo"`
	Commit       string             `json:"commit"`
	ReviewStatus string             `json:"review_status"`
	Conclusion   string             `json:"conclusion,omitempty"`
	Findings     []review.PRFinding `json:"findings"`
}

var findingsCmd = &cobra.Command{
	Use:   "findings [pr-number]",
	Short: "Fetch codecanary review findings from a PR",
	Long: `Fetch codecanary-bot review comments posted on a PR and emit them as
structured output. With --watch, blocks until the in-flight review check
completes before returning.

PR number is auto-detected from the current branch when omitted. Output
defaults to a human-readable markdown table; use --output json for
machine consumption (e.g. the codecanary-loop Claude skill).`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, _ := cmd.Flags().GetString("repo")
		output, _ := cmd.Flags().GetString("output")
		watch, _ := cmd.Flags().GetBool("watch")
		sinceCommit, _ := cmd.Flags().GetString("since-commit")
		timeoutMinutes, _ := cmd.Flags().GetInt("timeout")

		if repo == "" {
			detected, err := review.DetectRepo()
			if err != nil {
				return fmt.Errorf("could not detect repo (pass --repo owner/name): %w", err)
			}
			repo = detected
		}

		prNumber, err := resolveFindingsPR(args, repo)
		if err != nil {
			return err
		}

		var status review.ReviewStatus
		if watch {
			timeout := time.Duration(timeoutMinutes) * time.Minute
			status, err = review.WaitForReview(repo, prNumber, timeout)
		} else {
			status, err = review.FetchReviewStatus(repo, prNumber)
		}
		if err != nil {
			return err
		}

		comments, err := review.FetchPRComments(repo, prNumber)
		if err != nil {
			return err
		}

		findings := review.ParseFindingMarkers(comments)
		if sinceCommit != "" {
			findings = filterSinceCommit(findings, sinceCommit)
		}

		payload := findingsOutput{
			PR:           prNumber,
			Repo:         repo,
			Commit:       status.HeadSHA,
			ReviewStatus: status.Status,
			Conclusion:   status.Conclusion,
			Findings:     findings,
		}

		switch output {
		case "json":
			return emitFindingsJSON(payload)
		default:
			return emitFindingsMarkdown(payload)
		}
	},
}

// resolveFindingsPR returns the PR number from the positional arg or by
// auto-detecting from the current branch via gh.
func resolveFindingsPR(args []string, repo string) (int, error) {
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil {
			return 0, fmt.Errorf("invalid PR number %q: %w", args[0], err)
		}
		return n, nil
	}
	n, err := review.DetectPRNumber(repo)
	if err != nil {
		return 0, fmt.Errorf("%w (or pass the PR number as an argument)", err)
	}
	return n, nil
}

// filterSinceCommit drops findings anchored on the given commit SHA.
// Callers pass the previous HEAD after pushing a new commit to get
// only findings on the fresh commit. Prefix match (7+ chars) so short
// hashes are accepted.
func filterSinceCommit(findings []review.PRFinding, sinceCommit string) []review.PRFinding {
	if len(sinceCommit) < 7 {
		return findings
	}
	out := findings[:0]
	for _, f := range findings {
		if strings.HasPrefix(f.CommitID, sinceCommit) {
			continue
		}
		out = append(out, f)
	}
	return out
}

func emitFindingsJSON(p findingsOutput) error {
	// Ensure findings is `[]` not `null` in the JSON output.
	if p.Findings == nil {
		p.Findings = []review.PRFinding{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}

func emitFindingsMarkdown(p findingsOutput) error {
	fmt.Printf("# CodeCanary findings — %s PR #%d\n\n", p.Repo, p.PR)
	fmt.Printf("Commit: `%s`  •  Review: `%s`", shortSHA(p.Commit), p.ReviewStatus)
	if p.Conclusion != "" {
		fmt.Printf(" (%s)", p.Conclusion)
	}
	fmt.Println()
	fmt.Println()

	if len(p.Findings) == 0 {
		fmt.Println("No findings.")
		return nil
	}

	fmt.Println("| Severity | File:Line | Fix ref | Title |")
	fmt.Println("|---|---|---|---|")
	for _, f := range p.Findings {
		fmt.Printf("| %s | `%s:%d` | `%s` | %s |\n",
			severityIcon(f.Severity)+" "+f.Severity,
			f.File, f.Line, f.FixRef, f.Title)
	}
	fmt.Println()
	for _, f := range p.Findings {
		fmt.Printf("## %s %s — `%s`\n\n",
			severityIcon(f.Severity), f.Severity, f.ID)
		fmt.Printf("**%s**  (`%s:%d`, fix_ref `%s`)\n\n",
			f.Title, f.File, f.Line, f.FixRef)
		fmt.Println(f.Description)
		if f.Suggestion != "" {
			fmt.Println()
			fmt.Println("> " + strings.ReplaceAll(f.Suggestion, "\n", "\n> "))
		}
		if f.CommentURL != "" {
			fmt.Printf("\n[view comment](%s)\n", f.CommentURL)
		}
		fmt.Println()
	}
	return nil
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func severityIcon(sev string) string {
	switch strings.ToLower(sev) {
	case "critical":
		return "🔴"
	case "bug":
		return "🟠"
	case "warning":
		return "🟡"
	case "suggestion":
		return "🔵"
	case "nitpick":
		return "⚪"
	default:
		return "·"
	}
}

func init() {
	findingsCmd.Flags().StringP("repo", "r", "", "GitHub repo (owner/name); defaults to current repo")
	findingsCmd.Flags().StringP("output", "o", "markdown", "Output format: markdown or json")
	findingsCmd.Flags().Bool("watch", false, "Poll until the review check completes before returning")
	findingsCmd.Flags().String("since-commit", "", "Drop findings anchored on this commit SHA (used for loop deduplication)")
	findingsCmd.Flags().Int("timeout", 15, "Max minutes to wait when --watch is set (0 = no timeout)")
	rootCmd.AddCommand(findingsCmd)
}
