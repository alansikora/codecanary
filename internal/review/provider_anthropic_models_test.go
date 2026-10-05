package review

import (
	"strings"
	"testing"
)

// Substring matching takes the first hit, so a newer ID must not fall into
// an older family's entry (claude-opus-4-8 into the claude-opus-4- catch-all,
// claude-fable-5-1 into claude-fable-5).
func TestAnthropicPricingCurrentModels(t *testing.T) {
	for model, want := range map[string]modelPricing{
		"claude-sonnet-5-5": {2, 10, 2.50, 0.20},
		"claude-sonnet-5":   {2, 10, 2.50, 0.20},
		"claude-sonnet-4-6": {3, 15, 3.75, 0.30},
		"claude-opus-5-5":   {4, 20, 5, 0.20},
		"claude-opus-5":     {5, 25, 6.25, 0.50},
		"claude-opus-4-8":   {5, 25, 6.25, 0.50},
		"claude-opus-4-7":   {5, 25, 6.25, 0.50},
		"claude-opus-4-1":   {15, 75, 18.75, 1.50},
		"claude-fable-5-1":  {10, 50, 12.50, 0.25},
		"claude-fable-5":    {10, 50, 12.50, 1.00},
		"claude-haiku-4-5":  {1, 5, 1.25, 0.10},
	} {
		got := lookupPricing(model)
		if got == nil || *got != want {
			t.Errorf("lookupPricing(%q) = %+v, want %+v", model, got, want)
		}
	}
}

func TestAnthropicMaxOutputCurrentModels(t *testing.T) {
	for model, want := range map[string]int{
		"claude-sonnet-5-5": 128_000,
		"claude-opus-5-5":   128_000,
		"claude-fable-5-1":  128_000,
		"claude-opus-4-8":   128_000,
		"claude-sonnet-4-6": 64_000,
	} {
		if got := lookupMaxOutputTokens(model); got != want {
			t.Errorf("lookupMaxOutputTokens(%q) = %d, want %d", model, got, want)
		}
	}
}

func TestRefusalError(t *testing.T) {
	if err := refusalError(&anthropicResponse{StopReason: "end_turn"}); err != nil {
		t.Errorf("end_turn should not be an error, got %v", err)
	}

	r := &anthropicResponse{StopReason: "refusal"}
	r.StopDetails = &struct {
		Category    string `json:"category"`
		Explanation string `json:"explanation"`
	}{Category: "cyber", Explanation: "declined"}
	err := refusalError(r)
	if err == nil || !strings.Contains(err.Error(), "category: cyber") || !strings.Contains(err.Error(), "declined") {
		t.Errorf("refusal error should name the category and explanation, got %v", err)
	}

	if err := refusalError(&anthropicResponse{StopReason: "refusal"}); err == nil || !strings.Contains(err.Error(), "category: unspecified") {
		t.Errorf("refusal without stop_details should still error, got %v", err)
	}
}
