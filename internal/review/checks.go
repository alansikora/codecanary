package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// RequiredChecks is the state of a PR's required checks other than
// CodeCanary's own, as reported by `codecanary checks`.
type RequiredChecks struct {
	// State is "pass" (every required check passed, or none is required),
	// "fail" (at least one failed or was cancelled) or "pending".
	State   string   `json:"state"`
	Failing []string `json:"failing,omitempty"`
	Pending []string `json:"pending,omitempty"`
}

// ghCheck is one entry of `gh pr checks --required --json name,bucket,workflow`.
type ghCheck struct {
	Name     string `json:"name"`
	Bucket   string `json:"bucket"` // pass | fail | pending | skipping | cancel
	Workflow string `json:"workflow"`
}

// isCodecanaryCheck reports whether c is CodeCanary's own: the commit status
// it posts, or the review job of its workflow. Waiting on those would
// deadlock a draft (CodeCanary doesn't run there) or wait on the review the
// caller is about to request.
func isCodecanaryCheck(c ghCheck) bool {
	return c.Name == ReviewCommitStatusContext ||
		(strings.EqualFold(c.Name, reviewCheckName) && strings.EqualFold(c.Workflow, "CodeCanary"))
}

// requiredChecksState folds required checks into one state. Skipped checks
// count as passed, as GitHub's merge gate treats them.
func requiredChecksState(checks []ghCheck) RequiredChecks {
	rc := RequiredChecks{State: "pass"}
	for _, c := range checks {
		if isCodecanaryCheck(c) {
			continue
		}
		switch c.Bucket {
		case "fail", "cancel":
			rc.Failing = append(rc.Failing, c.Name)
		case "pending":
			rc.Pending = append(rc.Pending, c.Name)
		}
	}
	switch {
	case len(rc.Failing) > 0:
		rc.State = "fail"
	case len(rc.Pending) > 0:
		rc.State = "pending"
	}
	return rc
}

// FetchRequiredChecks returns the state of the PR's required checks, leaving
// out CodeCanary's own. A repo with no required checks reports "pass".
func FetchRequiredChecks(repo string, prNumber int) (RequiredChecks, error) {
	args := []string{"pr", "checks", fmt.Sprintf("%d", prNumber),
		"--required", "--json", "name,bucket,workflow"}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	// gh exits non-zero when a check failed or is still pending, and also
	// when the repo requires none; the JSON is on stdout in the first two
	// cases, so read it whatever the exit status.
	var stderr bytes.Buffer
	cmd := exec.Command("gh", args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil && len(bytes.TrimSpace(out)) == 0 {
		if strings.Contains(stderr.String(), "no required checks") {
			return RequiredChecks{State: "pass"}, nil
		}
		return RequiredChecks{}, fmt.Errorf("gh pr checks: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var checks []ghCheck
	if err := json.Unmarshal(out, &checks); err != nil {
		return RequiredChecks{}, fmt.Errorf("parsing pr checks: %w", err)
	}
	return requiredChecksState(checks), nil
}

// WaitForRequiredChecks polls until the PR's required checks (other than
// CodeCanary's) pass or one fails. A zero timeout waits indefinitely.
func WaitForRequiredChecks(repo string, prNumber int, timeout time.Duration) (RequiredChecks, error) {
	return waitForRequiredChecks(func() (RequiredChecks, error) {
		return FetchRequiredChecks(repo, prNumber)
	}, prNumber, timeout, 15*time.Second, os.Stderr)
}

func waitForRequiredChecks(
	fetch func() (RequiredChecks, error),
	prNumber int,
	timeout, pollInterval time.Duration,
	progress io.Writer,
) (RequiredChecks, error) {
	deadline := time.Time{}
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	for {
		rc, err := fetch()
		if err != nil {
			return rc, err
		}
		if rc.State != "pending" {
			_, _ = fmt.Fprintln(progress)
			return rc, nil
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return rc, fmt.Errorf("timed out after %s waiting for required checks on PR #%d (pending: %s)",
				timeout, prNumber, strings.Join(rc.Pending, ", "))
		}
		_, _ = fmt.Fprint(progress, ".")
		time.Sleep(pollInterval)
	}
}
