package review

import "testing"

func boolPtr(v bool) *bool { return &v }

func TestIsPossiblyNonActionable(t *testing.T) {
	tests := []struct {
		name     string
		finding  Finding
		expected bool
	}{
		{
			name: "clearly actionable finding",
			finding: Finding{
				Description: "This function does not handle the error returned by Close(). This can silently swallow I/O errors.",
				Suggestion:  "Consider wrapping the Close() call in an error check.",
			},
			expected: false,
		},
		{
			name: "non-actionable - concludes code is fine",
			finding: Finding{
				Description: "The allowlist permits dots, which is necessary for semver tags. The real concern is that TAG is interpolated into a curl URL but it is double-quoted, so this is safe. No action needed on that front.",
			},
			expected: true,
		},
		{
			name: "non-actionable - this is fine",
			finding: Finding{
				Description: "Looked at the error handling here. The fallback catches all edge cases. This is fine.",
			},
			expected: true,
		},
		{
			name: "non-actionable - correctly handled",
			finding: Finding{
				Description: "The race condition concern is correctly handled by the mutex acquired on line 45.",
			},
			expected: true,
		},
		{
			name: "mixed - dismissive phrase but also actionable suggestion",
			finding: Finding{
				Description: "The current validation is fine for now, but this is safe only because the input is trusted. You should add input sanitization if this is ever exposed to user input.",
				Suggestion:  "Consider adding bounds checking.",
			},
			expected: false,
		},
		{
			name: "actionable field explicitly false",
			finding: Finding{
				Description: "The code looks reasonable.",
				Actionable:  boolPtr(false),
			},
			expected: true,
		},
		{
			name: "actionable field explicitly true",
			finding: Finding{
				Description: "No action needed.",
				Actionable:  boolPtr(true),
			},
			// Actionable=true does not override pattern detection
			expected: true,
		},
		{
			name: "actionable field nil with no dismissive language",
			finding: Finding{
				Description: "Buffer overflow when input exceeds 256 bytes.",
			},
			expected: false,
		},
		{
			name: "non-actionable - no real issue",
			finding: Finding{
				Description: "Investigated the potential null pointer dereference. The guard clause on line 12 prevents this. No real issue here.",
			},
			expected: true,
		},
		{
			name: "dismissive phrase in suggestion does not count alone",
			finding: Finding{
				Description: "The retry logic has no backoff, which can cause thundering herd.",
				Suggestion:  "The current approach is fine for low-traffic services, but consider exponential backoff.",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.finding.IsPossiblyNonActionable()
			if got != tt.expected {
				t.Errorf("IsPossiblyNonActionable() = %v, want %v\n  description: %s", got, tt.expected, tt.finding.Description)
			}
		})
	}
}
