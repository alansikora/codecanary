package review

import "testing"

func boolPtr(v bool) *bool { return &v }

func TestFilterNonActionable(t *testing.T) {
	tests := []struct {
		name      string
		findings  []Finding
		wantCount int
	}{
		{
			name: "actionable false is dropped",
			findings: []Finding{
				{ID: "a", Actionable: boolPtr(false)},
			},
			wantCount: 0,
		},
		{
			name: "actionable true is kept",
			findings: []Finding{
				{ID: "a", Actionable: boolPtr(true)},
			},
			wantCount: 1,
		},
		{
			name: "actionable nil is kept",
			findings: []Finding{
				{ID: "a", Actionable: nil},
			},
			wantCount: 1,
		},
		{
			name: "mixed findings",
			findings: []Finding{
				{ID: "keep-1", Actionable: boolPtr(true)},
				{ID: "drop", Actionable: boolPtr(false)},
				{ID: "keep-2", Actionable: nil},
			},
			wantCount: 2,
		},
		{
			name:      "empty input",
			findings:  []Finding{},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterNonActionable(tt.findings)
			if len(got) != tt.wantCount {
				t.Errorf("FilterNonActionable() returned %d findings, want %d", len(got), tt.wantCount)
			}
		})
	}
}
