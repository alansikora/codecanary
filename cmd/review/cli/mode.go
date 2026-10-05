package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

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

The output also carries update status so callers that parse stdout (the
codecanary-fix skill) can surface it: the running version, the latest
known release from the cached version check, and whether the skill
installed by 'codecanary install-skill' differs from the copy embedded in
this binary.

Use --output json to get the full detection payload for automation.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		output, _ := cmd.Flags().GetString("output")

		mode, err := review.DetectMode()
		if err != nil {
			return err
		}
		info := buildModeOutput(mode)

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

// modeOutput is the `codecanary mode` payload: the detected loop mode plus
// version and skill-freshness status. Update status lives here rather than
// in review.ModeInfo because it's a CLI/distribution concern, not part of
// mode detection.
type modeOutput struct {
	*review.ModeInfo
	Version         string     `json:"version"`
	LatestVersion   string     `json:"latest_version,omitempty"`
	UpdateAvailable bool       `json:"update_available"`
	Skill           skillState `json:"skill"`
}

// skillState describes the skill at the default install-skill location.
// Copies placed elsewhere (--dest, or a project-mode .claude/skills copy)
// are not inspected — we can't tell which file Claude Code actually loaded.
type skillState struct {
	Path      string `json:"path"`
	Installed bool   `json:"installed"`
	Stale     bool   `json:"stale"`
}

func buildModeOutput(mode *review.ModeInfo) *modeOutput {
	out := &modeOutput{ModeInfo: mode, Version: DisplayVersion()}

	latest, hasUpdate := checkForUpdate()
	out.UpdateAvailable = hasUpdate
	if hasUpdate {
		out.LatestVersion = strings.TrimPrefix(latest, "v")
	}

	// Best-effort: an unreadable skill file must not fail mode detection.
	installed, differs, path, err := skillNeedsUpgrade()
	out.Skill = skillState{Path: path}
	if err == nil {
		out.Skill.Installed = installed
		out.Skill.Stale = installed && differs
	}
	return out
}

func emitModeHuman(info *modeOutput) error {
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
	if info.UpdateAvailable {
		fmt.Printf("Version: %s (update available: %s — run 'codecanary upgrade')\n",
			info.Version, info.LatestVersion)
	} else {
		fmt.Printf("Version: %s\n", info.Version)
	}
	switch {
	case !info.Skill.Installed:
		fmt.Println("Skill: (not installed via install-skill)")
	case info.Skill.Stale:
		fmt.Printf("Skill: %s (stale — run 'codecanary install-skill --force')\n", info.Skill.Path)
	default:
		fmt.Printf("Skill: %s\n", info.Skill.Path)
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
