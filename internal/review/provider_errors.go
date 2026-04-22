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

	// The provider is registered but we have no hint for this specific Kind
	// (e.g. a 400/422 against anthropic). Return the header + upstream message
	// alone; the "no formatter" banner is reserved for providers that aren't
	// in the registry at all.
	if _, providerRegistered := providerHints[e.Provider]; providerRegistered {
		return b.String()
	}

	// Unregistered provider — surface the banner and preserve the raw body so
	// the user still has something to debug with. Truncate defensively: some
	// upstreams return HTML error pages that would otherwise swamp terminal
	// output and logs.
	fmt.Fprintf(
		&b,
		"\n\nNo formatted error handler for %q provider — showing raw upstream response.",
		e.Provider,
	)
	if e.RawBody != "" {
		body := e.RawBody
		if len(body) > maxRawBodyDisplay {
			body = body[:maxRawBodyDisplay] + "... (truncated)"
		}
		b.WriteString("\n\n")
		b.WriteString(body)
	}
	return b.String()
}

// maxRawBodyDisplay caps how many bytes of the raw upstream body we echo
// inside Error(). Chosen as a balance between debuggability and not flooding
// terminals or log aggregators with HTML error pages from proxies.
const maxRawBodyDisplay = 2048

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
