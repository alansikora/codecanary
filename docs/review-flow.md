# Review Flow

How CodeCanary reviews a pull request, step by step.

## Overview

The review pipeline has two modes of operation:

- **First review**: Reviews the full PR diff against the base branch.
- **Incremental review**: Re-evaluates previous findings and reviews only new changes since the last review.

Both modes run through the same `Run()` function in `runner.go`. The pipeline is platform-agnostic -- GitHub and local modes differ only in which `ReviewPlatform` adapter is injected.

## Platforms

Two platforms, routed strictly by `--post`:

| Context | Platform | How it runs | State storage | Output |
|---------|----------|-------------|---------------|--------|
| **GitHub PR** | `GithubPlatform` | `codecanary review --post` (locally or in CI) | PR review threads via API | Posts review comments on the PR |
| **Local** | `LocalPlatform` | `codecanary review` (with or without a PR for the branch) | `~/.codecanary/repos/<owner>/<repo>/state/<branch>.json` | Prints to terminal |

`codecanary review` without `--post` is always local — even if the branch has an open PR. "Local is local": the branch diff (including uncommitted changes) is reviewed against the default base, and previous findings come from the repo-scoped state file. There is no hybrid mode that reads GitHub but writes local state. Two consecutive local runs go incremental off the saved state (locked in by `TestLocalPlatformIncrementalHandoff` in `state_test.go`).

State files are keyed by `owner/repo/branch` so the same branch name across different repos (e.g. `main` in repo A vs. repo B) no longer collides. When the git remote can't be resolved (detached worktree, no remote), state falls back to the legacy `~/.codecanary/state/<branch>.json` path. On first save after the upgrade, any existing legacy file is read once, migrated to the repo-scoped path, and the legacy copy is removed — transparent to the operator.

## Pipeline Steps

### 1. Fetch PR data

**GitHub PR** (`--post`): Fetches PR metadata (title, body, author, branches) and diff via `gh pr view` and `gh pr diff`.

**Local**: Detects the default branch (`main`, falling back to `master`, or the explicit `--base`) and computes diff from merge-base to HEAD via `git diff $(git merge-base HEAD <default-branch>)..HEAD`. Uncommitted working-tree changes scoped to the branch files are appended to the incremental diff on subsequent runs. Uses current branch name as the title and `git config user.name` as the author.

If the PR is a setup PR (only adds workflow files with no real code changes), the review is skipped with an informational comment.

### 2. Prepare review context

`prepareReview()` loads everything the review needs:

- **Config**: Reads `config.yml` (provider, models, budgets, timeouts). If a `review.yml` exists alongside it, its rules/context/ignore fields override the config. If a `review.local.yml` also exists, its fields are appended (not replaced) on top of `review.yml`.
- **Project docs**: Discovers CLAUDE.md files at the repo root and in every ancestor directory of a changed PR file. Skips `vendor/`, `node_modules/`, hidden dirs, and other build artifacts. Up to 10 files, 16 KB each, 48 KB total. Monorepos commonly keep per-app conventions (e.g. `apps/exchange-api/CLAUDE.md`) — those load automatically when a PR touches files under that directory, so the reviewer sees the conventions specific to the code being changed rather than only the repo-root overview.
- **File contents** (`FetchFileContents`, `scopePRForPrompt` in `coverage.go`): Reads changed files from disk and sorts them into three groups:
  - *Full contents*: read into the prompt, up to `max_file_size` per file (default 100KB) and `max_total_size` in total (default 500KB), in PR file order.
  - *Diff only*: files over `max_file_size`, files that would push the total past `max_total_size`, and paths that go through a symlink leaving the repository or into `.git` (`readRepoFile`; links inside the repository are read normally). Their contents are left out of the prompt, but they stay in the file list and their hunks stay in the diff, so their changes are still reviewed (and their previous threads are triaged normally rather than auto-resolved as "file removed").
  - *Excluded*: files matching an `ignore` pattern, and binary files. These leave the review entirely: they are dropped from the file list and their hunks are removed from the prompt diff (via `ScopeDiffToFiles`).

  Files that can't be read (deleted in the PR) are in none of the groups; their diff is reviewed as usual.
- **Diff size guard** (`capDiff`): if the prompt diff is larger than `max_diff_size` (default 300KB), it is trimmed so a huge change (a schema dump, a generated file) can't push the prompt past the provider's context window. The budget is shared per file, smallest first: each file gets an equal share of what is left, files under their share keep their whole diff, and only the largest files are cut — keeping the file header and the first lines that fit, followed by a `[codecanary: diff truncated ...]` marker line. The result is deterministic. There is no token-based fitting against the model's context window; `max_total_size` + `max_diff_size` bound the two largest prompt sections.
- **Coverage**: the diff-only, truncated, and excluded files are recorded on `ReviewResult.Coverage` and rendered by each platform (see step 8). The untouched PR diff is always kept in `FullDiff` for finding validation.
- **Environment**: Builds a filtered env for LLM subprocesses (only allowed prefixes like `CODECANARY_`, `GITHUB_`, plus essential vars like `PATH`). Injects keychain credentials if not already set.

### 3. Create providers

Two `ModelProvider` instances are created from config:

- **Review provider**: The main model that reviews code (configured via `review_model` in config). When `advisor_model` is set (anthropic or claude provider only), the review provider also enables Anthropic's server-side advisor tool so a stronger advisor model can weigh in mid-generation — the triage provider never uses advisor, since its classifier turns are too short to benefit.
- **Triage provider**: A cheaper model for re-evaluating previous findings (configured via `triage_model` in config).

Each provider is constructed via the factory registry in `provider.go`. The provider name determines which adapter handles the API call (Anthropic, OpenAI, OpenRouter, or Claude CLI).

**Claude CLI specifics** (`provider_claude.go`): every call — review and triage — runs with `--setting-sources user --strict-mcp-config` and `disableAllHooks` in the generated `--settings`, so the checked-out PR's own `.claude/` settings, hooks and `.mcp.json` servers never load; the CLI's environment is the filtered env above minus GitHub tokens and the raw `CODECANARY_PROVIDER_SECRET` (mapped to `CLAUDE_CODE_OAUTH_TOKEN` first). Calls are single-shot (`--tools ""`) unless `claude_review_tools` is set, in which case only the **review** provider gets `--tools Read,Grep,Glob` (or a subset) so it can check claims against code outside the diff. Those calls are confined to the repository root: the CLI runs from `git rev-parse --show-toplevel` with `--restricted`, `--permission-mode dontAsk`, `blockReadsOutsideWorkingDirectories` and `Read` deny rules for credential and system paths. No repo root, or a user `--settings` in `claude_args`, turns tools off. See [configuration.md](configuration.md#reviewer-tool-use) for the security model.

### 4. Load previous findings

The platform adapter loads unresolved findings from the last review:

**GitHub PR** (`--post`): Fetches review threads via GraphQL. Filters to CodeCanary findings only (detected by HTML marker comments). Extracts the previous review's HEAD SHA from the most recent review body — clean and all-clear reviews embed this marker too, so the baseline advances even when a push produced no findings. Returns unresolved threads, the SHA, and a count for fix_ref numbering.

**Local**: Reads `~/.codecanary/repos/<owner>/<repo>/state/<branch>.json`, which stores the SHA, branch name, and findings array from the previous review. Falls back to `~/.codecanary/state/<branch>.json` when the repo slug can't be resolved or a pre-migration state file is present. Converts saved findings into `ReviewThread` shape for the triage pipeline.

If no previous findings exist, this is a first review.

### 5. Triage and build prompt

This step diverges based on whether a previous review SHA exists. A previous SHA alone is enough to enter the incremental path — if previous findings were all resolved (no open threads), the incremental diff still scopes the review to commits since the last baseline, avoiding a redundant full re-review.

#### First review path

Calls `BuildPrompt()` to assemble the full review prompt. The prompt includes (in order):

1. System instructions (reviewer role, diff-only rules, side-effect awareness)
2. PR metadata (number, title, author, description)
3. Additional context from config
4. Project documentation (CLAUDE.md files in `<project-doc>` tags)
5. Review rules (from config) — filtered to rules whose `paths:` / `exclude_paths:` globs match at least one PR file. Rules scoped to file types not in the diff (e.g. CSS rules on a Ruby-only change) are omitted to keep LLM attention focused. Falls back to a general review instruction when no rules apply.
6. Ignore patterns
7. Explicit file allowlist (anti-hallucination)
8. Full contents of changed files with line numbers
9. The unified diff
10. Output format instructions (JSON schema, examples, escaping rules)

The prompt is not fitted to the model's context window after it is built; its size is bounded up front by `max_total_size` (file contents) and `max_diff_size` (diff) in step 2.

#### Incremental review path (triage)

`runTriage()` handles the incremental case in two phases.

**Phase 1 -- Classify and evaluate previous findings**

First, an incremental diff is computed by `platform.GetIncrementalDiff(previousSHA, pr)`, which both platforms implement on top of the shared `IncrementalDiff()` (`incremental.go`):

- **Linear history** (`previousSHA` is an ancestor of HEAD): `git diff <previousSHA>..HEAD`.
- **Rebase / force-push** (`git merge-base --is-ancestor` fails): a file-level interdiff. For each file, the PR patch before (`merge-base(previousSHA, base)..previousSHA`) is compared with the patch after (`merge-base(HEAD, base)..HEAD`), after normalising away hunk headers, context lines and `index` lines so pure line shifts and base-branch edits around the change don't count. Files whose patch changed contribute their current PR diff; unchanged files are dropped. A rebase with no author changes yields an empty diff, which takes the usual "no new changes" path.
- **GitHub only**: CI clones are shallow, so before diffing `fetchIncrementalHistory()` (`github.go`) fetches the previous SHA by hash if it's missing, and on a rebase fetches the base branch into `refs/remotes/origin/<base>` and deepens (100 → 500 → 2000) until both merge-bases resolve. `--depth`/`--deepen` are only passed when the repo is already shallow, so a full local clone is never made shallow. Local mode uses the local base branch and full history as-is.
- Any failure (commit not fetchable, merge-base out of reach) returns an error and the run falls back to the full PR diff, logging the reason.

Two diffs serve different purposes:

- **Activity diff** (incremental): Determines whether there's new activity to evaluate. If empty, threads with no replies are skipped (no LLM cost).
- **Context diff** (full PR diff): Used for classification and evaluation context. Ensures fixes from earlier pushes are visible even if they predate the incremental window.

`ClassifyThreads()` assigns each unresolved thread one of seven classifications:

| Classification | Condition | Evaluation |
|---|---|---|
| `TriagePreviouslyAcked` | Bot already posted a `<!-- codecanary:ack:* -->` reply and no human reply since | Auto-resolved by Go code (no LLM) -- prior reason carried forward |
| `TriageSkip` | No activity diff, not outdated, no replies | Skipped (no LLM) |
| `TriageCodeChanged` | GitHub outdated flag, or file in PR diff | LLM evaluates with file-scoped diff + file snippet |
| `TriageHasReply` | Human replied (no code changes) | LLM evaluates reply intent with a file snippet around the finding |
| `TriageCodeChangedReply` | Both code changed and human replied | LLM evaluates both |
| `TriageCrossFileChange` | Changes in other files only | LLM evaluates with full PR diff |
| `TriageFileRemovedFromPR` | File no longer in PR | Auto-resolved by Go code (no LLM) -- thread resolved on GitHub |

`TriagePreviouslyAcked` is checked first. When the bot already recorded a deferral (`<!-- codecanary:ack:dismissed/rebutted/acknowledged -->`) and the author hasn't added a fresh reply since, `EvaluateThreadsParallel` short-circuits without an LLM call and re-emits the same fixedThread the prior cycle produced. `computeReviewSummary` then routes the thread back to its original Dismissed/Acknowledged/Rebutted bucket and the `CodeCanary / review` commit status stays green across pushes that don't touch the deferred finding. A new human reply after the ack flips the thread back to `TriageHasReply` (or `TriageCodeChangedReply`) so the fresh signal gets re-evaluated.

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
- Includes a "Known Issues (Open)" section with each unresolved thread's title, severity, description, and the author's latest reply (bot ack replies excluded, capped at 600 bytes). With the text, the reviewer can spot a reworded duplicate and treat an answered question as settled, which a bare `path:line` list didn't allow. It is also told to raise a new finding when the incremental diff undermines an open finding's premise, not to re-emit it
- Includes a "Recently Resolved Issues" section with findings fixed by code changes (prevents re-raising similar issues -- anti-ping-pong)
- Only includes file contents for files touched in the incremental diff

The prompt is then fitted to the context window, same as the first review path.

### 6. LLM call

If not a dry run and budget permits, the review prompt is sent to the review provider. The provider handles API communication (Anthropic Messages API, OpenAI Chat Completions, OpenRouter, or Claude CLI).

If the response is truncated (hit max output tokens), a warning is logged. The pipeline attempts to salvage complete findings from the truncated JSON by scanning backward for valid objects.

### 7. Process findings

`processFindings()` parses and validates the LLM's output:

1. **Parse JSON**: Extracts the findings array from the ```json fence. Falls back to bracket-matching if embedded code blocks break the regex.
2. **Anchoring** (`anchorFinding` in `findings.go`): the one rule for whether and where a finding becomes a review thread, shared by validation and `PostReview` so a finding is never counted as new without a thread:
   - no file, or a file not in the PR: dropped;
   - a line within 20 lines of an *added* line in the PR diff: kept, posted inline on that added line;
   - a line further than that: dropped (hallucinated line or scope creep);
   - no line, or a PR file with no added lines (pure deletion, binary or mode-only change): kept, posted as a file-level comment.
3. **Actionable filter**: Removes findings where `actionable: false`.
4. **Status tagging**: Tags all findings as `"new"` if this is an incremental review.

Incremental reviews then apply two history filters (`findings.go`), in Go, after the LLM:

5. **Late-finding gate** (`FilterLateFindings`): a non-blocking finding (suggestion or nitpick, below `blockingSeverity`) that sits more than 5 lines from anything in the incremental diff is about code a previous review already saw, so it's dropped. Each review samples the touched files afresh; without this gate a PR keeps surfacing one more suggestion about old code per push and never converges. Blocking findings (warning and above) in old code still pass — the same threshold that fails the commit status. Skipped when the review fell back to the full PR diff.
6. **Known-duplicate filter** (`FilterKnownDuplicates`): drops a finding that restates a thread the PR already has (open, or answered and acked) — same file, within 15 lines, and either the same `id` or titles with ≥ 50% word overlap. The prompt's Known Issues list asks the model not to repeat these, but it still rewords and re-raises them, most often after a rebase forces a full re-review.

Finally, for every review:

7. **Open questions** (`SplitOpenQuestions`): findings the model flagged `needs_verification: true` — their validity hinges on code, callers or external behavior it could not see in the diff, files and docs — move from `result.Findings` to `result.Questions`. They are not posted as threads, not counted in the status block or `NewFindings`, do not affect the commit status, and are not persisted to local state. They are rendered in a collapsed "Open questions" section of the review body (GitHub) or after the findings (local terminal/markdown; `questions` in JSON). The prompt's uncertainty rule asks for the flag together with severity capped at suggestion and the assumption stated; without it, "Verify …" findings each cost the author a reply cycle.

### 8. Publish results

**GitHub PR** (`--post`): Every cycle emits exactly one top-level CodeCanary review, decided by an edit-vs-post rule. `FetchLatestCodecanaryReview` reads the commit SHA from the most recent CodeCanary review's hidden marker:

- **Same SHA** (reply-only run, or a duplicate `synchronize` webhook on the same HEAD): the existing body is updated in place with `UpdateReviewBody`. Only the status block between the `<!-- codecanary:status -->` markers is swapped — inline comments and prior findings text are untouched.
- **Different or no SHA** (new commits, or first review on the PR): a fresh review is posted. The body variant depends on the cycle outcome — findings review, all-clear, activity summary (no new findings but cycle activity to surface), or clean review. All variants carry the same status block, the open-questions section when there are any, and the baseline SHA marker. Older CodeCanary reviews are minimized (collapsed) before posting.

In a findings review (`PostReview`, payload built by the pure `buildReviewPosts`), every finding gets a thread: line-anchored findings are inline comments in the review's `comments[]`, and file-level findings are POSTed right after the review to `/pulls/{n}/comments` with `subject_type: "file"` (the create-review `comments[]` does not accept `subject_type`). Both carry the same `<!-- codecanary:finding {...} -->` marker, so `LoadPreviousFindings`, triage and `codecanary findings` see file-level threads like any other. GitHub reports no line for them; `FetchReviewThreads` falls back to the line stored in the marker. A failed file-level post fails the run rather than leaving a counted finding without a thread. GitHub wraps each standalone comment in its own empty-bodied review; those carry no `codecanary:review` marker, so baseline-SHA lookup, edit-in-place and minimization ignore them.

**Update notice.** A freshly posted review (any variant; not in-place edits) may end with one or two small-print lines when the repo's CodeCanary install has fallen behind (`UpdateCheck` in `update_notice.go`, passed as `notes` to the body builders):

- *Workflow outdated*: the repo's workflow file — found the same way `codecanary mode` finds it — carries an older `# codecanary-workflow: v<N>` marker than the template embedded in the binary (`setup.TemplateVersion()`), or no marker at all. Markers are compared instead of file contents so customized copies (secret name, action ref, extra steps) are not flagged; the template's marker is bumped whenever it changes, enforced by a fingerprint test in `internal/setup/workflow_test.go`. No workflow file found means no note.
- *Binary outdated*: the running binary is a stable release older than the latest stable release (`selfupdate.LatestRelease`, a direct fetch bounded at 3s — the home-dir cache used by the interactive CLI notice is skipped in CI). Typically means `codecanary_version` is pinned. Canary (`vX.Y.Z-SNAPSHOT-<sha>`) and `dev` builds are never checked: canary is built from `main` after the last release, so it is not behind it even when its version string sorts lower.

Both checks are best-effort: a failed fetch or unreadable file logs a warning and produces no note; they never fail a review. Local mode has no such note — the CLI already prints an upgrade hint in interactive terminals.

The status block lists non-zero counts for: new findings, resolved by code, file removed, dismissed by author, acknowledged by author, rebutted by author, still unresolved. The block renders nothing when all counts are zero, so clean reviews remain copy-exact.

Per-thread ack replies for dismissed/acknowledged/rebutted resolutions are posted earlier in the pipeline (`HandleResolutions`). Dedup is reason-agnostic: if the thread already carries *any* `<!-- codecanary:ack:... -->` marker, no further ack reply is posted. Reasons can shift across triage runs (LLM non-determinism), and all three convey the same outcome ("keeping open"), so one ack per thread is enough. Reply-only runs skip `SaveState` so the empty findings slice doesn't overwrite persisted state.

`codecanary findings` applies the same marker to filter deferrals out of its default output: threads with any `codecanary:ack:*` reply are treated as handled and omitted alongside GitHub-resolved threads. Pass `--include-resolved` to see them. This keeps the codecanary-fix skill from re-prompting on findings the operator already deferred.

After the review is posted (or updated in place), `GithubPlatform.Publish` also POSTs a `CodeCanary / review` commit status on the reviewed SHA via `PostReviewCommitStatus`. State is `failure` while any *blocking* finding — severity `warning` or above (`blockingSeverity` in `findings.go`) — is new this cycle or still open with no classification, and `success` otherwise. Suggestions and nitpicks stay on the PR without failing the check, so they never cost the author another push. Description is the blocking count ("2 unresolved blocking findings"), or "N non-blocking findings open" / "all findings resolved" / "no findings". `codecanary signoff` applies the same rule through `CommitStatusForFindings`. Teams that add `CodeCanary / review` as a required status check in branch protection get auto-gating: merges are blocked until a review run posts a green status on HEAD. Status posting failures are logged as warnings and do not abort Publish — the review itself has already landed. The local `codecanary signoff` command posts a status under the same context, so a team can rely on a single required check that either the bot (pr-loop) or a local reviewer (local-loop) satisfies.

**Local**: Prints the formatted result to stdout. Format depends on context: terminal (colored, human-readable), markdown, or JSON.

**Coverage note** (both platforms): when `ReviewResult.Coverage` is non-empty, the output lists the files the review saw only partially — reviewed from the diff only, or diff truncated. Excluded files (ignored/binary) are left out of the note, since configuration excludes them on every review and listing them would put the note on every review; they remain in the JSON `coverage` field. On GitHub it is a collapsed `<details>` block (`renderCoverageNote`) in every top-level review body (findings, clean, all-clear, activity); in the terminal it is a dim footer; in JSON it is the `coverage` field. Nothing is rendered when no file was cut short.

### 9. Save state

**GitHub PR** (`--post`): No-op. State is stored in the review threads themselves (the embedded JSON marker contains the SHA and findings).

**Local**: Writes `~/.codecanary/repos/<owner>/<repo>/state/<branch>.json` with the current HEAD SHA, branch name, and combined findings (still-open + new). If a legacy `~/.codecanary/state/<branch>.json` exists from a pre-migration run, it is removed after the new file lands. This enables incremental reviews on the next run.

### 10. Report usage

**GitHub PR** (`--post`): Writes token counts and cost to `GITHUB_ENV` for downstream workflow steps, and appends a per-phase markdown table (phase, model, input/output tokens, cost) to `GITHUB_STEP_SUMMARY` so the run's cost is visible on the Actions job page without opening the logs. Both are no-ops outside GitHub Actions, and the step summary is skipped when the report has no calls.

**Local**: Prints a usage summary table to stderr (model, tokens, cost, duration) if running in a terminal.

### 11. Exit status

With `--fail-on <severity>`, `Run()` returns a `FailOnSeverityError` when any finding is at or above that severity in the canonical order (`critical`, `bug`, `warning`, `suggestion`, `nitpick`), making the CLI exit non-zero so CI can gate on review results. The check runs *after* publishing, saving state and reporting usage, so a failing threshold never costs the PR its comments or its telemetry. The flag value is validated up front against `review.ValidateSeverity`, which reads the same `severityLevels` slice that config validation uses.

### 12. Telemetry

If telemetry is enabled (opt-in), fires an anonymous event with aggregate stats: provider, platform, finding counts by severity, token counts, cost, and duration. No code content is sent.

## Key Design Decisions

**Single pipeline, two platforms.** `Run()` never branches on "am I on GitHub?" The `ReviewPlatform` interface absorbs all environment differences. Adding a new platform (e.g. GitLab) means implementing the interface, not forking the pipeline.

**Two diffs for triage.** The incremental diff (changes since last review) decides whether to skip evaluation. The full PR diff (all changes) provides context for evaluation. This prevents the "triage horizon" bug where fixes committed before the triage baseline become invisible.

**Rebase-aware incremental diff.** `<previousSHA>..HEAD` breaks after a rebase or force-push: the old SHA is often absent from the shallow CI clone (so the run fell back to a full re-review that re-raised already-discussed issues), and when present the range drags in every base-branch change to PR files. Comparing the per-file PR patch before and after the rebase isolates what the author actually changed, and lets a no-op rebase cost nothing.

**Two-level triage evaluation.** Same-file evaluations (`TriageCodeChanged`) start with a file-scoped diff (level 1) to reduce noise — the full PR diff can drown out the relevant fix with changes from unrelated files. If level 1 finds no fix, a widened-scope fallback (level 2) sends the full PR diff to catch cross-file fixes. Cross-file evaluations (`TriageCrossFileChange`) go straight to the full diff. The file snippet (current code state) is presented first in all evaluation prompts, so the LLM checks whether the issue still exists before analyzing the diff.

**Per-thread evaluation.** Each unresolved thread gets its own LLM call with tailored context, rather than one bulk prompt. This allows fine-grained classification, parallel execution, and per-thread budget control.

**Anti-ping-pong.** The incremental prompt includes recently resolved findings so the LLM doesn't re-raise similar issues. Non-code resolutions (dismissed, acknowledged, rebutted) keep threads open for re-triage on future pushes, but post ack replies to avoid duplicate acknowledgments.

**Sticky ack across pushes.** Once the bot has recorded a deferral on a thread, subsequent pushes preserve that classification (via `TriagePreviouslyAcked`) until the author adds a new reply. Without this, the next push would re-triage the thread as `TriageCodeChanged` (when the file was touched) or `TriageSkip` (when it wasn't), and the resolution reason would evaporate from the summary — flipping `Acknowledged by author: N` to `Still unresolved: N` and failing the commit status check on a thread the operator already deferred.

**The PR checkout is untrusted input to the Claude CLI.** Under `pull_request_target` the CLI runs inside the PR head with a provider secret in its environment, so nothing in that checkout may configure it: project settings, hooks and `.mcp.json` are excluded on every call, not just when tools are on. Reviewer tool use is opt-in and limited to read-only file tools confined to the repo root, because the anchoring guards control where a finding lands but not what its text quotes.

**One run per PR at a time, nothing dropped.** The edit-vs-post rule in Publish assumes no two runs on the same PR publish at once. The workflow template enforces that with a job-level `concurrency` group per PR (`codecanary-pr-<n>`, `cancel-in-progress: false`, `queue: max`). It is job-level so runs whose job is skipped by `if:` (the bot's own ack replies, non-reply comments) never join the group, and `queue: max` lets several runs wait instead of GitHub's default of one pending run per group, where each newly queued run cancels the pending one. With the old workflow-level group, a human reply (running) followed by a push (pending) followed by the bot's ack reply cancelled the push review, so HEAD was never reviewed.

**Bounded prompt size, no post-build fitting.** Size is bounded up front in `prepareReview`, before the prompt is built: `max_file_size` and `max_total_size` cap full file contents (files over them are reviewed from the diff only), and `max_diff_size` caps the diff (`capDiff` trims the largest file diffs first). There is no token estimation or trimming after the prompt is built. Files reviewed partially are listed in the review's coverage note.

**Finding validation.** All findings are validated against the PR diff regardless of what diff the LLM prompt contained. Line proximity checks (within 20 lines of an added line) catch hallucinated line numbers and prevent scope creep from rebase noise. Validation and posting share `anchorFinding`, so what is counted is exactly what gets a thread.

## The codecanary-fix loop

The `codecanary-fix` Claude skill wraps the review pipeline in a confirm-and-apply loop. The skill calls `codecanary mode --output json` once at startup; the CLI returns one of three modes based on whether an open PR exists for the current branch and whether a CodeCanary workflow file is detected under `.github/workflows/`:

| Mode | PR | Workflow | Findings source | Cycle finalization |
|---|---|---|---|---|
| `pr-loop` | yes | yes | `codecanary findings --watch` (bot posts on push) | commit + push; bot re-runs |
| `local-loop-git` | yes | no | `codecanary review` (local engine) | commit on PR branch, **no push**; operator is asked at session end whether to push accumulated commits |
| `local-loop-nogit` | no | — | `codecanary review` (local engine) | no commits, no pushes; fixes applied in place |

Workflow detection is a textual scan for a non-commented `uses: alansikora/codecanary...` step in any workflow file on the current branch. All three modes share the same triage UX (Markdown table, `AskUserQuestion` confirmation). The bot's ack layer (`<!-- codecanary:ack:* -->` markers) handles deferral persistence in `pr-loop`; local modes use an in-memory `DEFERRED_FIX_REFS` set in the skill so operator-skipped findings don't re-surface within a session. `pr-loop` failures (GHA broken, `conclusion: failure`) never silently fall back to a local mode — the operator is asked to investigate.

The same `mode` payload also carries update status, since the skill runs the CLI as a subprocess and never sees the stderr "new version available" notice: `version` (running binary), `latest_version` / `update_available` (from the 24h cached version check, `selfupdate.CheckCached`; skipped in CI, never blocks on the network), and `skill` (`path`, `installed`, `stale`) comparing the copy at the default `install-skill` location (`~/.claude/skills/codecanary-fix/SKILL.md`) against the skill embedded in the binary. Copies elsewhere (`--dest`, project-mode `.claude/skills/`) are not inspected. The skill prints a one-line notice under the mode line when either is out of date, and never runs `upgrade` or `install-skill` itself.
