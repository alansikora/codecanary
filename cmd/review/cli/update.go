package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/alansikora/codecanary/internal/review"
	"github.com/alansikora/codecanary/internal/update"
	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update codecanary to the latest version",
	Long:  "Check for and install the latest version of codecanary.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if Version == "dev" {
			return fmt.Errorf("cannot update a development build — install a release version first")
		}

		canary, _ := cmd.Flags().GetBool("canary")

		review.Stderrf(review.ColorDim, "Checking for updates...\n")

		latest, err := update.Update(context.Background(), Version, canary)
		if err != nil {
			return err
		}

		if update.IsNewer(Version, latest) || (canary && Version != latest) {
			review.Stderrf(review.ColorGreen, "Updated codecanary %s → %s\n",
				strings.TrimPrefix(Version, "v"), strings.TrimPrefix(latest, "v"))
		} else {
			review.Stderrf(review.ColorGreen, "Already up to date (%s)\n",
				strings.TrimPrefix(Version, "v"))
		}
		return nil
	},
}

func init() {
	updateCmd.Flags().Bool("canary", false, "Update to the latest prerelease version")
	rootCmd.AddCommand(updateCmd)
}
