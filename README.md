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
- **Multi-provider** — bring your own LLM: Anthropic, OpenAI, OpenRouter, Grok (xAI), or Claude CLI. No vendor lock-in.
- **Incremental reviews** — on re-push, Go-driven triage classifies existing threads at zero LLM cost. Only changed code gets re-evaluated.
- **Conversational** — when authors reply to a finding, CodeCanary re-evaluates in context. It distinguishes code fixes, dismissals, acknowledgments, and rebuttals.
- **Native PR integration** — posts inline comments on exact diff lines, auto-resolves threads when code is fixed, and minimizes stale reviews.
- **Anti-hallucination** — explicit file allowlists, line validation against the diff, and distance thresholds prevent fabricated findings.
- **Cost-efficient** — uses a fast triage model for thread re-evaluation and a full model for review. Tracks per-invocation usage so you see what you spend.
- **Configuration-as-code** — project-specific rules, severity levels, ignore patterns, and context in `.codecanary/config.yml`.
- **Agentic loop** — pairs with Claude Code via the bundled `codecanary-fix` skill to review, triage, fix, and push until the PR is clean.

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

Without `--post`, `codecanary review` is always local: it diffs your branch (with uncommitted changes) against the default branch and keeps state in `~/.codecanary/state/<branch>.json` for incremental re-runs. With `--post`, it fetches the PR from GitHub — pass a number or let it auto-detect from the current branch — and posts findings as review comments.

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

**Keeping the workflow current.** The workflow file is yours to customize, so it doesn't update itself. Its `# codecanary-workflow: v<N>` line records which template it came from; when a newer template ships, the bot adds a one-line note to its reviews. Re-run `codecanary setup github` (it asks before overwriting) or copy the changes from [the template](internal/setup/codecanary.yml), keeping the marker line. The same kind of note appears when the workflow pins an old `codecanary_version`.

### Gating merges on clean reviews

CodeCanary can block merges until a review comes back clean. After every review, the bot (and the local `codecanary signoff` command) posts a GitHub commit status under the context `CodeCanary / review`:

- `success` — no unresolved blocking findings (everything is either unraised, fixed by code, handled by the author, or below `warning`)
- `failure` — one or more findings at `warning` or above remain unresolved, with a description like `"3 unresolved blocking findings"`

Suggestions and nitpicks are still posted on the PR but don't fail the status (its description reads like `"2 non-blocking findings open"`), so they never force another push.

To turn this into a required check, add `CodeCanary / review` to your repo's required status checks via whichever branch protection mechanism you use (rulesets, classic branch protection rules, etc.). GitHub accepts any context name; if a review has already run, it will also show up in autocomplete.

Statuses are keyed by commit SHA, so staling is automatic: a new push has no status until the next review run posts one, which re-blocks the merge button.

**Without a workflow.** If your repo doesn't use the GitHub Action, `codecanary signoff` posts the same status from your local machine after a clean `codecanary review`:

```sh
codecanary review     # reviews locally, stores findings in ~/.codecanary/...
# fix anything that came up, commit
codecanary signoff    # posts CodeCanary / review = success on HEAD
```

The command refuses to sign off unless HEAD matches the reviewed SHA and the tree is clean — this prevents attesting a review of code that isn't actually in the commit. It needs `gh` authenticated with `repo:status` scope (default `gh auth login` covers it).

Same required-check config works for both paths: the bot satisfies the check on PRs that run the workflow; `codecanary signoff` satisfies it for repos without the workflow, branches where the workflow doesn't run, or PRs from forks where secrets are unavailable.

## CLI Reference

| Command | Description |
|---------|-------------|
| `codecanary review [pr-number]` | Review a PR or local diff |
| `codecanary findings [pr-number]` | Fetch bot findings for a PR (markdown or JSON) |
| `codecanary reply --url <URL> --body <text>` | Post a reply on a review-comment thread (used by the skill when skipping) |
| `codecanary checks [pr-number]` | Report a PR's required checks other than CodeCanary's own; `--watch` waits until they pass or one fails |
| `codecanary fix <pr-number>` | Apply or refute a PR's open findings and push the fix — run by the workflow's `fix` job when `fix.enabled` is set (see [fixing findings in CI](#fixing-findings-in-ci)) |
| `codecanary signoff` | Post a `CodeCanary / review` commit status from the last local review (see [gating merges](#gating-merges-on-clean-reviews)) |
| `codecanary install-skill` | Install the `codecanary-fix` Claude Code skill |
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
review_model: claude-sonnet-5-5
triage_model: claude-haiku-4-5
```

### Config with rules and context

```yaml
version: 1
provider: anthropic
review_model: claude-sonnet-5-5
triage_model: claude-haiku-4-5

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

### Fixing findings in CI

With the `claude` provider, the workflow can fix what a review finds, with no one at a laptop. After a review leaves findings open, the workflow's `fix` job runs Claude on the PR branch: per finding it applies the minimal edit, or refutes the finding with a reply on its thread when it doesn't hold for this repo. It commits once (authored by the PR author, co-authored by Claude), pushes, and the push gets the next review.

```yaml
review_on: push        # required: a fix push must be reviewed
fix:
  enabled: true        # default false
  model: opus          # Claude CLI model for the fix (default opus)
  max_per_push: 2      # fix commits since your last push (default 2)
  max_per_pr: 5        # fix commits over the PR's life (default 5)
  format_command: [bin/format, --fix]  # optional: run on the fix's files before it commits
```

`format_command` gets the files the fix changed as arguments, so the fix commit passes your formatters. It is your repository's code, run in a job that holds the app token, so it runs only inside a [bubblewrap](https://github.com/containers/bubblewrap) sandbox (Linux runners; the action installs it): no tokens in its environment, the rest of the system read-only, a throwaway `HOME`, network on for toolchain installs. Only its changes to the fix's files are kept. If it fails or the sandbox is unavailable, the fix commits anyway, with a warning in the job log.

At a limit the fix stops and comments with the findings still open; a push of yours resets `max_per_push`. If the fix can't run (rate limit, provider error), it comments once and leaves CI green: fix locally with `/codecanary-fix`. It never runs on fork PRs or drafts, and never edits `.github/`, `.codecanary/` or `.claude/`. Needs workflow template v3 (`codecanary setup github`). See [docs/review-flow.md](docs/review-flow.md#the-ci-fix-cycle).

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

**Grok (xAI)**:
```yaml
version: 1
provider: grok
review_model: grok-4.20-0309-non-reasoning
triage_model: grok-4-1-fast-non-reasoning
```

**Claude CLI** (uses your logged-in `claude` session, no API key needed):
```yaml
version: 1
provider: claude
review_model: claude-sonnet-5-5
triage_model: haiku
```

With the Claude CLI provider you can opt the reviewer into read-only `Read,Grep,Glob` tool use (`claude_review_tools`) so it can check code outside the diff before flagging it; file access is confined to the repository. See [Reviewer tool use](docs/configuration.md#reviewer-tool-use) for the security model before enabling it on repos that take fork PRs.

You can also create a `.codecanary/review.local.yml` for personal overrides (gitignored) — its rules, context, and ignore patterns are appended to the shared `review.yml`.

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
| Grok (xAI) | API key | [console.x.ai](https://console.x.ai) |
| Claude CLI | Logged-in `claude` binary | Run `claude` and complete the login flow |

## How It Works

### First review

1. Fetches PR metadata and diff (via `gh` CLI or local git)
2. Reads file contents for context (respecting ignore patterns and size limits)
3. Auto-discovers project docs (CLAUDE.md files) and path-scoped `.claude/rules/*.md` for additional context
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

CodeCanary ships with a [Claude Code](https://docs.claude.com/en/docs/claude-code) skill, `codecanary-fix`, that drives a review → triage → fix cycle until the PR is clean. It triages each finding against your project's conventions and applies the legitimate ones without asking you to confirm them; the polling, fetching, and CI watching is handled by the CLI.

Install the skill once:

```sh
codecanary install-skill
```

This writes the embedded skill to `~/.claude/skills/codecanary-fix/SKILL.md`, where Claude Code discovers it in every session. `codecanary upgrade` offers to refresh it when the embedded copy changes; otherwise run `codecanary install-skill --force`. The skill also tells you at startup when a newer codecanary release is out or the installed skill is stale (via the `version`, `update_available`, and `skill.stale` fields of `codecanary mode --output json`).

Then in Claude Code, ask it to `handle codecanary` on your PR (or invoke `/codecanary-fix` directly) — the skill is auto-discovered and matched to your request via its frontmatter description. Two modes:

- **PR mode** (default) — watches the GitHub Actions review check via `codecanary findings --watch` and triages without asking you to confirm individual findings. Who applies the fixes depends on `fix_on_ci` (reported by `codecanary mode --output json`, from `fix.enabled` in `.codecanary/config.yml`). With `fix_on_ci`, the workflow's fix job applies the fixes and pushes them, so the skill only waits for the bot's commits (`codecanary findings --watch`, `codecanary checks --watch`), pulls them, and reports clean once the head settles. It takes over locally — triages and applies the open findings itself, in one commit and one push — only when `codecanary findings --output json` reports `fix_stop`, or the `CodeCanary / review` status or a bot comment says the bot stopped. Without `fix_on_ci`, the skill applies the fixes itself, commits, pushes and loops on the next review. Every finding it skips gets a reply posted on its review thread explaining why, via `codecanary reply`. `review_on: ready` is a legacy value: pushes to a ready PR aren't reviewed under it, so the loop ends after one cycle of fixes.
- **Local mode** — triggered automatically when no PR is detected for the current branch. Single pass against your dirty working tree. Applies the fixes it judges legitimate without committing or pushing.

The full skill contract lives at [internal/skills/codecanary-fix/SKILL.md](internal/skills/codecanary-fix/SKILL.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for architecture details and how to add new LLM providers or platforms.

## License

MIT
