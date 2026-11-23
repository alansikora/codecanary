# CodeCanary

AI-powered code review for GitHub pull requests.

The project structure map is in `.claude/rules/project-structure.md`, and test guidance is in `.claude/rules/go-tests.md`. Both are loaded with this file. **Keep this file under 16 KB.** CodeCanary truncates each CLAUDE.md at `maxDocBytes` (16384, `internal/review/docs.go`), so anything past that point never reaches the reviewer. Path-scoped guidance belongs in `.claude/rules/*.md`, which has its own budget (8 KB per file).

## Binary

- **`codecanary`** — single binary for reviews, setup, and credential management. Installed locally via `install.sh`, also used by the GitHub Action.
- **`evalsnap`** (`cmd/evalsnap`) — developer tool, not shipped. `go build ./cmd/review` does not pull it in.

## Prompt evaluation harness

`internal/review/testdata/corpus/` holds frozen review inputs captured from real PRs. `TestPromptGolden` renders each into a prompt and diffs it against a checked-in golden, and `SIZES.txt` records every prompt's rendered size.

This exists because prompt edits are otherwise invisible in code review: their effect surfaces later as a change in the findings, when the cause is hard to attribute. Instruction overhead is paid on every review forever, so growth should be a number in the diff, not a surprise.

It measures prompt *construction*, not review *quality* — no model runs. Judging whether a prompt change helps needs labelled findings, repeated runs to establish variance, and real model calls; that is a separate layer that does not exist yet.

Regenerate goldens with `go test ./internal/review/ -run Golden -update`, and state the size delta in the PR description.

**Fixtures committed to this repo must come from public repositories only.** A fixture embeds the PR diff and the full contents of every file the reviewer read; committing one from a private repo publishes that source permanently. `evalsnap` refuses to write a private repo's fixture into any git-tracked directory. Keep those outside the repo and point `$CODECANARY_EVAL_CORPUS` at them. See `internal/review/testdata/corpus/README.md`.

## Build

```sh
go build ./cmd/review    # builds codecanary
```

Version is set via ldflags: `-X main.version=v{version}`

## Lint

```sh
golangci-lint run ./...
```

All code must pass `golangci-lint` with default linters (errcheck, staticcheck, etc.) plus the `gofmt` formatter (enabled in `.golangci.yml`). Run this before committing. golangci-lint v2 doesn't check formatting by default, so before `.golangci.yml` existed, unformatted files kept landing and needed gofmt-only follow-up PRs.

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

**Implementations**: `anthropic`, `openai`, `openrouter`, `grok`, `claude` (CLI), `codex` (CLI, fix-only).
**Selection**: factory registry in `provider.go` — `NewProviderForRole(mc, env)` returns the right implementation based on `mc.Provider`.

Adding a new LLM provider means: create `provider_<name>.go` and register a `ProviderFactory` (constructor, validation, pricing, default models) via `init()`.

### Platform layer — `ReviewPlatform` interface (`platform.go`)

Abstracts environment-specific operations: loading previous findings, publishing results, saving state, resolving threads, reporting usage.

**Implementations**: `GithubPlatform` (posts to PRs, reads threads via API), `LocalPlatform` (prints to terminal, persists state to `~/.codecanary/repos/<owner>/<repo>/state/<branch>.json` — per-repo scoping keeps branch names like `main` from colliding across repos; falls back to `~/.codecanary/state/<branch>.json` when no git remote is resolvable).

Routing is strict: `codecanary review --post` → `GithubPlatform`; `codecanary review` (no `--post`) → `LocalPlatform`, even when the branch has an open PR. Local is local — the branch diff (with uncommitted changes) is reviewed against the default base, previous findings come from local state, nothing is fetched from or posted to GitHub. The old `GithubPlatform`-with-`Post=false` hybrid is gone; it was the source of the "state written locally, read from GitHub" asymmetry that kept breaking incremental local reviews.

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

- **Config** is split across two files in `.codecanary/`: `config.yml` (provider, models, budgets, timeouts) and `review.yml` (rules, context, ignore patterns). `review.yml` is optional — if present, its fields override rules/context/ignore in `config.yml`. A personal `review.local.yml` can add rules, context, and ignore patterns on top of `review.yml` (append semantics, not replacement). Legacy `.codecanary.yml` at repo root is still supported with a deprecation warning.
- **Incremental reviews**: on re-push, triage existing threads (Go-driven classifier in `triage.go`), evaluate changed threads via provider (triage model), then review only new code
- **Dual marker detection**: reads both `codecanary:review` and legacy `clanopy:review` HTML markers for backward compatibility
- **Anti-hallucination**: explicit file allowlist, line validation against diff, max finding distance threshold
- **OIDC worker** (`oidc/worker/`): OIDC token exchange proxy at `oidc.codecanary.sh` — verifies GitHub Actions OIDC token, returns GitHub App installation token
- **Telemetry worker** (`telemetry/worker/`): anonymous usage ingest at `telemetry.codecanary.sh` — writes to a Cloudflare Analytics Engine dataset
- **Telemetry dashboard** (`telemetry/dashboard/`): internal analytics view at `dashboard.codecanary.sh` — Cloudflare Pages + Pages Functions, gated by Cloudflare Access. Reads the AE dataset via the SQL HTTP API with a read-only API token (no AE binding, so writes are platform-impossible)
- **Setup** is a subcommand (`codecanary setup`) using `charmbracelet/huh` forms, with `local` and `github` sub-flows
- **Credentials** use a single env var `CODECANARY_PROVIDER_SECRET` for all providers. Stored via `go-keyring` (OS keychain) with a file-based fallback (`~/.codecanary/credentials.json`, mode `0600`). `resolveEnv()` in `runner.go` injects the stored credential into the filtered env when not already set.

## Rules

- **Keep the core engine agnostic.** `runner.go`, `triage.go`, `prompt.go`, `findings.go` must never import or reference a specific LLM provider or platform. All provider/platform specifics go behind the `ModelProvider` or `ReviewPlatform` interfaces. No `if provider == "openai"` in core logic.
- **Use the adapter/provider pattern for new integrations.** New LLM backends → create `provider_<name>.go` with a `ProviderFactory` registration in `init()`. New deployment targets → implement `ReviewPlatform` + wire in CLI. Never fork the pipeline.
- **One pipeline, not two.** There must be a single `Run()` path. GitHub and local modes differ only in which `ReviewPlatform` implementation is injected — the orchestration logic is shared.
- **Shared types for similar providers.** OpenAI-compatible APIs share request/response types via `provider_compat.go`. Don't duplicate HTTP client logic across providers.
- **Don't repeat yourself.** Before writing new code, search the codebase for existing functions, mappings, or logic that already does what you need — then call it instead of reimplementing it. This applies to everything: switch statements, helper functions, validation logic, data mappings, HTTP calls. One source of truth, callers import it. Don't merge scaffolding or unused exports — if it's not called yet, it doesn't ship yet.
- **File names are ownership boundaries.** A function defined in `local.go` implies it belongs to the local flow; one in `github.go` implies it belongs to GitHub. If a function is called by multiple files in the same package, it belongs in a shared file (e.g., `forms.go` for setup helpers, `platform.go` for platform-shared logic). Never define shared infrastructure in a flow-specific file — move it to the file that matches its actual scope.
- **Canonical provider registration points.** Provider names live in the factory map in `provider.go`. All providers use `CODECANARY_PROVIDER_SECRET` for credentials (defined in `internal/credentials/keyring.go`). When adding a new provider, register a `ProviderFactory` in `provider.go` and add config validation in `config.go`.
- **Minimize shell code.** `install.sh` and the GitHub Action (`action.yml`) should be kept as thin as possible. All logic must live in Go.
- **Workflow template is embedded.** `internal/setup/codecanary.yml` is the single source of truth for the GitHub Actions workflow, embedded via `//go:embed`. `.github/workflows/codecanary.yml` must be identical — `go test ./internal/setup/` enforces this. When changing the workflow, edit either file and copy to the other, and bump the `# codecanary-workflow: v<N>` marker (plus the fingerprint in `internal/setup/workflow_test.go`) — the review bot compares that marker with consumer repos' copies to tell them their workflow is outdated.
- **Claude skills are embedded.** `internal/skills/codecanary-fix/SKILL.md` is the single source of truth for the codecanary-fix skill, embedded via `//go:embed` and materialized by `codecanary install-skill`. `.claude/skills/codecanary-fix/SKILL.md` must be identical so Claude Code's project-mode discovery finds it when working in this repo — `go test ./internal/skills/` enforces this. When changing the skill, edit either file and copy to the other.
- **Keep `docs/review-flow.md` in sync.** This document describes the full review pipeline — every step, the triage flow, platform differences, and key design decisions. When changing `runner.go`, `triage.go`, `prompt.go`, `findings.go`, `github.go`, `local.go`, `platform.go`, or the `ReviewPlatform` implementations, update the doc to reflect the new behavior.
- **Never swallow a config load error into a default.** If a config file exists but fails to load or validate, warn on stderr with the file name and the error, or fail. Don't quietly return the default `status_context`, `fix_on_ci: false`, or similar. A silent fallback makes the CLI disagree with CI with no explanation (for example `signoff` posting a differently named status than the required check).
- **Wire a new config field into every entry point.** Config is read by `review` (GitHub and local), `fix`, `signoff`, `checks` and `mode`, and not all of them call `LoadRules` or load the same file. In GitHub mode, rule sources come from the base branch (`rulesource.go`). Locally, `LoadReviewConfig("")` can resolve to `~/.codecanary/repos/...`. When adding or renaming a field, check that each of these paths reads it from the same place.
- **Model output and PR-author text are untrusted.** Any text a model wrote after reading the PR (rationales, fix reasons, summaries), and anything from the PR body or a fork author's reply, must go through `escapeAllTags` before it is posted. Otherwise it can forge a `codecanary:ack` marker or other bot markers (see `fixReplyBody`). Truncate on rune boundaries (`truncateUTF8`). Parse permission- or mode-like inputs fail-closed: reject unknown values instead of falling back to the most privileged behavior.
- **Prompts and their validators must agree.** If Go code rejects a value (a resolution reason, a disposition, a severity), the prompt that asks the model for it must not offer that value. Tests that pin the prompt text must pin the restricted set. Prompts shared across providers must not name provider-specific tools.
- **Update every mention, not just the lines you touched.** When a value, kind or step changes in `docs/review-flow.md`, the skill, or a prompt, grep the whole file (and its mirror) for the old value. Sync rules have repeatedly failed on a second paragraph further down the same doc.
- Be careful with refactors: run `go test ./...` and `go vet ./...`. CI runs them on Ubuntu, not macOS (see `.claude/rules/go-tests.md`).
