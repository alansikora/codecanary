package review

import (
	"fmt"
	"strings"
)

// ErrorKind classifies a provider failure at the protocol level. Hints are
// derived from Kind + Provider so that providers without a registered hint
// still get status-code-based classification.
type ErrorKind int

const (
	ErrorKindUnknown ErrorKind = iota
	ErrorKindRateLimit
	ErrorKindAuth
	ErrorKindServer
)

func (k ErrorKind) String() string {
	switch k {
	case ErrorKindRateLimit:
		return "rate limit"
	case ErrorKindAuth:
		return "authentication"
	case ErrorKindServer:
		return "server error"
	default:
		return "error"
	}
}

// ProviderError is the typed error returned by provider adapters when the
// upstream service reports a failure. It carries enough context for the
// formatter to render a friendly message with provider-specific hints, and
// falls back to a raw-body dump for providers that have not been taught to
// populate Message.
type ProviderError struct {
	Provider string    // e.g. "anthropic", "claude", "openai"
	Status   int       // HTTP status or api_error_status from the upstream
	Kind     ErrorKind // protocol-level classification
	Message  string    // upstream-reported message, trimmed
	RawBody  string    // original response body, retained for diagnostics
}

func (e *ProviderError) Error() string {
	var b strings.Builder

	header := fmt.Sprintf("%s %s", e.Provider, e.Kind)
	if e.Status != 0 {
		header = fmt.Sprintf("%s (%d)", header, e.Status)
	}
	b.WriteString(header)
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}

	hint := lookupProviderHint(e.Provider, e.Kind)
	if hint != "" {
		b.WriteString("\n\nHint: ")
		b.WriteString(hint)
		return b.String()
	}

	// No provider-specific hint registered — tell the user we're falling back
	// to the raw upstream response so they can still debug.
	b.WriteString(fmt.Sprintf(
		"\n\nNo formatted error handler for %q provider — showing raw upstream response.",
		e.Provider,
	))
	if e.RawBody != "" {
		b.WriteString("\n\n")
		b.WriteString(e.RawBody)
	}
	return b.String()
}

// providerHints maps (provider, kind) to a human-readable next-step message.
// Providers that opt in register entries here; the formatter falls through to
// the generic "no formatter" banner when a provider is missing.
var providerHints = map[string]map[ErrorKind]string{
	"anthropic": {
		ErrorKindRateLimit: "Anthropic API rate limit hit. Check your workspace limits at console.anthropic.com, lower --max-budget-usd, or retry after the window resets.",
		ErrorKindAuth:      "Anthropic API rejected the credential. Run `codecanary auth status` and, if needed, `codecanary setup local` to refresh it.",
		ErrorKindServer:    "Anthropic API reported a server error. This is usually transient — retry in a minute.",
	},
	"claude": {
		ErrorKindRateLimit: "Your Claude subscription quota is exhausted (the reset time above is in your plan's timezone). Options: wait for reset, switch `provider:` in .codecanary/config.yml to `anthropic`/`openai`/`openrouter`/`grok`, or swap accounts with `codecanary auth delete` followed by `codecanary setup local`.",
		ErrorKindAuth:      "Claude CLI rejected the OAuth token. Run `codecanary auth delete` then `codecanary setup local` to re-authenticate.",
		ErrorKindServer:    "Claude CLI reported a server error. This is usually transient — retry in a minute.",
	},
}

func lookupProviderHint(provider string, kind ErrorKind) string {
	byKind, ok := providerHints[provider]
	if !ok {
		return ""
	}
	return byKind[kind]
}

// classifyProviderError builds a typed ProviderError from the raw pieces a
// provider adapter has on hand. The classifier uses HTTP status codes — which
// are protocol-level, not provider-specific — so even providers without a
// registered hint get the right Kind.
//
// Callers should pass the upstream message if they can parse one; raw is the
// full response body (or JSON envelope) retained for the fallback path.
func classifyProviderError(provider string, status int, message, raw string) *ProviderError {
	kind := kindFromStatus(status)
	// Some providers (notably the Claude CLI when exiting 0) don't report a
	// numeric status on every error. Heuristically upgrade Unknown to
	// RateLimit when the message itself clearly says so.
	if kind == ErrorKindUnknown && looksLikeRateLimit(message) {
		kind = ErrorKindRateLimit
	}
	return &ProviderError{
		Provider: provider,
		Status:   status,
		Kind:     kind,
		Message:  strings.TrimSpace(message),
		RawBody:  strings.TrimSpace(raw),
	}
}

func kindFromStatus(status int) ErrorKind {
	switch {
	case status == 429:
		return ErrorKindRateLimit
	case status == 401 || status == 403:
		return ErrorKindAuth
	case status >= 500 && status < 600:
		return ErrorKindServer
	default:
		return ErrorKindUnknown
	}
}

func looksLikeRateLimit(message string) bool {
	if message == "" {
		return false
	}
	m := strings.ToLower(message)
	return strings.Contains(m, "rate limit") ||
		strings.Contains(m, "rate_limit") ||
		strings.Contains(m, "hit your limit") ||
		strings.Contains(m, "quota")
}
