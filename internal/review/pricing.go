package review

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// modelPricing holds per-million-token prices for a model.
type modelPricing struct {
	InputPerMTok      float64 // base input price per million tokens
	OutputPerMTok     float64 // output price per million tokens
	CacheWritePerMTok float64 // cache write price per million tokens (0 = same as input)
	CacheReadPerMTok  float64 // cache hit/read price per million tokens (0 = same as input)
}

// warnedModels tracks models we've already warned about to avoid spam.
var (
	warnedMu     sync.Mutex
	warnedModels = make(map[string]bool)
)

// lookupPricing returns the pricing for a model, or nil if unknown.
// It searches each provider's Pricing entries (order within a provider's
// slice is preserved; cross-provider order is nondeterministic but safe
// because model name substrings do not overlap between providers).
func lookupPricing(model string) *modelPricing {
	lower := strings.ToLower(model)
	for _, pf := range providers {
		for _, entry := range pf.Pricing {
			if strings.Contains(lower, entry.Substring) {
				p := entry.Pricing
				return &p
			}
		}
	}
	return nil
}

// estimateCost calculates the cost in USD from token counts and model pricing.
// Returns 0 and prints a warning if the model is not in the pricing table.
func estimateCost(usage CallUsage) float64 {
	p := lookupPricing(usage.Model)
	if p == nil {
		if usage.Model != "" {
			warnedMu.Lock()
			alreadyWarned := warnedModels[usage.Model]
			if !alreadyWarned {
				warnedModels[usage.Model] = true
			}
			warnedMu.Unlock()
			if !alreadyWarned {
				fmt.Fprintf(os.Stderr, "Warning: unknown model %q — cost estimate unavailable (pricing table may be outdated)\n", usage.Model)
			}
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
