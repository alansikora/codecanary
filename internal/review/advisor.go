package review

import (
	"fmt"
	"strings"
)

// Anthropic advisor tool constants.
//
// The advisor tool lets a faster executor model consult a higher-intelligence
// advisor model mid-generation for strategic guidance. Anthropic exposes it as
// a server-side tool; the executor emits a server_tool_use block with empty
// input and the server runs a separate sub-inference on the advisor model.
//
// See: https://platform.claude.com/docs/en/agents-and-tools/tool-use/advisor-tool
const (
	advisorBetaHeader = "advisor-tool-2026-03-01"
	advisorToolType   = "advisor_20260301"
	advisorToolName   = "advisor"
)

// advisorValidExecutors lists executor model substrings supported by the
// advisor tool. The advisor model itself must be at least as capable as the
// executor; Anthropic's documented pairings use claude-opus-4-7 as the advisor.
var advisorValidExecutors = []string{
	"claude-haiku-4-5",
	"claude-sonnet-4-6",
	"claude-opus-4-6",
	"claude-opus-4-7",
	// CLI aliases — the Claude CLI resolves these server-side.
	"haiku", "sonnet", "opus",
}

// advisorValidAdvisors lists advisor model substrings supported by the tool.
// Anthropic's beta currently only ships claude-opus-4-7 as a valid advisor.
var advisorValidAdvisors = []string{
	"claude-opus-4-7",
	"opus",
}

// validateAdvisorPairing checks whether the given executor/advisor pair is
// supported by the advisor tool. Matches by substring so provider-specific
// aliases (e.g. "sonnet") and dated IDs both work.
func validateAdvisorPairing(provider, executor, advisor string) error {
	execOK := matchesAdvisorModel(executor, advisorValidExecutors)
	advOK := matchesAdvisorModel(advisor, advisorValidAdvisors)
	if !execOK {
		return fmt.Errorf("advisor_model not supported for review_model %q — executor must be one of %s", executor, strings.Join(advisorValidExecutors, ", "))
	}
	if !advOK {
		return fmt.Errorf("advisor_model %q is not a supported advisor — advisor must be one of %s", advisor, strings.Join(advisorValidAdvisors, ", "))
	}
	return nil
}

func matchesAdvisorModel(model string, candidates []string) bool {
	lower := strings.ToLower(strings.TrimSpace(model))
	for _, c := range candidates {
		if lower == c || strings.Contains(lower, c) {
			return true
		}
	}
	return false
}
