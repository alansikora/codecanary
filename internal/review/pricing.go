package review

import (
	"fmt"
	"os"
	"strings"
)

// modelPricing holds per-million-token prices for a model.
type modelPricing struct {
	InputPerMTok      float64 // base input price per million tokens
	OutputPerMTok     float64 // output price per million tokens
	CacheWritePerMTok float64 // cache write price per million tokens (0 = same as input)
	CacheReadPerMTok  float64 // cache hit/read price per million tokens (0 = same as input)
}

// knownPricing maps model name substrings to pricing. Matched in order;
// first match wins. Entries use substrings so that both full model IDs
// (e.g. "claude-sonnet-4-20250514") and OpenRouter-style prefixed names
// (e.g. "anthropic/claude-sonnet-4-20250514") are matched.
//
// Last updated: 2026-04-01. If a model is not found, estimateCost returns 0
// and a warning is printed to stderr.
var knownPricing = []struct {
	substring string
	pricing   modelPricing
}{
	// ── Anthropic ──

	// Opus 4.6 / 4.5
	{"claude-opus-4-6", modelPricing{5, 25, 6.25, 0.50}},
	{"claude-opus-4-5", modelPricing{5, 25, 6.25, 0.50}},
	// Opus 4.1 / 4
	{"claude-opus-4-1", modelPricing{15, 75, 18.75, 1.50}},
	{"claude-opus-4-", modelPricing{15, 75, 18.75, 1.50}},
	// Sonnet 4.6 / 4.5 / 4
	{"claude-sonnet-4", modelPricing{3, 15, 3.75, 0.30}},
	// Haiku 4.5
	{"claude-haiku-4-5", modelPricing{1, 5, 1.25, 0.10}},
	// Haiku 3.5
	{"claude-haiku-3-5", modelPricing{0.80, 4, 1.0, 0.08}},
	// Haiku 3
	{"claude-haiku-3", modelPricing{0.25, 1.25, 0.30, 0.03}},

	// ── OpenAI ──

	// GPT-5.4 family
	{"gpt-5.4-nano", modelPricing{0.20, 1.25, 0.20, 0.02}},
	{"gpt-5.4-mini", modelPricing{0.75, 4.50, 0.75, 0.075}},
	{"gpt-5.4", modelPricing{2.50, 15, 2.50, 0.25}},
	// GPT-4.1 family
	{"gpt-4.1-nano", modelPricing{0.05, 0.20, 0.05, 0.025}},
	{"gpt-4.1-mini", modelPricing{0.20, 0.80, 0.20, 0.10}},
	{"gpt-4.1", modelPricing{2, 8, 2, 0.50}},
	// GPT-4o family
	{"gpt-4o-mini", modelPricing{0.15, 0.60, 0.15, 0.075}},
	{"gpt-4o", modelPricing{2.50, 10, 2.50, 1.25}},
	// o-series reasoning
	{"o4-mini", modelPricing{0.55, 2.20, 0.55, 0.275}},
	{"o3-mini", modelPricing{0.55, 2.20, 0.55, 0.55}},
	{"o3", modelPricing{2, 8, 2, 0.50}},
	{"o1-mini", modelPricing{0.55, 2.20, 0.55, 0.55}},
	{"o1", modelPricing{15, 60, 15, 7.50}},
}

// warnedModels tracks models we've already warned about to avoid spam.
var warnedModels = make(map[string]bool)

// lookupPricing returns the pricing for a model, or nil if unknown.
func lookupPricing(model string) *modelPricing {
	lower := strings.ToLower(model)
	for _, entry := range knownPricing {
		if strings.Contains(lower, entry.substring) {
			p := entry.pricing
			return &p
		}
	}
	return nil
}

// estimateCost calculates the cost in USD from token counts and model pricing.
// Returns 0 and prints a warning if the model is not in the pricing table.
func estimateCost(usage CallUsage) float64 {
	p := lookupPricing(usage.Model)
	if p == nil {
		if usage.Model != "" && !warnedModels[usage.Model] {
			warnedModels[usage.Model] = true
			fmt.Fprintf(os.Stderr, "Warning: unknown model %q — cost estimate unavailable (pricing table may be outdated)\n", usage.Model)
		}
		return 0
	}

	cost := 0.0
	cost += float64(usage.InputTokens) / 1_000_000 * p.InputPerMTok
	cost += float64(usage.OutputTokens) / 1_000_000 * p.OutputPerMTok
	cost += float64(usage.CacheCreateTokens) / 1_000_000 * p.CacheWritePerMTok
	cost += float64(usage.CacheReadTokens) / 1_000_000 * p.CacheReadPerMTok

	return cost
}
