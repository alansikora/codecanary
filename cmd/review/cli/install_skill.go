package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/alansikora/codecanary/internal/skills"
	"github.com/spf13/cobra"
)

var installSkillCmd = &cobra.Command{
	Use:   "install-skill",
	Short: "Install the codecanary-loop Claude Code skill onto your machine",
	Long: `Writes the embedded codecanary-loop skill to disk so Claude Code can
discover and invoke it. The skill drives a review → triage → fix → push
feedback loop against a PR and converges to zero findings.

Default destination is ~/.claude/skills/codecanary-loop/SKILL.md, which
makes the skill available in every Claude Code session regardless of
working directory. Use --dest for a custom path (e.g. project-local),
--print to dump the content to stdout without writing, or --force to
overwrite an existing file.

The skill content is embedded in the codecanary binary; re-run this
command after upgrading codecanary to pick up any updates.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dest, _ := cmd.Flags().GetString("dest")
		printOnly, _ := cmd.Flags().GetBool("print")
		force, _ := cmd.Flags().GetBool("force")

		content := skills.CodecanaryLoop()

		if printOnly {
			_, err := fmt.Print(content)
			return err
		}

		if dest == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("locating home directory: %w", err)
			}
			dest = filepath.Join(home, ".claude", "skills", "codecanary-loop", "SKILL.md")
		}

		if _, err := os.Stat(dest); err == nil && !force {
			return fmt.Errorf(
				"file already exists at %s — pass --force to overwrite", dest)
		}

		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("creating parent directory: %w", err)
		}
		if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
			return fmt.Errorf("writing skill file: %w", err)
		}

		fmt.Fprintf(os.Stderr, "✓ installed codecanary-loop skill to %s\n", dest)
		fmt.Fprintln(os.Stderr, "  Restart Claude Code to pick it up.")
		return nil
	},
}

func init() {
	installSkillCmd.Flags().String("dest", "",
		"Destination file path (default: ~/.claude/skills/codecanary-loop/SKILL.md)")
	installSkillCmd.Flags().Bool("print", false,
		"Print the skill content to stdout instead of writing to disk")
	installSkillCmd.Flags().Bool("force", false,
		"Overwrite the destination file if it already exists")
	rootCmd.AddCommand(installSkillCmd)
}
