# CodeCanary

AI-powered code review for GitHub pull requests.

## Project structure

```
cmd/
  review/          # Review binary (used by GitHub Action)
    main.go        # Entry point
    cli/           # Cobra commands
      root.go      # Root "codecanary" command
      review.go    # codecanary review <pr>
      generate.go  # codecanary review generate
  setup/           # Setup binary (used by curl | sh installer)
    main.go        # Interactive setup wizard (single file, no framework)
internal/
  review/
    runner.go            # Core review pipeline — single Run() entry point
    config.go            # Config loading, validation, defaults
    # Provider layer (LLM abstraction)
    provider.go          # ModelProvider interface + factory registry
    provider_anthropic.go
    provider_openai.go
    provider_openrouter.go
    provider_claude.go   # Claude CLI wrapper
    provider_openai_compat.go  # Shared types for OpenAI-compatible APIs
    pricing.go           # Token-based cost estimation
    # Platform layer (environment abstraction)
    platform.go          # ReviewPlatform interface
    platform_github.go   # GitHub Actions implementation
    platform_local.go    # Local CLI implementation
    # Supporting modules
    prompt.go            # Prompt building (review, incremental, per-thread)
    findings.go          # Finding parsing, filtering, result structures
    triage.go            # Thread classification + parallel LLM evaluation
    formatter.go         # JSON/Markdown/Terminal output formatting
    usage.go             # Token tracking, budget checking
    github.go            # GitHub API calls (fetch threads, post reviews)
    local.go             # Local diff & git operations
    state.go             # Local state persistence
    generate.go          # Config generation from repo analysis
    docs.go              # Project doc discovery
  auth/            # OAuth PKCE flow, GitHub App installation
worker/            # Cloudflare Worker — OIDC token proxy (TypeScript)
setup.sh           # Thin shell wrapper — downloads and runs codecanary-setup
```

## Two binaries

- **`codecanary`** — review binary, called by the GitHub Action. Users never install this.
- **`codecanary-setup`** — interactive setup wizard, downloaded temporarily by `setup.sh` and cleaned up after.

## Build

```sh
go build ./cmd/review    # builds codecanary
go build ./cmd/setup     # builds codecanary-setup
```

Version is set via ldflags: `-X main.version=v{version}`

## Key dependencies

- `spf13/cobra` — CLI framework (review binary only)
- `bmatcuk/doublestar` — glob pattern matching for ignore rules
- `gopkg.in/yaml.v3` — config parsing
- `golang.org/x/term` — secure password input (setup binary)

## Architecture

### Core principle: adapters keep the engine agnostic

The review engine (`runner.go`) is provider- and platform-agnostic. It depends only on two interfaces — never on concrete GitHub APIs, LLM SDKs, or environment-specific logic. All environment and provider specifics live behind adapters.

### Provider layer — `ModelProvider` interface (`provider.go`)

Abstracts LLM invocations. The core engine calls `provider.Run(ctx, prompt, opts)` and gets back text + usage metadata. It never knows which LLM backend is being used.

**Implementations**: `anthropic`, `openai`, `openrouter`, `claude` (CLI).
**Selection**: factory registry in `provider.go` — `NewProvider(cfg, env)` returns the right implementation based on `cfg.Provider`.

Adding a new LLM provider means: implement `ModelProvider`, register it in the factory map, add pricing entries.

### Platform layer — `ReviewPlatform` interface (`platform.go`)

Abstracts environment-specific operations: loading previous findings, publishing results, saving state, resolving threads, reporting usage.

**Implementations**: `GithubPlatform` (posts to PRs, reads threads via API), `LocalPlatform` (prints to terminal, persists state to `.codecanary/state/`).

Adding a new platform (e.g., GitLab) means: implement `ReviewPlatform`, wire it in the CLI.

### Unified review pipeline (`runner.go`)

There is a **single `Run()` function** — not separate paths for GitHub vs. local. The pipeline is:

1. Fetch PR data (or local diff)
2. Load config, project docs, file contents
3. Create provider via `NewProvider()` (factory, provider-agnostic)
4. Load previous findings via `platform.LoadPreviousFindings()`
5. If incremental: triage threads, evaluate via provider, handle resolutions
6. Build and execute main review prompt
7. Parse findings, filter non-actionable
8. `platform.Publish()` → `platform.SaveState()` → `platform.ReportUsage()`

### Other architecture notes

- **Config** is `.codecanary/config.yml` (directory structure). Legacy `.codecanary.yml` at repo root is still supported with a deprecation warning.
- **Incremental reviews**: on re-push, triage existing threads (Go-driven classifier in `triage.go`), evaluate changed threads via provider (triage model), then review only new code
- **Dual marker detection**: reads both `codecanary:review` and legacy `clanopy:review` HTML markers for backward compatibility
- **Anti-hallucination**: explicit file allowlist, line validation against diff, max finding distance threshold
- **Worker** (`worker/`): OIDC token exchange proxy at `oidc.codecanary.sh` — verifies GitHub Actions OIDC token, returns GitHub App installation token
- **Setup** is a standalone binary with no CLI framework — just a sequential interactive flow

## Rules

- **Keep the core engine agnostic.** `runner.go`, `triage.go`, `prompt.go`, `findings.go` must never import or reference a specific LLM provider or platform. All provider/platform specifics go behind the `ModelProvider` or `ReviewPlatform` interfaces. No `if provider == "openai"` in core logic.
- **Use the adapter/provider pattern for new integrations.** New LLM backends → implement `ModelProvider` + register in factory. New deployment targets → implement `ReviewPlatform` + wire in CLI. Never fork the pipeline.
- **One pipeline, not two.** There must be a single `Run()` path. GitHub and local modes differ only in which `ReviewPlatform` implementation is injected — the orchestration logic is shared.
- **Shared types for similar providers.** OpenAI-compatible APIs share request/response types via `provider_openai_compat.go`. Don't duplicate HTTP client logic across providers.
- **Minimize shell code.** `setup.sh` and the GitHub Action (`alansikora/codecanary-action`) should be kept as thin as possible. All logic must live in Go.
- **Keep the setup generator in sync.** `cmd/setup/main.go` contains an embedded workflow template. Any change to `.github/workflows/codecanary.yml` (actions, steps, permissions, etc.) must also be applied to that template, and vice versa.
- **Keep the breaking-change manifest in sync.** `.github/workflows/breaking-change-check.yml` contains a manifest of user-facing files. When adding new user-facing surfaces (config fields, CLI flags, public endpoints, etc.), add them to the manifest.
- Tests exist for config, findings, formatting, and triage. Be careful with refactors — run `go test ./...` and `go vet ./...`.
