package review

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// validSHA matches a full-length lowercase hex Git SHA.
var validSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// IncrementalDiff returns the changes made on the current branch since
// previousSHA was reviewed. It is shared by both platforms; the GitHub
// platform first makes sure the needed history is present in the (shallow)
// CI clone via fetchIncrementalHistory.
//
//   - Linear history (previousSHA is an ancestor of HEAD): the plain
//     `git diff previousSHA..HEAD`, as before.
//   - Rebased / force-pushed (previousSHA is not an ancestor of HEAD): a
//     file-level interdiff. The PR patch of each file before the rebase
//     (merge-base(previousSHA, baseRef)..previousSHA) is compared with the
//     patch after it (merge-base(HEAD, baseRef)..HEAD), ignoring line-number
//     shifts and context. Files whose patch changed contribute their current
//     PR diff; unchanged files are dropped. A rebase with no author changes
//     yields "".
//
// baseRef is the base branch ref to compute merge-bases against. It is only
// needed for the rebased case; an empty baseRef there returns an error so
// the caller falls back to the full PR diff.
func IncrementalDiff(previousSHA, baseRef string) (string, error) {
	if !validSHA.MatchString(previousSHA) {
		return "", fmt.Errorf("invalid SHA format: %q", previousSHA)
	}
	if !commitExists(previousSHA) {
		return "", fmt.Errorf("previous review commit %s is not available locally", shortSHA(previousSHA))
	}

	linear, err := isAncestor(previousSHA, "HEAD")
	if err != nil {
		return "", err
	}
	if linear {
		return gitOutput("diff", previousSHA+"..HEAD")
	}

	if baseRef == "" {
		return "", fmt.Errorf("previous review commit %s is not an ancestor of HEAD (rebase/force-push) and the base branch is unavailable", shortSHA(previousSHA))
	}
	fmt.Fprintf(os.Stderr, "Previous review commit %s is not an ancestor of HEAD (rebase/force-push); comparing PR patches against %s\n",
		shortSHA(previousSHA), baseRef)

	oldBase, err := mergeBase(previousSHA, baseRef)
	if err != nil {
		return "", err
	}
	newBase, err := mergeBase("HEAD", baseRef)
	if err != nil {
		return "", err
	}
	before, err := gitOutput("diff", oldBase+".."+previousSHA)
	if err != nil {
		return "", err
	}
	after, err := gitOutput("diff", newBase+"..HEAD")
	if err != nil {
		return "", err
	}

	diff := patchInterdiff(before, after)
	if diff == "" {
		fmt.Fprintf(os.Stderr, "Rebase did not change the PR patch since %s\n", shortSHA(previousSHA))
	}
	return diff, nil
}

// patchInterdiff returns the blocks of after whose file patch differs from
// the same file's patch in before (after normalizePatch). Files only present
// in before (dropped from the PR) produce nothing.
func patchInterdiff(before, after string) string {
	prev := make(map[string]string)
	for _, b := range splitDiffByFile(before) {
		prev[b.path] = normalizePatch(b.text)
	}
	var changed []string
	for _, b := range splitDiffByFile(after) {
		if old, ok := prev[b.path]; ok && old == normalizePatch(b.text) {
			continue
		}
		changed = append(changed, strings.TrimRight(b.text, "\n"))
	}
	if len(changed) == 0 {
		return ""
	}
	return strings.Join(changed, "\n") + "\n"
}

// fileDiffBlock is one "diff --git" section of a unified diff.
type fileDiffBlock struct {
	path string // post-image path (pre-image path for deletions)
	text string
}

// splitDiffByFile splits a unified diff into per-file blocks.
func splitDiffByFile(diff string) []fileDiffBlock {
	var blocks []fileDiffBlock
	lines := strings.Split(diff, "\n")
	start := -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		block := lines[start:end]
		blocks = append(blocks, fileDiffBlock{path: blockPath(block), text: strings.Join(block, "\n")})
	}
	for i, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			flush(i)
			start = i
		}
	}
	flush(len(lines))
	return blocks
}

// blockPath returns the file a diff block is about: the "+++ b/" path, or the
// "--- a/" path for deletions, or the header's b/ path for mode-only or
// binary changes.
func blockPath(block []string) string {
	var oldPath string
	for _, line := range block {
		switch {
		case strings.HasPrefix(line, "@@"):
			return oldPath
		case strings.HasPrefix(line, "+++ b/"):
			return strings.TrimRight(line[len("+++ b/"):], "\r")
		case strings.HasPrefix(line, "--- a/"):
			oldPath = strings.TrimRight(line[len("--- a/"):], "\r")
		}
	}
	if oldPath != "" {
		return oldPath
	}
	if idx := strings.LastIndex(block[0], " b/"); idx >= 0 {
		return block[0][idx+len(" b/"):]
	}
	return block[0]
}

// normalizePatch reduces a single-file diff block to what the PR author
// changed, so the same change re-applied on a moved base compares equal:
//   - hunk headers (line numbers, function context) are dropped;
//   - context lines are dropped (the base may have changed around the edit);
//   - "index" lines are dropped, since the pre-image blob changes when the
//     base moves. For binary files only the post-image blob id is kept, as
//     it is the only signal that the binary content changed.
func normalizePatch(block string) string {
	lines := strings.Split(block, "\n")
	binary := false
	for _, l := range lines {
		if strings.HasPrefix(l, "Binary files ") || l == "GIT binary patch" {
			binary = true
			break
		}
	}

	var out []string
	inHunk := false
	for _, l := range lines {
		l = strings.TrimRight(l, "\r")
		switch {
		case strings.HasPrefix(l, "@@"):
			inHunk = true
		case inHunk:
			if strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-") || strings.HasPrefix(l, `\`) {
				out = append(out, l)
			}
		case strings.HasPrefix(l, "index "):
			if binary {
				out = append(out, "index "+postImageBlob(l))
			}
		case strings.HasPrefix(l, "similarity index "), strings.HasPrefix(l, "dissimilarity index "):
			// Rename scores depend on the base content.
		default:
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// postImageBlob extracts the post-image blob id from an "index a..b [mode]" line.
func postImageBlob(indexLine string) string {
	fields := strings.Fields(strings.TrimPrefix(indexLine, "index "))
	if len(fields) == 0 {
		return ""
	}
	if _, after, ok := strings.Cut(fields[0], ".."); ok {
		return after
	}
	return fields[0]
}

// gitOutput runs git with args in the working directory and returns stdout.
// stderr is included in the error for diagnosis.
func gitOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// commitExists reports whether sha names a commit present in the local object store.
func commitExists(sha string) bool {
	return exec.Command("git", "cat-file", "-e", sha+"^{commit}").Run() == nil
}

// isAncestor reports whether ancestor is reachable from descendant.
func isAncestor(ancestor, descendant string) (bool, error) {
	err := exec.Command("git", "merge-base", "--is-ancestor", ancestor, descendant).Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w", shortSHA(ancestor), descendant, err)
}

// mergeBase returns the best common ancestor of a and b. It fails when the
// histories do not meet, e.g. when a shallow clone cuts them off.
func mergeBase(a, b string) (string, error) {
	out, err := gitOutput("merge-base", a, b)
	if err != nil {
		return "", fmt.Errorf("no merge-base between %s and %s: %w", shortSHA(a), b, err)
	}
	return strings.TrimSpace(out), nil
}
