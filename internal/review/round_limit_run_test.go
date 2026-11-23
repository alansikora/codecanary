package review

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// limitFakeProvider is the provider name the round-limit run tests register.
const limitFakeProvider = "roundlimit-fake"

// limitModelCall is one model call a round-limit run test saw.
type limitModelCall struct {
	model  string
	prompt string
}

// limitModels records the calls of every provider the factory built, and
// answers triage prompts with triageAnswer.
type limitModels struct {
	mu           sync.Mutex
	calls        []limitModelCall
	triageAnswer string
}

type limitModel struct {
	models *limitModels
	model  string
}

func (m *limitModel) Run(_ context.Context, prompt string, _ RunOpts) (*providerResult, error) {
	m.models.mu.Lock()
	defer m.models.mu.Unlock()
	m.models.calls = append(m.models.calls, limitModelCall{model: m.model, prompt: prompt})
	if m.model == "review-model" {
		return &providerResult{Text: "```json\n{\"findings\": []}\n```"}, nil
	}
	return &providerResult{Text: m.models.triageAnswer}, nil
}

func (m *limitModels) callsTo(model string) []limitModelCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []limitModelCall
	for _, c := range m.calls {
		if c.model == model {
			out = append(out, c)
		}
	}
	return out
}

// useLimitModels registers the fake provider for one test.
func useLimitModels(t *testing.T, triageAnswer string) *limitModels {
	t.Helper()
	models := &limitModels{triageAnswer: triageAnswer}
	providers[limitFakeProvider] = ProviderFactory{
		New: func(mc *ModelConfig, _ []string) ModelProvider {
			return &limitModel{models: models, model: mc.Model}
		},
	}
	t.Cleanup(func() { delete(providers, limitFakeProvider) })
	return models
}

// limitPlatform is a ReviewPlatform for a PR whose tier's round limit is
// used up (atLimit). It records what the run published.
type limitPlatform struct {
	cfg         *ReviewConfig
	root        string
	threads     []ReviewThread
	previousSHA string
	incDiff     string
	atLimit     bool

	published      *ReviewResult
	publishThreads []ReviewThread
	publishFixed   []fixedThread
	resolved       []fixedThread
	statuses       []string
}

func (p *limitPlatform) LoadPreviousFindings() ([]ReviewThread, string, int) {
	return p.threads, p.previousSHA, len(p.threads)
}
func (p *limitPlatform) ExcludedAuthor([]ReviewThread) string { return BotLogin }
func (p *limitPlatform) HandleResolutions(_ []ReviewThread, fixed []fixedThread) {
	p.resolved = append(p.resolved, fixed...)
}
func (p *limitPlatform) Publish(result *ReviewResult, _ *PRData, threads []ReviewThread, fixed []fixedThread) error {
	p.published, p.publishThreads, p.publishFixed = result, threads, fixed
	return nil
}
func (p *limitPlatform) SaveState(*ReviewResult, []Finding, bool) error { return nil }
func (p *limitPlatform) GetIncrementalDiff(string, *PRData) (string, error) {
	return p.incDiff, nil
}
func (p *limitPlatform) ReportUsage(*UsageTracker)                    {}
func (p *limitPlatform) SkipReview(*ReviewConfig, bool) (bool, error) { return false, nil }
func (p *limitPlatform) AtRoundLimit(string, Tier) (bool, error)      { return p.atLimit, nil }
func (p *limitPlatform) ReportFailure(error, bool)                    {}
func (p *limitPlatform) LoadRules(*PRData, string) (*RuleSources, error) {
	return &RuleSources{Config: p.cfg, Root: p.root}, nil
}
func (p *limitPlatform) SetRunStatus(sha, state, desc string) {
	p.statuses = append(p.statuses, state+" "+desc)
}
func (p *limitPlatform) PublishGate(string, []string) error                  { return nil }
func (p *limitPlatform) AnswerRejectedReplies([]ReviewThread, []fixedThread) {}

const limitFile = "gate.mjs"

// limitFixDiff is the push after the limit: it fixes the finding on line 3.
const limitFixDiff = "diff --git a/gate.mjs b/gate.mjs\n" +
	"--- a/gate.mjs\n+++ b/gate.mjs\n" +
	"@@ -1,4 +1,4 @@\n line one\n line two\n-const held = await read()\n+const held = await read().catch(() => null)\n line four\n"

// limitThread is the blocking finding round 2 raised, as GitHub returns it.
func limitThread(outdated bool, replies ...ThreadReply) ReviewThread {
	body := FormatFindingComment(&Finding{
		ID:          "status-read-failure-leaves-stale-green",
		File:        limitFile,
		Line:        3,
		Severity:    "warning",
		Title:       "A failed read of the held status skips the post",
		Description: "A throw from the read leaves the previous status in place.",
	})
	return ReviewThread{
		ID:       "T1",
		Path:     limitFile,
		Line:     3,
		Body:     body,
		Author:   BotLogin,
		Outdated: outdated,
		Replies:  replies,
	}
}

// runAtRoundLimit runs the review pipeline on a tier:peer PR whose two
// rounds are used up, with platform p, and returns the fake models.
func runAtRoundLimit(t *testing.T, p *limitPlatform, replyOnly bool, triageAnswer string) *limitModels {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	repo := gitRepo{t: t, dir: dir}
	repo.git("init", "-q")
	repo.git("config", "user.email", "test@example.com")
	repo.git("config", "user.name", "test")
	repo.write(limitFile, "line one\nline two\nconst held = await read().catch(() => null)\nline four\n")
	head := repo.commitAll("fix the read")
	t.Chdir(dir)
	t.Setenv("CODECANARY_NO_TELEMETRY", "1")
	t.Setenv("CODECANARY_PROVIDER_SECRET", "test")
	t.Setenv("GITHUB_OUTPUT", "")

	models := useLimitModels(t, triageAnswer)
	p.cfg = &ReviewConfig{
		Provider:    limitFakeProvider,
		ReviewModel: "review-model",
		TriageModel: "triage-model",
		Tiers:       map[string]TierConfig{"tier:peer": {MaxRounds: 2}},
		DefaultTier: "tier:peer",
	}
	p.root = dir
	p.atLimit = true
	p.previousSHA = "reviewed-round-2"
	pr := &PRData{
		Number:  105,
		Labels:  []string{"tier:peer"},
		Files:   []string{limitFile},
		Diff:    limitFixDiff,
		HeadSHA: head,
	}
	err := Run(RunOptions{
		Repo:      "owner/repo",
		PRNumber:  105,
		Post:      true,
		ReplyOnly: replyOnly,
		PR:        pr,
		Platform:  p,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return models
}

// limitStatus is the status GithubPlatform posts for what the run published.
func limitStatus(t *testing.T, p *limitPlatform) (string, string) {
	t.Helper()
	if p.published == nil {
		t.Fatal("the run past the round limit published nothing: its open threads were never re-checked")
	}
	return roundLimitStatus(computeReviewSummary(p.publishThreads, p.publishFixed, p.published.Findings), 2)
}

// Step 3 of the PR 105 report: after the limit, a push fixes the finding
// round 2 raised. The thread is re-checked on the triage model, resolved,
// and the head's status passes; the review model is never called.
func TestRoundLimitPushThatFixesTheFindingPasses(t *testing.T) {
	p := &limitPlatform{threads: []ReviewThread{limitThread(true)}, incDiff: limitFixDiff}
	models := runAtRoundLimit(t, p, false,
		"```json\n{\"resolved\": true, \"reason\": \"code_change\", \"rationale\": \"The read failure is now caught.\"}\n```")

	if calls := models.callsTo("review-model"); len(calls) != 0 {
		t.Errorf("the review model ran past the round limit (%d call(s)): no new findings may be raised", len(calls))
	}
	if calls := models.callsTo("triage-model"); len(calls) != 1 {
		t.Fatalf("triage calls = %d, want 1 (the open thread)", len(calls))
	}
	if len(p.resolved) != 1 || p.resolved[0].Reason != "code_change" {
		t.Errorf("resolutions = %+v, want the thread resolved by code_change", p.resolved)
	}
	if p.published != nil && p.published.RoundLimit != 2 {
		t.Errorf("published RoundLimit = %d, want 2", p.published.RoundLimit)
	}
	if p.published != nil && len(p.published.Findings) != 0 {
		t.Errorf("new findings past the limit: %+v", p.published.Findings)
	}
	state, desc := limitStatus(t, p)
	if state != "success" || desc != "blocking: 0, non-blocking: 0; review limit reached (2 rounds)" {
		t.Errorf("status = (%s, %q), want success with the limit noted", state, desc)
	}
}

// Step 4: after the limit, the author answers the thread (no code change)
// and the reply settles it. The reply is evaluated and the status passes.
func TestRoundLimitReplyThatSettlesTheFindingPasses(t *testing.T) {
	reply := ThreadReply{Author: "pr-author", Body: "Not fixed in this PR, by the owner's decision: a follow-up PR closes it."}
	p := &limitPlatform{threads: []ReviewThread{limitThread(false, reply)}}
	models := runAtRoundLimit(t, p, true,
		"```json\n{\"resolved\": true, \"reason\": \"acknowledged\", \"rationale\": \"The author defers it to a follow-up PR.\"}\n```")

	if calls := models.callsTo("review-model"); len(calls) != 0 {
		t.Errorf("the review model ran on a reply past the round limit (%d call(s))", len(calls))
	}
	if len(p.resolved) != 1 || p.resolved[0].Reason != "acknowledged" {
		t.Errorf("resolutions = %+v, want the thread acknowledged", p.resolved)
	}
	state, desc := limitStatus(t, p)
	if state != "success" || desc != "blocking: 0, non-blocking: 0; review limit reached (2 rounds)" {
		t.Errorf("status = (%s, %q), want success with the limit noted", state, desc)
	}
}

// A push past the limit that does not fix the finding leaves it open and
// the status failing: nothing turns green by itself.
func TestRoundLimitPushThatDoesNotFixStaysFailing(t *testing.T) {
	p := &limitPlatform{threads: []ReviewThread{limitThread(true)}, incDiff: limitFixDiff}
	runAtRoundLimit(t, p, false,
		"```json\n{\"resolved\": false}\n```")

	if len(p.resolved) != 0 {
		t.Errorf("resolutions = %+v, want none", p.resolved)
	}
	state, desc := limitStatus(t, p)
	if state != "failure" || desc != "blocking: 1, non-blocking: 0; review limit reached (2 rounds)" {
		t.Errorf("status = (%s, %q), want failure with the limit noted", state, desc)
	}
}

// With no thread open past the limit the head passes, without a model call.
func TestRoundLimitNothingOpenPasses(t *testing.T) {
	p := &limitPlatform{incDiff: limitFixDiff}
	models := runAtRoundLimit(t, p, false, "")
	if n := len(models.callsTo("review-model")) + len(models.callsTo("triage-model")); n != 0 {
		t.Errorf("%d model call(s) with nothing open", n)
	}
	state, desc := limitStatus(t, p)
	if state != "success" || !strings.HasSuffix(desc, "; review limit reached (2 rounds)") {
		t.Errorf("status = (%s, %q), want success with the limit noted", state, desc)
	}
}

// Past the limit, GithubPlatform publishes only the head's status: no review
// (which would mark the head reviewed) and no `findings` output (which
// would start the fix job). PATH is emptied so any gh call fails the test.
func TestGithubPublishAtRoundLimitPostsOnlyTheStatus(t *testing.T) {
	out := t.TempDir() + "/output"
	if err := os.WriteFile(out, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_OUTPUT", out)
	t.Setenv("PATH", "")

	var posted []string
	g := &GithubPlatform{Repo: "owner/repo", PRNumber: 105}
	g.postStatus = func(sha, state, desc string) error {
		posted = append(posted, sha+" "+state+" "+desc)
		return nil
	}
	threads := []ReviewThread{limitThread(true)}
	fixed := []fixedThread{{Index: 0, Reason: "code_change"}}
	if err := g.Publish(&ReviewResult{SHA: "head", RoundLimit: 2}, &PRData{}, threads, fixed); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	want := "head success blocking: 0, non-blocking: 0; review limit reached (2 rounds)"
	if len(posted) != 1 || posted[0] != want {
		t.Errorf("statuses = %q, want [%q]", posted, want)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "findings=") {
		t.Errorf("the fix job's findings output was written past the limit:\n%s", data)
	}
}

// When the previous review cannot be read past the limit (no threads and
// no SHA), the run fails instead of passing the head.
func TestRoundLimitUnreadablePreviousReviewIsAnError(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	repo := gitRepo{t: t, dir: dir}
	repo.git("init", "-q")
	repo.git("config", "user.email", "test@example.com")
	repo.git("config", "user.name", "test")
	repo.write(limitFile, "line one\n")
	repo.commitAll("init")
	t.Chdir(dir)
	t.Setenv("CODECANARY_NO_TELEMETRY", "1")
	t.Setenv("CODECANARY_PROVIDER_SECRET", "test")
	useLimitModels(t, "")

	p := &limitPlatform{
		cfg: &ReviewConfig{
			Provider: limitFakeProvider, ReviewModel: "review-model", TriageModel: "triage-model",
			Tiers: map[string]TierConfig{"tier:peer": {MaxRounds: 2}}, DefaultTier: "tier:peer",
		},
		root:    dir,
		atLimit: true,
	}
	err := Run(RunOptions{Repo: "owner/repo", PRNumber: 105, Post: true, Platform: p,
		PR: &PRData{Number: 105, Labels: []string{"tier:peer"}, Files: []string{limitFile}}})
	if err == nil || !strings.Contains(err.Error(), "review limit reached") {
		t.Fatalf("Run = %v, want a review limit error", err)
	}
	if p.published != nil {
		t.Errorf("published %+v with no previous review loaded", p.published)
	}
	if last := p.statuses[len(p.statuses)-1]; !strings.HasPrefix(last, "error ") {
		t.Errorf("last status = %q, want error", last)
	}
}
