package review

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repository helpers shared across flows: workflow discovery and the
// claude provider's tool-use confinement root both need the repo root.

// gitRepoRoot returns the absolute path to the current git repository's
// root via `git rev-parse --show-toplevel`, or an empty string when
// not inside a git repo. An empty return makes filepath.Join collapse
// to the cwd-relative path, which is the right fallback for tests
// that Chdir into a bare temp directory.
func gitRepoRoot() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// errSymlinkInPath is returned by readRepoFile for a path that goes through a
// symbolic link.
var errSymlinkInPath = errors.New("path goes through a symbolic link")

// readRepoFile reads rel, a path relative to root (or to the current
// directory when root is ""), refusing any path in which a component is a
// symbolic link. In CI the checkout is the PR's code, and on a fork PR an
// attacker could commit a link such as notes.md -> /proc/self/environ: a
// plain os.ReadFile would put the process environment, provider secret
// included, into the prompt, where injected instructions could get it
// quoted in a public finding. Links that stay inside the repository are
// refused too: a review reads the diff of a link (its target path), not the
// target's contents.
//
// PR file paths are repository-relative. An absolute rel (tests only) is
// checked from its parent directory down, since the parent of a temp dir can
// itself be a system symlink (/var -> /private/var on macOS).
func readRepoFile(root, rel string) ([]byte, error) {
	if filepath.IsAbs(rel) {
		root, rel = filepath.Dir(rel), filepath.Base(rel)
	}
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(realRoot, rel)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	if resolved != path {
		return nil, fmt.Errorf("%s: %w", rel, errSymlinkInPath)
	}
	return os.ReadFile(path)
}
