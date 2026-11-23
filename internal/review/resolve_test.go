package review

import (
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
		}}
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
