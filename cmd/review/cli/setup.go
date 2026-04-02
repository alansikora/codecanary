package cli

import (
	"fmt"
	"os"

	"github.com/alansikora/codecanary/internal/setup"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Set up CodeCanary for local or GitHub use",
	Long:  "Interactive setup wizard. Run `codecanary setup local` or `codecanary setup github`, or run without arguments to choose.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return fmt.Errorf("setup requires an interactive terminal")
		}
		mode, err := setup.SelectSetupMode()
		if err != nil {
			return err
		}
		switch mode {
		case "local":
			return setup.RunLocal()
		case "github":
			canary, err := cmd.Flags().GetBool("canary")
			if err != nil {
				return fmt.Errorf("flag --canary: %w", err)
			}
			return setup.RunGitHub(canary)
		default:
			return fmt.Errorf("unknown setup mode: %s", mode)
		}
	},
}

var setupLocalCmd = &cobra.Command{
	Use:   "local",
	Short: "Set up CodeCanary for local development",
	Long:  "Configure a provider and API key for reviewing changes on this machine.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return fmt.Errorf("setup requires an interactive terminal")
		}
		return setup.RunLocal()
	},
}

var setupGithubCmd = &cobra.Command{
	Use:   "github",
	Short: "Set up CodeCanary for GitHub Actions",
	Long:  "Configure automated PR reviews via GitHub Actions. Creates a workflow, sets secrets, and opens a PR.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return fmt.Errorf("setup requires an interactive terminal")
		}
		canary, err := cmd.Flags().GetBool("canary")
		if err != nil {
			return fmt.Errorf("flag --canary: %w", err)
		}
		return setup.RunGitHub(canary)
	},
}

func init() {
	setupCmd.PersistentFlags().Bool("canary", false, "Use canary (prerelease) version")
	setupCmd.AddCommand(setupLocalCmd)
	setupCmd.AddCommand(setupGithubCmd)
	rootCmd.AddCommand(setupCmd)
}
