package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alansikora/codecanary/internal/review"
	"github.com/alansikora/codecanary/internal/skills"
)

// stubModeEnv sandboxes HOME, clears CI, pins Version, and replaces the
// cached version check so buildModeOutput runs without touching the
// network or the real ~/.codecanary cache.
func stubModeEnv(t *testing.T, version, latest string, hasUpdate bool) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CI", "")

	origVersion, origCheck := Version, checkLatestVersion
	t.Cleanup(func() { Version, checkLatestVersion = origVersion, origCheck })
	Version = version
	checkLatestVersion = func(string) (string, bool) { return latest, hasUpdate }
	return home
}

func seedSkill(t *testing.T, home, content string) string {
	t.Helper()
	dest := filepath.Join(home, ".claude", "skills", "codecanary-fix", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatalf("seeding dir: %v", err)
	}
	if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding SKILL.md: %v", err)
	}
	return dest
}

// modeJSON builds the payload and round-trips it through JSON so the test
// asserts on the wire format the skill parses, not on Go struct fields.
func modeJSON(t *testing.T) map[string]any {
	t.Helper()
	pr := 42
	out := buildModeOutput(&review.ModeInfo{Mode: "pr-loop", PR: &pr, Branch: "feat/x", Reasons: []string{}})
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func TestModeOutput_UpdateAvailable(t *testing.T) {
	stubModeEnv(t, "v1.2.3", "v1.3.0", true)
	m := modeJSON(t)

	if m["mode"] != "pr-loop" || m["branch"] != "feat/x" || m["pr"] != float64(42) {
		t.Fatalf("mode fields not flattened into payload: %v", m)
	}
	if m["version"] != "1.2.3" {
		t.Errorf("version = %v, want 1.2.3", m["version"])
	}
	if m["latest_version"] != "1.3.0" {
		t.Errorf("latest_version = %v, want 1.3.0", m["latest_version"])
	}
	if m["update_available"] != true {
		t.Errorf("update_available = %v, want true", m["update_available"])
	}
}

func TestModeOutput_NoUpdate(t *testing.T) {
	stubModeEnv(t, "v1.3.0", "v1.3.0", false)
	m := modeJSON(t)
	if m["update_available"] != false {
		t.Errorf("update_available = %v, want false", m["update_available"])
	}
	// The cache knows the latest version, but it isn't newer: the skill's
	// contract is that latest_version is absent when there's no update.
	if _, ok := m["latest_version"]; ok {
		t.Errorf("latest_version should be omitted without an update, got %v", m["latest_version"])
	}
}

func TestModeOutput_CISkipsUpdateCheck(t *testing.T) {
	stubModeEnv(t, "v1.2.3", "v1.3.0", true)
	t.Setenv("CI", "true")
	m := modeJSON(t)
	if m["update_available"] != false {
		t.Errorf("update_available = %v, want false in CI", m["update_available"])
	}
	if _, ok := m["latest_version"]; ok {
		t.Errorf("latest_version should be omitted in CI, got %v", m["latest_version"])
	}
}

func TestModeOutput_Skill(t *testing.T) {
	tests := []struct {
		name          string
		seed          *string
		wantInstalled bool
		wantStale     bool
	}{
		{name: "not installed", seed: nil, wantInstalled: false, wantStale: false},
		{name: "fresh", seed: ptr(skills.CodecanaryFix()), wantInstalled: true, wantStale: false},
		{name: "stale", seed: ptr("older skill content"), wantInstalled: true, wantStale: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := stubModeEnv(t, "v1.2.3", "", false)
			wantPath := filepath.Join(home, ".claude", "skills", "codecanary-fix", "SKILL.md")
			if tt.seed != nil {
				seedSkill(t, home, *tt.seed)
			}

			skill, ok := modeJSON(t)["skill"].(map[string]any)
			if !ok {
				t.Fatalf("skill object missing from payload")
			}
			if skill["path"] != wantPath {
				t.Errorf("skill.path = %v, want %s", skill["path"], wantPath)
			}
			if skill["installed"] != tt.wantInstalled {
				t.Errorf("skill.installed = %v, want %v", skill["installed"], tt.wantInstalled)
			}
			if skill["stale"] != tt.wantStale {
				t.Errorf("skill.stale = %v, want %v", skill["stale"], tt.wantStale)
			}
		})
	}
}

func ptr(s string) *string { return &s }
