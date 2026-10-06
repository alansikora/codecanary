package review

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A fork PR can commit symbolic links. Reading through one must never pull a
// file from outside the checkout (the classic target is /proc/self/environ,
// which holds the provider secret) or from .git into the prompt. Links that
// stay inside the repository, such as CLAUDE.md -> AGENTS.md, still work.
func TestReadRepoFileSymlinks(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "environ")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(secret, []byte("CODECANARY_PROVIDER_SECRET=sk-test"), 0o600))

	repo := t.TempDir()
	must(os.WriteFile(filepath.Join(repo, "real.go"), []byte("package x"), 0o644))
	must(os.MkdirAll(filepath.Join(repo, ".git"), 0o755))
	must(os.WriteFile(filepath.Join(repo, ".git", "config"), []byte("extraheader = AUTHORIZATION: basic sk-token"), 0o600))
	must(os.Symlink(secret, filepath.Join(repo, "notes.md")))                              // file link out of the repo
	must(os.Symlink(outside, filepath.Join(repo, "linkdir")))                              // directory link out of the repo
	must(os.Symlink(filepath.Join(repo, ".git", "config"), filepath.Join(repo, "gitcfg"))) // link into .git
	must(os.Symlink("real.go", filepath.Join(repo, "inside-link.go")))                     // link that stays inside

	for _, rel := range []string{"real.go", "inside-link.go"} {
		if data, err := readRepoFile(repo, rel); err != nil || string(data) != "package x" {
			t.Errorf("readRepoFile(%q) = %q, %v; want the file's contents", rel, data, err)
		}
	}
	for _, rel := range []string{"notes.md", filepath.Join("linkdir", "environ"), "gitcfg"} {
		if _, err := readRepoFile(repo, rel); !errors.Is(err, errSymlinkInPath) {
			t.Errorf("readRepoFile(%q) err = %v, want errSymlinkInPath", rel, err)
		}
	}

	// FetchFileContents reads PR files relative to the cwd. A link out of the
	// repo is reviewed from its diff only: never read, never dropped.
	t.Chdir(repo)
	external := []string{"notes.md", filepath.Join("linkdir", "environ"), "gitcfg"}
	res := FetchFileContents(append([]string{"real.go", "inside-link.go"}, external...), nil, 1<<20, 1<<20)
	for path, content := range res.Contents {
		if strings.Contains(content, "sk-") {
			t.Fatalf("secret leaked into contents via %s", path)
		}
	}
	if _, ok := res.Contents["inside-link.go"]; !ok {
		t.Errorf("a link inside the repo should be read, got %v", res.Contents)
	}
	if got, want := strings.Join(res.DiffOnly, ","), strings.Join(external, ","); got != want {
		t.Errorf("DiffOnly = %q, want the external links %q", got, want)
	}
	if len(res.Excluded) != 0 {
		t.Errorf("links shouldn't be excluded from the review, got %v", res.Excluded)
	}

	// Project docs: CLAUDE.md -> AGENTS.md loads; a doc linking out doesn't.
	must(os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("Use tabs."), 0o644))
	must(os.Symlink("AGENTS.md", filepath.Join(repo, "CLAUDE.md")))
	must(os.MkdirAll(filepath.Join(repo, ".claude"), 0o755))
	must(os.Symlink(secret, filepath.Join(repo, ".claude", "CLAUDE.md")))
	docs := readProjectDocsFrom(repo, []string{"real.go"})
	if docs["CLAUDE.md"] != "Use tabs." {
		t.Errorf("CLAUDE.md -> AGENTS.md should load, got %q", docs["CLAUDE.md"])
	}
	for path, content := range docs {
		if strings.Contains(content, "sk-") {
			t.Fatalf("secret leaked into project docs via %s", path)
		}
	}
}
