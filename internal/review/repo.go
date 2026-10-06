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

// errSymlinkInPath is returned by readRepoFile for a path that goes through
// a symbolic link leading outside the repository, or into its .git directory.
var errSymlinkInPath = errors.New("path goes through a symbolic link that leaves the repository")

// readRepoFile reads rel, a path relative to root (or to the current
// directory when root is ""). A path that goes through a symbolic link is
// read only when the link's real target stays inside root and outside
// root/.git. In CI the checkout is the PR's code, and on a fork PR an
// attacker could commit a link such as notes.md -> /proc/self/environ: a
// plain os.ReadFile would put the process environment, provider secret
// included, into the prompt, where injected instructions could get it
// quoted in a public finding. .git is excluded because on same-repo PRs the
// checkout keeps the token in .git/config. Links inside the repository, such
// as CLAUDE.md -> AGENTS.md, keep working.
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
	if resolved != path && !insideRepo(realRoot, resolved) {
		return nil, fmt.Errorf("%s: %w", rel, errSymlinkInPath)
	}
	return os.ReadFile(resolved)
}

// insideRepo reports whether path is under root and not under root/.git.
func insideRepo(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return rel != ".git" && !strings.HasPrefix(rel, ".git"+string(filepath.Separator))
}
