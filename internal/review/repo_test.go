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
// which holds the provider secret) into the prompt.
func TestReadRepoFileRefusesSymlinks(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "environ")
	if err := os.WriteFile(secret, []byte("CODECANARY_PROVIDER_SECRET=sk-test"), 0o600); err != nil {
		t.Fatal(err)
	}

	repo := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(repo, "real.go"), []byte("package x"), 0o644))
	must(os.Symlink(secret, filepath.Join(repo, "notes.md")))          // file link out of the repo
	must(os.Symlink(outside, filepath.Join(repo, "linkdir")))          // directory link out of the repo
	must(os.Symlink("real.go", filepath.Join(repo, "inside-link.go"))) // link that stays inside

	if data, err := readRepoFile(repo, "real.go"); err != nil || string(data) != "package x" {
		t.Fatalf("regular file: %q, %v", data, err)
	}
	for _, rel := range []string{"notes.md", filepath.Join("linkdir", "environ"), "inside-link.go"} {
		if _, err := readRepoFile(repo, rel); !errors.Is(err, errSymlinkInPath) {
			t.Errorf("readRepoFile(%q) err = %v, want errSymlinkInPath", rel, err)
		}
	}

	// FetchFileContents reads PR files relative to the cwd.
	t.Chdir(repo)
	res := FetchFileContents([]string{"real.go", "notes.md", filepath.Join("linkdir", "environ")}, nil, 1<<20, 1<<20)
	for path, content := range res.Contents {
		if strings.Contains(content, "sk-test") {
			t.Fatalf("secret leaked into contents via %s", path)
		}
	}
	if _, ok := res.Contents["real.go"]; !ok {
		t.Errorf("regular file should be read, got %v", res.Contents)
	}
	if got := strings.Join(res.Excluded, ","); got != "notes.md,"+filepath.Join("linkdir", "environ") {
		t.Errorf("symlinked paths should be excluded, got %q", got)
	}

	// CLAUDE.md that is a link out of the repo is not loaded as a project doc.
	must(os.Symlink(secret, filepath.Join(repo, "CLAUDE.md")))
	for path, content := range readProjectDocsFrom(repo, []string{"real.go"}) {
		if strings.Contains(content, "sk-test") {
			t.Fatalf("secret leaked into project docs via %s", path)
		}
	}
}
