package review

import (
	"encoding/json"
	"os"
)

// githubEventAction is the `action` of the pull request event the workflow
// runs for (e.g. "synchronize", "ready_for_review"), or "" outside Actions.
func githubEventAction() string {
	path := os.Getenv("GITHUB_EVENT_PATH")
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var event struct {
		Action string `json:"action"`
	}
	if json.Unmarshal(data, &event) != nil {
		return ""
	}
	return event.Action
}

// skipsPush reports whether review_on: ready makes this run skip: a review
// (not a reply-only run) for a push to a pull request.
func skipsPush(cfg *ReviewConfig, replyOnly bool, action string) bool {
	return cfg.ReviewOn == ReviewOnReady && !replyOnly && action == "synchronize"
}
