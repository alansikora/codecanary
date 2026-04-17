package review

import (
	"strings"
	"testing"
)

func TestRenderSummaryBlockEmpty(t *testing.T) {
	if got := renderSummaryBlock(ReviewSummary{}); got != "" {
		t.Errorf("expected empty string for zero summary, got %q", got)
	}
}

func TestRenderSummaryBlockOmitsZeroRows(t *testing.T) {
	got := renderSummaryBlock(ReviewSummary{Dismissed: 2})
	if !strings.Contains(got, "Dismissed by author: **2**") {
		t.Errorf("missing dismissed row:\n%q", got)
	}
	if strings.Contains(got, "New findings") {
		t.Errorf("zero-count New findings row should be omitted:\n%q", got)
	}
	if !strings.Contains(got, statusBlockOpen) || !strings.Contains(got, statusBlockClose) {
		t.Errorf("status block markers missing:\n%q", got)
	}
}

func TestComputeReviewSummary(t *testing.T) {
	threads := []ReviewThread{{}, {}, {}, {}, {}}
	fixed := []fixedThread{
		{Index: 0, Reason: "code_change"},
		{Index: 1, Reason: "dismissed"},
		{Index: 2, Reason: "rebutted"},
		{Index: 3, Reason: "file_removed"},
	}
	findings := []Finding{{ID: "a"}, {ID: "b"}}

	got := computeReviewSummary(threads, fixed, findings)
	want := ReviewSummary{
		NewFindings:    2,
		ResolvedByCode: 1,
		FileRemoved:    1,
		Dismissed:      1,
		Rebutted:       1,
		StillOpen:      1, // thread index 4 not in fixed
	}
	if got != want {
		t.Errorf("summary = %+v, want %+v", got, want)
	}
}

func TestReplaceSummaryBlockAppendsWhenMissing(t *testing.T) {
	body := "hello"
	got := replaceSummaryBlock(body, ReviewSummary{NewFindings: 1})
	if !strings.HasPrefix(got, "hello") {
		t.Errorf("existing body should be preserved:\n%q", got)
	}
	if !strings.Contains(got, statusBlockOpen) {
		t.Errorf("new block should be appended:\n%q", got)
	}
}

func TestReplaceSummaryBlockUpdatesInPlace(t *testing.T) {
	original := "prefix\n" + renderSummaryBlock(ReviewSummary{NewFindings: 1}) + "suffix"
	updated := replaceSummaryBlock(original, ReviewSummary{Dismissed: 2})
	if strings.Contains(updated, "New findings") {
		t.Errorf("old block should be replaced, got:\n%q", updated)
	}
	if !strings.Contains(updated, "Dismissed by author: **2**") {
		t.Errorf("new block should be present, got:\n%q", updated)
	}
	if !strings.HasPrefix(updated, "prefix\n") {
		t.Errorf("prefix should be preserved, got:\n%q", updated)
	}
	if !strings.HasSuffix(updated, "suffix") {
		t.Errorf("suffix should be preserved, got:\n%q", updated)
	}
}

func TestReplaceSummaryBlockStripsWhenEmpty(t *testing.T) {
	original := "prefix\n" + renderSummaryBlock(ReviewSummary{NewFindings: 1}) + "suffix"
	updated := replaceSummaryBlock(original, ReviewSummary{})
	if strings.Contains(updated, statusBlockOpen) {
		t.Errorf("status block should be stripped when empty, got:\n%q", updated)
	}
	if !strings.HasPrefix(updated, "prefix\n") || !strings.HasSuffix(updated, "suffix") {
		t.Errorf("non-block content should be preserved, got:\n%q", updated)
	}
}
