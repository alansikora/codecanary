package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/alansikora/codecanary/internal/selfupdate"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var Version = "dev"

func DisplayVersion() string {
	return strings.TrimPrefix(Version, "v")
}

var rootCmd = &cobra.Command{
	Use:   "codecanary",
	Short: "AI-powered code review for GitHub pull requests",
	Long:  "Catch bugs, security issues, and quality problems before they land in main.",
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		// Skip version check in CI, for the upgrade command itself, or if
		// the user just asked for --version.
		if os.Getenv("CI") != "" {
			return
		}
		if cmd.Name() == "upgrade" {
			return
		}

		latest, hasUpdate := selfupdate.CheckCached(Version)
		if hasUpdate {
			fmt.Fprintf(os.Stderr,
				"\nA new version of codecanary is available: %s → %s\nRun 'codecanary upgrade' to update.\n",
				Version, latest)
		}
	},
}

func Execute() error {
	rootCmd.Version = DisplayVersion()
	return rootCmd.Execute()
}

// requireTerminal returns an error if stdin is not an interactive terminal.
func requireTerminal(name string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("%s requires an interactive terminal", name)
	}
	return nil
}
