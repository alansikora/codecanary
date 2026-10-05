package review

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// ReviewCoverage records the changed files the review did not see in full.
// prepareReview fills it in; each platform renders it next to the findings so
// a reader can tell what the review covered.
type ReviewCoverage struct {
	// DiffOnly files were reviewed from their diff hunks alone: the full
	// contents exceeded max_file_size or the max_total_size budget.
	DiffOnly []string `json:"diff_only,omitempty"`
	// TruncatedDiff files had their diff trimmed to fit max_diff_size, so
	// only the start of their changes was reviewed.
	TruncatedDiff []string `json:"truncated_diff,omitempty"`
	// Excluded files were not reviewed at all: they match an ignore pattern
	// or are binary.
	Excluded []string `json:"excluded,omitempty"`
}

// IsEmpty reports whether every changed file was reviewed in full.
func (c *ReviewCoverage) IsEmpty() bool {
	return c == nil || len(c.DiffOnly)+len(c.TruncatedDiff)+len(c.Excluded) == 0
}

// FileContentsResult is what FetchFileContents read and what it left out.
type FileContentsResult struct {
	Contents map[string]string // path -> full file content
	Excluded []string          // ignored or binary — drop from the review entirely
	DiffOnly []string          // over the size limits — review from the diff alone
}

// FetchFileContents reads the full contents of changed files from disk.
//
// Files matching an ignore pattern, and binary files, are Excluded: they are
// not part of the review. Files larger than maxPerFile, or that would push the
// running total past maxTotal, are DiffOnly: their contents are left out of
// the prompt but their diff is still reviewed. Files that cannot be read
// (deleted in the PR) appear in neither list.
func FetchFileContents(files []string, ignorePatterns []string, maxPerFile, maxTotal int) FileContentsResult {
	res := FileContentsResult{Contents: make(map[string]string)}
	totalSize := 0

	for _, path := range files {
		if matchesIgnore(path, ignorePatterns) {
			res.Excluded = append(res.Excluded, path)
			continue
		}

		data, err := readRepoFile("", path)
		if errors.Is(err, errSymlinkInPath) {
			// Never follow a link out of the checkout; review its diff only.
			res.Excluded = append(res.Excluded, path)
			continue
		}
		if err != nil {
			// File may have been deleted in this PR — skip gracefully.
			continue
		}

		// Binary files (null bytes in first 512 bytes).
		peek := data
		if len(peek) > 512 {
			peek = peek[:512]
		}
		if bytes.ContainsRune(peek, 0) {
			res.Excluded = append(res.Excluded, path)
			continue
		}

		size := len(data)
		if size > maxPerFile || totalSize+size > maxTotal {
			res.DiffOnly = append(res.DiffOnly, path)
			continue
		}

		res.Contents[path] = string(data)
		totalSize += size
	}

	return res
}

// scopePRForPrompt applies what FetchFileContents found to pr: it stores the
// file contents, drops ignored/binary files from pr.Files and pr.Diff, keeps
// too-large files in the review (diff only), and trims pr.Diff to
// maxDiffSize. pr.FullDiff keeps the untouched diff for finding validation.
// Returns what the prompt does not show in full.
func scopePRForPrompt(pr *PRData, fc FileContentsResult, maxDiffSize int) *ReviewCoverage {
	// Keep the unfiltered diff for finding validation (line-number checks
	// must run against the full PR diff); pr.Diff below is what the prompt
	// sees and may be scoped or trimmed.
	if pr.FullDiff == "" {
		pr.FullDiff = pr.Diff
	}

	pr.FileContents = fc.Contents
	coverage := &ReviewCoverage{DiffOnly: fc.DiffOnly, Excluded: fc.Excluded}

	if len(fc.Excluded) > 0 {
		// Ignored and binary files leave the review entirely: drop them from
		// the file allowlist and their hunks from the prompt diff.
		fmt.Fprintf(os.Stderr, "Excluded %d ignored/binary file(s): %s\n", len(fc.Excluded), strings.Join(fc.Excluded, ", "))
		excluded := make(map[string]bool, len(fc.Excluded))
		for _, f := range fc.Excluded {
			excluded[f] = true
		}
		allowedFiles := make(map[string]bool, len(pr.Files))
		filtered := make([]string, 0, len(pr.Files))
		for _, f := range pr.Files {
			if !excluded[f] {
				allowedFiles[f] = true
				filtered = append(filtered, f)
			}
		}
		pr.Files = filtered
		pr.Diff = ScopeDiffToFiles(pr.Diff, allowedFiles)
	}
	if len(fc.DiffOnly) > 0 {
		// Too large for full contents, but still part of the review.
		fmt.Fprintf(os.Stderr, "Reviewing %d file(s) from the diff only (contents over max_file_size/max_total_size): %s\n", len(fc.DiffOnly), strings.Join(fc.DiffOnly, ", "))
	}

	if capped, truncated := capDiff(pr.Diff, maxDiffSize); len(truncated) > 0 {
		Stderrf(ansiYellow, "Diff is %d bytes, over max_diff_size (%d) — trimmed the diff of %d file(s): %s\n", len(pr.Diff), maxDiffSize, len(truncated), strings.Join(truncated, ", "))
		pr.Diff = capped
		coverage.TruncatedDiff = truncated
	}
	return coverage
}

// capDiff trims a unified diff to roughly budget bytes so a huge change
// (a generated schema dump, a vendored file) cannot push the prompt past the
// provider's context window. It returns the diff unchanged when it fits.
//
// The budget is shared out per file, smallest first: each file gets an equal
// share of what is left, files under their share keep their whole diff and
// pass the remainder on, and only the largest files are cut. A cut keeps the
// file header and whole lines up to the file's share, then a marker line.
// The result is deterministic for a given diff and budget. It returns the
// paths of the files whose diff was cut, in diff order.
//
// The budget is approximate: a cut file always keeps its header (the lines
// before the first hunk, typically under 200 bytes) and gains a marker line,
// even when its share is smaller than that.
func capDiff(diff string, budget int) (string, []string) {
	if budget <= 0 || len(diff) <= budget {
		return diff, nil
	}
	prefix, blocks := splitDiffBlocks(diff)
	if len(blocks) == 0 {
		return diff, nil
	}

	order := make([]int, len(blocks))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return len(blocks[order[a]]) < len(blocks[order[b]]) })

	remaining := budget - len(prefix)
	cut := make([]bool, len(blocks))
	for k, i := range order {
		share := max(remaining, 0) / (len(order) - k)
		if len(blocks[i]) <= share {
			remaining -= len(blocks[i])
			continue
		}
		blocks[i] = truncateDiffBlock(blocks[i], share)
		cut[i] = true
		remaining -= share
	}

	var truncated []string
	for i, b := range blocks {
		if cut[i] {
			truncated = append(truncated, diffBlockPath(b))
		}
	}
	return prefix + strings.Join(blocks, ""), truncated
}

// splitDiffBlocks splits a unified diff into the text before the first
// "diff --git" line and one block per file. Joining prefix and blocks gives
// back the input.
func splitDiffBlocks(diff string) (string, []string) {
	start := 0
	if !strings.HasPrefix(diff, "diff --git") {
		i := strings.Index(diff, "\ndiff --git")
		if i < 0 {
			return diff, nil
		}
		start = i + 1
	}
	prefix := diff[:start]
	var blocks []string
	rest := diff[start:]
	for rest != "" {
		next := strings.Index(rest[1:], "\ndiff --git")
		if next < 0 {
			blocks = append(blocks, rest)
			break
		}
		blocks = append(blocks, rest[:next+2])
		rest = rest[next+2:]
	}
	return prefix, blocks
}

// truncateDiffBlock keeps a file block's header (everything before the first
// hunk) plus whole lines while the block stays within limit bytes, then
// appends a marker saying how much was left out.
func truncateDiffBlock(block string, limit int) string {
	var b strings.Builder
	inHunks := false
	for _, line := range strings.SplitAfter(block, "\n") {
		if strings.HasPrefix(line, "@@") {
			inHunks = true
		}
		if inHunks && b.Len()+len(line) > limit {
			break
		}
		b.WriteString(line)
	}
	kept := b.Len()
	if kept > 0 && !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "[codecanary: diff truncated to fit max_diff_size, %d of %d bytes omitted]\n", len(block)-kept, len(block))
	return b.String()
}

// diffBlockPath returns the file path of a single-file diff block: the new
// path, or the old one when the file was deleted.
func diffBlockPath(block string) string {
	var old string
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			return line[6:]
		case strings.HasPrefix(line, "--- a/"):
			old = line[6:]
		case strings.HasPrefix(line, "@@"):
			return old
		}
	}
	return old
}
