package review

import (
	"strings"
	"testing"
)

var (
	intentBefore = &PRIntent{IntentHeading: "Intent", TestingHeading: "Testing",
		Intent: "`success` for ready-to-merge, `failure` for every other step."}
	intentAfter = &PRIntent{IntentHeading: "Intent", TestingHeading: "Testing",
		Intent: "`success` for ready-to-merge, `failure` for every other step, and `error` when a PR could not be judged."}
)

// intentThread is an intent-not-asked-for thread raised against raisedOn.
func intentThread(raisedOn *PRIntent, outdated bool, replies ...ThreadReply) ReviewThread {
	f := []Finding{{
		ID:          "intent-not-asked-for",
		File:        "gate.mjs",
		Line:        3,
		Severity:    "warning",
		Description: "The intent does not ask for the `error` state.",
	}}
	enforceIntentFindings(f, raisedOn)
	return ReviewThread{ID: "T1", Path: "gate.mjs", Line: 3, Body: FormatFindingComment(&f[0]),
		Author: BotLogin, Outdated: outdated, Replies: replies}
}

func TestIntentFindingsRecordTheIntent(t *testing.T) {
	f := []Finding{
		{ID: "intent-not-asked-for", Severity: "warning"},
		{ID: "style", Severity: "nitpick", IntentHash: "forged"},
	}
	enforceIntentFindings(f, intentBefore)
	if f[0].IntentHash != intentBefore.Hash() || f[0].IntentHash == "" {
		t.Errorf("intent finding hash = %q, want %q", f[0].IntentHash, intentBefore.Hash())
	}
	if f[1].IntentHash != "" {
		t.Errorf("a non-intent finding kept a hash: %q", f[1].IntentHash)
	}
	if intentBefore.Hash() == intentAfter.Hash() {
		t.Error("an edited intent should hash differently")
	}
	// The hash survives the round trip through the thread's marker.
	if got := FindingFromThread(intentThread(intentBefore, false)).IntentHash; got != intentBefore.Hash() {
		t.Errorf("hash from the thread = %q", got)
	}
}

// PR 105: the intent was corrected before the push that triggered round 2,
// and the code near the thread changed. The evaluator must see the intent
// as it reads now, or it judges an intent finding from the code alone.
func TestIntentThreadEvaluationSeesTheCurrentIntent(t *testing.T) {
	th := intentThread(intentBefore, true)
	triaged := ClassifyThreads([]ReviewThread{th}, limitFixDiff, limitFixDiff, BotLogin, []string{"gate.mjs"}, nil)
	applyIntent(triaged, intentAfter, limitFixDiff, nil)
	if triaged[0].Class != TriageCodeChanged {
		t.Fatalf("class = %v, want code changed", triaged[0].Class)
	}
	prompt := BuildPerThreadPrompt(triaged[0], &ReviewConfig{})
	if !strings.Contains(prompt, "`error` when a PR could not be judged") {
		t.Errorf("the triage prompt does not carry the current intent:\n%s", prompt)
	}
	if !strings.Contains(prompt, "untrusted data") {
		t.Error("the intent is not framed as untrusted data")
	}
}

// A body edit with no code change near the thread: the intent changed since
// the finding was raised, so the thread is re-checked instead of skipped.
// An unchanged intent with no other activity stays skipped (no model call).
func TestIntentThreadRecheckedWhenTheIntentChanged(t *testing.T) {
	reply := ThreadReply{Author: "pr-author", Body: "The Intent now names the error state."}
	cases := []struct {
		name   string
		thread ReviewThread
		intent *PRIntent
		want   ThreadClassification
	}{
		{"intent edited, no activity", intentThread(intentBefore, false), intentAfter, TriageCodeChanged},
		{"intent edited, with a reply", intentThread(intentBefore, false, reply), intentAfter, TriageCodeChangedReply},
		{"intent unchanged, no activity", intentThread(intentBefore, false), intentBefore, TriageSkip},
		{"intent unchanged, with a reply", intentThread(intentBefore, false, reply), intentBefore, TriageHasReply},
	}
	for _, c := range cases {
		triaged := ClassifyThreads([]ReviewThread{c.thread}, "", limitFixDiff, BotLogin, []string{"gate.mjs"}, nil)
		applyIntent(triaged, c.intent, limitFixDiff, nil)
		if triaged[0].Class != c.want {
			t.Errorf("%s: class = %v, want %v", c.name, triaged[0].Class, c.want)
		}
		if triaged[0].Intent != c.intent {
			t.Errorf("%s: the thread does not carry the intent", c.name)
		}
	}
}

// Other threads, and every thread with intent review off, are untouched.
func TestApplyIntentLeavesOtherThreadsAlone(t *testing.T) {
	other := limitThread(false)
	triaged := ClassifyThreads([]ReviewThread{other, intentThread(intentBefore, false)}, "", limitFixDiff, BotLogin, []string{"gate.mjs"}, nil)
	applyIntent(triaged, intentAfter, limitFixDiff, nil)
	if triaged[0].Class != TriageSkip || triaged[0].Intent != nil {
		t.Errorf("non-intent thread changed: class %v, intent %v", triaged[0].Class, triaged[0].Intent)
	}

	off := ClassifyThreads([]ReviewThread{intentThread(intentBefore, false)}, "", limitFixDiff, BotLogin, []string{"gate.mjs"}, nil)
	applyIntent(off, nil, limitFixDiff, nil)
	if off[0].Class != TriageSkip || off[0].Intent != nil {
		t.Errorf("intent review off: class %v, intent %v", off[0].Class, off[0].Intent)
	}
	if !strings.Contains(BuildPerThreadPrompt(triaged[1], &ReviewConfig{}), "Pull Request Intent") {
		t.Error("intent thread prompt has no intent section")
	}
}
