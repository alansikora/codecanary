package review

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alansikora/codecanary/internal/selfupdate"
)

// Update notice: a short note appended to the top-level review the bot
// posts on GitHub when the repo's CodeCanary install has fallen behind —
// its workflow file predates the template this binary ships, or the
// binary is older than the latest stable release (a pinned
// codecanary_version). Consumer repos otherwise never learn about either.
//
// Both checks are best-effort: they never fail or delay a review beyond
// updateCheckTimeout, and errors are logged as warnings only.

const (
	updateCheckTimeout  = 3 * time.Second
	workflowTemplateURL = "https://github.com/alansikora/codecanary/blob/main/internal/setup/codecanary.yml"
)

// UpdateCheck carries what the update notice compares the repo's install
// against. The zero value disables both checks.
type UpdateCheck struct {
	// BinaryVersion is the running binary's version as set by ldflags
	// ("v0.6.24"; canary builds are "v0.6.24-SNAPSHOT-<sha>"; "dev").
	BinaryVersion string
	// TemplateVersion is the marker version of the workflow template
	// embedded in this binary (setup.TemplateVersion()).
	TemplateVersion int
	// LatestRelease returns the latest stable release tag. Nil means
	// selfupdate.LatestRelease; tests inject a stub.
	LatestRelease func(ctx context.Context) (string, error)
}

// Notice runs both checks and returns the rendered note, or "" when the
// install is current or nothing could be determined.
func (u UpdateCheck) Notice() string {
	var lines []string

	if u.TemplateVersion > 0 {
		wf, found := findCodecanaryWorkflow()
		if line := workflowNoticeLine(wf, found, u.TemplateVersion); line != "" {
			lines = append(lines, line)
		}
	}

	if line := u.binaryNoticeLine(); line != "" {
		lines = append(lines, line)
	}

	return renderUpdateNotice(lines)
}

// workflowNoticeLine returns the note for a workflow copy older than the
// embedded template, or "" when it is current or there is no workflow
// file to compare (the action may be invoked from a reusable workflow or
// another repo; nothing to say then).
//
// Comparison is by template marker, not content: users customize their
// copy (secret name, action ref, extra steps), and a content diff would
// flag every one of them forever. A copy without a marker predates the
// first marked template and counts as version 0.
func workflowNoticeLine(wf codecanaryWorkflow, found bool, templateVersion int) string {
	if !found {
		return ""
	}
	have := WorkflowTemplateVersion(wf.Content)
	if have >= templateVersion {
		return ""
	}
	from := "predates the current template"
	if have > 0 {
		from = fmt.Sprintf("is on template v%d", have)
	}
	return fmt.Sprintf("This repo's CodeCanary workflow (`%s`) %s (v%d). "+
		"Update it with `codecanary setup github` or copy the [latest template](%s).",
		wf.Path, from, templateVersion, workflowTemplateURL)
}

// binaryNoticeLine returns the note for a binary older than the latest
// stable release, or "" when it is current or the check is skipped.
//
// Only stable builds are checked. Canary builds are cut from main after
// the last release, so they are never behind it in content even when
// their version string (the last release plus a -SNAPSHOT suffix) sorts
// below a release published since; "dev" builds have no version at all.
func (u UpdateCheck) binaryNoticeLine() string {
	if !selfupdate.IsStable(u.BinaryVersion) {
		return ""
	}
	latestFn := u.LatestRelease
	if latestFn == nil {
		latestFn = selfupdate.LatestRelease
	}
	ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
	defer cancel()
	latest, err := latestFn(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not check for a newer CodeCanary release: %v\n", err)
		return ""
	}
	if !selfupdate.IsNewer(u.BinaryVersion, latest) {
		return ""
	}
	return fmt.Sprintf("This review ran CodeCanary %s; the latest release is %s. "+
		"Update or remove the `codecanary_version` input in the workflow to pick it up.",
		u.BinaryVersion, latest)
}

// renderUpdateNotice renders the note lines as small print below the
// review body, or "" when there are none. Shaped like the other optional
// review notes: starts with a blank line, ends with a newline, so it can
// be appended to any body.
func renderUpdateNotice(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	for _, l := range lines {
		fmt.Fprintf(&b, "\n<sub>ℹ️ %s</sub>\n", l)
	}
	return b.String()
}
