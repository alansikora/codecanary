package review

import (
	"fmt"
	"os/exec"
	"strings"
)

// isAncestor checks if the given SHA is an ancestor of HEAD.
// Returns (false, nil) when sha is valid but not an ancestor (rebase).
// Returns (false, err) when the git command itself fails.
func isAncestor(sha string) (bool, error) {
	err := exec.Command("git", "merge-base", "--is-ancestor", sha, "HEAD").Run()
	if err == nil {
		return true, nil
	}
	// merge-base --is-ancestor exits 1 for "not an ancestor" and 128+ for
	// actual errors (bad object, not a repo, etc.). Go's exec surfaces the
	// exit code via *exec.ExitError.
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor: %w", err)
}

// FetchLocalDiff computes a diff of the current branch against the default
// branch and returns a PRData suitable for review without a GitHub PR.
func FetchLocalDiff() (*PRData, error) {
	base := detectDefaultBranch()
	if base == "" {
		return nil, fmt.Errorf("could not detect default branch (tried main, master)")
	}

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

// workingTreeDiff returns uncommitted changes (staged + unstaged) for the
// given set of files. Returns empty string if there are no dirty changes.
func workingTreeDiff(files []string) (string, error) {
	if len(files) == 0 {
		return "", nil
	}
	args := append([]string{"diff", "HEAD", "--"}, files...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", fmt.Errorf("git diff HEAD: %w", err)
	}
	return string(out), nil
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
