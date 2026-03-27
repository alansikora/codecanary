# <img width="75" alt="codecanary" src="https://github.com/user-attachments/assets/bb494aa1-9bb2-486c-a253-ba8a9a2939e4" /> CodeCanary

AI-powered code review for GitHub pull requests. Catch bugs, security issues, and quality problems before they land in main.

## Why CodeCanary?

Most AI code review tools are one-shot: paste a PR, get feedback, repeat from scratch next time. CodeCanary is different — it's a stateful, automated reviewer that lives in your CI pipeline.

- **Fully automated** — runs as a GitHub Action on every push. No one needs to open a tool or paste a link.
- **Native PR integration** — posts inline comments on exact diff lines, auto-resolves threads when code is fixed, and minimizes stale reviews to keep PRs clean.
- **Incremental reviews** — on re-push, Go-driven triage classifies existing threads at zero Claude cost. Only threads where code actually changed get re-evaluated.
- **Thread lifecycle** — understands code fixes, author dismissals, acknowledgments, and rebuttals as distinct resolution types. Each finding is tracked independently.
- **Anti-hallucination** — explicit file allowlists, line number validation against the diff, and distance thresholds prevent fabricated findings.
- **Cost-efficient** — uses faster models for triage, full models for review. Tracks usage per invocation so you can see exactly what you're spending.
- **Configuration-as-code** — project-specific rules, severity levels, ignore patterns, and context in a single `.codecanary.yml` file.

## Quick Setup

Run this in your repo:

```sh
curl -fsSL https://codecanary.sh/setup | sh
```

This walks you through:
1. Installing the CodeCanary Review GitHub App
2. Authenticating with Claude (OAuth or API key)
3. Creating the GitHub Actions workflow
4. Generating a `.codecanary.yml` config tailored to your project
5. Opening a PR with everything ready to merge

## Canary

Want the canary version of CodeCanary? Living dangerously has never been this meta.

```sh
curl -fsSL https://codecanary.sh/setup | sh -s -- --canary
```

This installs the latest prerelease and pins your workflow to `@canary` instead of `@v1`.

## Config

CodeCanary uses a `.codecanary.yml` file at your repo root:

```yaml
version: 1

context: |
  Go REST API using chi router. Tests use testify.
  All exported functions must have doc comments.

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

review_model: sonnet   # Model for main review (default: sonnet)
triage_model: haiku    # Model for thread re-evaluation (default: haiku)

evaluation:
  code_change:
    context: |
      Fixes must use errors.Wrap, not bare returns.
  reply:
    context: |
      Treat WONTFIX as acknowledgment.
```

### Models

You can configure which Claude models are used for reviews and thread triage:

| Field | Description | Default | Allowed values |
|-------|-------------|---------|----------------|
| `review_model` | Model for the main code review | `sonnet` | `haiku`, `sonnet`, `opus` |
| `triage_model` | Model for re-evaluating existing threads on re-push | `haiku` | `haiku`, `sonnet`, `opus` |

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
4. Calls Claude to analyze the changes
5. Posts findings as inline PR review comments

### Incremental Reviews

On subsequent pushes, CodeCanary is smarter:

1. **Go-driven triage** classifies existing threads — no Claude calls for unchanged code
2. **Parallel evaluation** re-checks threads where code changed or the author replied (using the `triage_model`, default: Haiku)
3. **New code review** only covers the incremental diff, excluding known issues
4. **Auto-resolution** marks threads as resolved when the code fix addresses the finding

### Thread Lifecycle

- **Code fix detected** — thread is automatically resolved
- **Author dismisses** — acknowledged, kept open for re-check on future pushes
- **Author acknowledges** — noted, kept open
- **Author rebuts** — evaluated for technical merit, kept open
- **No changes** — skipped entirely (zero Claude cost)

### Safety

- **Anti-hallucination**: explicit file allowlist, line number validation against diff
- **Anti-ping-pong**: resolved findings injected as context to prevent re-raising
- **Prompt injection protection**: repository content escaped before inclusion in prompts

## License

MIT
