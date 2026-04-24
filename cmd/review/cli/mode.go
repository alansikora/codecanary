package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/alansikora/codecanary/internal/review"
	"github.com/spf13/cobra"
)

var modeCmd = &cobra.Command{
	Use:   "mode",
	Short: "Detect which review loop applies to the current branch",
	Long: `Report which review loop the codecanary-fix skill should run for the
current branch.

Three modes, resolved in order:

  pr-loop            — PR open and CodeCanary workflow detected on the
                       branch. The bot runs on every push; fixes commit
                       and push each cycle.
  local-loop-git     — PR open but no CodeCanary workflow detected. The
                       loop reviews locally and commits each cycle on
                       the PR branch without pushing; the operator is
                       prompted to push at session end.
  local-loop-nogit   — no PR. The loop reviews locally and applies
                       fixes in place; no git mutations.

Use --output json to get the full detection payload for automation.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		output, _ := cmd.Flags().GetString("output")

		info, err := review.DetectMode()
		if err != nil {
			return err
		}

		switch output {
		case "json":
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(info)
		default:
			return emitModeHuman(info)
		}
	},
}

func emitModeHuman(info *review.ModeInfo) error {
	fmt.Printf("Mode: %s\n", info.Mode)
	fmt.Printf("Branch: %s\n", info.Branch)
	if info.Repo != "" {
		fmt.Printf("Repo: %s\n", info.Repo)
	}
	if info.PR != nil {
		fmt.Printf("PR: #%d\n", *info.PR)
	} else {
		fmt.Println("PR: (none)")
	}
	if info.WorkflowDetected {
		fmt.Printf("Workflow: %s\n", info.WorkflowPath)
	} else {
		fmt.Println("Workflow: (none)")
	}
	if len(info.Reasons) > 0 {
		fmt.Println("Reasons:")
		for _, r := range info.Reasons {
			fmt.Printf("  - %s\n", r)
		}
	}
	return nil
}

func init() {
	modeCmd.Flags().StringP("output", "o", "human", "Output format: human or json")
	rootCmd.AddCommand(modeCmd)
}
