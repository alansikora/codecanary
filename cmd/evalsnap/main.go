// Command evalsnap freezes a pull request's review inputs into a corpus
// fixture, so a prompt can later be rendered from it without touching the
// network or a working tree.
//
// It is developer tooling and is deliberately not part of the codecanary
// binary: `go build ./cmd/review` does not pull it in.
//
// Fidelity comes from reusing the review package's own fetch path rather than
// reimplementing it — the same FetchPR, the same file-content reader with the
// same size and ignore filtering, the same project-doc discovery. A fixture is
// therefore what a real review would have seen, not an approximation of it.
// That reuse is also why the tool must run from inside a checkout of the
// target repository at the PR's head commit: file contents and project docs
// are read from the working tree, and reading them at the wrong commit would
// bake a mismatched snapshot into the corpus. The tool verifies HEAD before
// capturing rather than trusting the caller.
//
// Usage:
//
//	cd /path/to/target-repo
//	git fetch origin pull/1234/head && git checkout FETCH_HEAD
//	evalsnap --repo owner/name --pr 1234 --out "$CODECANARY_EVAL_CORPUS"
//
// --out has no default: a fixture embeds the PR's diff and the full contents
// of every file the reviewer read, so where it lands is a decision to make
// deliberately rather than one to fall into. Fixtures from a private
// repository must go somewhere git does not track — guardPrivateSource
// enforces that rather than trusting it.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/alansikora/codecanary/internal/evalcorpus"
	"github.com/alansikora/codecanary/internal/review"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "evalsnap: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		repo       = flag.String("repo", "", "GitHub repo as owner/name (required)")
		pr         = flag.Int("pr", 0, "pull request number (required)")
		out        = flag.String("out", "", "corpus directory to write into (required)")
		name       = flag.String("name", "", "fixture name (default: <owner>-<name>-pr<number>)")
		configPath = flag.String("config", "", "review config path (auto-detected when empty)")
		force      = flag.Bool("force", false, "capture even if HEAD is not the PR's head commit")
	)
	flag.Parse()

	switch {
	case *repo == "":
		return fmt.Errorf("--repo is required")
	case *pr == 0:
		return fmt.Errorf("--pr is required")
	case *out == "":
		return fmt.Errorf("--out is required (the corpus directory to write into)")
	}

	// Safety before work: this is the check that prevents publishing private
	// source, so it runs before any fetch, any read, and any write.
	if err := guardPrivateSource(*repo, *out); err != nil {
		return err
	}

	prData, err := review.FetchPR(*repo, *pr)
	if err != nil {
		return fmt.Errorf("fetching PR: %w", err)
	}

	headSHA, err := review.HeadSHA()
	if err != nil {
		return fmt.Errorf("reading HEAD (run this from inside a checkout of %s): %w", *repo, err)
	}
	prHead, err := prHeadSHA(*repo, *pr)
	if err != nil {
		return err
	}
	if headSHA != prHead && !*force {
		fmt.Fprintf(os.Stderr, `
File contents and project docs are read from the working tree, so capturing
now would freeze a snapshot that does not match the diff. Check out the PR
head first:

    git fetch origin pull/%d/head && git checkout FETCH_HEAD

Pass --force to capture anyway.

`, *pr)
		return fmt.Errorf("HEAD is %s but PR #%d is at %s", short(headSHA), *pr, short(prHead))
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading review config: %w", err)
	}

	fileContents, skipped := review.FetchFileContents(
		prData.Files, cfg.Ignore, cfg.EffectiveMaxFileSize(), cfg.EffectiveMaxTotalSize())
	if len(skipped) > 0 {
		fmt.Fprintf(os.Stderr, "skipped %d large/ignored file(s): %s\n",
			len(skipped), strings.Join(skipped, ", "))
	}

	fixture := &evalcorpus.Fixture{
		Name:        fixtureName(*name, *repo, *pr),
		Repo:        *repo,
		PRNumber:    *pr,
		HeadSHA:     headSHA,
		CapturedAt:  time.Now().UTC().Format(time.RFC3339),
		PR:          toPRInput(prData, fileContents),
		Config:      toConfigInput(cfg),
		ProjectDocs: review.ReadProjectDocs(prData.Files),
	}

	path, err := evalcorpus.Save(*out, fixture)
	if err != nil {
		return err
	}

	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s (%s, %d files, %d project doc(s))\n",
		path, humanBytes(info.Size()), len(fixture.PR.Files), len(fixture.ProjectDocs))
	return nil
}

// loadConfig resolves the review config the same way a real review does,
// falling back to an empty config when the target repo has none — a repo
// without a .codecanary config is a perfectly valid thing to capture, and a
// nil config is exactly what the prompt builder would receive there.
func loadConfig(path string) (*review.ReviewConfig, error) {
	if path == "" {
		found, err := review.FindConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "no review config found; capturing with an empty config\n")
			return &review.ReviewConfig{}, nil
		}
		path = found
	}
	return review.LoadConfig(path)
}

// guardPrivateSource refuses to write a fixture captured from a private
// repository into a directory that git tracks.
//
// A fixture embeds the PR's diff and the full contents of every file the
// reviewer read. Committing one captured from a private repository publishes
// that source, and git history makes the mistake permanent — so this is
// checked before any file content is read, not after.
//
// The test is "does git ignore the destination", not "is the destination
// inside this repository": a corpus kept anywhere git would track it carries
// the same risk, and an ignored path is the arrangement that is actually safe.
func guardPrivateSource(repo, out string) error {
	private, err := isPrivateRepo(repo)
	if err != nil {
		return fmt.Errorf("could not determine whether %s is private (refusing to guess): %w", repo, err)
	}
	if !private {
		return nil
	}
	tracked, err := gitWouldTrack(out)
	if err != nil {
		return err
	}
	if !tracked {
		return nil
	}
	fmt.Fprintf(os.Stderr, `
A fixture embeds the PR's diff and the full contents of every file the
reviewer read. Committing it would publish that source permanently.

Write the corpus somewhere git does not track — a private repository, or a
gitignored directory — and point the harness at it:

    export %s=/path/to/private/corpus
    evalsnap --repo %s --pr N --out "$%s"

`, evalcorpus.EnvCorpusDir, repo, evalcorpus.EnvCorpusDir)
	return fmt.Errorf("refusing to capture: %s is private but %s is tracked by git", repo, out)
}

// isPrivateRepo asks GitHub rather than inferring from the name, so a rename
// or a transfer cannot quietly turn the guard off.
func isPrivateRepo(repo string) (bool, error) {
	cmd := exec.Command("gh", "repo", "view", repo, "--json", "isPrivate", "--jq", ".isPrivate")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

// gitWouldTrack reports whether git would track files written to dir: it is
// inside a work tree and not ignored. A path outside any repository, or one
// git ignores, is safe to write a private corpus into.
func gitWouldTrack(dir string) (bool, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false, err
	}
	// check-ignore needs an existing ancestor to resolve against.
	probe := abs
	for {
		if _, err := os.Stat(probe); err == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return false, nil // nothing on this path exists yet
		}
		probe = parent
	}

	inTree := exec.Command("git", "-C", probe, "rev-parse", "--is-inside-work-tree")
	if out, err := inTree.Output(); err != nil || strings.TrimSpace(string(out)) != "true" {
		return false, nil // not a git work tree at all
	}

	// check-ignore exits 0 when the path IS ignored, 1 when it is not.
	ignored := exec.Command("git", "-C", probe, "check-ignore", "-q", abs)
	if err := ignored.Run(); err == nil {
		return false, nil // ignored, therefore not tracked
	}
	return true, nil
}

// prHeadSHA asks GitHub for the PR's head commit so the working tree can be
// checked against it before anything is read from disk.
func prHeadSHA(repo string, pr int) (string, error) {
	cmd := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/pulls/%d", repo, pr), "--jq", ".head.sha")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolving PR head sha: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

func fixtureName(explicit, repo string, pr int) string {
	if explicit != "" {
		return explicit
	}
	return fmt.Sprintf("%s-pr%d", strings.ReplaceAll(repo, "/", "-"), pr)
}

func toPRInput(pr *review.PRData, contents map[string]string) evalcorpus.PRInput {
	return evalcorpus.PRInput{
		Number:       pr.Number,
		Title:        pr.Title,
		Body:         pr.Body,
		Author:       pr.Author,
		BaseBranch:   pr.BaseBranch,
		HeadBranch:   pr.HeadBranch,
		Diff:         pr.Diff,
		Files:        pr.Files,
		FileContents: contents,
	}
}

func toConfigInput(cfg *review.ReviewConfig) *evalcorpus.ConfigInput {
	if cfg == nil {
		return nil
	}
	rules := make([]evalcorpus.RuleInput, 0, len(cfg.Rules))
	for _, r := range cfg.Rules {
		rules = append(rules, evalcorpus.RuleInput{
			ID:           r.ID,
			Description:  r.Description,
			Severity:     r.Severity,
			Paths:        r.Paths,
			ExcludePaths: r.ExcludePaths,
		})
	}
	return &evalcorpus.ConfigInput{Rules: rules, Context: cfg.Context, Ignore: cfg.Ignore}
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
