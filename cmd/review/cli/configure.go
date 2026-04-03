package cli

import (
	"fmt"
	"os"

	"github.com/alansikora/codecanary/internal/setup"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var configureCmd = &cobra.Command{
	Use:   "configure",
	Short: "Reconfigure provider, models, and credentials",
	Long:  "Update the AI provider, review/triage models, and API credentials on an existing config.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return fmt.Errorf("configure requires an interactive terminal")
		}
		return setup.RunConfigure()
	},
}

func init() {
	rootCmd.AddCommand(configureCmd)
}
