# Contributing to CodeCanary

## Build

```sh
go build ./cmd/review    # builds codecanary
```

Version is set via ldflags: `-X main.version=v{version}`

## Lint

```sh
golangci-lint run ./...
```

All code must pass `golangci-lint` with default linters. Run before committing.

## Test

```sh
go test ./... && go vet ./...
```

Tests exist for config, findings, formatting, and triage. Be careful with refactors.

## Architecture

### Core principle: adapters keep the engine agnostic

The review engine (`runner.go`) depends only on two interfaces — `ModelProvider` and `ReviewPlatform`. All environment and provider specifics live behind adapters. There is a single `Run()` function — not separate paths for GitHub vs. local.

### Provider layer — `ModelProvider` interface

Abstracts LLM invocations. The core engine calls `provider.Run(ctx, prompt, opts)` and gets back text + usage metadata.

**Implementations**: `anthropic`, `openai`, `openrouter`, `claude` (CLI wrapper).

**Selection**: factory registry in `provider.go` — `NewProviderForRole(mc, env)` returns the right implementation based on `mc.Provider`.

### Platform layer — `ReviewPlatform` interface

Abstracts environment-specific operations: loading previous findings, publishing results, saving state, resolving threads, reporting usage.

**Implementations**: `GithubPlatform` (posts to PRs, reads threads via API), `LocalPlatform` (prints to terminal, persists state to `~/.codecanary/state/`).

## Adding a new LLM provider

Create `internal/review/provider_<name>.go`. Your file does two things: implements `ModelProvider` and registers the factory via `init()`.

### 1. Register a `ProviderFactory`

```go
func init() {
    providers["myprovider"] = ProviderFactory{
        New:      newMyProvider,
        Validate: validateMyProvider,
        Pricing: []PricingEntry{
            // More specific substrings first — first match wins.
            {"my-model-large", modelPricing{InputPerMTok: 3, OutputPerMTok: 15, CacheWritePerMTok: 3.75, CacheReadPerMTok: 0.30}},
            {"my-model-small", modelPricing{InputPerMTok: 1, OutputPerMTok: 5, CacheWritePerMTok: 1.25, CacheReadPerMTok: 0.10}},
        },
        SuggestedReviewModel: "my-model-large",
        SuggestedTriageModel: "my-model-small",
    }
}
```

- `Pricing` entries use substring matching against model names. If your provider reports cost directly (like the Claude CLI), omit pricing entries.
- `Validate` runs during config validation. Use it to enforce constraints (e.g., `api_base` format, allowed model names).

### 2. Implement `ModelProvider`

```go
type ModelProvider interface {
    Run(ctx context.Context, prompt string, opts RunOpts) (*providerResult, error)
}
```

`Run` receives the full prompt and returns a `providerResult` with the response text and a `CallUsage` struct (input/output/cache tokens, cost, duration). See `provider_openai.go` for a minimal example.

If your provider uses an OpenAI-compatible chat completions API, reuse the shared `doChat` helper and types from `provider_compat.go`.

### 3. Run tests

```sh
go test ./... && go vet ./...
```

## Adding a new platform

Implement the `ReviewPlatform` interface and wire it in the CLI. See `platform_github.go` and `platform_local.go` for reference.

## Key dependencies

- `spf13/cobra` — CLI framework
- `charmbracelet/huh` — terminal form builder (setup wizard)
- `zalando/go-keyring` — OS keychain (with file-based fallback)
- `bmatcuk/doublestar` — glob pattern matching for ignore rules
- `gopkg.in/yaml.v3` — config parsing
