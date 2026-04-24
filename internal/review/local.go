package review

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// FetchLocalDiff computes a diff of the current branch against the given base
// branch and returns a PRData suitable for review without a GitHub PR. If
// baseBranch is empty, it auto-detects main or master.
func FetchLocalDiff(baseBranch string) (*PRData, error) {
	base := baseBranch
	if base == "" {
		base = detectDefaultBranch()
		if base == "" {
			return nil, fmt.Errorf("could not detect default branch (tried main, master); use --base to specify one")
		}
	}

	head, err := CurrentBranch()
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

// appendWorkingTreeDiff appends uncommitted changes (scoped to prFiles) to
// diff, returning the combined result. Used by both platform implementations.
func appendWorkingTreeDiff(diff string, prFiles []string) (string, error) {
	wtDiff, err := workingTreeDiff(prFiles)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not compute working-tree diff: %v\n", err)
		return diff, nil
	}
	if wtDiff == "" {
		return diff, nil
	}
	if diff == "" {
		return wtDiff, nil
	}
	return diff + "\n" + wtDiff, nil
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

// CurrentBranch returns the name of the current git branch.
func CurrentBranch() (string, error) {
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

// HeadSHA returns the full 40-character SHA of the current HEAD commit.
func HeadSHA() (string, error) {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// WorkingTreeDirty reports whether the working tree has uncommitted
// changes (staged, unstaged, or untracked).
func WorkingTreeDirty() (bool, error) {
	out, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		return false, fmt.Errorf("git status failed: %w", err)
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}
