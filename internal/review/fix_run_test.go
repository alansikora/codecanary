package review

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeFixPlatform records what RunFix asks of the platform.
type fakeFixPlatform struct {
	target   *FixTarget
	findings []PRFinding
	messages []string
	pushErr  error

	pushed    int
	replies   map[string]string // fix_ref → latest body
	nReplies  int
	notices   map[string]string // key → body
	nComments int               // notice comments created (an upsert edit doesn't count)
	statuses  []string          // "sha state desc"
}

func (f *fakeFixPlatform) LoadFixTarget() (*FixTarget, error)     { return f.target, nil }
func (f *fakeFixPlatform) LoadOpenFindings() ([]PRFinding, error) { return f.findings, nil }
func (f *fakeFixPlatform) LoadCommitMessages() ([]string, error)  { return f.messages, nil }
func (f *fakeFixPlatform) OpenThreadsStatus() (string, string, error) {
	return "failure", "1 unresolved blocking finding", nil
}
func (f *fakeFixPlatform) SetReviewStatus(sha, state, desc string) error {
	f.statuses = append(f.statuses, sha+" "+state+" "+desc)
	return nil
}
func (f *fakeFixPlatform) ReplyToFinding(fd PRFinding, body string) error {
	f.replies[fd.FixRef] = body
	f.nReplies++
	// The reply lands on the thread, where the next load reads it back.
	reply := findingsCommentNode{Body: body}
	reply.Author.Login = BotLogin
	for i := range f.findings {
		if f.findings[i].FixRef == fd.FixRef {
			f.findings[i].LeftOpenOn = append(f.findings[i].LeftOpenOn, leftOpenSHAs([]findingsCommentNode{reply})...)
		}
	}
	return nil
}
func (f *fakeFixPlatform) PostNotice(key, body string) error {
	if _, ok := f.notices[key]; !ok {
		f.notices[key] = body
		f.nComments++
	}
	return nil
}
func (f *fakeFixPlatform) UpsertNotice(key, body string) error {
	if _, ok := f.notices[key]; !ok {
		f.nComments++
	}
	f.notices[key] = body
	return nil
}
func (f *fakeFixPlatform) Push(*FixTarget) error {
	f.pushed++
	return f.pushErr
}
func (f *fakeFixPlatform) ReportUsage(*UsageTracker) {}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Dev", "GIT_AUTHOR_EMAIL=dev@example.com", "GIT_COMMITTER_NAME=Dev", "GIT_COMMITTER_EMAIL=dev@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// fixRepo sets up a git checkout with fix.enabled and a fake Claude CLI that
// runs script (a shell snippet run in the checkout) and then prints answer as
// the CLI's JSON envelope. It returns the checkout and the fake CLI's path,
// and leaves the test in the checkout.
func fixRepo(t *testing.T, script, answer string) (dir, bin string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake claude is a shell script")
	}
	dir = t.TempDir()
	runGit(t, dir, "init", "-q")
	if err := os.MkdirAll(filepath.Join(dir, ".codecanary"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		".codecanary/config.yml": "version: 1\nprovider: claude\nreview_model: sonnet\ntriage_model: haiku\nfix:\n  enabled: true\n",
		"a.go":                   "package a\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "feat: a")

	envelope, err := json.Marshal(map[string]any{"type": "result", "result": answer, "is_error": false})
	if err != nil {
		t.Fatal(err)
	}
	bin = filepath.Join(t.TempDir(), "claude")
	fake := "#!/bin/sh\ncat > /dev/null\n" + script + "\ncat <<'EOF'\n" + string(envelope) + "\nEOF\n"
	if err := os.WriteFile(bin, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir, bin
}

func newFakeFixPlatform(head string) *fakeFixPlatform {
	return &fakeFixPlatform{
		target: &FixTarget{
			Number: 7, HeadSHA: head, HeadBranch: "feat/a", SameRepo: true,
			Author:    CommitIdentity{Name: "Ada Lovelace", Email: "1+ada@users.noreply.github.com"},
			Committer: CommitIdentity{Name: "codecanary-bot[bot]", Email: "2+codecanary-bot[bot]@users.noreply.github.com"},
		},
		findings: testFindings(),
		messages: []string{"feat: a"},
		replies:  map[string]string{},
		notices:  map[string]string{},
	}
}

const fixAnswer = "Done.\n```json\n[" +
	`{"fix_ref": "7-1", "disposition": "applied", "reason": "Initialised the map."},` +
	`{"fix_ref": "7-2", "disposition": "refuted", "reason": "Modules don't indent under private here."},` +
	`{"fix_ref": "7-3", "disposition": "skipped", "reason": "Needs a workflow edit."}` +
	"]\n```"

func TestRunFix_AppliesRefutesAndCommits(t *testing.T) {
	// The fake model fixes a.go, and also tries a workflow and a new
	// CodeCanary config file, which the fix must revert before committing.
	script := `printf 'package a\n\nvar m = map[string]int{}\n' > a.go
mkdir -p .github/workflows && echo 'on: push' > .github/workflows/evil.yml
echo 'fix: {}' >> .codecanary/config.yml`
	dir, bin := fixRepo(t, script, fixAnswer)
	head := runGit(t, dir, "rev-parse", "HEAD")
	p := newFakeFixPlatform(head)

	if err := RunFix(FixOptions{ConfigPath: ".codecanary/config.yml", ClaudePath: bin, ReviewedSHA: head, Platform: p}); err != nil {
		t.Fatalf("RunFix: %v", err)
	}

	if p.pushed != 1 {
		t.Errorf("pushed %d times, want 1", p.pushed)
	}
	if got := runGit(t, dir, "rev-list", "--count", "HEAD"); got != "2" {
		t.Fatalf("commits = %s, want 2 (one fix commit)", got)
	}
	if files := runGit(t, dir, "show", "--name-only", "--format=", "HEAD"); files != "a.go" {
		t.Errorf("fix commit touched %q, want only a.go", files)
	}
	if status := runGit(t, dir, "status", "--porcelain"); status != "" {
		t.Errorf("checkout left dirty:\n%s", status)
	}
	who := runGit(t, dir, "log", "-1", "--format=%an <%ae>|%cn <%ce>")
	if who != "Ada Lovelace <1+ada@users.noreply.github.com>|codecanary-bot[bot] <2+codecanary-bot[bot]@users.noreply.github.com>" {
		t.Errorf("author|committer = %s", who)
	}
	msg := runGit(t, dir, "log", "-1", "--format=%B")
	for _, want := range []string{"CodeCanary: fix 1 finding", "- 7-1: Nil map write", "CodeCanary-Fix: " + head, "Co-authored-by: Claude <noreply@anthropic.com>"} {
		if !strings.Contains(msg, want) {
			t.Errorf("commit message lacks %q:\n%s", want, msg)
		}
	}

	if len(p.replies) != 2 || !strings.Contains(p.replies["7-2"], "Modules don't indent") || !isFixReply(p.replies["7-2"]) {
		t.Errorf("replies = %v, want a fix reply on 7-2", p.replies)
	}
	// The skipped finding gets its reason on the thread; with a fix pushed,
	// the next review runs, so there is no stop notice.
	if r := p.replies["7-3"]; !strings.Contains(r, "Needs a workflow edit.") || isFixReply(r) {
		t.Errorf("left-open reply on 7-3 = %q", r)
	}
	if len(p.notices) != 0 || len(p.statuses) != 0 {
		t.Errorf("unexpected notices %v / statuses %v", p.notices, p.statuses)
	}
}

func TestRunFix_StopsAtLimitWithoutTheModel(t *testing.T) {
	// The fake model would leave a marker file if it ran.
	dir, bin := fixRepo(t, "touch model-ran", fixAnswer)
	head := runGit(t, dir, "rev-parse", "HEAD")
	p := newFakeFixPlatform(head)
	p.messages = []string{"feat: a", fixMsg("CodeCanary: fix 1 finding"), fixMsg("CodeCanary: fix 2 findings")}

	if err := RunFix(FixOptions{ConfigPath: ".codecanary/config.yml", ClaudePath: bin, ReviewedSHA: head, Platform: p}); err != nil {
		t.Fatalf("RunFix: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "model-ran")); err == nil {
		t.Error("the fix model ran past the limit")
	}
	if p.pushed != 0 {
		t.Error("pushed past the limit")
	}
	notice, ok := p.notices[fixNoticeKey("limit", head)]
	if !ok || !strings.Contains(notice, "stopped after 2 fixes since your last push") || !strings.Contains(notice, "Nil map write") {
		t.Errorf("stop notice = %q", notice)
	}
	want := head + " failure 1 unresolved blocking finding; stopped after 2 fixes"
	if len(p.statuses) != 1 || p.statuses[0] != want {
		t.Errorf("statuses = %v, want [%s]", p.statuses, want)
	}
}

func TestRunFix_ProviderFailurePostsOnceAndSucceeds(t *testing.T) {
	envelope := `{"type":"result","is_error":true,"api_error_status":429,"result":"rate limited"}`
	script := "cat <<'EOF'\n" + envelope + "\nEOF\nexit 1"
	dir, bin := fixRepo(t, script, "")
	head := runGit(t, dir, "rev-parse", "HEAD")
	p := newFakeFixPlatform(head)

	for range 2 {
		if err := RunFix(FixOptions{ConfigPath: ".codecanary/config.yml", ClaudePath: bin, ReviewedSHA: head, Platform: p}); err != nil {
			t.Fatalf("RunFix returned %v; provider failures must not fail the job", err)
		}
	}
	notice := p.notices[fixNoticeKey("failed", head)]
	if len(p.notices) != 1 || !strings.Contains(notice, "/codecanary-fix") || !strings.Contains(notice, "rate limit") {
		t.Errorf("notices = %v", p.notices)
	}
	if p.pushed != 0 || len(p.replies) != 0 {
		t.Errorf("pushed=%d replies=%v after a provider failure", p.pushed, p.replies)
	}
}

func TestRunFix_PushFailureStillReplies(t *testing.T) {
	dir, bin := fixRepo(t, `printf 'package a\n// fixed\n' > a.go`, fixAnswer)
	head := runGit(t, dir, "rev-parse", "HEAD")
	p := newFakeFixPlatform(head)
	p.pushErr = errors.New("remote: Permission to o/r.git denied")

	if err := RunFix(FixOptions{ConfigPath: ".codecanary/config.yml", ClaudePath: bin, ReviewedSHA: head, Platform: p}); err != nil {
		t.Fatalf("RunFix: %v", err)
	}
	if n := p.notices[fixNoticeKey("failed", head)]; !strings.Contains(n, "could not be pushed") || !strings.Contains(n, "Permission") {
		t.Errorf("failure notice = %q", n)
	}
	if _, ok := p.replies["7-2"]; !ok {
		t.Error("refutation not posted after a failed push")
	}
}

func TestRunFix_SkipsWhenTheHeadMoved(t *testing.T) {
	dir, bin := fixRepo(t, "touch model-ran", fixAnswer)
	head := runGit(t, dir, "rev-parse", "HEAD")
	p := newFakeFixPlatform(head)

	if err := RunFix(FixOptions{ConfigPath: ".codecanary/config.yml", ClaudePath: bin, ReviewedSHA: "0000000older", Platform: p}); err != nil {
		t.Fatalf("RunFix: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "model-ran")); err == nil {
		t.Error("the fix model ran for a superseded review")
	}

	// Forks are never pushed to.
	p.target.SameRepo = false
	if err := RunFix(FixOptions{ConfigPath: ".codecanary/config.yml", ClaudePath: bin, ReviewedSHA: head, Platform: p}); err != nil {
		t.Fatalf("RunFix: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "model-ran")); err == nil {
		t.Error("the fix model ran on a fork PR")
	}
}

func TestRunFix_DiscardsUnclaimedEdits(t *testing.T) {
	answer := "```json\n[" + `{"fix_ref": "7-1", "disposition": "refuted", "reason": "Already fixed in b.go."}` + "]\n```"
	dir, bin := fixRepo(t, `echo '// stray' >> a.go`, answer)
	head := runGit(t, dir, "rev-parse", "HEAD")
	p := newFakeFixPlatform(head)
	p.findings = p.findings[:1]

	if err := RunFix(FixOptions{ConfigPath: ".codecanary/config.yml", ClaudePath: bin, ReviewedSHA: head, Platform: p}); err != nil {
		t.Fatalf("RunFix: %v", err)
	}
	if p.pushed != 0 || runGit(t, dir, "rev-parse", "HEAD") != head {
		t.Error("committed edits no finding claimed")
	}
	if status := runGit(t, dir, "status", "--porcelain"); status != "" {
		t.Errorf("unclaimed edits left in the checkout:\n%s", status)
	}
	if _, ok := p.replies["7-1"]; !ok {
		t.Error("refutation not posted")
	}
}

// thetechfx/bedrock#7946: one finding, left open, nothing to commit. The
// finding gets its reason on its thread, and the PR one comment saying the
// loop stopped; a re-run on the same head posts neither again, and edits
// the comment.
func TestRunFix_LeftOpenNothingToCommit(t *testing.T) {
	answer := "```json\n[" + `{"fix_ref": "7-1", "disposition": "skipped", "reason": "Valid, but no stored marker exists for 'ever classified', so the author must decide."}` + "]\n```"
	dir, bin := fixRepo(t, "", answer)
	head := runGit(t, dir, "rev-parse", "HEAD")
	p := newFakeFixPlatform(head)
	p.findings = p.findings[:1]

	for range 2 {
		if err := RunFix(FixOptions{ConfigPath: ".codecanary/config.yml", ClaudePath: bin, ReviewedSHA: head, Platform: p}); err != nil {
			t.Fatalf("RunFix: %v", err)
		}
	}
	if p.pushed != 0 || runGit(t, dir, "rev-parse", "HEAD") != head {
		t.Error("committed with nothing applied")
	}

	reply := p.replies["7-1"]
	if p.nReplies != 1 || !strings.Contains(reply, "no stored marker exists") {
		t.Errorf("%d replies, 7-1 = %q; want one reply with the reason", p.nReplies, reply)
	}
	// The workflow re-runs the review only for the fix-reply marker, and
	// triage would read that reply as the author's: neither may happen here.
	if isFixReply(reply) || isAckReply(reply) || !strings.Contains(reply, fixLeftOpenMarkerPrefix+head+" -->") {
		t.Errorf("left-open reply carries the wrong marker: %q", reply)
	}

	notice, ok := p.notices[fixNoticeKey("left-open", head)]
	if !ok || p.nComments != 1 || len(p.notices) != 1 {
		t.Fatalf("notices = %v (%d created), want one left-open notice", p.notices, p.nComments)
	}
	for _, want := range []string{
		"stopped on `" + shortSHA(head) + "` with nothing to commit: it left 1 finding open for you to decide",
		"- **bug** `a.go:3` — Nil map write ([thread](https://github.com/o/r/pull/7#discussion_r1))",
		"no stored marker exists",
		"/codecanary-fix",
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice lacks %q:\n%s", want, notice)
		}
	}
}

// A finding reported applied whose edit left nothing to commit is still open.
func TestRunFix_AppliedWithoutChangesIsLeftOpen(t *testing.T) {
	answer := "```json\n[" + `{"fix_ref": "7-1", "disposition": "applied", "reason": "Initialised the map."}` + "]\n```"
	dir, bin := fixRepo(t, "", answer)
	head := runGit(t, dir, "rev-parse", "HEAD")
	p := newFakeFixPlatform(head)
	p.findings = p.findings[:1]

	if err := RunFix(FixOptions{ConfigPath: ".codecanary/config.yml", ClaudePath: bin, ReviewedSHA: head, Platform: p}); err != nil {
		t.Fatalf("RunFix: %v", err)
	}
	if r := p.replies["7-1"]; !strings.Contains(r, "left no change to commit") {
		t.Errorf("reply = %q", r)
	}
	if _, ok := p.notices[fixNoticeKey("left-open", head)]; !ok {
		t.Errorf("notices = %v, want a left-open notice", p.notices)
	}
}
