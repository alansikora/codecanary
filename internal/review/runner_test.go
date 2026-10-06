package review

import "testing"

// --fail-on gates CI, so it must see the findings still open from previous
// reviews too — a push that raises nothing new but leaves a bug unresolved
// is exactly the state the flag exists to catch.
func TestCountAtOrAboveSeverity(t *testing.T) {
	cases := []struct {
		name      string
		result    *ReviewResult
		threshold string
		want      int
	}{
		{
			name:      "new findings only",
			result:    &ReviewResult{Findings: []Finding{{Severity: "bug"}, {Severity: "nitpick"}}},
			threshold: "warning",
			want:      1,
		},
		{
			name:      "still-open findings count too",
			result:    &ReviewResult{StillOpen: []Finding{{Severity: "critical"}, {Severity: "bug"}}},
			threshold: "warning",
			want:      2,
		},
		{
			name: "both slices are summed",
			result: &ReviewResult{
				Findings:  []Finding{{Severity: "warning"}},
				StillOpen: []Finding{{Severity: "bug"}, {Severity: "suggestion"}},
			},
			threshold: "warning",
			want:      2,
		},
		{
			name:      "threshold excludes lower severities",
			result:    &ReviewResult{StillOpen: []Finding{{Severity: "suggestion"}, {Severity: "nitpick"}}},
			threshold: "bug",
			want:      0,
		},
		{
			name:      "empty result",
			result:    &ReviewResult{},
			threshold: "nitpick",
			want:      0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := countAtOrAboveSeverity(tc.result, tc.threshold); got != tc.want {
				t.Errorf("countAtOrAboveSeverity(%q) = %d, want %d", tc.threshold, got, tc.want)
			}
		})
	}
}
