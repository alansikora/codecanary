# CodeCanary

AI-powered code review for GitHub pull requests.

## Project structure

```
cmd/
  review/          # Main binary — review CLI + setup wizard
    main.go        # Entry point
    cli/           # Cobra commands
      root.go      # Root "codecanary" command
      review.go    # codecanary review <pr>
      setup.go     # codecanary setup [local|github]
      auth.go      # codecanary auth [status|delete]
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
    provider_compat.go   # Shared types for OpenAI-compatible APIs
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
    docs.go              # Project doc discovery
  credentials/     # Credential storage (keychain with file fallback)
    keyring.go     # Store/Retrieve/Delete — keychain first, ~/.codecanary/credentials.json fallback
  setup/           # Setup wizard logic (huh forms)
    forms.go       # Shared huh form components
    validate.go    # API key validation via test calls
    guidance.go    # Token/permissions guidance text
    workflow.go    # GitHub Actions workflow template
    local.go       # RunLocal() — local setup flow
    github.go      # RunGitHub() — GitHub Actions setup flow
  auth/            # OAuth PKCE flow, GitHub App installation
worker/            # Cloudflare Worker — OIDC token proxy (TypeScript)
install.sh         # Downloads and installs codecanary binary permanently
```

## Binary

- **`codecanary`** — single binary for reviews, setup, and credential management. Installed locally via `install.sh`, also used by the GitHub Action.

## Build

```sh
go build ./cmd/review    # builds codecanary
```

Version is set via ldflags: `-X main.version=v{version}`

## Key dependencies

- `spf13/cobra` — CLI framework
- `charmbracelet/huh` — terminal form builder (setup wizard)
- `zalando/go-keyring` — OS keychain (with file-based fallback for systems without one)
- `bmatcuk/doublestar` — glob pattern matching for ignore rules
- `gopkg.in/yaml.v3` — config parsing
- `golang.org/x/term` — terminal detection

## Architecture

### Core principle: adapters keep the engine agnostic

The review engine (`runner.go`) is provider- and platform-agnostic. It depends only on two interfaces — never on concrete GitHub APIs, LLM SDKs, or environment-specific logic. All environment and provider specifics live behind adapters.

### Provider layer — `ModelProvider` interface (`provider.go`)

Abstracts LLM invocations. The core engine calls `provider.Run(ctx, prompt, opts)` and gets back text + usage metadata. It never knows which LLM backend is being used.

**Implementations**: `anthropic`, `openai`, `openrouter`, `claude` (CLI).
**Selection**: factory registry in `provider.go` — `NewProviderForRole(mc, env)` returns the right implementation based on `mc.Provider`.

Adding a new LLM provider means: create `provider_<name>.go` and register a `ProviderFactory` (constructor, validation, pricing, default models) via `init()`.

### Platform layer — `ReviewPlatform` interface (`platform.go`)

Abstracts environment-specific operations: loading previous findings, publishing results, saving state, resolving threads, reporting usage.

**Implementations**: `GithubPlatform` (posts to PRs, reads threads via API), `LocalPlatform` (prints to terminal, persists state to `.codecanary/state/`).

Adding a new platform (e.g., GitLab) means: implement `ReviewPlatform`, wire it in the CLI.

### Unified review pipeline (`runner.go`)

There is a **single `Run()` function** — not separate paths for GitHub vs. local. The pipeline is:

1. Fetch PR data (or local diff)
2. Load config, project docs, file contents
3. Create providers via `NewProviderForRole()` (factory, provider-agnostic)
4. Load previous findings via `platform.LoadPreviousFindings()`
5. If incremental: triage threads, evaluate via provider, handle resolutions
6. Build and execute main review prompt
7. Parse findings, filter non-actionable
8. `platform.Publish()` → `platform.SaveState()` → `platform.ReportUsage()`

### Other architecture notes

- **Config** is split across two files in `.codecanary/`: `config.yml` (provider, models, budgets, timeouts) and `review.yml` (rules, context, ignore patterns). `review.yml` is optional — if present, its fields override rules/context/ignore in `config.yml`. Legacy `.codecanary.yml` at repo root is still supported with a deprecation warning.
- **Incremental reviews**: on re-push, triage existing threads (Go-driven classifier in `triage.go`), evaluate changed threads via provider (triage model), then review only new code
- **Dual marker detection**: reads both `codecanary:review` and legacy `clanopy:review` HTML markers for backward compatibility
- **Anti-hallucination**: explicit file allowlist, line validation against diff, max finding distance threshold
- **Worker** (`worker/`): OIDC token exchange proxy at `oidc.codecanary.sh` — verifies GitHub Actions OIDC token, returns GitHub App installation token
- **Setup** is a subcommand (`codecanary setup`) using `charmbracelet/huh` forms, with `local` and `github` sub-flows
- **Credentials** are stored via `go-keyring` (OS keychain) with a file-based fallback (`~/.codecanary/credentials.json`, mode `0600`). `resolveEnv()` in `runner.go` injects stored credentials into the filtered env when not already set. Env vars always take priority.

## Rules

- **Keep the core engine agnostic.** `runner.go`, `triage.go`, `prompt.go`, `findings.go` must never import or reference a specific LLM provider or platform. All provider/platform specifics go behind the `ModelProvider` or `ReviewPlatform` interfaces. No `if provider == "openai"` in core logic.
- **Use the adapter/provider pattern for new integrations.** New LLM backends → create `provider_<name>.go` with a `ProviderFactory` registration in `init()`. New deployment targets → implement `ReviewPlatform` + wire in CLI. Never fork the pipeline.
- **One pipeline, not two.** There must be a single `Run()` path. GitHub and local modes differ only in which `ReviewPlatform` implementation is injected — the orchestration logic is shared.
- **Shared types for similar providers.** OpenAI-compatible APIs share request/response types via `provider_compat.go`. Don't duplicate HTTP client logic across providers.
- **Don't repeat yourself.** Before writing new code, search the codebase for existing functions, mappings, or logic that already does what you need — then call it instead of reimplementing it. This applies to everything: switch statements, helper functions, validation logic, data mappings, HTTP calls. One source of truth, callers import it. Don't merge scaffolding or unused exports — if it's not called yet, it doesn't ship yet.
- **File names are ownership boundaries.** A function defined in `local.go` implies it belongs to the local flow; one in `github.go` implies it belongs to GitHub. If a function is called by multiple files in the same package, it belongs in a shared file (e.g., `forms.go` for setup helpers, `platform.go` for platform-shared logic). Never define shared infrastructure in a flow-specific file — move it to the file that matches its actual scope.
- **Canonical provider registration points.** Provider names live in the factory map in `provider.go`. Provider-to-env-var mapping lives in `providerEnvVars` in `internal/credentials/keyring.go`. When adding a new provider, update both of these plus config validation in `config.go` — don't add ad-hoc mappings elsewhere.
- **Minimize shell code.** `install.sh` and the GitHub Action (`alansikora/codecanary-action`) should be kept as thin as possible. All logic must live in Go.
- **Keep the workflow template in sync.** `internal/setup/workflow.go` contains the embedded workflow template. Any change to `.github/workflows/codecanary.yml` (actions, steps, permissions, etc.) must also be applied to that template, and vice versa.
- **Keep the breaking-change manifest in sync.** `.github/workflows/breaking-change-check.yml` contains a manifest of user-facing files. When adding new user-facing surfaces (config fields, CLI flags, public endpoints, etc.), add them to the manifest.
- Tests exist for config, findings, formatting, and triage. Be careful with refactors — run `go test ./...` and `go vet ./...`.
