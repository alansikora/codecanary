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
		// Skip the notice for the upgrade command itself; checkForUpdate
		// already skips CI.
		if cmd.Name() == "upgrade" {
			return
		}

		latest, hasUpdate := checkForUpdate()
		if hasUpdate {
			fmt.Fprintf(os.Stderr,
				"\nA new version of codecanary is available: %s → %s\nRun 'codecanary upgrade' to update.\n",
				Version, latest)
		}
	},
}

// checkLatestVersion is selfupdate.CheckCached, swappable in tests.
var checkLatestVersion = selfupdate.CheckCached

// checkForUpdate reports the latest known release and whether it is newer
// than the running binary. It reads the 24h version-check cache (refreshing
// it in the background when stale) and never blocks on the network. CI runs
// skip the check entirely.
func checkForUpdate() (latest string, hasUpdate bool) {
	if os.Getenv("CI") != "" {
		return "", false
	}
	return checkLatestVersion(Version)
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
