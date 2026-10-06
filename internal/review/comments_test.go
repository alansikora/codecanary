package review

import "testing"

func TestParseFindingMarkersFiltersByAuthor(t *testing.T) {
	comments := []PRReviewComment{
		{
			// Not authored by the bot — should be ignored even if a
			// marker somehow appears in the body.
			User: struct {
				Login string `json:"login"`
			}{Login: "someuser"},
			Body: `<!-- codecanary:finding {"id":"x","file":"a.go","line":1,"severity":"bug","title":"t","description":"d","fix_ref":"1-1"} -->`,
		},
	}
	got := ParseFindingMarkers(comments)
	if len(got) != 0 {
		t.Fatalf("expected 0 findings from non-bot author, got %d", len(got))
	}
}

func TestParseFindingMarkersExtractsJSON(t *testing.T) {
	marker := `<!-- codecanary:finding {"id":"sql","file":"x.ts","line":42,"severity":"critical","title":"SQL injection","description":"desc","fix_ref":"123-1","actionable":true} -->`
	body := "🔴 **critical** — `sql`\n\nprose here\n\n" + marker

	comments := []PRReviewComment{
		{
			User: struct {
				Login string `json:"login"`
			}{Login: BotLogin},
			Body:      body,
			HTMLURL:   "https://github.com/owner/repo/pull/123#discussion_r1",
			CommitID:  "abc1234",
			CreatedAt: "2026-04-13T10:00:00Z",
			Path:      "x.ts",
			Line:      42,
		},
	}

	got := ParseFindingMarkers(comments)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(got))
	}
	f := got[0]
	if f.ID != "sql" || f.File != "x.ts" || f.Line != 42 || f.Severity != "critical" {
		t.Errorf("unexpected finding fields: %+v", f)
	}
	if f.FixRef != "123-1" {
		t.Errorf("expected fix_ref=123-1, got %q", f.FixRef)
	}
	if f.CommentURL != "https://github.com/owner/repo/pull/123#discussion_r1" {
		t.Errorf("missing comment URL: %+v", f)
	}
	if f.CommitID != "abc1234" {
		t.Errorf("missing commit id: %+v", f)
	}
}

func TestParseFindingMarkersSkipsCommentsWithoutMarker(t *testing.T) {
	comments := []PRReviewComment{
		{
			User: struct {
				Login string `json:"login"`
			}{Login: BotLogin},
			Body: "just a plain bot comment with no finding marker",
		},
	}
	got := ParseFindingMarkers(comments)
	if len(got) != 0 {
		t.Fatalf("expected 0 findings, got %d", len(got))
	}
}

func TestParseFindingMarkersHandlesMalformedJSON(t *testing.T) {
	comments := []PRReviewComment{
		{
			User: struct {
				Login string `json:"login"`
			}{Login: BotLogin},
			Body: `prose <!-- codecanary:finding {not valid json} -->`,
		},
	}
	got := ParseFindingMarkers(comments)
	if len(got) != 0 {
		t.Fatalf("expected 0 findings from malformed marker, got %d", len(got))
	}
}

func rollupOf(draft bool, runs ...[2]string) ghStatusRollup {
	r := ghStatusRollup{HeadRefOid: "abc", IsDraft: draft}
	for _, run := range runs {
		r.StatusCheckRollup = append(r.StatusCheckRollup, struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		}{"review", run[0], run[1]})
	}
	return r
}

// With review_on: ready the loop pushes to a draft (the review job is
// skipped), then marks the PR ready, which starts a new run on the same
// commit. The watcher must wait for that run, not return the skipped one.
func TestReviewStatusFromRollup(t *testing.T) {
	cases := []struct {
		name               string
		rollup             ghStatusRollup
		wantStatus, wantCo string
	}{
		{"no run yet", rollupOf(false), "", ""},
		{"single completed run", rollupOf(false, [2]string{"COMPLETED", "SUCCESS"}), "completed", "success"},
		{"in progress", rollupOf(false, [2]string{"IN_PROGRESS", ""}), "in_progress", ""},
		{"ready PR, only the draft push's skipped run", rollupOf(false, [2]string{"COMPLETED", "SKIPPED"}), "", ""},
		{"ready PR, skipped run then a queued one", rollupOf(false, [2]string{"COMPLETED", "SKIPPED"}, [2]string{"QUEUED", ""}), "queued", ""},
		{"ready PR, skipped run then a finished one", rollupOf(false, [2]string{"COMPLETED", "SKIPPED"}, [2]string{"COMPLETED", "SUCCESS"}), "completed", "success"},
		{"draft PR, skipped: no review is coming", rollupOf(true, [2]string{"COMPLETED", "SKIPPED"}), "completed", "skipped"},
	}
	for _, c := range cases {
		got := reviewStatusFromRollup(c.rollup)
		if got.Status != c.wantStatus || got.Conclusion != c.wantCo {
			t.Errorf("%s: got %q/%q, want %q/%q", c.name, got.Status, got.Conclusion, c.wantStatus, c.wantCo)
		}
	}
}
