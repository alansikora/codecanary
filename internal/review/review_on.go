package review

// skipsPush reports whether review_on: ready makes this run skip: a review
// (not a reply-only run) for a push to a pull request. action is the pull
// request event's action, e.g. "synchronize" for a push.
func skipsPush(cfg *ReviewConfig, replyOnly bool, action string) bool {
	return cfg.ReviewOn == ReviewOnReady && !replyOnly && action == "synchronize"
}
