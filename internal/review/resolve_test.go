package review

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var errNotAccessible = errors.New("gh api graphql resolve: exit status 1\n{\"errors\":[{\"type\":\"FORBIDDEN\",\"message\":\"Resource not accessible by integration\"}]}")

func TestParseThreadIDs(t *testing.T) {
	got := ParseThreadIDs(" PRRT_a,,PRRT_b , PRRT_a,")
	if want := []string{"PRRT_a", "PRRT_b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ParseThreadIDs = %v, want %v", got, want)
	}
	if got := ParseThreadIDs(""); got != nil {
		t.Errorf("ParseThreadIDs(\"\") = %v, want nil", got)
	}
}

func TestResolveFindingThreads(t *testing.T) {
	threads := []ReviewThread{
		{ID: "PRRT_open", Path: "a.go", Line: 1},
		{ID: "PRRT_done", Path: "b.go", Line: 2, Resolved: true},
		{ID: "PRRT_denied", Path: "c.go", Line: 3},
	}
	var called []string
	resolve := func(id string) error {
		called = append(called, id)
		if id == "PRRT_denied" {
			return errNotAccessible
		}
		return nil
	}

	if err := resolveFindingThreads(threads, []string{"PRRT_open", "PRRT_done"}, resolve); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := []string{"PRRT_open"}; !reflect.DeepEqual(called, want) {
		t.Errorf("resolved %v, want %v (an already resolved thread is skipped)", called, want)
	}

	// A thread that isn't one of the PR's finding threads is never resolved,
	// and it and a refused thread both fail the step.
	called = nil
	err := resolveFindingThreads(threads, []string{"PRRT_foreign", "PRRT_denied", "PRRT_open"}, resolve)
	if err == nil {
		t.Fatal("expected an error for the foreign and the refused thread")
	}
	if want := []string{"PRRT_denied", "PRRT_open"}; !reflect.DeepEqual(called, want) {
		t.Errorf("resolve called for %v, want %v", called, want)
	}
	for _, want := range []string{"2 of 3", "PRRT_foreign", "c.go:3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestHandleResolutionsDefersWhatTheTokenCannotResolve(t *testing.T) {
	threads := []ReviewThread{
		{ID: "PRRT_one", Path: "a.go", Line: 1},
		{ID: "PRRT_two", Path: "b.go", Line: 2},
		{ID: "PRRT_three", Path: "c.go", Line: 3},
	}
	newPlatform := func() *GithubPlatform {
		return &GithubPlatform{resolveThread: func(id string) error {
			switch id {
			case "PRRT_one":
				return nil
			case "PRRT_two", "PRRT_three":
				return errNotAccessible
			}
			return errors.New("unexpected thread " + id)
		}, replyToThread: func(string, string) error { return nil }}
	}
	lastOutput := func(path string) string {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		last := ""
		for _, line := range strings.Split(string(data), "\n") {
			if v, ok := strings.CutPrefix(line, resolveThreadsOutput+"="); ok {
				last = v
			}
		}
		return last
	}

	out := filepath.Join(t.TempDir(), "output")
	if err := os.WriteFile(out, nil, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_OUTPUT", out)

	// The review calls HandleResolutions twice (file-removed threads, then
	// evaluated ones); the output must end up holding both calls' threads.
	g := newPlatform()
	g.HandleResolutions(threads, []fixedThread{{Index: 0, Reason: "code_change"}, {Index: 1, Reason: "code_change"}})
	if got := lastOutput(out); got != "PRRT_two" {
		t.Errorf("after the first call resolve_threads = %q, want PRRT_two", got)
	}
	g.HandleResolutions(threads, []fixedThread{{Index: 2, Reason: "code_change"}})
	if got := lastOutput(out); got != "PRRT_two,PRRT_three" {
		t.Errorf("after the second call resolve_threads = %q, want PRRT_two,PRRT_three", got)
	}

	// Outside GitHub Actions nothing can pick the threads up.
	t.Setenv("GITHUB_OUTPUT", "")
	g = newPlatform()
	g.HandleResolutions(threads, []fixedThread{{Index: 1, Reason: "code_change"}})
	if len(g.deferredResolves) != 0 {
		t.Errorf("deferred %v outside GitHub Actions", g.deferredResolves)
	}
}

func TestWriteResolveOutputCarriesTheReviewStatus(t *testing.T) {
	out := filepath.Join(t.TempDir(), "output")
	if err := os.WriteFile(out, nil, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_OUTPUT", out)

	g := &GithubPlatform{deferredResolves: []string{"PRRT_two"}}
	g.writeResolveOutput() // no status posted yet: threads only
	g.lastStatus = &ResolveStatus{SHA: "ea4315cd", Context: "CodeCanary / review", Description: "blocking: 0, non-blocking: 0"}
	g.writeResolveOutput()

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var status string
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, resolveStatusOutput+"="); ok {
			status = v
		}
	}
	var got ResolveStatus
	if err := json.Unmarshal([]byte(status), &got); err != nil {
		t.Fatalf("resolve_status = %q: %v", status, err)
	}
	if got != *g.lastStatus {
		t.Errorf("resolve_status = %+v, want %+v", got, *g.lastStatus)
	}
}

func TestUnresolvedStatusDescription(t *testing.T) {
	review := "blocking: 0, non-blocking: 0"
	cases := []struct {
		name                 string
		n                    int
		tokenErr, resolveErr string
		want                 string
	}{
		{"token refused", 1, `Unknown mode "resolve": expected "review" or "fix"`, "",
			`blocking: 0, non-blocking: 0; 1 resolved thread still open (resolve token refused: Unknown mode "resolve": expected "review" or "fix")`},
		{"resolve failed", 2, "", "2 of 2 thread(s) could not be resolved",
			"blocking: 0, non-blocking: 0; 2 resolved threads still open (2 of 2 thread(s) could not be resolved)"},
		{"no reason", 1, "", "",
			"blocking: 0, non-blocking: 0; 1 resolved thread still open (the resolve step failed; see the workflow log)"},
	}
	for _, c := range cases {
		if got := unresolvedStatusDescription(review, c.n, c.tokenErr, c.resolveErr); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
	long := unresolvedStatusDescription(review, 1, "", strings.Repeat("x", 300))
	if len(long) > commitStatusDescriptionLimit || !strings.HasSuffix(long, "…") || !strings.HasPrefix(long, review) {
		t.Errorf("long reason not capped at %d bytes: %d %q", commitStatusDescriptionLimit, len(long), long)
	}
}

func TestReportUnresolvedThreadsNeedsTheStatus(t *testing.T) {
	if err := ReportUnresolvedThreads("o/r", []string{"PRRT_x"}, "", "boom", ""); err == nil {
		t.Error("want an error without --status")
	}
}

// A thread the review judged fixed but could not resolve gets one reply with
// fixedMarker, so a later run can resolve it (TriagePreviouslyFixed).
func TestHandleResolutionsRecordsDeferredFixedVerdict(t *testing.T) {
	out := filepath.Join(t.TempDir(), "output")
	if err := os.WriteFile(out, nil, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_OUTPUT", out)

	replies := map[string][]string{}
	g := &GithubPlatform{
		resolveThread: func(string) error { return errNotAccessible },
		replyToThread: func(id, body string) error {
			replies[id] = append(replies[id], body)
			return nil
		},
	}
	threads := []ReviewThread{
		{ID: "PRRT_new", Path: "a.go", Line: 1},
		{ID: "PRRT_recorded", Path: "b.go", Line: 2, Replies: []ThreadReply{{Author: "bot", Body: fixedMarker + "\nFixed by a code change — resolving."}}},
		{ID: "PRRT_removed", Path: "c.go", Line: 3},
	}
	g.HandleResolutions(threads, []fixedThread{
		{Index: 0, Reason: "code_change"},
		{Index: 1, Reason: "code_change"},
		{Index: 2, Reason: "file_removed"},
	})

	if got := replies["PRRT_new"]; len(got) != 1 || !strings.HasPrefix(got[0], fixedMarker) {
		t.Errorf("PRRT_new replies = %q, want one fixed-verdict reply", got)
	}
	if got := replies["PRRT_recorded"]; len(got) != 0 {
		t.Errorf("PRRT_recorded already carries the verdict, got replies %q", got)
	}
	for _, body := range replies["PRRT_removed"] {
		if strings.Contains(body, fixedMarker) {
			t.Errorf("a file_removed thread got the code-change verdict: %q", body)
		}
	}
	if len(g.deferredResolves) != 3 {
		t.Errorf("deferred %v, want all three threads", g.deferredResolves)
	}
}
