package cli

import (
	"fmt"
	"os"

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

		if err := selfupdate.Upgrade(Version, tag, os.Stderr); err != nil {
			return fmt.Errorf("upgrade failed: %w", err)
		}
		return nil
	},
}

func init() {
	upgradeCmd.Flags().Bool("canary", false, "Upgrade to the latest canary (pre-release) build")
	rootCmd.AddCommand(upgradeCmd)
}
