package cli

import (
	"os"
	"strings"

	"github.com/alansikora/codecanary/internal/review"
	"github.com/alansikora/codecanary/internal/update"
	"github.com/spf13/cobra"
)

var Version = "dev"

func DisplayVersion() string {
	v := Version
	if len(v) > 1 && v[0] == 'v' && (v[1] < '0' || v[1] > '9') {
		v = v[1:]
	}
	return v
}

var rootCmd = &cobra.Command{
	Use:   "codecanary",
	Short: "AI-powered code review for GitHub pull requests",
	Long:  "Catch bugs, security issues, and quality problems before they land in main.",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if Version == "dev" || os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" {
			return
		}
		if cmd.Name() == "update" {
			return
		}
		latest := update.CachedLatestVersion()
		if latest != "" && update.IsNewer(Version, latest) {
			review.Stderrf(review.ColorYellow,
				"Update available: %s → %s — run 'codecanary update' to upgrade\n",
				strings.TrimPrefix(Version, "v"), strings.TrimPrefix(latest, "v"))
		}
	},
}

func Execute() error {
	rootCmd.Version = DisplayVersion()
	return rootCmd.Execute()
}
