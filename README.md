# <img width="75" alt="codecanary" src="https://github.com/user-attachments/assets/bb494aa1-9bb2-486c-a253-ba8a9a2939e4" /> CodeCanary

AI-powered code review for GitHub pull requests. Catch bugs, security issues, and quality problems before they land in main.

## Why CodeCanary?

Most AI code review tools are one-shot: paste a PR, get feedback, repeat from scratch next time. CodeCanary is different — it's a stateful, automated reviewer that lives in your CI pipeline.

- **Fully automated** — runs as a GitHub Action on every push. No one needs to open a tool or paste a link.
- **Multi-provider** — bring your own LLM: Anthropic, OpenAI, OpenRouter, or Claude CLI. No vendor lock-in.
- **Native PR integration** — posts inline comments on exact diff lines, auto-resolves threads when code is fixed, and minimizes stale reviews to keep PRs clean.
- **Incremental reviews** — on re-push, Go-driven triage classifies existing threads at zero LLM cost. Only threads where code actually changed get re-evaluated.
- **Thread lifecycle** — understands code fixes, author dismissals, acknowledgments, and rebuttals as distinct resolution types. Each finding is tracked independently.
- **Anti-hallucination** — explicit file allowlists, line number validation against the diff, and distance thresholds prevent fabricated findings.
- **Cost-efficient** — uses faster models for triage, full models for review. Tracks usage per invocation so you can see exactly what you're spending.
- **Conversational** — when authors reply to a finding, CodeCanary reads the reply and re-evaluates in context. It reasons over code changes, dismissals, and rebuttals separately — not just "resolved or not."
- **Transparent** — every resolution is visible in the PR thread: why a finding was resolved, what the author said, and how CodeCanary interpreted it. No black-box decisions.
- **Configuration-as-code** — project-specific rules, severity levels, ignore patterns, and context in `.codecanary/config.yml`.

## Getting Started

### Step 1: Install

```sh
curl -fsSL https://codecanary.sh/install | sh
```

This downloads the `codecanary` binary and installs it to `/usr/local/bin` (or `~/.local/bin`).

### Step 2: Choose your setup

CodeCanary works in two modes. Pick one (or both):

#### Local reviews — review changes on your machine

```sh
codecanary setup local
```

This walks you through:
1. Choosing an AI provider (Anthropic, OpenAI, OpenRouter, or Claude CLI)
2. Entering and validating your API key (stored securely in your system keychain)
3. Creating a `.codecanary/config.yml` with your provider and model

Then review your changes:

```sh
codecanary review          # diffs against main and reviews locally
codecanary review --post   # same, but also posts findings to the PR on GitHub
```

If your branch has an open PR, CodeCanary auto-detects it. If not, it diffs against the default branch. Local reviews track state in `.codecanary/.state/` for incremental reviews on subsequent runs.

#### GitHub Actions — automated PR reviews on every push

```sh
codecanary setup github
```

This does everything from local setup, plus:
4. Installing the CodeCanary Review GitHub App
5. Setting your API key as a GitHub repo secret
6. Creating the GitHub Actions workflow (`.github/workflows/codecanary.yml`)
7. Opening a PR with everything ready to merge

Once merged, CodeCanary automatically reviews every PR on open and update.

### Canary

Want the canary version? Living dangerously has never been this meta.

```sh
curl -fsSL https://codecanary.sh/install | sh -s -- --canary
codecanary setup github --canary
```

## Credential Management

```sh
codecanary auth status    # show which API keys are stored
codecanary auth delete    # remove a stored API key
```

API keys are stored in your system keychain. Environment variables always override stored credentials — useful for CI or testing with a different key.

### Provider credentials

| Provider | What you need | Where to get it |
|----------|--------------|-----------------|
| Anthropic | API key | [console.anthropic.com](https://console.anthropic.com) |
| OpenAI | API key | [platform.openai.com](https://platform.openai.com) |
| OpenRouter | API key | [openrouter.ai](https://openrouter.ai) |
| Claude CLI | Logged-in `claude` binary | Run `claude` and complete the login flow |

## Config

CodeCanary uses a `.codecanary/config.yml` file in your repo. The `provider` field is required.

### Anthropic (native API with prompt caching)

```yaml
version: 1
provider: anthropic
# api_key_env: ANTHROPIC_API_KEY  # default

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

### OpenAI

```yaml
version: 1
provider: openai
# api_key_env: OPENAI_API_KEY  # default
# api_base: https://api.openai.com/v1  # default; override for Azure, Ollama, etc.
# review_model: gpt-5.4  # default
# triage_model: gpt-5.4-mini  # default
```

### OpenRouter

```yaml
version: 1
provider: openrouter
# api_key_env: OPENROUTER_API_KEY  # default
# review_model: anthropic/claude-sonnet-4-6  # default
# triage_model: anthropic/claude-haiku-4-5-20251001  # default
```

### Claude CLI

```yaml
version: 1
provider: claude
# review_model: claude-sonnet-4-6  # default
# triage_model: claude-haiku-4-5-20251001  # default
```

Uses your Claude CLI's authentication — make sure you're logged in by running `claude`.

### Full config reference

```yaml
version: 1
provider: anthropic             # required: anthropic, openai, openrouter, or claude

review_model: claude-sonnet-4-6 # model for main review (provider-specific default)
triage_model: claude-haiku-4-5-20251001  # model for thread re-evaluation

api_key_env: ANTHROPIC_API_KEY  # env var holding the API key (default per provider)
api_base: https://...           # override base URL (openai provider only)

max_budget_usd: 0.50            # per-review spending limit in USD
timeout_minutes: 5              # per-invocation timeout
max_file_size: 102400           # per-file content limit in bytes (default 100KB)
max_total_size: 512000          # total file content limit in bytes (default 500KB)

context: |
  Describe your project stack and conventions here.

rules:
  - id: example-rule
    description: "Describe what to check for"
    severity: warning
    paths: ["**/*.go"]
    exclude_paths: ["*_test.go"]

ignore:
  - "dist/**"
  - "*.lock"

evaluation:
  code_change:
    context: |
      Extra context for evaluating whether code changes fix a finding.
  reply:
    context: |
      Extra context for evaluating author replies.
```

### Models

Each provider has sensible defaults. Override with `review_model` and `triage_model`:

| Provider | Review default | Triage default |
|----------|---------------|----------------|
| `anthropic` | `claude-sonnet-4-6` | `claude-haiku-4-5-20251001` |
| `openai` | `gpt-5.4` | `gpt-5.4-mini` |
| `openrouter` | `anthropic/claude-sonnet-4-6` | `anthropic/claude-haiku-4-5-20251001` |
| `claude` | `claude-sonnet-4-6` | `claude-haiku-4-5-20251001` |

### Budget Enforcement

`max_budget_usd` caps total spending per review run. Set to `0` (default) for unlimited.

| Provider | How it works |
|----------|-------------|
| `claude` | Passed as `--max-budget-usd` to the CLI (enforced mid-stream). The runner also checks between calls as an additional safeguard. |
| `anthropic`, `openai`, `openrouter` | Enforced by the runner between LLM calls. After the triage phase completes, spending is checked before starting the review call. During parallel triage, each new evaluation is skipped once the budget is exceeded (already-running evaluations finish). |

Because checks happen _between_ calls, a single call can push spending over the limit — the cap is enforced before the _next_ call starts.

### Severity Levels

| Level | Use for |
|-------|---------|
| `critical` | Security vulnerabilities, data loss, crashes |
| `bug` | Logic errors, incorrect behavior |
| `warning` | Potential issues, performance problems, code smells |
| `suggestion` | Better patterns, readability improvements |
| `nitpick` | Minor style, naming, formatting |

## How It Works

### First Review

1. Fetches PR metadata and diff via `gh` CLI
2. Reads full file contents for context (respecting ignore patterns and size limits)
3. Builds a review prompt with your rules, context, and anti-hallucination guards
4. Calls your configured LLM provider to analyze the changes
5. Posts findings as inline PR review comments

### Incremental Reviews

On subsequent pushes, CodeCanary is smarter:

1. **Go-driven triage** classifies existing threads — no LLM calls for unchanged code
2. **Parallel evaluation** re-checks threads where code changed or the author replied (using the triage model)
3. **New code review** only covers the incremental diff, excluding known issues
4. **Auto-resolution** marks threads as resolved when the code fix addresses the finding

### Thread Lifecycle

- **Code fix detected** — thread is automatically resolved
- **Author dismisses** — acknowledged, kept open for re-check on future pushes
- **Author acknowledges** — noted, kept open
- **Author rebuts** — evaluated for technical merit, kept open
- **No changes** — skipped entirely (zero LLM cost)

### Draft PRs

Draft PRs are skipped by default — the workflow won't run until the PR is marked as ready for review. When you convert a draft to ready, CodeCanary triggers automatically.

To review draft PRs, remove the `github.event.pull_request.draft == false` condition from the workflow `if` in `.github/workflows/codecanary.yml`.

### Safety

- **Anti-hallucination**: explicit file allowlist, line number validation against diff
- **Anti-ping-pong**: resolved findings injected as context to prevent re-raising
- **Prompt injection protection**: repository content escaped before inclusion in prompts

## License

MIT
