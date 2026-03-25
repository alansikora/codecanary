package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/alansikora/codecanary/internal/review"
	"github.com/spf13/cobra"
)

var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate a review config by analyzing the project with Claude",
	RunE: func(cmd *cobra.Command, args []string) error {
		dryRun, _ := cmd.InheritedFlags().GetBool("dry-run")
		force, _ := cmd.Flags().GetBool("force")

		fmt.Fprintf(os.Stderr, "Analyzing project...\n")

		yamlStr, err := review.Generate()
		if err != nil {
			return fmt.Errorf("generating config: %w", err)
		}

		if dryRun {
			fmt.Print(yamlStr)
			return nil
		}

		configPath := ".codecanary.yml"

		// Back up existing file unless --force.
		if _, err := os.Stat(configPath); err == nil {
			if !force {
				bakPath := configPath + "." + time.Now().Format("20060102-150405") + ".bak"
				data, err := os.ReadFile(configPath)
				if err != nil {
					return fmt.Errorf("reading existing config for backup: %w", err)
				}
				if err := os.WriteFile(bakPath, data, 0644); err != nil {
					return fmt.Errorf("writing backup: %w", err)
				}
				fmt.Fprintf(os.Stderr, "  Backed up existing config to %s\n", bakPath)
			}
		}

		if err := os.WriteFile(configPath, []byte(yamlStr+"\n"), 0644); err != nil {
			return fmt.Errorf("writing config: %w", err)
		}

		fmt.Fprintf(os.Stderr, "  Created %s\n", configPath)
		return nil
	},
}

func init() {
	generateCmd.Flags().Bool("force", false, "Overwrite existing config without backup")
	reviewCmd.AddCommand(generateCmd)
}
