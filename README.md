# <img width="75" alt="codecanary" src="https://github.com/user-attachments/assets/bb494aa1-9bb2-486c-a253-ba8a9a2939e4" /> CodeCanary

AI-powered code review for GitHub pull requests. Catch bugs, security issues, and quality problems before they land in main.

## Quick Start

```sh
curl -fsSL https://codecanary.sh/install | sh
codecanary setup local
codecanary review
```

That's it. CodeCanary diffs your branch against main and reviews the changes locally.

## Why CodeCanary?

- **Fully automated** — runs as a GitHub Action on every push, or locally from the terminal.
- **Multi-provider** — bring your own LLM: Anthropic, OpenAI, OpenRouter, or Claude CLI. No vendor lock-in.
- **Incremental reviews** — on re-push, Go-driven triage classifies existing threads at zero LLM cost. Only changed code gets re-evaluated.
- **Conversational** — when authors reply to a finding, CodeCanary re-evaluates in context. It distinguishes code fixes, dismissals, acknowledgments, and rebuttals.
- **Native PR integration** — posts inline comments on exact diff lines, auto-resolves threads when code is fixed, and minimizes stale reviews.
- **Anti-hallucination** — explicit file allowlists, line validation against the diff, and distance thresholds prevent fabricated findings.
- **Cost-efficient** — uses a fast triage model for thread re-evaluation and a full model for review. Tracks per-invocation usage so you see what you spend.
- **Configuration-as-code** — project-specific rules, severity levels, ignore patterns, and context in `.codecanary/config.yml`.
- **Agentic loop** — pairs with Claude Code via the bundled `codecanary-loop` skill to review, triage, fix, and push until the PR is clean.

## Installation

```sh
curl -fsSL https://codecanary.sh/install | sh
```

Installs the `codecanary` binary to `/usr/local/bin` (or `~/.local/bin`). Supports Linux and macOS (amd64/arm64).

To self-update later:

```sh
codecanary upgrade
```

### Canary builds

```sh
curl -fsSL https://codecanary.sh/install | sh -s -- --canary
codecanary upgrade --canary
```

## Setup

### Local reviews

```sh
codecanary setup local
```

The setup wizard walks you through choosing a provider, entering your API key (stored in your system keychain), and selecting models. It creates a `.codecanary/config.yml` in your repo.

Then review your changes:

```sh
codecanary review                # diff against main, print to terminal
codecanary review --post         # same, but also post findings to the PR on GitHub
codecanary review --output json  # machine-readable output
```

If your branch has an open PR, CodeCanary auto-detects it. Otherwise it diffs against the default branch. State is tracked in `~/.codecanary/state/` for incremental reviews on subsequent runs.

### GitHub Actions

```sh
codecanary setup github
```

This runs the same provider and key selection, then:
1. Installs the CodeCanary Review GitHub App
2. Sets your API key as a GitHub repo secret
3. Creates the workflow (`.github/workflows/codecanary.yml`)
4. Opens a PR with everything ready to merge

Once merged, CodeCanary reviews every PR on open and push. Draft PRs are skipped by default.

## CLI Reference

| Command | Description |
|---------|-------------|
| `codecanary review [pr-number]` | Review a PR or local diff |
| `codecanary findings [pr-number]` | Fetch bot findings for a PR (markdown or JSON) |
| `codecanary install-skill` | Install the `codecanary-loop` Claude Code skill |
| `codecanary setup [local\|github]` | Interactive setup wizard |
| `codecanary auth status` | Show stored credential info |
| `codecanary auth delete` | Remove a stored API key |
| `codecanary auth refresh` | Validate and update stored credentials |
| `codecanary upgrade` | Update to the latest release |

### Review flags

| Flag | Description |
|------|-------------|
| `--repo, -r` | GitHub repo (owner/name) |
| `--output, -o` | Output format: `terminal`, `markdown`, or `json` (auto-detects TTY) |
| `--post` | Post findings as a PR review comment |
| `--config, -c` | Path to config file (auto-detected if empty) |
| `--reply-only` | Re-evaluate thread replies only, skip new findings |
| `--dry-run` | Show the prompt without calling the LLM |

## Configuration

CodeCanary uses `.codecanary/config.yml` in your repo. The `provider` field is required.

### Minimal config

```yaml
version: 1
provider: anthropic
review_model: claude-sonnet-4-6
triage_model: claude-haiku-4-5-20251001
```

### Config with rules and context

```yaml
version: 1
provider: anthropic
review_model: claude-sonnet-4-6
triage_model: claude-haiku-4-5-20251001

context: |
  Go REST API using chi router. Tests use testify.

rules:
  - id: error-handling
    description: "Errors must be wrapped with context using fmt.Errorf"
    severity: warning
    paths: ["**/*.go"]

  - id: sql-injection
    description: "Database queries must use parameterized statements"
    severity: critical

ignore:
  - "dist/**"
  - "*.lock"
  - "vendor/**"
```

Rules support `paths` and `exclude_paths` globs, and five severity levels: `critical`, `bug`, `warning`, `suggestion`, `nitpick`.

### Provider examples

**OpenAI** (also works with Azure, Ollama, or any OpenAI-compatible endpoint via `api_base`):
```yaml
version: 1
provider: openai
review_model: gpt-5.4
triage_model: gpt-5.4-mini
# api_base: https://your-endpoint.com/v1
```

**OpenRouter**:
```yaml
version: 1
provider: openrouter
review_model: anthropic/claude-sonnet-4-6
triage_model: anthropic/claude-haiku-4-5-20251001
```

**Claude CLI** (uses your logged-in `claude` session, no API key needed):
```yaml
version: 1
provider: claude
review_model: claude-sonnet-4-6
triage_model: haiku
```

For the full config reference including budget controls, size limits, timeouts, evaluation context, and the `review.yml` override file, see [docs/configuration.md](docs/configuration.md).

## Credential Management

```sh
codecanary auth status    # show which API keys are stored
codecanary auth delete    # remove a stored API key
codecanary auth refresh   # validate and update credentials
```

Keys are stored in your system keychain (macOS Keychain, GNOME Keyring, KDE Wallet) with a fallback to `~/.codecanary/credentials.json`. Environment variables always override stored credentials.

| Provider | What you need | Where to get it |
|----------|--------------|-----------------|
| Anthropic | API key | [console.anthropic.com](https://console.anthropic.com) |
| OpenAI | API key | [platform.openai.com](https://platform.openai.com) |
| OpenRouter | API key | [openrouter.ai](https://openrouter.ai) |
| Claude CLI | Logged-in `claude` binary | Run `claude` and complete the login flow |

## How It Works

### First review

1. Fetches PR metadata and diff (via `gh` CLI or local git)
2. Reads file contents for context (respecting ignore patterns and size limits)
3. Auto-discovers project docs (CLAUDE.md files) for additional context
4. Calls your configured LLM to analyze the changes
5. Posts findings as inline PR review comments (or prints to terminal)

### Incremental reviews (on re-push)

1. **Go-driven triage** classifies existing threads — no LLM calls for unchanged code
2. **Parallel evaluation** re-checks threads where code changed or the author replied (using the triage model)
3. **New code review** covers only the incremental diff, excluding known issues
4. **Auto-resolution** marks threads as resolved when the code fix addresses the finding

### Thread lifecycle

| Event | Result |
|-------|--------|
| Code fix detected | Thread auto-resolved |
| Author dismisses | Acknowledged, kept open for re-check |
| Author acknowledges | Noted, kept open |
| Author rebuts | Evaluated for technical merit, kept open |
| No changes | Skipped (zero LLM cost) |

### Safety

- **Anti-hallucination**: explicit file allowlist, line number validation against diff, max finding distance threshold
- **Anti-ping-pong**: resolved findings injected as context to prevent re-raising
- **Prompt injection protection**: repository content escaped before inclusion in prompts

## Agentic review loop

CodeCanary ships with a [Claude Code](https://docs.claude.com/en/docs/claude-code) skill, `codecanary-loop`, that drives a review → triage → fix → push cycle until the PR is clean. You stay in the loop — every fix is confirmed before it's applied — but the polling, fetching, and CI watching is handled by the CLI.

Install the skill once:

```sh
codecanary install-skill
```

This writes the embedded skill to `~/.claude/skills/codecanary-loop/SKILL.md`, where Claude Code discovers it in every session. Re-run the command after `codecanary upgrade` to pick up new versions.

Then in Claude Code, ask it to handle the codecanary review on your PR — the skill is auto-discovered and matched to your request via its frontmatter description. Two modes:

- **PR mode** (default) — watches the GitHub Actions review check via `codecanary findings --watch`, renders a triage table, asks you to confirm which fixes to apply, commits and pushes, then loops on the next review.
- **Local mode** — ask for a local pass (or mention `--local`). Single pass against your dirty working tree. Applies approved fixes without committing or pushing.

The full skill contract lives at [internal/skills/codecanary-loop/SKILL.md](internal/skills/codecanary-loop/SKILL.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for architecture details and how to add new LLM providers or platforms.

## License

MIT
