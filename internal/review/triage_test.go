package review

import (
	"strings"
	"testing"
)

func TestBuildCodeChangePrompt_OnlyAllowsCodeChangeReason(t *testing.T) {
	tt := TriagedThread{
		Thread: ReviewThread{
			Path: "main.go",
			Line: 10,
			Body: "Found a bug",
		},
		FileDiff: "+ fixed line",
	}
	prompt := buildCodeChangePrompt(tt, nil)

	if strings.Contains(prompt, `"acknowledged"`) {
		t.Error("buildCodeChangePrompt should not offer 'acknowledged' as a reason")
	}
	if strings.Contains(prompt, `"dismissed"`) {
		t.Error("buildCodeChangePrompt should not offer 'dismissed' as a reason")
	}
	if strings.Contains(prompt, `"rebutted"`) {
		t.Error("buildCodeChangePrompt should not offer 'rebutted' as a reason")
	}
	if !strings.Contains(prompt, `"code_change"`) {
		t.Error("buildCodeChangePrompt must offer 'code_change' as a reason")
	}
}

func TestBuildCrossFilePrompt_OnlyAllowsCodeChangeReason(t *testing.T) {
	tt := TriagedThread{
		Thread: ReviewThread{
			Path: "main.go",
			Line: 10,
			Body: "Found a bug",
		},
		FileDiff: "+ fixed in other file",
	}
	prompt := buildCrossFilePrompt(tt, nil)

	if strings.Contains(prompt, `"acknowledged"`) {
		t.Error("buildCrossFilePrompt should not offer 'acknowledged' as a reason")
	}
	if strings.Contains(prompt, `"dismissed"`) {
		t.Error("buildCrossFilePrompt should not offer 'dismissed' as a reason")
	}
	if strings.Contains(prompt, `"rebutted"`) {
		t.Error("buildCrossFilePrompt should not offer 'rebutted' as a reason")
	}
	if !strings.Contains(prompt, `"code_change"`) {
		t.Error("buildCrossFilePrompt must offer 'code_change' as a reason")
	}
}

func TestBuildReplyPrompt_AllowsAllReasons(t *testing.T) {
	tt := TriagedThread{
		Thread: ReviewThread{
			Path: "main.go",
			Line: 10,
			Body: "Found a bug",
			Replies: []ThreadReply{
				{Author: "user1", Body: "Will fix later"},
			},
		},
		BotLogin: "codecanary-bot",
	}
	prompt := buildReplyPrompt(tt, nil)

	for _, reason := range []string{`"code_change"`, `"acknowledged"`, `"dismissed"`, `"rebutted"`} {
		if !strings.Contains(prompt, reason) {
			t.Errorf("buildReplyPrompt must offer %s as a reason", reason)
		}
	}
}

func TestBuildCodeChangeReplyPrompt_AllowsAllReasons(t *testing.T) {
	tt := TriagedThread{
		Thread: ReviewThread{
			Path: "main.go",
			Line: 10,
			Body: "Found a bug",
			Replies: []ThreadReply{
				{Author: "user1", Body: "Fixed it"},
			},
		},
		FileDiff: "+ fixed line",
		BotLogin: "codecanary-bot",
	}
	prompt := buildCodeChangeReplyPrompt(tt, nil)

	for _, reason := range []string{`"code_change"`, `"acknowledged"`, `"dismissed"`, `"rebutted"`} {
		if !strings.Contains(prompt, reason) {
			t.Errorf("buildCodeChangeReplyPrompt must offer %s as a reason", reason)
		}
	}
}

func TestValidateResolutionReason_RejectsInvalidReasonForCodeChangeOnly(t *testing.T) {
	// Simulate Claude returning "acknowledged" for a code-change-only thread.
	output := "```json\n{\"resolved\": true, \"reason\": \"acknowledged\"}\n```"
	parsed := parseThreadResolution(output, 0)

	// parseThreadResolution itself accepts any reason (it's just a parser).
	if !parsed.Resolved || parsed.Reason != "acknowledged" {
		t.Fatal("parseThreadResolution should parse the raw response as-is")
	}

	// For code-change-only classifications, invalid reasons should be rejected.
	for _, class := range []ThreadClassification{TriageCodeChanged, TriageCrossFileChange} {
		res := validateResolutionReason(parsed, class)
		if res.Resolved {
			t.Errorf("class %d: resolution with reason 'acknowledged' should be rejected", class)
		}
		if res.Reason != "" {
			t.Errorf("class %d: reason should be cleared, got %q", class, res.Reason)
		}
	}

	// For reply-based classifications, the same reason should be accepted.
	for _, class := range []ThreadClassification{TriageHasReply, TriageCodeChangedReply} {
		res := validateResolutionReason(parsed, class)
		if !res.Resolved {
			t.Errorf("class %d: resolution with reason 'acknowledged' should be accepted", class)
		}
		if res.Reason != "acknowledged" {
			t.Errorf("class %d: reason should be 'acknowledged', got %q", class, res.Reason)
		}
	}
}
