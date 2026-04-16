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
	Short: "Install the codecanary-fix Claude Code skill onto your machine",
	Long: `Writes the embedded codecanary-fix skill to disk so Claude Code can
discover and invoke it. The skill drives a review → triage → fix → push
feedback loop against a PR and converges to zero findings.

Default destination is ~/.claude/skills/codecanary-fix/SKILL.md, which
makes the skill available in every Claude Code session regardless of
working directory. Use --dest for a custom path (e.g. project-local),
--print to dump the content to stdout without writing, or --force to
overwrite an existing file.

If a legacy codecanary-loop skill is present under the default path — left
over from pre-rename installs — it is removed on a successful install to
avoid duplicate discovery in Claude Code. Custom --dest installs skip
this cleanup since the caller is driving placement themselves.

The skill content is embedded in the codecanary binary; re-run this
command after upgrading codecanary to pick up any updates.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		destFlag, err := cmd.Flags().GetString("dest")
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

		content := skills.CodecanaryFix()

		if printOnly {
			_, err := fmt.Print(content)
			return err
		}

		// Track whether we're installing to the default path so we know
		// whether it's safe to auto-clean the legacy codecanary-loop
		// directory. Users who point --dest elsewhere are managing
		// placement themselves; don't reach into their home on their
		// behalf.
		usingDefaultDest := destFlag == ""
		dest := destFlag
		if usingDefaultDest {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("locating home directory: %w", err)
			}
			dest = filepath.Join(home, ".claude", "skills", "codecanary-fix", "SKILL.md")
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

		fmt.Fprintf(os.Stderr, "✓ installed codecanary-fix skill to %s\n", dest)

		if usingDefaultDest {
			removeLegacyLoopSkill()
		}

		fmt.Fprintln(os.Stderr, "  Restart Claude Code to pick it up.")
		return nil
	},
}

// removeLegacyLoopSkill deletes a prior install of the pre-rename
// codecanary-loop skill at ~/.claude/skills/codecanary-loop/ so users who
// reinstall after the rename don't end up with both skills competing for
// the same trigger phrases. Best-effort: logs and continues on any error
// so a failure here never blocks the primary install.
func removeLegacyLoopSkill() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	legacyDir := filepath.Join(home, ".claude", "skills", "codecanary-loop")
	legacyFile := filepath.Join(legacyDir, "SKILL.md")

	if _, err := os.Stat(legacyFile); err != nil {
		// Nothing to remove — either no legacy install, or we can't
		// read the path. Either way it's not our place to surface a
		// warning: the user's intent is to install the new skill,
		// not to audit stale files.
		return
	}

	if err := os.Remove(legacyFile); err != nil {
		fmt.Fprintf(os.Stderr,
			"  warning: could not remove legacy skill at %s: %v\n", legacyFile, err)
		return
	}
	// Only remove the directory if empty — the user may have dropped
	// their own files in there and we shouldn't nuke those.
	if rmErr := os.Remove(legacyDir); rmErr == nil {
		fmt.Fprintf(os.Stderr,
			"  removed legacy codecanary-loop skill from %s\n", legacyDir)
	} else {
		fmt.Fprintf(os.Stderr,
			"  removed legacy SKILL.md at %s (other files remain in the directory)\n",
			legacyFile)
	}
}

func init() {
	installSkillCmd.Flags().String("dest", "",
		"Destination file path (default: ~/.claude/skills/codecanary-fix/SKILL.md)")
	installSkillCmd.Flags().Bool("print", false,
		"Print the skill content to stdout instead of writing to disk")
	installSkillCmd.Flags().Bool("force", false,
		"Overwrite the destination file if it already exists")
	rootCmd.AddCommand(installSkillCmd)
}
