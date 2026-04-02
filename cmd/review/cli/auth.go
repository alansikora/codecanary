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
	Long:  "View and manage API keys stored in the system keychain.",
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show stored credential status",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Provider credentials:")
		for _, envVar := range credentials.KnownProviderEnvVars {
			if _, err := credentials.Retrieve(envVar); err == nil {
				fmt.Printf("  %s: stored in system keychain\n", envVar)
			} else {
				fmt.Printf("  %s: not found\n", envVar)
			}
		}
		return nil
	},
}

var authDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a stored credential",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Find which keys are stored.
		var stored []string
		for _, envVar := range credentials.KnownProviderEnvVars {
			if _, err := credentials.Retrieve(envVar); err == nil {
				stored = append(stored, envVar)
			}
		}
		if len(stored) == 0 {
			fmt.Println("No credentials stored in system keychain.")
			return nil
		}

		// Select which to delete.
		var selected string
		options := make([]huh.Option[string], len(stored))
		for i, s := range stored {
			options[i] = huh.NewOption(s, s)
		}
		err := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Which credential do you want to delete?").
					Options(options...).
					Value(&selected),
			),
		).Run()
		if err != nil {
			return err
		}

		// Confirm.
		var confirm bool
		err = huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(fmt.Sprintf("Delete %s from system keychain?", selected)).
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

		if err := credentials.Delete(selected); err != nil {
			return fmt.Errorf("deleting credential: %w", err)
		}
		fmt.Printf("Deleted %s from system keychain.\n", selected)
		return nil
	},
}

func init() {
	authCmd.AddCommand(authStatusCmd)
	authCmd.AddCommand(authDeleteCmd)
	rootCmd.AddCommand(authCmd)
}
