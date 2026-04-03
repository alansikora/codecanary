package selfupdate

import "testing"

func TestIsNewer(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"v0.1.0", "v0.2.0", true},
		{"v0.2.0", "v0.1.0", false},
		{"v0.1.0", "v0.1.0", false},
		{"v1.0.0", "v0.9.9", false},
		{"v0.9.9", "v1.0.0", true},
		{"v0.1.0", "v0.1.1", true},
		{"v0.1.1", "v0.1.0", false},
		{"0.1.0", "0.2.0", true},     // without v prefix
		{"v0.1.0", "0.2.0", true},    // mixed prefix
		{"dev", "v0.1.0", false},      // dev version
		{"v1.0.0-rc1", "v1.0.0", false}, // pre-release same version
		{"v0.9.0", "v1.0.0-rc1", true},  // pre-release newer major
		{"", "v1.0.0", false},
		{"v1.0.0", "", false},
		{"invalid", "v1.0.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.current+"_vs_"+tt.latest, func(t *testing.T) {
			if got := IsNewer(tt.current, tt.latest); got != tt.want {
				t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}

func TestParseSemver(t *testing.T) {
	tests := []struct {
		input string
		want  []int
	}{
		{"v1.2.3", []int{1, 2, 3}},
		{"1.2.3", []int{1, 2, 3}},
		{"v0.0.0", []int{0, 0, 0}},
		{"v1.2.3-rc1", []int{1, 2, 3}},
		{"dev", nil},
		{"", nil},
		{"v1.2", nil},
		{"v1.2.abc", nil},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseSemver(tt.input)
			if tt.want == nil {
				if got != nil {
					t.Errorf("parseSemver(%q) = %v, want nil", tt.input, got)
				}
				return
			}
			if got == nil {
				t.Errorf("parseSemver(%q) = nil, want %v", tt.input, tt.want)
				return
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("parseSemver(%q)[%d] = %d, want %d", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}
