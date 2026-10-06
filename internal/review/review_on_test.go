package review

import (
	"os"
	"path/filepath"
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
