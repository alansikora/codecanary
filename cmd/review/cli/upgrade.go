package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/alansikora/codecanary/internal/selfupdate"
	"github.com/spf13/cobra"
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade codecanary to the latest version",
	Long:  "Download and install the latest codecanary release, replacing the current binary.",
	RunE: func(cmd *cobra.Command, args []string) error {
		canary, _ := cmd.Flags().GetBool("canary")

		tag := ""
		if canary {
			tag = "canary"
		}

		if err := selfupdate.Upgrade(cmd.Context(), Version, tag, os.Stderr); err != nil {
			return fmt.Errorf("upgrade failed: %w", err)
		}

		// Binary has been swapped in place — re-exec it so the new
		// embedded skill is the one we compare against disk. The
		// currently-running process still holds the old skill bytes.
		// Best-effort: any failure here is logged but never fails the
		// upgrade, since the upgrade itself already succeeded.
		offerSkillRefresh()
		return nil
	},
}

// offerSkillRefresh re-execs the newly installed binary with the hidden
// `install-skill --post-upgrade` flag, which compares the embedded skill
// to the installed copy and prompts the operator when they diverge.
// Only reachable after a successful selfupdate.Upgrade — if we can't
// resolve the binary path or the subprocess errors, we swallow the
// failure rather than poisoning the upgrade's exit status.
func offerSkillRefresh() {
	execPath, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, err := filepath.EvalSymlinks(execPath); err == nil {
		execPath = resolved
	}
	refresh := exec.Command(execPath, "install-skill", "--post-upgrade")
	refresh.Stdin = os.Stdin
	refresh.Stdout = os.Stdout
	refresh.Stderr = os.Stderr
	if err := refresh.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "note: skill refresh subprocess exited with error: %v\n", err)
	}
}

func init() {
	upgradeCmd.Flags().Bool("canary", false, "Upgrade to the latest canary (pre-release) build")
	rootCmd.AddCommand(upgradeCmd)
}
