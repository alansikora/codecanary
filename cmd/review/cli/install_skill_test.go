package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alansikora/codecanary/internal/skills"
)

// TestSkillNeedsUpgrade covers the drift-detection helper that powers
// the post-upgrade skill refresh prompt. We exercise the three states
// callers have to distinguish (not installed / in sync / drifted) and
// sandbox HOME so the check runs against a temp dir.
func TestSkillNeedsUpgrade(t *testing.T) {
	t.Run("not installed — installed=false", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		installed, differs, _, err := skillNeedsUpgrade()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if installed || differs {
			t.Fatalf("expected installed=false, differs=false; got installed=%v differs=%v",
				installed, differs)
		}
	})

	t.Run("in sync — differs=false", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		dest := filepath.Join(home, ".claude", "skills", "codecanary-fix", "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			t.Fatalf("seeding dir: %v", err)
		}
		if err := os.WriteFile(dest, []byte(skills.CodecanaryFix()), 0o644); err != nil {
			t.Fatalf("seeding SKILL.md: %v", err)
		}

		installed, differs, got, err := skillNeedsUpgrade()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !installed || differs {
			t.Fatalf("expected installed=true, differs=false; got installed=%v differs=%v",
				installed, differs)
		}
		if got != dest {
			t.Fatalf("destPath = %q, want %q", got, dest)
		}
	})

	t.Run("drifted — differs=true", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		dest := filepath.Join(home, ".claude", "skills", "codecanary-fix", "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			t.Fatalf("seeding dir: %v", err)
		}
		if err := os.WriteFile(dest, []byte("older skill content"), 0o644); err != nil {
			t.Fatalf("seeding SKILL.md: %v", err)
		}

		installed, differs, _, err := skillNeedsUpgrade()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !installed || !differs {
			t.Fatalf("expected installed=true, differs=true; got installed=%v differs=%v",
				installed, differs)
		}
	})
}

// TestRemoveLegacyLoopSkill exercises the migration cleanup that runs on
// every default `install-skill` — users who installed before the
// codecanary-loop → codecanary-fix rename would otherwise end up with
// both skills registered in Claude Code. The real function consults
// os.UserHomeDir(); we point HOME at t.TempDir() so the cleanup acts on
// a sandbox instead of the developer's actual ~/.claude/skills/.
func TestRemoveLegacyLoopSkill(t *testing.T) {
	t.Run("removes whole directory when it only contains SKILL.md", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		legacyDir := filepath.Join(home, ".claude", "skills", "codecanary-loop")
		if err := os.MkdirAll(legacyDir, 0o755); err != nil {
			t.Fatalf("seeding legacy dir: %v", err)
		}
		legacyFile := filepath.Join(legacyDir, "SKILL.md")
		if err := os.WriteFile(legacyFile, []byte("stale"), 0o644); err != nil {
			t.Fatalf("seeding legacy SKILL.md: %v", err)
		}

		removeLegacyLoopSkill()

		if _, err := os.Stat(legacyDir); !os.IsNotExist(err) {
			t.Fatalf("expected legacy dir to be gone, got err=%v", err)
		}
	})

	t.Run("keeps directory when other files live alongside SKILL.md", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		legacyDir := filepath.Join(home, ".claude", "skills", "codecanary-loop")
		if err := os.MkdirAll(legacyDir, 0o755); err != nil {
			t.Fatalf("seeding legacy dir: %v", err)
		}
		legacyFile := filepath.Join(legacyDir, "SKILL.md")
		if err := os.WriteFile(legacyFile, []byte("stale"), 0o644); err != nil {
			t.Fatalf("seeding legacy SKILL.md: %v", err)
		}
		// A sibling file the user may have added — we must not delete it.
		sibling := filepath.Join(legacyDir, "notes.md")
		if err := os.WriteFile(sibling, []byte("user notes"), 0o644); err != nil {
			t.Fatalf("seeding sibling: %v", err)
		}

		removeLegacyLoopSkill()

		if _, err := os.Stat(legacyFile); !os.IsNotExist(err) {
			t.Fatalf("expected legacy SKILL.md to be gone, got err=%v", err)
		}
		if _, err := os.Stat(sibling); err != nil {
			t.Fatalf("expected sibling file to survive, got err=%v", err)
		}
		if _, err := os.Stat(legacyDir); err != nil {
			t.Fatalf("expected legacy dir to survive (sibling still inside), got err=%v", err)
		}
	})

	t.Run("is a no-op when no legacy install exists", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		// Shouldn't panic, shouldn't error, shouldn't create anything.
		removeLegacyLoopSkill()

		legacyDir := filepath.Join(home, ".claude", "skills", "codecanary-loop")
		if _, err := os.Stat(legacyDir); !os.IsNotExist(err) {
			t.Fatalf("expected legacy dir to remain absent, got err=%v", err)
		}
	})
}
