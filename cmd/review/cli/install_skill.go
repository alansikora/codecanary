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
		dest, err := cmd.Flags().GetString("dest")
		if err != nil {
			return fmt.Errorf("flag --dest: %w", err)
		}
		printOnly, err := cmd.Flags().GetBool("print")
		if err != nil {
			return fmt.Errorf("flag --print: %w", err)
		}
		force, err := cmd.Flags().GetBool("force")
		if err != nil {
			return fmt.Errorf("flag --force: %w", err)
		}

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

		// Distinguish "file exists" from other Stat errors (e.g.
		// permission denied on the parent) so we don't silently fall
		// through to writing in a genuinely inaccessible directory.
		switch _, statErr := os.Stat(dest); {
		case statErr == nil:
			if !force {
				return fmt.Errorf(
					"file already exists at %s — pass --force to overwrite", dest)
			}
		case !os.IsNotExist(statErr):
			return fmt.Errorf("checking destination %s: %w", dest, statErr)
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
