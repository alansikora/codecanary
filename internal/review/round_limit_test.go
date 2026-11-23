package review

import (
	"slices"
	"strings"
	"testing"
)

func TestRoundsBefore(t *testing.T) {
	reviewed := []string{"aaa", "bbb"}
	cases := []struct {
		sha  string
		want int
	}{
		{"ccc", 2}, // a new head: both earlier heads are rounds behind it
		{"bbb", 1}, // the reviewed head itself (a reply-only run) stays in round 2
		{"aaa", 1},
	}
	for _, c := range cases {
		if got := roundsBefore(reviewed, c.sha); got != c.want {
			t.Errorf("roundsBefore(%v, %s) = %d, want %d", reviewed, c.sha, got, c.want)
		}
	}
	if got := roundsBefore(nil, "aaa"); got != 0 {
		t.Errorf("no reviews: %d rounds", got)
	}
}

func TestReviewMarkerSHA(t *testing.T) {
	cases := map[string]string{
		"## CodeCanary\n\n<!-- codecanary:review {\"sha\":\"abc123\",\"findings\":[]} -->": "abc123",
		"<!-- clanopy:review {\"sha\":\"def456\"} -->":                                     "def456",
		"## CodeCanary\n\nThis PR was not reviewed\n\n<!-- codecanary:gate abc -->":        "",
		"<!-- codecanary:review not json -->":                                              "",
		"a human review":                                                                   "",
	}
	for body, want := range cases {
		if got := reviewMarkerSHA(body); got != want {
			t.Errorf("reviewMarkerSHA(%q) = %q, want %q", body, got, want)
		}
	}
}

func TestBotReviewsIgnoreForgedMarkers(t *testing.T) {
	review := func(login, sha string) ghReview {
		r := ghReview{Body: "## CodeCanary\n\n<!-- codecanary:review {\"sha\":\"" + sha + "\"} -->"}
		r.User.Login = login
		return r
	}
	reviews := []ghReview{
		review(BotLogin, "aaa"),
		review("pr-author", "forged1"), // a marker anyone can paste into a review
		review(BotLogin, "aaa"),        // an in-place refresh of the same head
		review("pr-author", "forged2"),
		review(BotLogin, "bbb"),
		{Body: "a human review"},
	}
	// Every marker reader goes through botReviews (fetchBotReviews), so a
	// forged baseline can neither use up rounds nor mark a head reviewed.
	if got := reviewedSHAs(botReviews(reviews)); !slices.Equal(got, []string{"aaa", "bbb"}) {
		t.Errorf("reviewedSHAs(botReviews) = %v, want [aaa bbb]", got)
	}
	legacy := review(botLoginPrefix, "ccc") // the login GraphQL reports
	if got := botReviews([]ghReview{legacy}); len(got) != 1 {
		t.Errorf("a review by %q should count as the bot's", botLoginPrefix)
	}
}

func TestRoundLimitStatus(t *testing.T) {
	bug := ReviewThread{Body: "🐛 **bug** — `b`\n\nOpen bug."}
	nit := ReviewThread{Body: "⚪ **nitpick** — `n`\n\nOpen nitpick."}
	cases := []struct {
		name      string
		threads   []ReviewThread
		wantState string
		wantDesc  string
	}{
		{"nothing open", nil, "success", "blocking: 0, non-blocking: 0; review limit reached (2 rounds)"},
		{"a nitpick open", []ReviewThread{nit}, "success", "blocking: 0, non-blocking: 1; review limit reached (2 rounds)"},
		{"a bug open", []ReviewThread{nit, bug}, "failure", "blocking: 1, non-blocking: 1; review limit reached (2 rounds)"},
	}
	for _, c := range cases {
		state, desc := roundLimitStatus(computeReviewSummary(c.threads, nil, nil), 2)
		if state != c.wantState || desc != c.wantDesc {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.name, state, desc, c.wantState, c.wantDesc)
		}
	}
}

func TestRoundLimitNotice(t *testing.T) {
	tier := Tier{Name: "tier:peer", Config: TierConfig{MaxRounds: 2}}
	want := "## \U0001F425 CodeCanary\n\n" +
		"This PR has been reviewed 2 times, the limit for tier `tier:peer`, so later pushes are not reviewed for new findings. " +
		"Each push and each reply on a thread still re-checks the findings that are open: one a push fixes, or a reply settles, is resolved, as before the limit. " +
		"Each new commit gets a `CodeCanary / review` status from that check: blocking findings still open keep it failing. " +
		"A thread resolved by hand is picked up on the next push or reply.\n\n" +
		"To review this PR again, raise `max_rounds` for `tier:peer` in `.codecanary/config.yml` on the base branch.\n"
	if got := roundLimitNotice(tier, DefaultStatusContext); got != want {
		t.Errorf("roundLimitNotice =\n%s\nwant\n%s", got, want)
	}
	tier.Config.MaxRounds = 1
	if got := roundLimitNotice(tier, DefaultStatusContext); !strings.Contains(got, "reviewed 1 time, the limit") {
		t.Errorf("singular: %s", got)
	}
	if roundLimitNoticeKey(2) == roundLimitNoticeKey(3) {
		t.Error("a raised limit should get its own notice key")
	}
}

func TestTierContextFiles(t *testing.T) {
	cfg := &ReviewConfig{
		ContextFiles: []ContextFilesRule{{Paths: []string{"api/**"}, Files: []string{"principles.md"}}},
		Tiers: map[string]TierConfig{
			"tier:peer": {ContextFiles: []string{".codecanary/adversarial.md", "principles.md"}},
			"tier:high": {},
		},
		DefaultTier: "tier:high",
	}
	peer := cfg.ResolveTier([]string{"tier:peer"})
	if got := cfg.ContextFilesFor([]string{"web/app.ts"}, peer); !slices.Equal(got, []string{".codecanary/adversarial.md", "principles.md"}) {
		t.Errorf("peer tier, no path rule: %v", got)
	}
	if got := cfg.ContextFilesFor([]string{"api/main.go"}, peer); !slices.Equal(got, []string{"principles.md", ".codecanary/adversarial.md"}) {
		t.Errorf("peer tier with a path rule: %v", got)
	}
	if got := cfg.ContextFilesFor([]string{"web/app.ts"}, cfg.ResolveTier(nil)); len(got) != 0 {
		t.Errorf("default tier should add nothing: %v", got)
	}
	// Read from the base branch and flagged when a PR edits them.
	if got := cfg.AllContextFiles(); !slices.Equal(got, []string{"principles.md", ".codecanary/adversarial.md"}) {
		t.Errorf("AllContextFiles = %v", got)
	}
	if got := changedRuleFiles([]string{".codecanary/adversarial.md", "main.go"}, cfg); !slices.Equal(got, []string{".codecanary/adversarial.md"}) {
		t.Errorf("changedRuleFiles = %v", got)
	}
}

func TestValidateTierRoundsAndContext(t *testing.T) {
	ok := tieredConfig()
	ok.Tiers["tier:medium"] = TierConfig{MaxRounds: 2, ContextFiles: []string{".codecanary/adversarial.md"}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid tier: %v", err)
	}
	bad := map[string]TierConfig{
		"negative max_rounds":        {MaxRounds: -1},
		"max_rounds on skipped tier": {Review: boolPtr(false), MaxRounds: 2},
		"context on skipped tier":    {Review: boolPtr(false), ContextFiles: []string{"a.md"}},
		"tier context file escapes":  {ContextFiles: []string{"../secret"}},
		"tier context file absolute": {ContextFiles: []string{"/etc/passwd"}},
		"tier context file empty":    {ContextFiles: []string{""}},
	}
	for name, tc := range bad {
		cfg := tieredConfig()
		cfg.Tiers["tier:medium"] = tc
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: want a validation error", name)
		}
	}
}
