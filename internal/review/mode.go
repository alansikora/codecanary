package review

import (
	"fmt"
)

// Mode identifies which review loop the codecanary-fix skill should run.
type Mode string

const (
	// ModePRLoop — PR exists and a CodeCanary GitHub Action is wired up
	// on the branch. The loop waits on the bot, applies approved fixes,
	// commits + pushes, and waits again.
	ModePRLoop Mode = "pr-loop"

	// ModeLocalLoopGit — PR exists but no CodeCanary workflow is
	// detected on the branch. The loop runs reviews locally and commits
	// each cycle on the PR branch without pushing; the operator is
	// prompted to push at the end of the session.
	ModeLocalLoopGit Mode = "local-loop-git"

	// ModeLocalLoopNoGit — no PR for the current branch. The loop runs
	// reviews locally, applies fixes in place, and never touches git.
	ModeLocalLoopNoGit Mode = "local-loop-nogit"
)

// ModeInfo is the full detection result. It serialises to the JSON
// shape consumed by the codecanary-fix skill; field names are load-bearing.
type ModeInfo struct {
	Mode             Mode     `json:"mode"`
	PR               *int     `json:"pr"`
	Branch           string   `json:"branch"`
	Repo             string   `json:"repo,omitempty"`
	WorkflowDetected bool     `json:"workflow_detected"`
	WorkflowPath     string   `json:"workflow_path,omitempty"`
	Reasons          []string `json:"reasons"`
}

// DetectMode resolves the loop mode for the current working tree.
//
// It calls CurrentBranch(), DetectPRNumber(), DetectRepo(), and scans
// .github/workflows/*.yml — all best-effort. A missing PR or missing
// workflow is not an error, just a signal that pushes the decision
// toward a local-loop mode.
func DetectMode() (*ModeInfo, error) {
	branch, err := CurrentBranch()
	if err != nil {
		return nil, err
	}

	info := &ModeInfo{Branch: branch}

	if repo, err := DetectRepo(); err == nil {
		info.Repo = repo
	}

	if pr, err := DetectPRNumber(""); err == nil {
		info.PR = &pr
		info.Reasons = append(info.Reasons,
			fmt.Sprintf("open PR #%d on branch %s", pr, branch))
	} else {
		info.Reasons = append(info.Reasons,
			fmt.Sprintf("no open PR for branch %s", branch))
	}

	if path, ok := detectCodecanaryWorkflow(); ok {
		info.WorkflowDetected = true
		info.WorkflowPath = path
		info.Reasons = append(info.Reasons,
			fmt.Sprintf("CodeCanary workflow detected at %s", path))
	} else {
		info.Reasons = append(info.Reasons,
			"no CodeCanary workflow detected on this branch")
	}

	switch {
	case info.PR != nil && info.WorkflowDetected:
		info.Mode = ModePRLoop
	case info.PR != nil:
		info.Mode = ModeLocalLoopGit
	default:
		info.Mode = ModeLocalLoopNoGit
	}

	return info, nil
}
