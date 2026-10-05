package setup

import (
	"testing"

	"github.com/charmbracelet/huh"

	"github.com/alansikora/codecanary/internal/review"
)

// The setup selects pre-fill the provider's suggested model. A suggestion
// missing from the option list is silently replaced by the first option, so
// every suggestion must be selectable.
func TestSuggestedModelsAreOptions(t *testing.T) {
	has := func(opts []huh.Option[string], v string) bool {
		for _, o := range opts {
			if o.Value == v {
				return true
			}
		}
		return false
	}
	for _, p := range []string{"anthropic", "openai", "grok", "openrouter", "claude"} {
		if s := review.GetSuggestedReviewModel(p); s != "" && !has(modelOptions(p), s) {
			t.Errorf("%s: suggested review model %q is not among the review model options", p, s)
		}
		if s := review.GetSuggestedTriageModel(p); s != "" && !has(triageModelOptions(p), s) {
			t.Errorf("%s: suggested triage model %q is not among the triage model options", p, s)
		}
	}
}
