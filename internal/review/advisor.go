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

// advisorValidExecutorIDs lists full model IDs supported as the executor for
// the advisor tool, matched by exact substring (so dated variants like
// claude-haiku-4-5-20251001 are accepted).
var advisorValidExecutorIDs = []string{
	"claude-haiku-4-5",
	"claude-sonnet-4-6",
	"claude-opus-4-6",
	"claude-opus-4-7",
}

// advisorValidAdvisorIDs lists full model IDs supported as the advisor.
// Anthropic's beta currently only ships claude-opus-4-7 as a valid advisor.
var advisorValidAdvisorIDs = []string{
	"claude-opus-4-7",
}

// advisorValidCLIAliases lists Claude CLI short aliases the user may type for
// either executor or advisor. The CLI resolves these server-side, so we can't
// know the exact model without querying — but we can at least accept the
// documented short names.
var advisorValidCLIAliases = []string{
	"haiku", "sonnet", "opus",
}

// validateAdvisorPairing checks whether the given executor/advisor pair is
// supported. Full model IDs are matched by substring; short CLI aliases are
// matched by exact equality so `"opus"` does not match `"claude-opus-4-7"`.
func validateAdvisorPairing(executor, advisor string) error {
	if !isValidAdvisorExecutor(executor) {
		return fmt.Errorf("advisor_model not supported for review_model %q — executor must be one of %s (or CLI aliases %s)",
			executor,
			strings.Join(advisorValidExecutorIDs, ", "),
			strings.Join(advisorValidCLIAliases, ", "))
	}
	if !isValidAdvisor(advisor) {
		return fmt.Errorf("advisor_model %q is not a supported advisor — advisor must be %s (or CLI alias %q)",
			advisor,
			strings.Join(advisorValidAdvisorIDs, ", "),
			"opus")
	}
	return nil
}

func isValidAdvisorExecutor(model string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(model))
	if matchesAlias(trimmed, advisorValidCLIAliases) {
		return true
	}
	return containsAny(trimmed, advisorValidExecutorIDs)
}

func isValidAdvisor(model string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(model))
	// Only `opus` is a valid advisor alias — the other CLI aliases (haiku,
	// sonnet) are not documented as advisors.
	if trimmed == "opus" {
		return true
	}
	return containsAny(trimmed, advisorValidAdvisorIDs)
}

func matchesAlias(model string, aliases []string) bool {
	for _, a := range aliases {
		if model == a {
			return true
		}
	}
	return false
}

func containsAny(model string, ids []string) bool {
	for _, id := range ids {
		if strings.Contains(model, id) {
			return true
		}
	}
	return false
}
