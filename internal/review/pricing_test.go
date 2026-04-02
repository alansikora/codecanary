package review

import (
	"strings"
	"testing"
)

// TestNoPricingSubstringOverlap verifies that no two providers register
// pricing entries whose substrings could match the same model name.
// This guards the nondeterministic map iteration order in lookupPricing:
// if substrings never overlap across providers, iteration order is irrelevant.
func TestNoPricingSubstringOverlap(t *testing.T) {
	type entry struct {
		provider  string
		substring string
	}
	var all []entry
	for name, pf := range providers {
		for _, e := range pf.Pricing {
			all = append(all, entry{name, strings.ToLower(e.Substring)})
		}
	}
	for i, a := range all {
		for _, b := range all[i+1:] {
			if a.provider == b.provider {
				continue
			}
			// Check if either substring contains the other — if so, a model
			// name matching the shorter one could hit either provider.
			if strings.Contains(a.substring, b.substring) || strings.Contains(b.substring, a.substring) {
				t.Errorf("pricing substring overlap between providers %q (%q) and %q (%q)",
					a.provider, a.substring, b.provider, b.substring)
			}
		}
	}
}
