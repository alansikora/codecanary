package review

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/alansikora/codecanary/internal/evalcorpus"
)

// -update rewrites the golden files instead of comparing against them.
//
//	go test ./internal/review/ -run Golden -update
var updateGolden = flag.Bool("update", false, "rewrite prompt golden files")

// defaultCorpusDir holds fixtures captured from this repository's own public
// pull requests. It is committed so the harness runs in CI and so the shape of
// a fixture is visible to anyone reading these tests. Fixtures from private
// repositories belong in $CODECANARY_EVAL_CORPUS instead — see the package
// doc on internal/evalcorpus and testdata/corpus/README.md.
const defaultCorpusDir = "testdata/corpus"

// TestPromptGolden renders the review prompt for every frozen fixture and
// compares it against a checked-in golden file.
//
// This does not measure review quality — no model runs, nothing is judged. It
// answers a narrower question that nothing else in the suite answers: did a
// change to prompt construction alter the prompt in a way its author did not
// intend, and by how much? Prompt edits are otherwise invisible in review;
// their effect only shows up later as a change in the findings, by which point
// the cause is hard to attribute.
//
// The size report the test writes alongside the goldens is the point as much
// as the diff is. Instruction overhead is paid on every review forever, so a
// PR that adds 4KB of prompt should have to say so out loud.
func TestPromptGolden(t *testing.T) {
	dir, ok := evalcorpus.Dir(defaultCorpusDir)
	if !ok {
		if custom := os.Getenv(evalcorpus.EnvCorpusDir); custom != "" {
			t.Fatalf("%s points at %q, which is not a directory", evalcorpus.EnvCorpusDir, custom)
		}
		t.Fatalf("corpus missing at %s — this directory is committed; the checkout looks incomplete", dir)
	}

	fixtures, err := evalcorpus.List(dir)
	if err != nil {
		t.Fatalf("loading corpus from %s: %v", dir, err)
	}
	if len(fixtures) == 0 {
		t.Fatalf("no fixtures in %s; capture one with `go run ./cmd/evalsnap --help`", dir)
	}

	sizes := make([]string, 0, len(fixtures))
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			got := BuildPrompt(toPRData(f), toReviewConfig(f.Config), 0, f.ProjectDocs)
			sizes = append(sizes, fmt.Sprintf("%-40s %7d", f.Name, len(got)))
			compareGolden(t, filepath.Join(dir, f.Name+".prompt.golden"), got)
		})
	}

	writeSizeReport(t, dir, sizes)
}

// compareGolden diffs rendered output against its golden file, reporting the
// first differing line rather than dumping two multi-kilobyte prompts.
func compareGolden(t *testing.T, path, got string) {
	t.Helper()

	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden: %v", err)
		}
		return
	}

	wantBytes, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Fatalf("no golden file at %s; run `go test ./internal/review/ -run Golden -update`", path)
		}
		t.Fatalf("reading golden: %v", err)
	}
	want := string(wantBytes)
	if got == want {
		return
	}

	gotLines, wantLines := strings.Split(got, "\n"), strings.Split(want, "\n")
	delta := len(got) - len(want)
	for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
		g, w := lineAt(gotLines, i), lineAt(wantLines, i)
		if g == w {
			continue
		}
		t.Errorf(`prompt changed (%+d chars, %d -> %d)

first difference at line %d:
  want: %s
   got: %s

If this change is intended, re-run with -update and make sure the size delta
is called out in the PR description.`, delta, len(want), len(got), i+1, truncate(w), truncate(g))
		return
	}
	t.Errorf("prompt changed (%+d chars) with no differing line; check trailing whitespace", delta)
}

// writeSizeReport records the rendered size of every prompt so growth shows up
// as a reviewable diff rather than as something nobody measured.
func writeSizeReport(t *testing.T, dir string, sizes []string) {
	t.Helper()
	if len(sizes) == 0 {
		return
	}
	sort.Strings(sizes)
	body := "# Rendered prompt sizes, in characters.\n" +
		"# Regenerate: go test ./internal/review/ -run Golden -update\n" +
		strings.Join(sizes, "\n") + "\n"

	path := filepath.Join(dir, "SIZES.txt")
	if *updateGolden {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("writing size report: %v", err)
		}
		return
	}
	prev, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		// The point of this file is that prompt growth cannot pass unnoticed,
		// so a missing one is a failure rather than a skip.
		t.Errorf("no size report at %s; run `go test ./internal/review/ -run Golden -update`", path)
	case err != nil:
		t.Errorf("reading size report: %v", err)
	case string(prev) != body:
		t.Errorf("prompt sizes changed:\n\n%s\nwant:\n\n%s\nre-run with -update", body, prev)
	}
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "<end of prompt>"
}

func truncate(s string) string {
	const max = 120
	if len(s) <= max {
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf("%q…", s[:max])
}

func toPRData(f *evalcorpus.Fixture) *PRData {
	return &PRData{
		Number:       f.PR.Number,
		Title:        f.PR.Title,
		Body:         f.PR.Body,
		Author:       f.PR.Author,
		BaseBranch:   f.PR.BaseBranch,
		HeadBranch:   f.PR.HeadBranch,
		Diff:         f.PR.Diff,
		Files:        f.PR.Files,
		FileContents: f.PR.FileContents,
	}
}

func toReviewConfig(c *evalcorpus.ConfigInput) *ReviewConfig {
	if c == nil {
		return nil
	}
	rules := make([]Rule, 0, len(c.Rules))
	for _, r := range c.Rules {
		rules = append(rules, Rule{
			ID:           r.ID,
			Description:  r.Description,
			Severity:     r.Severity,
			Paths:        r.Paths,
			ExcludePaths: r.ExcludePaths,
		})
	}
	return &ReviewConfig{Rules: rules, Context: c.Context, Ignore: c.Ignore}
}
