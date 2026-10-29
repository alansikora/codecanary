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
                       branch. The bot reviews the PR; review_on
                       (reported below) says which events it reviews.
                       With fix_on_ci, the workflow also fixes the
                       bot's findings in CI and pushes the result.
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
this binary. confirm_fixes reports whether the operator's
.codecanary/review.local.yml asks the skill to confirm findings before
applying them.

Use --output json to get the full detection payload for automation.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		output, _ := cmd.Flags().GetString("output")

		mode, err := review.DetectMode()
		if err != nil {
			return err
		}
		info, err := buildModeOutput(mode)
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
	// ReviewOn is the repo's review_on setting ("push" or "ready"): which
	// pull request events the bot reviews.
	ReviewOn string `json:"review_on"`
	// FixOnCI reports the repo's fix.enabled: the workflow fixes the bot's
	// findings in CI, so the skill's pr-loop has nothing to apply itself.
	FixOnCI bool `json:"fix_on_ci"`
	// ConfirmFixes reports confirm_fixes from the operator's
	// .codecanary/review.local.yml: the skill asks which findings to apply
	// instead of applying its own triage.
	ConfirmFixes bool `json:"confirm_fixes"`
}

// skillState describes the skill at the default install-skill location.
// Copies placed elsewhere (--dest, or a project-mode .claude/skills copy)
// are not inspected — we can't tell which file Claude Code actually loaded.
type skillState struct {
	Path      string `json:"path"`
	Installed bool   `json:"installed"`
	Stale     bool   `json:"stale"`
}

func buildModeOutput(mode *review.ModeInfo) (*modeOutput, error) {
	// Unlike the repo config, an unreadable review.local.yml fails mode:
	// reading it as false would drop the operator's request to confirm
	// fixes and let the skill apply them unasked.
	confirm, err := review.ConfirmFixes()
	if err != nil {
		return nil, err
	}
	cfg := repoConfig()
	out := &modeOutput{ModeInfo: mode, Version: DisplayVersion(), ReviewOn: review.ReviewOnPush, FixOnCI: cfg.FixEnabled(), ConfirmFixes: confirm}
	if cfg != nil && cfg.ReviewOn != "" {
		out.ReviewOn = cfg.ReviewOn
	}

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
	return out, nil
}

func emitModeHuman(info *modeOutput) error {
	fmt.Printf("Mode: %s\n", info.Mode)
	fmt.Printf("Branch: %s\n", info.Branch)
	fmt.Printf("Review on: %s\n", info.ReviewOn)
	fmt.Printf("Fix on CI: %t\n", info.FixOnCI)
	fmt.Printf("Confirm fixes: %t\n", info.ConfirmFixes)
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

// repoConfig returns the repo's committed config (.codecanary/config.yml),
// which is what the bot runs with — not the operator's local config. Nil
// when there is none or it can't be read: mode detection must not fail on
// config, and the defaults (review_on: push, no CI fix) apply.
func repoConfig() *review.ReviewConfig {
	path, err := review.FindRepoConfig()
	if err != nil {
		return nil
	}
	cfg, err := review.LoadConfig(path)
	if err != nil {
		return nil
	}
	return cfg
}
