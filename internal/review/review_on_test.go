package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkipsPush(t *testing.T) {
	ready := &ReviewConfig{ReviewOn: ReviewOnReady}
	push := &ReviewConfig{ReviewOn: ReviewOnPush}
	unset := &ReviewConfig{}

	cases := []struct {
		name      string
		cfg       *ReviewConfig
		replyOnly bool
		action    string
		want      bool
	}{
		{"ready skips a push", ready, false, "synchronize", true},
		{"ready reviews on ready_for_review", ready, false, "ready_for_review", false},
		{"ready reviews on opened", ready, false, "opened", false},
		{"ready reviews on reopened", ready, false, "reopened", false},
		{"ready still evaluates replies", ready, true, "synchronize", false},
		{"push reviews every push", push, false, "synchronize", false},
		{"unset reviews every push", unset, false, "synchronize", false},
	}
	for _, c := range cases {
		if got := skipsPush(c.cfg, c.replyOnly, c.action); got != c.want {
			t.Errorf("%s: skipsPush = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestGithubEventAction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(path, []byte(`{"action":"synchronize","number":7}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_EVENT_PATH", path)
	if got := githubEventAction(); got != "synchronize" {
		t.Errorf("githubEventAction = %q, want synchronize", got)
	}

	t.Setenv("GITHUB_EVENT_PATH", "")
	if got := githubEventAction(); got != "" {
		t.Errorf("githubEventAction without an event = %q, want empty", got)
	}
}

func TestReviewOnValidation(t *testing.T) {
	cfg := &ReviewConfig{ReviewOn: "sometimes"}
	if err := cfg.Validate(); err == nil {
		t.Error("Validate accepted review_on: sometimes")
	}
}

// A push review_on: ready skips still gets a status: failing while a blocking
// thread is open, passing otherwise. Acknowledged threads count as handled.
func TestSkippedPushStatus(t *testing.T) {
	bug := ReviewThread{Body: "🐛 **bug** — `b`\n\nOpen bug."}
	nit := ReviewThread{Body: "⚪ **nitpick** — `n`\n\nOpen nitpick."}
	ackedBug := ReviewThread{
		Body:    "🐛 **bug** — `a`\n\nDismissed bug.",
		Replies: []ThreadReply{{Author: "codecanary-bot[bot]", Body: "Keeping this open.\n<!-- codecanary:ack:dismissed -->"}},
	}

	cases := []struct {
		name    string
		threads []ReviewThread
		want    string
	}{
		{"no threads", nil, "success"},
		{"only a nitpick open", []ReviewThread{nit}, "success"},
		{"a blocking thread open", []ReviewThread{nit, bug}, "failure"},
		{"blocking thread acknowledged", []ReviewThread{ackedBug}, "success"},
	}
	for _, c := range cases {
		state, desc := skippedPushStatus(c.threads)
		if state != c.want {
			t.Errorf("%s: state = %q (%s), want %q", c.name, state, desc, c.want)
		}
		if !strings.Contains(desc, "review_on: ready") {
			t.Errorf("%s: description should say the push wasn't reviewed, got %q", c.name, desc)
		}
		if len(desc) > 140 {
			t.Errorf("%s: description is %d chars; GitHub caps commit status descriptions at 140", c.name, len(desc))
		}
	}
}

func TestLocalPlatformNeverSkips(t *testing.T) {
	skip, err := (&LocalPlatform{}).SkipReview(&ReviewConfig{ReviewOn: ReviewOnReady}, false)
	if skip || err != nil {
		t.Errorf("LocalPlatform.SkipReview = %v, %v; want false, nil", skip, err)
	}
}
