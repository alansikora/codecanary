package cli

import (
	"fmt"

	"github.com/alansikora/codecanary/internal/credentials"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage stored credentials",
	Long:  "View and manage API keys stored locally.",
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show stored credential status",
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, source, err := credentials.Retrieve(); err == nil {
			fmt.Printf("  %s: stored in %s\n", credentials.EnvVar, source)
		} else {
			fmt.Printf("  %s: not found\n", credentials.EnvVar)
		}
		return nil
	},
}

var authDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete stored credential",
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, _, err := credentials.Retrieve(); err != nil {
			fmt.Println("No stored credential found.")
			return nil
		}

		var confirm bool
		err := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(fmt.Sprintf("Delete %s?", credentials.EnvVar)).
					Value(&confirm),
			),
		).Run()
		if err != nil {
			return err
		}

		if !confirm {
			fmt.Println("Cancelled.")
			return nil
		}

		if err := credentials.Delete(); err != nil {
			return fmt.Errorf("deleting credential: %w", err)
		}
		fmt.Printf("Deleted %s.\n", credentials.EnvVar)
		return nil
	},
}

func init() {
	authCmd.AddCommand(authStatusCmd)
	authCmd.AddCommand(authDeleteCmd)
	rootCmd.AddCommand(authCmd)
}
