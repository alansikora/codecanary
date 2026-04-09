# Review Flow

How CodeCanary reviews a pull request, step by step.

## Overview

The review pipeline has two modes of operation:

- **First review**: Reviews the full PR diff against the base branch.
- **Incremental review**: Re-evaluates previous findings and reviews only new changes since the last review.

Both modes run through the same `Run()` function in `runner.go`. The pipeline is platform-agnostic -- GitHub and local modes differ only in which `ReviewPlatform` adapter is injected.

## Platforms

There are three runtime contexts:

| Context | Platform | How it runs | State storage | Output |
|---------|----------|-------------|---------------|--------|
| **GitHub CI** | `GithubPlatform` (Post=true) | GitHub Actions workflow | PR review threads via API | Posts review comments on the PR |
| **GitHub local-detect** | `GithubPlatform` (Post=false) | `codecanary review <pr>` locally | `~/.codecanary/state/<branch>.json` | Prints to terminal |
| **Local** | `LocalPlatform` | `codecanary review` (no PR number) | `~/.codecanary/state/<branch>.json` | Prints to terminal |

## Pipeline Steps

### 1. Fetch PR data

**GitHub mode**: Fetches PR metadata (title, body, author, branches) and diff via `gh pr view` and `gh pr diff`.

**Local mode**: Detects the default branch (`main`, falling back to `master`) and computes diff from merge-base to HEAD via `git diff $(git merge-base HEAD <default-branch>)..HEAD`. Uses current branch name as the title and `git config user.name` as the author.

If the PR is a setup PR (only adds workflow files with no real code changes), the review is skipped with an informational comment.

### 2. Prepare review context

`prepareReview()` loads everything the review needs:

- **Config**: Reads `config.yml` (provider, models, budgets, timeouts). If a `review.yml` exists alongside it, its rules/context/ignore fields override the config. If a `review.local.yml` also exists, its fields are appended (not replaced) on top of `review.yml`.
- **Project docs**: Discovers CLAUDE.md files (root, `.claude/`, top-level directories). Up to 5 files, 4KB each, 12KB total.
- **File contents**: Reads changed files from disk with size limits (default 100KB per file, 500KB total). Skips binary files, ignored patterns, and files exceeding limits.
- **Environment**: Builds a filtered env for LLM subprocesses (only allowed prefixes like `CODECANARY_`, `GITHUB_`, plus essential vars like `PATH`). Injects keychain credentials if not already set.

### 3. Create providers

Two `ModelProvider` instances are created from config:

- **Review provider**: The main model that reviews code (configured via `review_model` in config).
- **Triage provider**: A cheaper model for re-evaluating previous findings (configured via `triage_model` in config).

Each provider is constructed via the factory registry in `provider.go`. The provider name determines which adapter handles the API call (Anthropic, OpenAI, OpenRouter, or Claude CLI).

### 4. Load previous findings

The platform adapter loads unresolved findings from the last review:

**GitHub CI**: Fetches review threads via GraphQL. Filters to CodeCanary findings only (detected by HTML marker comments). Extracts the previous review's HEAD SHA from the most recent review body. Returns unresolved threads, the SHA, and a count for fix_ref numbering.

**Local modes**: Reads `~/.codecanary/state/<branch>.json`, which stores the SHA, branch name, and findings array from the previous review. Converts saved findings into `ReviewThread` shape for the triage pipeline.

If no previous findings exist, this is a first review.

### 5. Triage and build prompt

This step diverges based on whether previous findings exist.

#### First review path

Calls `BuildPrompt()` to assemble the full review prompt. The prompt includes (in order):

1. System instructions (reviewer role, diff-only rules, side-effect awareness)
2. PR metadata (number, title, author, description)
3. Additional context from config
4. Project documentation (CLAUDE.md files in `<project-doc>` tags)
5. Review rules (from config) or general review instruction
6. Ignore patterns
7. Explicit file allowlist (anti-hallucination)
8. Full contents of changed files with line numbers
9. The unified diff
10. Output format instructions (JSON schema, examples, escaping rules)

After building, `fitPromptForModel()` checks whether the prompt fits the review model's context window (context window minus max output tokens). If it exceeds the budget, it progressively drops the largest file contents first, then truncates the diff as a last resort.

#### Incremental review path (triage)

`runTriage()` handles the incremental case in two phases.

**Phase 1 -- Classify and evaluate previous findings**

First, an incremental diff is computed (`git diff <previousSHA>..HEAD`). Two diffs serve different purposes:

- **Activity diff** (incremental): Determines whether there's new activity to evaluate. If empty, threads with no replies are skipped (no LLM cost).
- **Context diff** (full PR diff): Used for classification and evaluation context. Ensures fixes from earlier pushes are visible even if they predate the incremental window.

`ClassifyThreads()` assigns each unresolved thread one of six classifications:

| Classification | Condition | Evaluation |
|---|---|---|
| `TriageSkip` | No activity diff, not outdated, no replies | Skipped (no LLM) |
| `TriageCodeChanged` | GitHub outdated flag, or file in PR diff | LLM evaluates with file-scoped diff + file snippet |
| `TriageHasReply` | Human replied (no code changes) | LLM evaluates reply intent |
| `TriageCodeChangedReply` | Both code changed and human replied | LLM evaluates both |
| `TriageCrossFileChange` | Changes in other files only | LLM evaluates with full PR diff |
| `TriageFileRemovedFromPR` | File no longer in PR | Auto-resolved by Go code (no LLM) -- thread resolved on GitHub |

Threads classified as `TriageFileRemovedFromPR` are auto-resolved without an LLM call. The Go code sets reason `file_removed` and resolves the thread directly.

For remaining threads, `EvaluateThreadsParallel()` runs up to 3 concurrent LLM calls using the triage model. Evaluation uses a **two-level approach** to balance precision and coverage:

- **Level 1 (file-scoped)**: The LLM receives the finding, the current file content (presented first), and a file-scoped diff. This catches same-file fixes with minimal noise. Most evaluations resolve here.
- **Level 2 (widened scope)**: Only when level 1 says "not resolved" and the thread is `TriageCodeChanged` (file is in the PR diff). The LLM receives the full PR diff with a prompt primed to look for cross-file fixes. This catches the edge case where the finding's file has unrelated changes but the actual fix is in a different file.

For `TriageCrossFileChange`, only the full PR diff is used (no level 1 — there's no file-scoped diff to show).

The LLM returns JSON: `{"resolved": true, "reason": "code_change"}` or `{"resolved": false}`.

LLM resolution reasons and their effects:

| Reason | Effect | Thread stays open? |
|---|---|---|
| `code_change` | Thread resolved on GitHub | No |
| `dismissed` | Ack reply posted | Yes (re-triaged on next push) |
| `acknowledged` | Ack reply posted | Yes |
| `rebutted` | Ack reply posted | Yes |

**Phase 2 -- Build prompt for new findings**

After triage, the pipeline builds an incremental review prompt using `BuildIncrementalPrompt()`. This is similar to `BuildPrompt()` but:

- Uses the incremental diff (or falls back to full PR diff if the incremental diff failed)
- Includes a "Known Issues" section listing unresolved threads (prevents duplicating them)
- Includes a "Recently Resolved Issues" section with findings fixed by code changes (prevents re-raising similar issues -- anti-ping-pong)
- Only includes file contents for files touched in the incremental diff

The prompt is then fitted to the context window, same as the first review path.

### 6. LLM call

If not a dry run and budget permits, the review prompt is sent to the review provider. The provider handles API communication (Anthropic Messages API, OpenAI Chat Completions, OpenRouter, or Claude CLI).

If the response is truncated (hit max output tokens), a warning is logged. The pipeline attempts to salvage complete findings from the truncated JSON by scanning backward for valid objects.

### 7. Process findings

`processFindings()` parses and validates the LLM's output:

1. **Parse JSON**: Extracts the findings array from the ```json fence. Falls back to bracket-matching if embedded code blocks break the regex.
2. **File validation**: Drops findings referencing files not in the PR.
3. **Line validation**: Drops findings whose line number is more than 20 lines from any changed line in the PR diff. This catches hallucinated line numbers and scope creep.
4. **Actionable filter**: Removes findings where `actionable: false`.
5. **Status tagging**: Tags all findings as `"new"` if this is an incremental review.

### 8. Publish results

**GitHub CI**: Posts a review via the REST API with inline comments anchored to diff lines. Findings that can't be mapped to a diff position go in the review body. If all previous findings were resolved, old review comments are minimized (collapsed) for a cleaner PR. If there are no findings at all, a "clean" or "all clear" comment is posted.

**Local modes**: Prints the formatted result to stdout. Format depends on context: terminal (colored, human-readable), markdown, or JSON.

### 9. Save state

**GitHub CI**: No-op. State is stored in the review threads themselves (the embedded JSON marker contains the SHA and findings).

**Local modes**: Writes `~/.codecanary/state/<branch>.json` with the current HEAD SHA, branch name, and combined findings (still-open + new). This enables incremental reviews on the next run.

### 10. Report usage

**GitHub CI**: Writes token counts and cost to `GITHUB_ENV` for downstream workflow steps.

**Local modes**: Prints a usage summary table to stderr (model, tokens, cost, duration) if running in a terminal.

### 11. Telemetry

If telemetry is enabled (opt-in), fires an anonymous event with aggregate stats: provider, platform, finding counts by severity, token counts, cost, and duration. No code content is sent.

## Key Design Decisions

**Single pipeline, two platforms.** `Run()` never branches on "am I on GitHub?" The `ReviewPlatform` interface absorbs all environment differences. Adding a new platform (e.g. GitLab) means implementing the interface, not forking the pipeline.

**Two diffs for triage.** The incremental diff (changes since last review) decides whether to skip evaluation. The full PR diff (all changes) provides context for evaluation. This prevents the "triage horizon" bug where fixes committed before the triage baseline become invisible.

**Two-level triage evaluation.** Same-file evaluations (`TriageCodeChanged`) start with a file-scoped diff (level 1) to reduce noise — the full PR diff can drown out the relevant fix with changes from unrelated files. If level 1 finds no fix, a widened-scope fallback (level 2) sends the full PR diff to catch cross-file fixes. Cross-file evaluations (`TriageCrossFileChange`) go straight to the full diff. The file snippet (current code state) is presented first in all evaluation prompts, so the LLM checks whether the issue still exists before analyzing the diff.

**Per-thread evaluation.** Each unresolved thread gets its own LLM call with tailored context, rather than one bulk prompt. This allows fine-grained classification, parallel execution, and per-thread budget control.

**Anti-ping-pong.** The incremental prompt includes recently resolved findings so the LLM doesn't re-raise similar issues. Non-code resolutions (dismissed, acknowledged, rebutted) keep threads open for re-triage on future pushes, but post ack replies to avoid duplicate acknowledgments.

**Context window fitting.** After building the prompt, the pipeline estimates token count and progressively trims file contents (largest first) then diff to fit the model's context window. This prevents API failures on large PRs.

**Finding validation.** All findings are validated against the PR diff regardless of what diff the LLM prompt contained. Line proximity checks (within 20 lines of a changed line) catch hallucinated line numbers and prevent scope creep from rebase noise.
