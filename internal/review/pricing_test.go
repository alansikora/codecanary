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

// TestIntraProviderPricingOrder verifies that within each provider's Pricing
// slice, overlapping substrings are ordered most-specific-first. Since
// lookupPricing returns the first match within a slice, a less specific
// entry appearing before a more specific one would shadow it.
func TestIntraProviderPricingOrder(t *testing.T) {
	for name, pf := range providers {
		entries := pf.Pricing
		for i, a := range entries {
			for j, b := range entries[i+1:] {
				aLower := strings.ToLower(a.Substring)
				bLower := strings.ToLower(b.Substring)
				// If a (earlier) contains b (later), that's fine — a is more specific.
				// But if b contains a, then a is the shorter/less-specific entry
				// and it would shadow b.
				if strings.Contains(bLower, aLower) && !strings.Contains(aLower, bLower) {
					t.Errorf("provider %q: pricing entry %d (%q) shadows entry %d (%q) — more specific substrings must come first",
						name, i, a.Substring, i+1+j, b.Substring)
				}
			}
		}
	}
}
