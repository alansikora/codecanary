---
name: codecanary-fix
description: |
  Drive a codecanary review → triage → fix feedback loop to convergence.
  Use this whenever the operator says "handle codecanary", "handle codecanary
  reviews", or invokes /codecanary-fix. The CLI auto-detects one of three
  modes: pr-loop (bot-driven; commit + push each cycle), local-loop-git
  (PR exists but no workflow; local reviews, commit each cycle without
  pushing, offer to push at exit), or local-loop-nogit (no PR; local
  reviews, apply in place, no git). Always confirms every finding with
  the operator before applying — never auto-applies. In pr-loop, every
  skipped finding gets a reply posted on its review thread explaining
  the rationale.
---

# codecanary-fix

You are driving the CodeCanary review-fix loop. The operator runs this skill
when they want you to iterate against CodeCanary's findings until the review
is clean. Stay disciplined: you are the glue between the CLI and the
operator's decisions, not the reviewer.

Trigger phrases: pick this skill up automatically when the operator says
"handle codecanary", "handle codecanary reviews", "run codecanary", or
invokes it explicitly as /codecanary-fix.

## Heavy lifting lives in the CLI

All polling, fetching, parsing, and PR/repo autodetection happens in
`codecanary findings` and `codecanary review`. Posting replies on review
threads happens via `codecanary reply`. You never shell out to `gh`
directly from this skill. You never parse HTML comment markers. You never
poll for CI status. The CLI emits structured JSON; you consume it.

This is intentional for token-efficiency: the loop machinery runs in
subprocesses whose output is small and structured. Your conversation budget
is spent on triage judgment and fix application, not on watching CI.

## Mode selection

The CLI decides the mode. Before the first iteration, run
`codecanary mode --output json` and parse the JSON. The `mode` field
is one of three values — you never guess, you never ask the operator
unless the CLI itself errors.

- **`pr-loop`** — an open PR exists for the branch *and* a CodeCanary
  workflow is wired up on the branch (`.github/workflows/*.yml`
  referencing `alansikora/codecanary`). Findings come from the bot;
  fixes commit and push each cycle, triggering the next bot run.
- **`local-loop-git`** — an open PR exists but no CodeCanary workflow
  is detected on the branch. Findings come from `codecanary review`
  run locally. Fixes commit on the PR branch each cycle **without
  pushing**; at session end the operator is asked whether to push the
  accumulated commits.
- **`local-loop-nogit`** — no open PR for the branch. Findings come
  from `codecanary review` run locally. Fixes are applied in place;
  no commits, no pushes, no prompts.

Only fall back to asking the operator if `codecanary mode` errors out
(e.g., detached HEAD, not in a git repo).

## Startup header

Before the first iteration:

1. Run `codecanary --version` and extract the version string (e.g.
   `codecanary version 0.6.13` → `0.6.13`).
2. Run `codecanary mode --output json` and parse the result. Stash:
   - `MODE` — one of `pr-loop`, `local-loop-git`, `local-loop-nogit`.
   - `PR` — the PR number, or null.
   - `WORKFLOW_DETECTED` — boolean.
   - `REASONS` — array of human-readable detection reasons.
3. Print a boxed hash-style banner to the operator.

Concrete example — if the version is `0.6.13`, the banner must be
exactly:

```
##################################
#                                #
#    CodeCanary v0.6.13 — Fix    #
#                                #
##################################
```

Here the top and bottom rows are 34 `#` characters; the title
`CodeCanary v0.6.13 — Fix` is 24 display columns and is wrapped by
`#` + 4 spaces on the left and 4 spaces + `#` on the right, for a
total of 34 columns. The blank interior rows are `#` + 32 spaces + `#`.

Rules for rendering:

- Use ASCII `#` characters only (no Unicode box-drawing).
- The banner is five lines: a top row of `#`, a blank-interior row, the
  title row, another blank-interior row, and a bottom row of `#`.
- The version string is variable-length — you must **recompute the
  padding** for each invocation so every row has the same column
  width. Do not copy the example padding literally if the version
  differs; count the characters in `CodeCanary v<VERSION> — Fix`
  and rebuild the border/padding around it.
- Keep at least four spaces of padding on each side of the title so
  it feels centered, and match top/bottom row widths to the title
  row width exactly.
- Count `—` (em dash) as one display column.
- Print the banner once per skill invocation, before the loop starts.
- Render it inside a fenced code block so the alignment survives in
  Markdown.

Right after the banner, print a one-line mode summary so the operator
can see what was detected before the loop starts. Format:

```
Mode: <mode>  —  <one-line reason>
```

Where the reason is synthesised from the `REASONS` array. Examples:

- `Mode: pr-loop  —  PR #167, CodeCanary workflow detected`
- `Mode: local-loop-git  —  PR #167, no CodeCanary workflow on this branch (fixes will commit, not push)`
- `Mode: local-loop-nogit  —  no open PR (fixes applied in place)`

If `MODE` came back as something unexpected (CLI error, empty JSON),
surface the error and stop. Do not proceed without a valid mode.

## The loop

Track this state across iterations:

- `CYCLE` — integer, starts at 0, increments at the top of every iteration.
- `DEFERRED_FIX_REFS` — set of strings, initially empty. Used in
  `local-loop-git` and `local-loop-nogit` to suppress findings the
  operator already skipped in a previous cycle (the bot's ack layer
  isn't there to do this for us, so we do it client-side).
- `CYCLE_COMMITS` — list of `{sha, subject}`, initially empty. Used
  in `local-loop-git` to list commits at session end for the push
  prompt.

### Iteration

1. `CYCLE = CYCLE + 1`.
2. Fetch findings, branched on `MODE`:
   - **`pr-loop`**: run `codecanary findings --watch --output json`.
     The command blocks until the review check completes; stdout is a
     single JSON object. Parse it. Findings the bot considers handled
     are excluded by default — that includes GitHub-resolved threads
     *and* threads where the bot has recorded the author's deferral
     (ack:dismissed / ack:rebutted / ack:acknowledged). Skip replies
     posted by the skill in earlier cycles therefore stop re-surfacing
     once the next bot run has ack'd them, so you should never see the
     same deferred finding twice.
   - **`local-loop-git` / `local-loop-nogit`**: run
     `codecanary review --output json`. The command runs the review
     inline; stdout is a JSON object with a `findings` array in the
     same shape. After parsing, **filter out any finding whose
     `fix_ref` is in `DEFERRED_FIX_REFS`** — those are prior-cycle
     skips and must not be re-surfaced.
3. **`pr-loop` only** — check the `conclusion` field in the JSON output.
   (Skip this step entirely for local modes — there is no check run.)
   If `conclusion` is `failure`, the review run itself broke. If
   `conclusion` is `cancelled` or `timed_out`, the run was interrupted
   (e.g. a newer push superseded it). In any of these cases — or any
   value other than `success` / `neutral` / empty — tell the operator
   the check failed, name the conclusion, and stop. Do not say the
   review is clean, even if `findings` is empty — an empty list on a
   failed run means findings were never published, not that the code
   is fine. Roll `CYCLE` back by one (`CYCLE = CYCLE - 1`) so the
   next retry starts at the correct count. Wait for the operator to
   explicitly ask you to retry before starting another cycle.

   **Do not silently fall back to a local mode on pr-loop failures.**
   A broken GHA is a signal the operator needs to investigate — hiding
   it by switching to local reviews obscures real problems.
4. If the findings list is empty (for any mode), tell the operator
   the review is clean and proceed to the **exit handling** section
   below. Do not loop further.
5. If `CYCLE > 1`, emit this reminder to the operator before the
   triage table, substituting *N* with the current value of `CYCLE`:
   > This is review cycle *N*. Before applying fixes, check whether the new
   > findings are caused by your previous fixes or are genuinely different
   > issues. If the reviewer keeps re-flagging the same `fix_ref` across
   > cycles, stop and verify your fix actually addresses what was meant —
   > don't keep patching symptoms.
6. Render a triage table (Markdown) summarizing the findings:
   - Columns: severity, file:line, fix_ref, title, proposed action
   - One row per finding. Keep proposed actions terse (one line each).
7. Ask the operator to confirm. Use `AskUserQuestion` with a single
   question whose options are:
   - "Apply all" *(Recommended)*
   - "Apply some (I'll specify which)"
   - "Skip this cycle" — treats all findings as deferred; exits the loop
   - "Abort" — exits the loop immediately
   Wait for the response before touching any files.
8. If the operator approved (all or some), apply the fixes. For each
   approved finding:
   - Read the file, make the minimal edit that addresses the finding,
     keeping the surrounding code intact (do not "improve" unrelated code).
   - If the suggestion in the finding is an exact code snippet and fits
     the context, prefer it verbatim; otherwise adapt it to the codebase
     conventions (existing imports, types, error-handling style).
9. Handle skipped findings. A skipped finding is any finding not
   applied this cycle — that covers both "Skip this cycle" (all
   skipped) and "Apply some" (the unselected ones). Branch on `MODE`:

   - **`pr-loop`** — post replies on the review threads. For each
     skipped finding, run:

     ```sh
     codecanary reply --url "<comment_url>" --body "<rationale>"
     ```

     where `<comment_url>` is the finding's `comment_url` field from the
     findings JSON, and `<rationale>` is a concise 1–2 sentence summary
     of *why* you're deferring this finding (your own analysis, not
     just "operator skipped"). Examples:
     - "Deferring: the bot's suggested rename conflicts with the public
       API exported in `pkg/foo`. Revisit after the v2 cutover."
     - "Skipping: the flagged line is dead code slated for removal in
       the next PR (#154)."
     - "Skipping: dot notation in the README is deliberate — matches
       upstream xAI naming. Fix is to update the bot's context, not
       the README."

     Post one reply per skipped finding, sequentially. If a reply fails
     (e.g. thread already resolved), surface the error to the operator
     and continue with the remaining skips.

   - **`local-loop-git` / `local-loop-nogit`** — there is no review
     thread to reply to. Instead, add each skipped finding's
     `fix_ref` to `DEFERRED_FIX_REFS` so it is filtered out in
     future iterations. No `codecanary reply` calls in local modes.
10. Finalize the cycle, branched on `MODE`:
    - **`pr-loop`**:
      - Run `go build ./...` and `go test ./...` if any Go files changed.
      - Commit with a message like:
        `fix: address codecanary review on #<PR> (cycle <N>)`
        plus a brief bullet list of which findings were addressed.
      - Push the branch.
      - Go back to step 1.
    - **`local-loop-git`**:
      - Run `go build ./...` and `go test ./...` if any Go files changed.
      - Commit with a message like:
        `fix: address codecanary review (cycle <N>) [local]`
        plus a brief bullet list of which findings were addressed.
      - **Do not push.** Capture the commit SHA and subject into
        `CYCLE_COMMITS`.
      - Go back to step 1.
    - **`local-loop-nogit`**:
      - Do not commit, do not push.
      - Go back to step 1.

### Exit handling

When the loop terminates — findings empty, operator aborts, operator
chose "Skip this cycle", CLI errors out — do the mode-specific exit:

- **`pr-loop`**: report the outcome (clean / aborted / stopped due to
  check failure) and stop. No push prompt (pushes already happened
  each cycle).
- **`local-loop-git`**: if `CYCLE_COMMITS` is non-empty, print the
  list exactly like this:

  ```
  Committed during this session (not yet pushed):
    <sha-short>  <subject>
    <sha-short>  <subject>
  ```

  Then `AskUserQuestion` with:
  - "Push these commits now"
  - "Leave local (I'll push later)" *(Recommended)*
  - "Show diff first"

  On "Push these commits now", run `git push`. On "Show diff first",
  run `git log --stat --oneline <base>..HEAD` where `<base>` is the
  PR's base branch (from the CLI's JSON if available, else the git
  default), then ask the same question again without the "Show diff
  first" option.

  If `CYCLE_COMMITS` is empty (nothing was applied), just report the
  outcome and stop.
- **`local-loop-nogit`**: report the outcome and the summary of
  applied fixes. No commits to list, no prompts.

## Stopping conditions

Exit the loop (and tell the operator *why*) whenever any of these hold:

- **`pr-loop`**: the findings list comes back empty and `conclusion` is
  healthy (`success` or `neutral`) — normal success.
- **`local-loop-git` / `local-loop-nogit`**: the findings list comes
  back empty — normal success. (There is no `conclusion` field in
  local modes; its absence is expected.)
- The operator chose "Skip this cycle" or "Abort". For "Skip this
  cycle" in `pr-loop`, still post the skip replies from step 9 before
  exiting. For "Skip this cycle" in local modes, still update
  `DEFERRED_FIX_REFS`.
- The CLI errors out (network failure, no PR detected, timeout on
  `--watch`, `codecanary mode` failed to resolve). Surface the error
  verbatim and stop.
- You detect you're in a stable disagreement loop: the same `fix_ref`
  values appear in two consecutive cycles after you applied fixes for
  them. This is the signal from step 5 turning into a hard stop — tell
  the operator which fix_refs keep re-emerging and ask them to review
  whether the fix is correct before continuing. Applies to all three
  modes — a local reviewer can re-flag a bad fix the same way the bot
  does.

Always proceed to the **exit handling** section after stopping — it
is where the push prompt for `local-loop-git` lives.

## What not to do

- Don't iterate without operator confirmation.
- Don't auto-apply nitpicks or "obvious" fixes.
- Don't skip a finding silently — in `pr-loop`, every skip gets a
  `codecanary reply` with the rationale (step 9). In local modes,
  every skip adds its `fix_ref` to `DEFERRED_FIX_REFS` so the next
  cycle doesn't surface it again.
- Don't guess the mode. Always read it from `codecanary mode --output json`.
- Don't silently fall back from `pr-loop` to a local mode when the
  GHA fails — surface the error, stop, let the operator decide.
- Don't write your own logic to parse `<!-- codecanary:finding ... -->`
  markers — the CLI already returns structured Findings.
- Don't `gh api` or `gh pr view` yourself — the CLI handles that
  (`codecanary findings` for reads, `codecanary reply` for thread
  replies, `codecanary mode` for mode detection).
- Don't attempt concurrent PR work. One branch at a time.
- Don't commit to `main` or an unrelated branch; always stay on the PR's
  feature branch. Applies to both `pr-loop` and `local-loop-git`.
- Don't force-push. The loop only appends commits.
- Don't push in `local-loop-git` without asking — the push prompt at
  session end is the only authorised push.

## Example operator turns

```
user: handle codecanary on this PR

A: (runs `codecanary mode --output json` → pr-loop,
    prints banner + mode line, invokes
    `codecanary findings --watch --output json`, parses JSON,
    renders triage table, asks for confirmation, applies approved
    fixes, runs `codecanary reply` on each skipped finding with a
    rationale, commits, pushes, loops)
```

```
user: handle codecanary

A: (runs `codecanary mode --output json` → local-loop-nogit,
    prints banner + mode line, invokes
    `codecanary review --output json`, parses JSON, renders triage
    table, asks for confirmation, applies approved fixes, tracks
    skipped fix_refs in DEFERRED_FIX_REFS, loops. On empty findings
    or "Abort", reports the summary and stops — no commit, no push)
```

```
user: handle codecanary (on a branch with a PR but no GHA)

A: (runs `codecanary mode --output json` → local-loop-git,
    prints banner + mode line, invokes
    `codecanary review --output json`, parses JSON, renders triage
    table, asks for confirmation, applies approved fixes, commits
    locally without pushing, tracks skipped fix_refs, loops. On
    empty findings, prints the list of accumulated commits and asks
    whether to push)
```
