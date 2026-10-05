package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizePatchIgnoresLineShiftsAndContext(t *testing.T) {
	before := `diff --git a/app.go b/app.go
index 1111111..2222222 100644
--- a/app.go
+++ b/app.go
@@ -10,6 +10,7 @@ func main() {
 	a := 1
 	b := 2
 	c := 3
+	d := 4
 	e := 5
 	f := 6
 	g := 7`
	// Same author change, applied after the base inserted lines above it and
	// edited a nearby context line.
	after := `diff --git a/app.go b/app.go
index 3333333..4444444 100644
--- a/app.go
+++ b/app.go
@@ -25,6 +25,7 @@ func run() {
 	a := 1
 	b := 2
 	c := 30
+	d := 4
 	e := 5
 	f := 6
 	g := 7`
	if normalizePatch(before) != normalizePatch(after) {
		t.Fatalf("expected equal normalized patches:\n%s\n---\n%s", normalizePatch(before), normalizePatch(after))
	}

	changed := strings.Replace(after, "+\td := 4", "+\td := 5", 1)
	if normalizePatch(before) == normalizePatch(changed) {
		t.Fatal("expected a changed added line to be detected")
	}
}

func TestNormalizePatchBinaryKeepsPostImage(t *testing.T) {
	mk := func(index string) string {
		return "diff --git a/logo.png b/logo.png\n" + index + "\nBinary files a/logo.png and b/logo.png differ"
	}
	if normalizePatch(mk("index aaa..ccc 100644")) != normalizePatch(mk("index bbb..ccc 100644")) {
		t.Error("same post-image blob should compare equal regardless of pre-image")
	}
	if normalizePatch(mk("index aaa..ccc 100644")) == normalizePatch(mk("index aaa..ddd 100644")) {
		t.Error("different post-image blob should compare unequal")
	}
}

func TestPatchInterdiff(t *testing.T) {
	fileDiff := func(path, hunkHeader, added string) string {
		return "diff --git a/" + path + " b/" + path + "\n" +
			"index 1..2 100644\n--- a/" + path + "\n+++ b/" + path + "\n" +
			hunkHeader + "\n ctx\n+" + added + "\n"
	}
	before := fileDiff("a.go", "@@ -1,1 +1,2 @@", "x") +
		fileDiff("b.go", "@@ -1,1 +1,2 @@", "y") +
		fileDiff("gone.go", "@@ -1,1 +1,2 @@", "z")
	after := fileDiff("a.go", "@@ -9,1 +9,2 @@", "x") + // shifted only
		fileDiff("b.go", "@@ -1,1 +1,2 @@", "y2") + // author change
		fileDiff("new.go", "@@ -1,1 +1,2 @@", "n") // new to the PR

	got := patchInterdiff(before, after)
	files := FilesFromDiff(got)
	if strings.Join(files, ",") != "b.go,new.go" {
		t.Fatalf("files = %v, want [b.go new.go]\n%s", files, got)
	}
	if !strings.Contains(got, "+y2") {
		t.Errorf("interdiff should carry the current patch of b.go:\n%s", got)
	}

	if got := patchInterdiff(before, before); got != "" {
		t.Errorf("identical patches should yield empty interdiff, got:\n%s", got)
	}
}

func TestSplitDiffByFileDeletionAndModeChange(t *testing.T) {
	diff := `diff --git a/old.go b/old.go
deleted file mode 100644
index 1234567..0000000
--- a/old.go
+++ /dev/null
@@ -1 +0,0 @@
-gone
diff --git a/run.sh b/run.sh
old mode 100644
new mode 100755
`
	blocks := splitDiffByFile(diff)
	if len(blocks) != 2 || blocks[0].path != "old.go" || blocks[1].path != "run.sh" {
		t.Fatalf("unexpected blocks: %+v", blocks)
	}
}

// --- integration: real git repositories ---

// gitRepo runs git commands in a fixed directory with an isolated config.
type gitRepo struct {
	t   *testing.T
	dir string
}

func (r gitRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r gitRepo) write(name, content string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, name), []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r gitRepo) commitAll(msg string) string {
	r.git("add", "-A")
	r.git("commit", "-q", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

func isolateGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "Test")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "test@example.com")
	}
}

func numberedLines(prefix string, n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString(prefix)
		b.WriteString(strings.Repeat("x", i%3))
		b.WriteString("\n")
	}
	return b.String()
}

// rebaseScenario builds a repo where the "pr" branch is reviewed at
// reviewedSHA, then main moves (shifting lines in a file the PR touches and
// editing another file) and the PR is rebased onto it. When authorChange is
// true, the rebased PR also edits b.txt. HEAD is left at the rebased PR.
func rebaseScenario(t *testing.T, dir string, authorChange bool) (r gitRepo, reviewedSHA string) {
	t.Helper()
	r = gitRepo{t: t, dir: dir}
	r.git("init", "-q", "-b", "main")
	shared := numberedLines("shared", 40)
	r.write("a.txt", shared)
	r.write("b.txt", "one\ntwo\nthree\n")
	r.write("c.txt", "base only\n")
	r.commitAll("base")

	r.git("switch", "-q", "-c", "pr")
	r.write("a.txt", shared+"PR TAIL\n")
	r.write("b.txt", "one\ntwo\nthree\nfour\n")
	reviewedSHA = r.commitAll("pr change")

	// main moves: insert lines at the top of a.txt and touch c.txt.
	r.git("switch", "-q", "main")
	r.write("a.txt", "header1\nheader2\nheader3\n"+shared)
	r.write("c.txt", "base only\nmoved on\n")
	r.commitAll("main moves")

	r.git("switch", "-q", "pr")
	r.git("rebase", "-q", "main")
	if authorChange {
		r.write("b.txt", "one\ntwo\nthree\nfour\nfive\n")
		r.git("commit", "-q", "-a", "--amend", "--no-edit")
	}
	return r, reviewedSHA
}

func TestIncrementalDiffLinear(t *testing.T) {
	isolateGit(t)
	dir := t.TempDir()
	r := gitRepo{t: t, dir: dir}
	r.git("init", "-q", "-b", "main")
	r.write("a.txt", "a\n")
	r.commitAll("base")
	r.git("switch", "-q", "-c", "pr")
	r.write("a.txt", "a\nb\n")
	reviewed := r.commitAll("one")
	r.write("a.txt", "a\nb\nc\n")
	r.commitAll("two")
	t.Chdir(dir)

	got, err := IncrementalDiff(reviewed, "main")
	if err != nil {
		t.Fatal(err)
	}
	if want := r.git("diff", reviewed+"..HEAD"); strings.TrimSpace(got) != want {
		t.Errorf("linear diff mismatch:\n%s\nwant:\n%s", got, want)
	}
}

func TestIncrementalDiffRebaseWithoutAuthorChanges(t *testing.T) {
	isolateGit(t)
	dir := t.TempDir()
	_, reviewed := rebaseScenario(t, dir, false)
	t.Chdir(dir)

	got, err := IncrementalDiff(reviewed, "main")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("pure rebase should yield empty incremental diff, got:\n%s", got)
	}
}

func TestIncrementalDiffRebaseWithAuthorChange(t *testing.T) {
	isolateGit(t)
	dir := t.TempDir()
	r, reviewed := rebaseScenario(t, dir, true)
	t.Chdir(dir)

	got, err := IncrementalDiff(reviewed, "main")
	if err != nil {
		t.Fatal(err)
	}
	if files := FilesFromDiff(got); len(files) != 1 || files[0] != "b.txt" {
		t.Fatalf("files = %v, want [b.txt]\n%s", files, got)
	}
	if want := r.git("diff", "main..HEAD", "--", "b.txt"); strings.TrimSpace(got) != want {
		t.Errorf("expected b.txt's current PR diff:\n%s\nwant:\n%s", got, want)
	}
}

func TestIncrementalDiffRebaseWithoutBaseRefErrors(t *testing.T) {
	isolateGit(t)
	dir := t.TempDir()
	_, reviewed := rebaseScenario(t, dir, false)
	t.Chdir(dir)

	if _, err := IncrementalDiff(reviewed, ""); err == nil {
		t.Fatal("expected an error so the caller falls back to the full PR diff")
	}
}

// TestFetchIncrementalHistoryShallowClone mirrors CI: a depth-1 clone of the
// force-pushed PR head that has neither the reviewed commit nor the base branch.
func TestFetchIncrementalHistoryShallowClone(t *testing.T) {
	isolateGit(t)
	root := t.TempDir()
	originDir := filepath.Join(root, "origin")
	if err := os.Mkdir(originDir, 0o755); err != nil {
		t.Fatal(err)
	}
	origin, reviewed := rebaseScenario(t, originDir, true)
	origin.git("config", "uploadpack.allowAnySHA1InWant", "true")
	head := origin.git("rev-parse", "HEAD")

	cloneDir := filepath.Join(root, "clone")
	gitRepo{t: t, dir: root}.git("clone", "-q", "--depth=1", "--branch=pr", "file://"+originDir, cloneDir)
	clone := gitRepo{t: t, dir: cloneDir}
	if clone.git("rev-parse", "HEAD") != head {
		t.Fatal("clone HEAD mismatch")
	}
	t.Chdir(cloneDir)

	if commitExists(reviewed) {
		t.Fatal("precondition: reviewed commit should be missing from the shallow clone")
	}
	if _, err := IncrementalDiff(reviewed, ""); err == nil {
		t.Fatal("precondition: diff should fail before fetching history")
	}

	baseRef := fetchIncrementalHistory(reviewed, "main")
	if baseRef != "refs/remotes/origin/main" {
		t.Fatalf("baseRef = %q", baseRef)
	}
	got, err := IncrementalDiff(reviewed, baseRef)
	if err != nil {
		t.Fatal(err)
	}
	if files := FilesFromDiff(got); len(files) != 1 || files[0] != "b.txt" {
		t.Fatalf("files = %v, want [b.txt]\n%s", files, got)
	}
}
