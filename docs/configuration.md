# Configuration Reference

CodeCanary uses a `.codecanary/config.yml` file in your repository. The setup wizard creates this file for you, but you can edit it directly.

## Config file locations

Config is resolved in this order (first match wins):

1. `--config` flag on the CLI
2. `.codecanary/config.yml` in the repo
3. `~/.codecanary/repos/<owner>/<repo>/config.yml` (local per-repo config)

Legacy: `.codecanary.yml` at repo root is still supported with a deprecation warning.

## Full config reference

```yaml
version: 1
provider: anthropic             # required: anthropic, openai, openrouter, or claude

review_model: claude-sonnet-4-6 # model for main review (provider-specific default)
triage_model: claude-haiku-4-5-20251001  # required: model for thread re-evaluation

api_key_env: ANTHROPIC_API_KEY  # env var holding the API key (default per provider)
api_base: https://...           # override base URL (openai provider only)

max_budget_usd: 0.50            # per-review spending limit in USD (default: 0 = unlimited)
timeout_minutes: 5              # per-invocation timeout
max_file_size: 102400           # per-file content limit in bytes (default 100KB)
max_total_size: 512000          # total file content limit in bytes (default 500KB)

context: |
  Describe your project stack and conventions here.

rules:
  - id: example-rule
    description: "Describe what to check for"
    severity: warning            # critical, bug, warning, suggestion, or nitpick
    paths: ["**/*.go"]           # only apply to matching files
    exclude_paths: ["*_test.go"] # skip matching files

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

## Provider configs

### Anthropic (native API with prompt caching)

```yaml
version: 1
provider: anthropic
review_model: claude-sonnet-4-6
triage_model: claude-haiku-4-5-20251001
# api_key_env: ANTHROPIC_API_KEY  # default
```

### OpenAI

```yaml
version: 1
provider: openai
review_model: gpt-5.4
triage_model: gpt-5.4-mini
# api_key_env: OPENAI_API_KEY  # default
# api_base: https://api.openai.com/v1  # default; override for Azure, Ollama, etc.
```

### OpenRouter

```yaml
version: 1
provider: openrouter
review_model: anthropic/claude-sonnet-4-6
triage_model: anthropic/claude-haiku-4-5-20251001
# api_key_env: OPENROUTER_API_KEY  # default
```

### Claude CLI

```yaml
version: 1
provider: claude
review_model: claude-sonnet-4-6
triage_model: haiku
```

Uses your Claude CLI's authentication — make sure you're logged in by running `claude`.

## Models

`review_model` has a sensible default per provider. `triage_model` is required — there is no default, since providers like OpenRouter can proxy any model.

| Provider | Review default | Triage (example) |
|----------|---------------|------------------|
| `anthropic` | `claude-sonnet-4-6` | `claude-haiku-4-5-20251001` |
| `openai` | `gpt-5.4` | `gpt-5.4-mini` |
| `openrouter` | `anthropic/claude-sonnet-4-6` | `anthropic/claude-haiku-4-5-20251001` |
| `claude` | `claude-sonnet-4-6` | `haiku` |

## Budget enforcement

`max_budget_usd` caps total spending per review run. Set to `0` (default) for unlimited.

| Provider | How it works |
|----------|-------------|
| `claude` | Passed as `--max-budget-usd` to the CLI (enforced mid-stream). The runner also checks between calls as an additional safeguard. |
| `anthropic`, `openai`, `openrouter` | Enforced by the runner between LLM calls. After the triage phase completes, spending is checked before starting the review call. During parallel triage, each new evaluation is skipped once the budget is exceeded (already-running evaluations finish). |

Because checks happen _between_ calls, a single call can push spending over the limit — the cap is enforced before the _next_ call starts.

## Severity levels

| Level | Use for |
|-------|---------|
| `critical` | Security vulnerabilities, data loss, crashes |
| `bug` | Logic errors, incorrect behavior |
| `warning` | Potential issues, performance problems, code smells |
| `suggestion` | Better patterns, readability improvements |
| `nitpick` | Minor style, naming, formatting |

## Split config: review.yml

You can optionally split review-specific settings into `.codecanary/review.yml`. If present, its `rules`, `context`, and `ignore` fields override those in `config.yml`. This lets you keep provider/model config separate from review rules.

```yaml
# .codecanary/review.yml
context: |
  Go REST API using chi router. Tests use testify.

rules:
  - id: error-handling
    description: "Errors must be wrapped with context using fmt.Errorf"
    severity: warning
    paths: ["**/*.go"]

ignore:
  - "dist/**"
  - "*.lock"
```

## Project docs auto-discovery

CodeCanary automatically reads `CLAUDE.md` files from your repo root, `.claude/` directory, and top-level subdirectories. These are injected into the review prompt as additional context. Per-file cap is 4KB, total cap is 12KB.

## Draft PRs

Draft PRs are skipped by default in the GitHub Actions workflow. When you convert a draft to ready, CodeCanary triggers automatically.

To review draft PRs, remove the `github.event.pull_request.draft == false` condition from the workflow `if` in `.github/workflows/codecanary.yml`.
