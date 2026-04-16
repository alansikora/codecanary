package cli

import (
	"os"
	"path/filepath"
	"testing"
)

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
