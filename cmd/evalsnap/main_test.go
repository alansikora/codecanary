package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitWouldTrack is what stands between a private repository's source and a
// public git history, so its edges are worth pinning down: a destination that
// does not exist yet, one git ignores, one git would track, and one outside
// any repository at all.
func TestGitWouldTrack(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored/\n"), 0o644); err != nil {
		t.Fatalf("writing .gitignore: %v", err)
	}
	for _, d := range []string{"tracked", "ignored"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0o755); err != nil {
			t.Fatalf("creating %s: %v", d, err)
		}
	}

	cases := []struct {
		name string
		dir  string
		want bool
	}{
		{"tracked directory inside a repo", filepath.Join(repo, "tracked"), true},
		{"gitignored directory", filepath.Join(repo, "ignored"), false},
		{"gitignored directory that does not exist yet", filepath.Join(repo, "ignored", "corpus"), false},
		{"path outside any git work tree", t.TempDir(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := gitWouldTrack(tc.dir)
			if err != nil {
				t.Fatalf("gitWouldTrack: %v", err)
			}
			if got != tc.want {
				t.Errorf("gitWouldTrack(%s) = %v, want %v", tc.dir, got, tc.want)
			}
		})
	}
}

// A destination that does not exist yet inside a tracked directory must still
// read as tracked: creating it is exactly what evalsnap is about to do, and
// answering "false" because it is absent would open the hole the guard exists
// to close.
func TestGitWouldTrack_UncreatedPathUnderTrackedDir(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")

	got, err := gitWouldTrack(filepath.Join(repo, "corpus", "nested"))
	if err != nil {
		t.Fatalf("gitWouldTrack: %v", err)
	}
	if !got {
		t.Error("an uncreated path under a tracked repo should read as tracked")
	}
}

func TestFixtureName(t *testing.T) {
	if got := fixtureName("", "owner/name", 42); got != "owner-name-pr42" {
		t.Errorf("fixtureName = %q, want owner-name-pr42", got)
	}
	if got := fixtureName("custom", "owner/name", 42); got != "custom" {
		t.Errorf("explicit name should win, got %q", got)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
