---
description: Map of the repository — where each command, pipeline stage, provider and platform lives
---

# Project structure

```
cmd/
  review/          # Main binary — review CLI + setup wizard
    main.go        # Entry point
    cli/           # Cobra commands
      root.go      # Root "codecanary" command
      review.go    # codecanary review <pr>
      findings.go  # codecanary findings <pr> — fetch bot findings for the review skill
      reply.go     # codecanary reply --url <comment-url> --body <text> — post a reply on a review thread
      resolve.go   # codecanary resolve <pr> --threads <ids> — action step: resolve the threads a review settled
      fix.go       # codecanary fix <pr> — CI fix cycle (workflow fix job; fix.enabled)
      checks.go    # codecanary checks <pr> — a PR's required checks other than CodeCanary's own
      mode.go      # codecanary mode — detect which loop the codecanary-fix skill runs (JSON is load-bearing)
      signoff.go   # codecanary signoff — post the commit status from the latest local review
      costs.go     # codecanary costs — usage summary from a review run
      upgrade.go   # codecanary upgrade — self-update, then offer a skill refresh
      install_skill.go # codecanary install-skill — write embedded Claude skill to disk
      setup.go     # codecanary setup [local|github]
      auth.go      # codecanary auth [status|delete]
  evalsnap/        # Dev tool — freeze a PR's review inputs into a corpus fixture
    main.go        # NOT part of the codecanary binary
internal/
  review/
    runner.go            # Core review pipeline — single Run() entry point
    config.go            # Config loading, validation, defaults
    policy.go            # PR policy keys: tiers, intent, context_files, status_context
    rulesource.go        # Rule sources (config, CLAUDE.md, Claude rules, context_files) — read from the base branch in GitHub mode
    github_rules.go      # GitHub mode: copy rule sources out of the base commit's git objects
    rules.go             # .claude/rules/*.md loading and size caps
    # Provider layer (LLM abstraction)
    provider.go          # ModelProvider interface + factory registry
    provider_anthropic.go
    provider_openai.go
    provider_openrouter.go
    provider_grok.go     # xAI Grok (OpenAI-compatible)
    provider_claude.go   # Claude CLI wrapper (project-config isolation, opt-in confined reviewer tools)
    provider_codex.go    # Codex CLI — fix-only (never reviews)
    provider_compat.go   # Shared types for OpenAI-compatible APIs
    provider_errors.go   # ProviderError + ErrorKind classification and hints
    advisor.go           # Anthropic advisor tool (executor consults a stronger model)
    pricing.go           # Token-based cost estimation
    # Platform layer (environment abstraction)
    platform.go          # ReviewPlatform interface
    platform_github.go   # GitHub Actions implementation
    platform_local.go    # Local CLI implementation
    # Supporting modules
    prompt.go            # Prompt building (review, incremental, per-thread)
    findings.go          # Finding parsing, filtering, result structures
    intent.go            # Intent review: the PR body's intent section, escaped and framed as untrusted data
    incremental.go       # IncrementalDiff since the last reviewed SHA (both platforms)
    coverage.go          # File contents fetch + ReviewCoverage (files not seen in full)
    summary.go           # Review status block + commit status from findings
    update_notice.go     # "Your workflow/binary is outdated" note on the bot's review
    triage.go            # Thread classification + parallel LLM evaluation
    formatter.go         # JSON/Markdown/Terminal output formatting
    usage.go             # Token tracking, budget checking
    github.go            # GitHub API calls (fetch threads, post reviews)
    comments.go          # PR review comment fetch + finding marker parser + review-check watcher
    checks.go            # Required-check fetch/wait for `codecanary checks`
    mode.go              # DetectMode() for `codecanary mode`
    review_on.go         # review_on: ready skip logic
    color.go             # ANSI color helpers (TTY-aware)
    local.go             # Local diff & git operations
    repo.go              # Git repo helpers shared across flows (repo root)
    state.go             # Local state persistence
    # CI fix cycle (codecanary fix)
    fix.go               # RunFix() + FixPlatform interface, loop limits
    fix_prompt.go        # Fix prompt + disposition parsing (applied/refuted/skipped)
    fix_git.go           # Fix-commit counting, commit, protected-path revert
    fix_format.go        # fix.format_command on the fix's files, inside a bubblewrap sandbox
    fix_github.go        # GithubPlatform's FixPlatform implementation (push with app token)
    reply.go             # Review-thread replies (shared by `codecanary reply` and the fix)
    resolve.go           # Resolving finding threads after a review (`codecanary resolve`; the review token can't)
    docs.go              # Project doc discovery
    prompt_golden_test.go # Renders corpus fixtures into prompts, diffs vs goldens
    testdata/corpus/     # Frozen review inputs + prompt goldens + SIZES.txt (public PRs only)
  credentials/     # Credential storage (keychain with file fallback)
    keyring.go     # Store/Retrieve/Delete — keychain first, ~/.codecanary/credentials.json fallback
  evalcorpus/      # Frozen review inputs — fixture format + load/save (dev tooling)
    corpus.go      # Fixture type, Dir/Load/Save/List
  skills/          # Claude Code skills embedded in the binary via //go:embed
    skills.go      # Exports CodecanaryFix() returning the skill body
    codecanary-fix/SKILL.md  # Canonical skill source (duplicated at .claude/skills/codecanary-fix/SKILL.md; parity enforced by skills_test.go)
  setup/           # Setup wizard logic (huh forms)
    forms.go       # Shared huh form components
    validate.go    # API key validation via test calls
    guidance.go    # Token/permissions guidance text
    workflow.go    # GitHub Actions workflow template
    local.go       # RunLocal() — local setup flow
    github.go      # RunGitHub() — GitHub Actions setup flow
  auth/            # OAuth PKCE flow, GitHub App installation
  selfupdate/      # Binary self-update (`codecanary upgrade`)
  telemetry/       # Anonymous usage reporting client (ingest side is telemetry/worker/)
telemetry/         # Telemetry domain (anonymous usage analytics)
  worker/          # Cloudflare Worker — telemetry ingestion (TypeScript)
  dashboard/       # Cloudflare Pages — internal analytics dashboard (vanilla JS + Chart.js)
oidc/              # OIDC domain
  worker/          # Cloudflare Worker — OIDC token exchange proxy (TypeScript)
action.yml         # GitHub Action definition (composite action)
install.sh         # Downloads and installs codecanary binary permanently
.claude/
  skills/
    codecanary-fix/ # Claude Code skill — drives review→fix→push loop using `codecanary findings` + `codecanary reply`
```
