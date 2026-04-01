package review

import (
	"fmt"
	"os/exec"
	"strings"
)

// FetchLocalDiff computes a diff of the current branch against the default
// branch and returns a PRData suitable for review without a GitHub PR.
// If baseBranch is non-empty it is used directly; otherwise the default branch
// is auto-detected (tries main, then master).
func FetchLocalDiff(baseBranch string) (*PRData, error) {
	base := baseBranch
	if base == "" {
		base = detectDefaultBranch()
	}
	if base == "" {
		return nil, fmt.Errorf("could not detect default branch (tried main, master)")
	}
	base = resolveRef(base)

	head, err := currentBranch()
	if err != nil {
		return nil, fmt.Errorf("detecting current branch: %w", err)
	}
	if head == base {
		return nil, fmt.Errorf("current branch is %s — nothing to review (check out a feature branch)", base)
	}

	// Find the merge-base to get only branch-specific changes.
	mergeBaseOut, err := exec.Command("git", "merge-base", "HEAD", base).Output()
	if err != nil {
		return nil, fmt.Errorf("computing merge-base against %s: %w", base, err)
	}
	mergeBase := strings.TrimSpace(string(mergeBaseOut))

	// Compute diff from merge-base to HEAD.
	diffOut, err := exec.Command("git", "diff", mergeBase+"..HEAD").Output()
	if err != nil {
		return nil, fmt.Errorf("computing diff against %s: %w", base, err)
	}
	diff := string(diffOut)
	if diff == "" {
		return nil, fmt.Errorf("no changes found between %s and HEAD", base)
	}

	files := FilesFromDiff(diff)

	// Get git user for author context.
	authorOut, _ := exec.Command("git", "config", "user.name").Output()
	author := strings.TrimSpace(string(authorOut))
	if author == "" {
		author = "local"
	}

	return &PRData{
		Number:     0,
		Title:      fmt.Sprintf("Changes on %s", head),
		Author:     author,
		BaseBranch: base,
		HeadBranch: head,
		Diff:       diff,
		Files:      files,
	}, nil
}

// detectDefaultBranch returns "main" or "master" based on what exists locally.
// Returns empty string if neither exists.
func detectDefaultBranch() string {
	if err := exec.Command("git", "rev-parse", "--verify", "main").Run(); err == nil {
		return "main"
	}
	if err := exec.Command("git", "rev-parse", "--verify", "master").Run(); err == nil {
		return "master"
	}
	return ""
}

// resolveRef resolves a branch name to a usable git ref, preferring the remote
// tracking ref over the local branch. Local branches in fork/shallow clones may
// exist as refs but lack connectivity with HEAD, causing merge-base to fail.
// Resolution order:
//  1. origin/<ref> — already-fetched remote tracking ref (best connectivity)
//  2. Local branch <ref>
//  3. Fetch origin/<ref> on demand, then use the tracking ref
//  4. Return original name unchanged so git surfaces a clear error
func resolveRef(ref string) string {
	remote := "origin/" + ref
	if err := exec.Command("git", "rev-parse", "--verify", remote).Run(); err == nil {
		return remote
	}
	if err := exec.Command("git", "rev-parse", "--verify", ref).Run(); err == nil {
		return ref
	}
	// Not found locally — try fetching from origin.
	exec.Command("git", "fetch", "origin", ref).Run() //nolint:errcheck
	if err := exec.Command("git", "rev-parse", "--verify", remote).Run(); err == nil {
		return remote
	}
	return ref
}

// currentBranch returns the name of the current git branch.
func currentBranch() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(string(out))
	if branch == "HEAD" {
		return "", fmt.Errorf("detached HEAD state — check out a branch to review")
	}
	return branch, nil
}
