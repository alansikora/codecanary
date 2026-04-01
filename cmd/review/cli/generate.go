package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alansikora/codecanary/internal/review"
	"github.com/spf13/cobra"
)

var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate a review config by analyzing the project with Claude",
	RunE: func(cmd *cobra.Command, args []string) error {
		dryRun, _ := cmd.InheritedFlags().GetBool("dry-run")
		force, _ := cmd.Flags().GetBool("force")

		configPath := filepath.Join(".codecanary", "config.yml")

		// If config exists and not --force, ask for confirmation.
		if !dryRun && !force {
			if _, err := os.Stat(configPath); err == nil {
				fmt.Fprintf(os.Stderr, "%s already exists. Re-generate? [y/N] ", configPath)
				reader := bufio.NewReader(os.Stdin)
				answer, _ := reader.ReadString('\n')
				if answer = strings.TrimSpace(strings.ToLower(answer)); answer != "y" && answer != "yes" {
					fmt.Fprintf(os.Stderr, "Keeping current config.\n")
					return nil
				}
			}
		}

		fmt.Fprintf(os.Stderr, "Analyzing project...\n")

		yamlStr, err := review.Generate(nil)
		if err != nil {
			return fmt.Errorf("generating config: %w", err)
		}

		if dryRun {
			fmt.Print(yamlStr)
			return nil
		}

		// Ensure .codecanary/ directory exists.
		if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
			return fmt.Errorf("creating .codecanary directory: %w", err)
		}

		if err := os.WriteFile(configPath, []byte(yamlStr+"\n"), 0644); err != nil {
			return fmt.Errorf("writing config: %w", err)
		}

		fmt.Fprintf(os.Stderr, "  Created %s\n", configPath)
		return nil
	},
}

func init() {
	generateCmd.Flags().Bool("force", false, "Overwrite existing config without prompting")
	reviewCmd.AddCommand(generateCmd)
}
