package review

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// GitHub mode reads every rule source (see rulesource.go) from the PR's base
// commit. The files are copied out of git objects into a temporary
// directory, so nothing in the PR's checkout — including a symbolic link a
// fork committed — decides what is read.

// LoadRules loads the config, policy files, project docs and context files
// from the PR's base commit, and remembers the settings the platform itself
// applies (status name, fix gate, reply resolution).
func (g *GithubPlatform) LoadRules(pr *PRData, configPath string) (*RuleSources, error) {
	if pr == nil || !validSHA.MatchString(pr.BaseSHA) {
		return nil, fmt.Errorf("config error: the pull request's base commit is unknown, so its rules cannot be read")
	}
	if err := ensureCommit(pr.BaseSHA); err != nil {
		return nil, fmt.Errorf("config error: fetching base commit %s: %w", shortSHA(pr.BaseSHA), err)
	}
	root, err := os.MkdirTemp("", "codecanary-base-")
	if err != nil {
		return nil, err
	}
	paths, err := baseRuleSourcePaths(pr.BaseSHA)
	if err != nil {
		return nil, fmt.Errorf("config error: listing base commit %s: %w", shortSHA(pr.BaseSHA), err)
	}
	if err := materializeFromCommit(pr.BaseSHA, paths, root); err != nil {
		return nil, fmt.Errorf("config error: %w", err)
	}

	rel, err := repoRelativeConfigPath(configPath, root)
	if err != nil {
		return nil, err
	}
	cfg, err := loadConfig(filepath.Join(root, rel), false)
	if err != nil {
		return nil, fmt.Errorf("config error (base branch %s): %w", pr.BaseBranch, err)
	}
	// context_files can be anywhere; copy the ones the config lists. A
	// missing one is reported by readContextFiles.
	if err := materializeFromCommit(pr.BaseSHA, cfg.AllContextFiles(), root); err != nil {
		return nil, fmt.Errorf("config error: %w", err)
	}

	g.StatusContext = cfg.EffectiveStatusContext()
	g.FixOnCI = cfg.FixEnabled()
	g.ResolveAcceptedReplies = cfg.ResolveAcceptedReplies
	g.reviewOn = cfg.ReviewOn
	return &RuleSources{Config: cfg, Root: root, BaseSHA: pr.BaseSHA}, nil
}

// repoRelativeConfigPath maps --config to a repository path, checked against
// the copy of the base files in root. Empty means the default location
// (.codecanary/config.yml, then legacy .codecanary.yml).
func repoRelativeConfigPath(configPath, root string) (string, error) {
	if configPath == "" {
		for _, p := range []string{".codecanary/config.yml", ".codecanary.yml"} {
			if _, err := os.Stat(filepath.Join(root, p)); err == nil {
				return p, nil
			}
		}
		return "", fmt.Errorf("config error: no .codecanary/config.yml on the base branch")
	}
	rel := configPath
	if filepath.IsAbs(rel) {
		repoRoot := gitRepoRoot()
		r, err := filepath.Rel(repoRoot, rel)
		if repoRoot == "" || err != nil || strings.HasPrefix(r, "..") {
			return "", fmt.Errorf("config error: --config %s is outside the repository; in GitHub mode the config is read from the base branch", configPath)
		}
		rel = r
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	if strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("config error: --config %s is outside the repository", configPath)
	}
	if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
		return "", fmt.Errorf("config error: %s does not exist on the base branch", rel)
	}
	return rel, nil
}

// ensureCommit makes sha available in the local clone. The checkout holds
// only the PR head; a fork PR's checkout has no credentials, so a failed
// fetch is retried with the installation token (GH_TOKEN) as an HTTP header
// passed in the environment, never on the command line.
func ensureCommit(sha string) error {
	if commitExists(sha) {
		return nil
	}
	args := []string{"fetch", "--no-tags"}
	if isShallowRepo() {
		args = append(args, "--depth=1")
	}
	args = append(args, "origin", sha)
	_, err := gitOutput(args...)
	token := os.Getenv("GH_TOKEN")
	if err == nil || token == "" {
		return err
	}
	cmd := exec.Command("git", args...)
	auth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.extraheader",
		"GIT_CONFIG_VALUE_0=AUTHORIZATION: basic "+auth,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git fetch: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// baseRuleSourcePaths lists the rule-source paths in the commit's tree.
func baseRuleSourcePaths(sha string) ([]string, error) {
	out, err := exec.Command("git", "ls-tree", "-r", "-z", "--name-only", "--full-tree", sha).Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-tree: %w", err)
	}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" && isRuleSourcePath(p) {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// materializeFromCommit copies the given repository paths, as they are in
// sha, under dir. Links inside the tree are followed by git
// (--follow-symlinks), so CLAUDE.md -> AGENTS.md works; a link leaving the
// tree, a missing path, or anything that is not a file is skipped.
func materializeFromCommit(sha string, paths []string, dir string) error {
	// git cat-file --batch reads one name per line.
	paths = slices.DeleteFunc(slices.Clone(paths), func(p string) bool { return strings.ContainsAny(p, "\n\r") })
	if len(paths) == 0 {
		return nil
	}
	var in bytes.Buffer
	for _, p := range paths {
		fmt.Fprintf(&in, "%s:%s\n", sha, p)
	}
	cmd := exec.Command("git", "cat-file", "--batch", "--follow-symlinks")
	cmd.Stdin = &in
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git cat-file: %w", err)
	}
	r := bufio.NewReader(bytes.NewReader(out))
	for _, p := range paths {
		header, err := r.ReadString('\n')
		if err != nil {
			return fmt.Errorf("reading %s from %s: %w", p, shortSHA(sha), err)
		}
		fields := strings.Fields(header)
		// "<oid> <type> <size>" for an object; "<name> missing" (one line,
		// also for an ambiguous name); "symlink|dangling|loop|notdir <size>"
		// followed by <size> bytes.
		var size int
		var isBlob bool
		switch {
		case len(fields) >= 2 && (fields[len(fields)-1] == "missing" || fields[len(fields)-1] == "ambiguous"):
			continue
		case len(fields) == 3:
			size, err = strconv.Atoi(fields[2])
			isBlob = fields[1] == "blob"
		case len(fields) == 2:
			size, err = strconv.Atoi(fields[1])
		default:
			return fmt.Errorf("unexpected git cat-file output for %s: %q", p, header)
		}
		if err != nil {
			return fmt.Errorf("unexpected git cat-file output for %s: %q", p, header)
		}
		body := make([]byte, size+1) // content plus its trailing newline
		if _, err := io.ReadFull(r, body); err != nil {
			return fmt.Errorf("reading %s from %s: %w", p, shortSHA(sha), err)
		}
		if !isBlob {
			Stderrf(ansiYellow, "Skipping %s on the base branch: not a file inside the repository\n", p)
			continue
		}
		clean := path.Clean(p)
		if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			continue
		}
		target := filepath.Join(dir, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, body[:size], 0o644); err != nil {
			return err
		}
	}
	return nil
}
