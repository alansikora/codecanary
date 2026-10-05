package review

import (
	"os/exec"
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
