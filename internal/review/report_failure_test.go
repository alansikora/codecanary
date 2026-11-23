package review

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestIsRunFailure(t *testing.T) {
	if isRunFailure(nil) {
		t.Error("nil is not a failure")
	}
	if !isRunFailure(errors.New("provider: HTTP 529 overloaded")) {
		t.Error("a provider error is a failure")
	}
	// --fail-on fires after the review was published: not a failed run.
	if isRunFailure(fmt.Errorf("run: %w", &FailOnSeverityError{Severity: "bug", Count: 2})) {
		t.Error("a --fail-on threshold hit must not be reported as a failed run")
	}
}

func TestBuildReviewFailureNotice(t *testing.T) {
	const sha = "6971e4164c0a4df9d89aefdb874174a56df420d4"
	providerErr := fmt.Errorf("review: %w", &ProviderError{Provider: "anthropic", Kind: ErrorKindRateLimit, Message: "secret detail"})

	got := buildReviewFailureNotice(sha, providerErr, false, "https://github.com/o/r/actions/runs/1", DefaultStatusContext)
	for _, want := range []string{"`6971e416`", "**not reviewed**", "anthropic rate limit", "[Workflow run](https://github.com/o/r/actions/runs/1)"} {
		if !strings.Contains(got, want) {
			t.Errorf("full-review notice lacks %q: %q", want, got)
		}
	}
	if strings.Contains(got, "secret detail") {
		t.Errorf("notice must carry the one-line summary, not the provider message: %q", got)
	}

	got = buildReviewFailureNotice(sha, errors.New("fetching PR: boom\nsecond line"), true, "", DefaultStatusContext)
	if !strings.Contains(got, "latest replies") || strings.Contains(got, "second line") || strings.Contains(got, "Workflow run") {
		t.Errorf("reply-only notice without a run URL: %q", got)
	}

	// The parse error seen on a real PR ends its first line with a colon;
	// the sentence must not end in ":.".
	got = buildReviewFailureNotice(sha, errors.New("parsing findings: no ```json code fence found in output:\nNo new findings."), false, "", "Team / review")
	if strings.Contains(got, ":.") || !strings.Contains(got, "found in output.") {
		t.Errorf("trailing punctuation not trimmed: %q", got)
	}
	// A later success leaves the notice, so it must say where the current
	// result is, naming the configured status.
	if !strings.Contains(got, "not removed when a later run succeeds") || !strings.Contains(got, "`Team / review` status") {
		t.Errorf("notice does not say it can be outdated: %q", got)
	}
}

func TestReviewNoticeKeyIsPerKindAndHead(t *testing.T) {
	if reviewNoticeKey("failed", "a") == reviewNoticeKey("replies-failed", "a") ||
		reviewNoticeKey("failed", "a") == reviewNoticeKey("failed", "b") {
		t.Error("keys must differ by kind and head")
	}
	if reviewNoticeKey("failed", "a") == fixNoticeKey("failed", "a") {
		t.Error("review and fix notices must not share keys")
	}
}

func TestWorkflowRunURL(t *testing.T) {
	t.Setenv("GITHUB_SERVER_URL", "https://github.com")
	t.Setenv("GITHUB_REPOSITORY", "o/r")
	t.Setenv("GITHUB_RUN_ID", "42")
	if got := workflowRunURL(); got != "https://github.com/o/r/actions/runs/42" {
		t.Errorf("workflowRunURL() = %q", got)
	}
	t.Setenv("GITHUB_RUN_ID", "")
	if got := workflowRunURL(); got != "" {
		t.Errorf("outside Actions workflowRunURL() = %q, want empty", got)
	}
}
