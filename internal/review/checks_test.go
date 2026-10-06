package review

import (
	"io"
	"testing"
)

func TestRequiredChecksState(t *testing.T) {
	cc := ghCheck{Name: "CodeCanary / review", Bucket: "pending"}
	ccJob := ghCheck{Name: "review", Bucket: "pending", Workflow: "CodeCanary"}
	cases := []struct {
		name   string
		checks []ghCheck
		want   string
	}{
		{"none required", nil, "pass"},
		{"only CodeCanary's own, pending", []ghCheck{cc, ccJob}, "pass"},
		{"ci passed", []ghCheck{{Name: "ci", Bucket: "pass"}, cc}, "pass"},
		{"ci skipped counts as passed", []ghCheck{{Name: "ci", Bucket: "skipping"}}, "pass"},
		{"ci running", []ghCheck{{Name: "ci", Bucket: "pending"}}, "pending"},
		{"ci failed", []ghCheck{{Name: "ci", Bucket: "fail"}, {Name: "lint", Bucket: "pending"}}, "fail"},
		{"ci cancelled", []ghCheck{{Name: "ci", Bucket: "cancel"}}, "fail"},
		{"a non-CodeCanary job named review still counts", []ghCheck{{Name: "review", Bucket: "pending", Workflow: "CI"}}, "pending"},
	}
	for _, c := range cases {
		if got := requiredChecksState(c.checks); got.State != c.want {
			t.Errorf("%s: state = %q (%+v), want %q", c.name, got.State, got, c.want)
		}
	}
}

func TestWaitForRequiredChecksStopsOnResult(t *testing.T) {
	states := []string{"pending", "pending", "fail"}
	i := 0
	fetch := func() (RequiredChecks, error) {
		s := states[i]
		i++
		return RequiredChecks{State: s}, nil
	}
	rc, err := waitForRequiredChecks(fetch, 7, 0, 0, io.Discard)
	if err != nil || rc.State != "fail" || i != 3 {
		t.Errorf("got %+v, %v after %d polls; want fail after 3", rc, err, i)
	}
}
