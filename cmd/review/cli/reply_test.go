package cli

import "testing"

func TestParseReviewCommentURL(t *testing.T) {
	t.Parallel()

	// Happy path — the URL shape the CLI emits from `codecanary findings`.
	owner, name, pr, commentID, err := parseReviewCommentURL(
		"https://github.com/alansikora/codecanary/pull/123#discussion_r987654321")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if owner != "alansikora" || name != "codecanary" || pr != 123 || commentID != 987654321 {
		t.Fatalf("parsed wrong values: owner=%q name=%q pr=%d id=%d",
			owner, name, pr, commentID)
	}

	// Reject URLs that aren't PR review comments — the reply endpoint
	// won't accept them, so catch the mistake at parse time.
	cases := []struct {
		name string
		url  string
	}{
		{"non-github host", "https://gitlab.com/alan/repo/pull/1#discussion_r1"},
		{"github subdomain", "https://api.github.com/alan/repo/pull/1#discussion_r1"},
		{"missing pull segment", "https://github.com/alan/repo/issues/1#discussion_r1"},
		{"no comment fragment", "https://github.com/alan/repo/pull/1"},
		{"malformed PR number", "https://github.com/alan/repo/pull/notanumber#discussion_r1"},
		{"malformed URL", "not a url"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, _, _, _, err := parseReviewCommentURL(tc.url); err == nil {
				t.Fatalf("expected error for %q, got nil", tc.url)
			}
		})
	}
}
