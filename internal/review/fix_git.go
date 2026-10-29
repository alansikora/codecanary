package review

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// fixTrailerRe matches the fixTrailer line of a commit message.
var fixTrailerRe = regexp.MustCompile(`(?mi)^` + regexp.QuoteMeta(fixTrailer) + `:[ \t]*\S`)

// isFixCommit reports whether a commit message carries the fixTrailer.
func isFixCommit(message string) bool {
	return fixTrailerRe.MatchString(message)
}

// countFixCommits counts CodeCanary fix commits in a pull request's commit
// messages (oldest first): sincePush is the run of fix commits at the tip,
// after the last commit that is not a fix — a human push resets it — and
// total is every fix commit on the pull request. Both loop limits come from
// these, so the fix keeps no state of its own.
func countFixCommits(messages []string) (sincePush, total int) {
	for _, m := range messages {
		if isFixCommit(m) {
			total++
			sincePush++
		} else {
			sincePush = 0
		}
	}
	return sincePush, total
}

// buildFixCommitMessage is the fix commit's message: a subject counting the
// applied findings, one line per finding, the fixTrailer naming the reviewed
// commit, and the Claude co-author trailer.
func buildFixCommitMessage(applied []FixDisposition, reviewedSHA string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CodeCanary: fix %d finding%s\n\n", len(applied), pluralS(len(applied)))
	for _, d := range applied {
		title := strings.Join(strings.Fields(d.Finding.Title), " ")
		fmt.Fprintf(&b, "- %s: %s\n", d.FixRef, title)
	}
	fmt.Fprintf(&b, "\n%s: %s\n%s\n", fixTrailer, reviewedSHA, claudeCoAuthorTrailer)
	return b.String()
}

// fixGit runs git in the repository root, with stdin and extra environment.
func fixGit(stdin string, env []string, args ...string) (string, error) {
	root := gitRepoRoot()
	if root == "" {
		return "", fmt.Errorf("not inside a git repository")
	}
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// statusEntry is one path from `git status --porcelain`.
type statusEntry struct {
	Code string // two-letter XY status, "??" for untracked
	Path string // relative to the repository root
}

// workingTreeStatus lists the checkout's changed and untracked paths.
func workingTreeStatus() ([]statusEntry, error) {
	out, err := fixGit("", nil, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	return parsePorcelainZ(out), nil
}

// parsePorcelainZ parses `git status --porcelain=v1 -z`: NUL-separated
// "XY path" records, where a rename or copy is followed by its source path.
func parsePorcelainZ(out string) []statusEntry {
	var entries []statusEntry
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		rec := fields[i]
		if len(rec) < 4 {
			continue
		}
		e := statusEntry{Code: rec[:2], Path: rec[3:]}
		entries = append(entries, e)
		if e.Code[0] == 'R' || e.Code[0] == 'C' {
			i++ // skip the source path
		}
	}
	return entries
}

// changedPaths lists the paths the checkout changed, untracked ones included.
func changedPaths() ([]string, error) {
	entries, err := workingTreeStatus()
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	return paths, nil
}

// isProtectedPath reports whether the fix may not change path.
func isProtectedPath(path string) bool {
	path = filepath.ToSlash(path)
	for _, pat := range protectedWritePaths {
		if ok, _ := doublestar.Match(pat, path); ok {
			return true
		}
	}
	return false
}

// revertProtectedChanges undoes every change the fix model made under
// protectedWritePaths and returns the paths it reverted.
func revertProtectedChanges() ([]string, error) {
	entries, err := workingTreeStatus()
	if err != nil {
		return nil, err
	}
	var reverted []string
	for _, e := range entries {
		if !isProtectedPath(e.Path) {
			continue
		}
		if e.Code == "??" {
			if _, err := fixGit("", nil, "clean", "-fq", "--", e.Path); err != nil {
				return reverted, err
			}
		} else if _, err := fixGit("", nil, "checkout", "-q", "HEAD", "--", e.Path); err != nil {
			return reverted, err
		}
		reverted = append(reverted, e.Path)
	}
	return reverted, nil
}

// discardChanges returns the checkout to HEAD. Best-effort: it runs only
// when the fix is abandoned, and the job's checkout is thrown away after.
func discardChanges() {
	if _, err := fixGit("", nil, "reset", "-q", "--hard", "HEAD"); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}
	if _, err := fixGit("", nil, "clean", "-fdq"); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}
}

// commitFix stages every change and commits it with the given message: the
// pull request's author as author, the bot as committer. Hooks and signing
// are off — the fix never runs code from the checkout.
func commitFix(target *FixTarget, message string) error {
	if _, err := fixGit("", nil, "add", "-A"); err != nil {
		return err
	}
	env := []string{
		"GIT_AUTHOR_NAME=" + target.Author.Name,
		"GIT_AUTHOR_EMAIL=" + target.Author.Email,
		"GIT_COMMITTER_NAME=" + target.Committer.Name,
		"GIT_COMMITTER_EMAIL=" + target.Committer.Email,
	}
	_, err := fixGit(message, env, "-c", "commit.gpgsign=false", "commit", "-q", "--no-verify", "-F", "-")
	return err
}
