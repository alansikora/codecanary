---
name: codecanary-fix
description: |
  Drive a codecanary review → triage → fix feedback loop to convergence.
  Use this whenever the operator says "handle codecanary", "handle codecanary
  reviews", or invokes /codecanary-fix. The CLI auto-detects one of three
  modes: pr-loop (bot-driven; when the repo fixes on CI — fix_on_ci — wait
  for the bot's fix commits and pull them, taking over locally only when
  the bot stopped or asked for a local fix; otherwise fix, commit and push
  each cycle), local-loop-git (PR exists but no workflow; local reviews,
  commit each cycle without pushing, offer to push at exit), or
  local-loop-nogit (no PR; local reviews, apply in place, no git). Every
  finding is triaged against the project's conventions: legitimate ones
  are applied, wrong ones are refuted. With confirmation on
  (confirm_fixes: true in .codecanary/review.local.yml, or
  "/codecanary-fix confirm" / "ask me first"), the triage is shown and the
  operator picks which findings to apply. In pr-loop, every skipped
  finding gets a reply posted on its review thread explaining the
  rationale.
---

# codecanary-fix

You are driving the CodeCanary review-fix loop. The operator runs this skill
when they want you to iterate against CodeCanary's findings until the review
is clean. Stay disciplined: you are the glue between the CLI and the
project's conventions, not the reviewer. You triage each finding on its
merits and, by default, act on your own judgment; the operator is not
asked to approve individual findings. When confirmation is on (see
"Confirmation"), you still triage, but the operator decides which
findings to apply.

Trigger phrases: pick this skill up automatically when the operator says
"handle codecanary", "handle codecanary reviews", "run codecanary", or
invokes it explicitly as /codecanary-fix.

## Heavy lifting lives in the CLI

All polling, fetching, parsing, and PR/repo autodetection happens in
`codecanary findings`, `codecanary checks` and `codecanary review`.
Posting replies on review threads happens via `codecanary reply`. You never
parse HTML comment markers. You never poll for CI status yourself —
`codecanary checks --watch` does. The CLI emits structured JSON; you
consume it.

This is intentional for token-efficiency: the loop machinery runs in
subprocesses whose output is small and structured. Your conversation budget
is spent on triage judgment and fix application, not on watching CI.

Fetching is already one call with a small payload, so there is nothing to
gain by delegating it to a subagent. If you delegate it anyway, carry the
findings **verbatim** — severity, `fix_ref`, `file:line`, title, full
description, `comment_url`, `commit_id` — truncated where long, never
paraphrased. Dispositions turn on exact wording: "the rule is silent on
instances" and "the rule bails before reaching the comparison" collapse
into the same one-line summary and point triage at opposite answers. The
loss is invisible, because the summary reads fine.

`codecanary findings` takes an optional `[pr-number]`, so you can drive a
cycle from any directory without checking the branch out. `--watch` blocks
up to `--timeout` minutes (default 15); pass a smaller value when you don't
want to hold a foreground call that long.

## Mode selection

The CLI decides the mode. Before the first iteration, run
`codecanary mode --output json` and parse the JSON. The `mode` field
is one of three values — you never guess, you never ask the operator
unless the CLI itself errors.

- **`pr-loop`** — an open PR exists for the branch *and* a CodeCanary
  workflow is wired up on the branch (`.github/workflows/*.yml`
  referencing `alansikora/codecanary`). Findings come from the bot, and
  every push is reviewed. Who applies the fixes depends on `fix_on_ci`
  (reported by `codecanary mode`):
  - `fix_on_ci` true — the repo fixes on CI: after a review with
    findings, a CI job applies the fixes, commits them as the PR author
    and pushes, and the push triggers the next review. This skill does
    **not** fix. It waits for the head to settle, pulls the bot's fix
    commits, and takes over locally only when the bot has stopped (see
    "Waiting for the bot").
  - `fix_on_ci` false — this skill fixes: apply, commit, push, and the
    push triggers the next review.
  - `review_on: ready` is a legacy value: under it, pushes to a ready PR
    are not reviewed, so a fix cannot be re-reviewed, and the repo should
    move to `review_on: push`.
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

## Confirmation

By default the skill applies its own triage without asking. The operator
can opt in to confirming each cycle's fixes. `CONFIRM` is resolved once,
at startup, in this order:

1. **The invocation.** `/codecanary-fix confirm`, or a request that asks
   for approval ("handle codecanary, ask me first", "let me approve the
   fixes") → `CONFIRM` true. `/codecanary-fix auto`, or "don't ask me" →
   `CONFIRM` false. Either overrides the setting for this run.
2. **The setting.** Otherwise `CONFIRM` is `confirm_fixes` from
   `codecanary mode` — `confirm_fixes: true` in the operator's
   `.codecanary/review.local.yml`. Treat a missing field as false (older
   CLI).

`CONFIRM` only changes what happens between the triage table and the
edits (step 7): the operator chooses which findings you apply. It
applies wherever **you** fix — `pr-loop` without `FIX_ON_CI`, both local
modes, and `pr-loop` with `FIX_ON_CI` after you take over from a stopped
bot. While the bot is fixing on CI it has nothing to gate: the CI fix
job never asks, and the skill only waits and pulls its commits.

## Startup header

Before the first iteration:

1. Run `codecanary --version` and extract the version string (e.g.
   `codecanary version 0.6.13` → `0.6.13`).
2. Run `codecanary mode --output json` and parse the result. Stash:
   - `MODE` — one of `pr-loop`, `local-loop-git`, `local-loop-nogit`.
   - `PR` — the PR number, or null.
   - `WORKFLOW_DETECTED` — boolean.
   - `REASONS` — array of human-readable detection reasons.
   - `FIX_ON_CI` — boolean, from `fix_on_ci`. Treat a missing field as
     false (older CLI).
   - `CONFIRM` — boolean, resolved from the invocation and
     `confirm_fixes` as in "Confirmation".
   - `REVIEW_ON` — `push` or `ready`, from `review_on`. Treat a missing
     field as `push` (older CLI).
   - `UPDATE_AVAILABLE` / `LATEST_VERSION` — from `update_available` and
     `latest_version` (the latter is absent when no update is known).
   - `SKILL_STALE` — from `skill.stale`: true when the skill installed by
     `codecanary install-skill` (at `skill.path`) differs from the copy
     embedded in this binary. Treat missing fields as false (older CLI).
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

- `Mode: pr-loop (fix on CI)  —  PR #167, CodeCanary workflow detected (the bot fixes; waiting for it)`
- `Mode: pr-loop  —  PR #167, CodeCanary workflow detected (fixes commit and push each cycle)`
- `Mode: local-loop-git  —  PR #167, no CodeCanary workflow on this branch (fixes will commit, not push)`
- `Mode: local-loop-nogit  —  no open PR (fixes applied in place)`

If `CONFIRM` is true, print a line under the mode line saying so and
where it came from:

- `Confirm: on (review.local.yml)  —  you choose which findings to apply each cycle`
- `Confirm: on (requested)  —  you choose which findings to apply each cycle`

With `FIX_ON_CI` in `pr-loop`, say instead
`Confirm: on  —  the bot fixes on CI without asking; you'll choose only if I take over`.

Then, only if something is out of date, print exactly one notice line
under the mode line:

- `UPDATE_AVAILABLE` and `SKILL_STALE`:
  `Update: codecanary <LATEST_VERSION> is available (running <VERSION>) — run 'codecanary upgrade', then 'codecanary install-skill --force'.`
- `UPDATE_AVAILABLE` only:
  `Update: codecanary <LATEST_VERSION> is available (running <VERSION>) — run 'codecanary upgrade'.`
- `SKILL_STALE` only:
  `Update: the installed codecanary-fix skill is out of date with this binary — run 'codecanary install-skill --force' and restart Claude Code.`

This is informational. Never run `codecanary upgrade` or
`codecanary install-skill` yourself, never ask the operator about it,
and never let it delay or block the loop — print the line and continue.

If `REVIEW_ON` is `ready`, print one more notice line:
`Notice: review_on: ready is legacy — pushes to a ready PR are not reviewed under it; move the repo to review_on: push.`
Under it a push is never re-reviewed, so in `pr-loop` the loop ends after
one cycle of fixes (see step 9).

If `MODE` came back as something unexpected (CLI error, empty JSON),
surface the error and stop. Do not proceed without a valid mode.

## The loop

Track this state across iterations:

- `CYCLE` — integer, starts at 0, increments at the top of every iteration.
- `DEFERRED_FIX_REFS` — set of strings, initially empty. Used in
  `local-loop-git` and `local-loop-nogit` to suppress findings you
  already refuted in a previous cycle (the bot's ack layer isn't there
  to do this for us, so we do it client-side).
- `CYCLE_COMMITS` — list of `{sha, subject}`, initially empty. Used
  in `local-loop-git` to list commits at session end for the push
  prompt.

### Waiting for the bot (`pr-loop` with `FIX_ON_CI`)

When `FIX_ON_CI` is true, the fix happens on CI, not here. Use this
procedure for step 2 in place of a plain fetch:

1. Run `codecanary findings --watch --output json` to wait for the
   review of the current head, then `codecanary checks <PR> --watch
   --output json` to wait for the PR's required checks (it leaves out
   CodeCanary's own). Check the findings `conclusion` as in step 3, and
   the checks `state` (`pass` or `fail`, with `failing` names).
2. `git fetch` and compare the PR branch with your local branch. When
   the bot pushed fix commits, `git pull --ff-only`, then go back to
   step 1 of this procedure: the push triggers a new review, and
   `codecanary findings --watch` waits for it. A fast-forward pull that
   fails means you have local commits the bot did not see; report it and
   stop — never force-push, never merge over the bot's commit unasked.
3. Decide whether the head has settled:
   - Checks `state` is `fail`, or the command errors or times out:
     report the failing checks and stop. Never call the PR clean while
     required checks fail; fixing CI is a separate job.
   - Findings empty, conclusion healthy, checks `state` `pass`, nothing
     new on the branch: the review is clean — go to **exit handling**.
   - The bot **stopped**: the `CodeCanary / review` commit status
     description contains `stopped after N fixes`, or the PR carries the
     bot's comment asking for a local fix (it says to fix locally with
     `/codecanary-fix`; a rate limit produces this one). Take over: the
     current findings are yours. Go on to step 5, triage and apply them
     as the local modes do, commit and push once. Read the status
     description and PR comments from the CLI's JSON when it carries
     them; otherwise `gh pr view <PR> --json comments,statusCheckRollup`
     is the only `gh` call this skill makes.
   - Findings remain, but there is no stop signal and no new commit: the
     fix job is probably still running. Wait once more as in step 1 and
     fetch again. If two consecutive rounds bring no new commit and no
     stop signal, report what you saw and stop; do not apply fixes the
     bot may be about to push, and do not spin.

After you take over, triage and apply as in steps 5–8, commit, and
push **once**. The push triggers the next review (and resets the bot's
fix count); go back to the start of this procedure.

### Iteration

1. `CYCLE = CYCLE + 1`.
2. Fetch findings, branched on `MODE`:
   - **`pr-loop`** with `FIX_ON_CI` true: follow **Waiting for the bot**
     above. The findings it hands you are the ones from the latest
     `codecanary findings` run.
   - **`pr-loop`** with `FIX_ON_CI` false: run
     `codecanary findings --watch --output json`.
     The command blocks until the review check completes; stdout is a
     single JSON object. Parse it. Findings the bot considers handled
     are excluded by default — that includes GitHub-resolved threads
     *and* threads where the bot has recorded the author's deferral
     (ack:dismissed / ack:rebutted / ack:acknowledged). Skip replies
     posted by the skill in earlier cycles therefore stop re-surfacing
     once the next bot run has ack'd them, so you should never see the
     same deferred finding twice.

     **Compare each finding's `commit_id` to the top-level `commit`
     (the reviewed head) before you treat the finding as new.** When
     they differ, GitHub has marked that thread *outdated*: the code
     the finding was anchored to has changed since the comment was
     posted. GitHub re-anchors `commit_id` forward to head for as long
     as the hunk survives, so a lagging `commit_id` is a reliable
     signal that the anchor moved.

     Outdated is **not** the same as fixed, and the check is only a
     prompt to go read the code:
     - The anchor also moves on an edit that had nothing to do with
       the finding, so an outdated finding can still be live. Never
       ack one without reading the file at head.
     - A fix that landed in a *different* file leaves `commit_id`
       equal to head. Equality is not evidence the finding is live.

     Either way, judge an outdated finding against the file at head —
     not against the diff hunk quoted in the bot's comment, which is
     stale by definition. If the issue is genuinely gone, the
     disposition is a `codecanary reply` naming the commit that fixed
     it (step 8), never a second fix. The same goes for a finding the
     bot's own fix commit already addressed.
   - **`local-loop-git` / `local-loop-nogit`**: run
     `codecanary review --output json`. The command runs the review
     inline; stdout is a JSON object with a `findings` array in the
     same shape. After parsing, **filter out any finding whose
     `fix_ref` is in `DEFERRED_FIX_REFS`** — those are prior-cycle
     refutations and must not be re-surfaced.
3. **`pr-loop` only** — check the `conclusion` field in the JSON output.
   (Skip this step entirely for local modes — there is no check run.)
   If `conclusion` is `failure`, the review run itself broke. If
   `conclusion` is `cancelled` or `timed_out`, the run was interrupted
   (e.g. a newer push superseded it). If it is `skipped`, no review ran
   for this head (under legacy `review_on: ready` that is the normal
   state of a ready PR after a push). In any of these cases — or any
   value other than `success` / `neutral` / empty — tell the operator
   the check did not complete, name the conclusion, and stop. Do not say
   the review is clean, even if `findings` is empty — an empty list on a
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

   **In local modes, check the JSON's `coverage` field before calling
   an empty result clean.** It is present only when the reviewer did
   not see every changed file in full:
   - `excluded`: files left out entirely (they match an `ignore`
     pattern, or are binary). When they include files this change
     actually touches, the reviewer did not look at them — report "no
     findings, but these files were not reviewed: …", naming them,
     never a clean review.
   - `diff_only` / `truncated_diff`: reviewed from their diff only, or
     from a trimmed diff. Say so alongside "no findings" when they are
     files this change is about.

   **Open questions are not findings.** Items the reviewer flagged
   `needs_verification` (it couldn't confirm them from what it saw)
   arrive in the `questions` array of `codecanary review` in local
   modes; on GitHub they sit in a collapsed "Open questions" section
   of the review and `codecanary findings` doesn't list them. They have
   no thread, don't affect the commit status, and need no reply;
   mention one to the operator only if it is worth checking.
5. If `CYCLE > 1`, emit this reminder to the operator before the
   triage table, substituting *N* with the current value of `CYCLE`:
   > This is review cycle *N*. Before applying fixes, check whether the new
   > findings are caused by your previous fixes or are genuinely different
   > issues. If the reviewer keeps re-flagging the same `fix_ref` across
   > cycles, stop and verify your fix actually addresses what was meant —
   > don't keep patching symptoms.
6. Triage the findings and render the result as a table (Markdown). The
   table is a report of what you decided, not a question (with
   `CONFIRM`, it is your recommendation, and step 7 asks):
   - Columns: severity, file:line, fix_ref, title, disposition
     (`apply`, `refute`, or `already fixed`) with a terse reason. With
     `CONFIRM`, add a leading `#` column numbering the rows from 1.
   - One row per finding. Keep dispositions terse (one line each).

   **Test each finding against the project's own conventions before
   deciding to apply it.** A finding can be correct in general and
   wrong for this repo: a style rule the project deliberately splits
   (indent under `private` in classes but not modules), a pattern the
   surrounding code establishes on purpose, or a premise about the code
   that doesn't hold (the rule the bot invokes bails earlier, so it
   can't fire). Read the rule the finding implies, and read the nearest
   sibling file, before taking the bot as right. When it's wrong, the
   disposition is a reply naming *why* — worth more than a fix, because
   a refutation the bot can read stops the finding recurring.

   Do not use this as licence to wave findings away. "This change
   contradicts its own stated purpose" and "this duplicates a decision
   made elsewhere in the codebase" are the highest-value things the bot
   produces, and both are easy to mistake for nitpicks. The rule is to
   *read*, not to default either way. A legitimate finding is applied
   even when it is a nitpick.
7. Decide what to apply. Without `CONFIRM`, that is every finding whose
   disposition is `apply`. With `CONFIRM`, ask first and touch no file
   until the operator answers. Use `AskUserQuestion` with one question
   whose options are:
   - "Apply the recommended fixes" *(Recommended)* — every `apply` row
   - "Choose individually"
   - "Skip all this cycle" — nothing is applied; every finding is
     skipped (step 8) and the loop ends
   - "Stop" — exit now: no edits, no replies

   On "Choose individually", ask which rows to apply: a multi-select
   `AskUserQuestion` listing the findings (`#<n> <file:line> — <title>`)
   when there are four or fewer, otherwise have the operator type the row
   numbers. The operator may pick a row you marked `refute`; apply it.
   A finding the operator declines is skipped (step 8); keep any reason
   they gave for it.

   Apply every chosen finding, then continue to step 8; the single
   commit comes in step 9. For each one:
   - Read the file, make the minimal edit that addresses the finding,
     keeping the surrounding code intact (do not "improve" unrelated code).
   - If the suggestion in the finding is an exact code snippet and fits
     the context, prefer it verbatim; otherwise adapt it to the codebase
     conventions (existing imports, types, error-handling style).
8. Handle skipped findings. A skipped finding is any finding not
   applied this cycle: one you refuted in triage, one that was already
   fixed, or, with `CONFIRM`, one the operator declined. Branch on
   `MODE`:

   - **`pr-loop`** — post replies on the review threads. For each
     skipped finding, run:

     ```sh
     codecanary reply --url "<comment_url>" --body "<rationale>"
     ```

     where `<comment_url>` is the finding's `comment_url` field from the
     findings JSON, and `<rationale>` is a concise 1–2 sentence summary
     of *why* you're not applying this finding (your own analysis). For
     a finding the operator declined, use the reason they gave; with
     none, say the author chose not to apply it in this PR, plus your
     own read of the finding when you had refuted it too.
     Examples:
     - "Not applying: the bot's suggested rename conflicts with the public
       API exported in `pkg/foo`. Revisit after the v2 cutover."
     - "Not applying: the flagged line is dead code slated for removal in
       the next PR (#154)."
     - "Not applying: dot notation in the README is deliberate — matches
       upstream xAI naming. Fix is to update the bot's context, not
       the README."

     Post one reply per skipped finding, sequentially. If a reply fails
     (e.g. thread already resolved), surface the error to the operator
     and continue with the remaining skips.

     **A finding you FIXED sometimes needs a reply too** (`pr-loop`
     only; local modes have no threads). The bot's
     triage re-reads each unresolved thread against the new code and
     resolves the ones it can see were fixed — that is the normal path
     and it needs nothing from you. It fails when the fix isn't visible
     where the evaluator looks: the fix landed in another file, or the
     change was a restructure it reads as unrelated. So when a finding
     you already fixed in an earlier cycle comes back, reply naming the
     commit and the change that fixed it, and do **not** fix it twice.

     A rationale recorded anywhere else is invisible to the bot — it
     reads replies on the review thread and nothing else. A PR
     issue-comment, a commit message, or the PR body acks nothing.

     Note that your reply is an *input* to the next triage pass, not an
     ack in itself: the bot writes the `codecanary:ack:` marker after
     its own evaluation agrees with you. A reply the evaluator finds
     unconvincing leaves the thread open.

   - **`local-loop-git` / `local-loop-nogit`** — there is no review
     thread to reply to. Instead, add each skipped finding's
     `fix_ref` to `DEFERRED_FIX_REFS` so it is filtered out in
     future iterations. No `codecanary reply` calls in local modes.
9. Finalize the cycle, branched on `MODE`:
   - **`pr-loop`**:
     - If no finding was applied this cycle (all skipped), there is
       nothing to push and no new review to request: go to
       **exit handling**.
     - Run `go build ./...` and `go test ./...` if any Go files changed.
     - Commit every applied fix in **one** commit with a message like:
       `fix: address codecanary review on #<PR> (cycle <N>)`
       plus a brief bullet list of which findings were addressed.
     - Push the branch. The push triggers the next review.
     - With `REVIEW_ON = ready` (legacy), that push is not reviewed:
       report the commit and go to **exit handling** instead of looping.
     - Otherwise go back to step 1 (with `FIX_ON_CI` true, that means
       waiting for the bot again).
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

When the loop terminates — findings empty, nothing left to apply, CLI
errors out — do the mode-specific exit:

- **`pr-loop`**: report the outcome (clean / stopped on a failed or
  skipped check / waiting on the bot gave up / pushed under legacy
  `review_on: ready`) and stop. No push prompt (pushes already happened
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
- With `CONFIRM`, the operator chose "Stop": exit at once — no edits,
  no replies.
- A cycle ends with every finding skipped (including "Skip all this
  cycle"): nothing was applied, so nothing will change on the next
  review. In `pr-loop`, still post the
  replies from step 8 before exiting; in local modes, `DEFERRED_FIX_REFS`
  is already updated.
- The CLI errors out (network failure, no PR detected, timeout on
  `--watch`, `codecanary mode` failed to resolve). Surface the error
  verbatim and stop.
- `pr-loop` with `FIX_ON_CI`: the bot has neither pushed nor stopped
  after two rounds of waiting (see **Waiting for the bot**), or your
  local branch cannot fast-forward to the bot's commits.
- You detect you're in a stable disagreement loop: the same `fix_ref`
  values appear in two consecutive cycles after fixes were applied for
  them (by you or, with `FIX_ON_CI`, by the bot). This is the signal
  from step 5 turning into a hard stop — tell the operator which
  fix_refs keep re-emerging and ask them to review whether the fix is
  correct before continuing. Applies to all three modes — a local
  reviewer can re-flag a bad fix the same way the bot does.

  **Rule out the benign explanations first — a recurrence is not by
  itself a disagreement.** Before escalating, for each repeating
  `fix_ref`, read the file at head to confirm the issue is actually
  still there. Escalate only when it is. When the code at head no
  longer has the issue, the finding came back without being live:
  - **`pr-loop`**: check whether anyone replied on the thread. A
    finding that was fixed correctly but never replied to recurs
    whenever triage couldn't see the fix, and it recurs *identically*
    — same `fix_ref`, title, file and line. It needs a reply naming
    the commit (step 8), not a stop.
  - **`local-loop-git` / `local-loop-nogit`**: there is no thread to
    reply to. Tell the operator the reviewer re-flagged a finding the
    code no longer has, and add its `fix_ref` to `DEFERRED_FIX_REFS`
    so it doesn't surface again this session.

  Escalating either case sends the operator hunting for a defect that
  isn't there.

Always proceed to the **exit handling** section after stopping — it
is where the push prompt for `local-loop-git` lives.

## What not to do

- Don't ask the operator to approve findings unless `CONFIRM` is on.
  By default triage is your judgment; report it, act on it. With
  `CONFIRM`, never edit a file before the operator has answered step 7,
  and apply only what they chose.
- Don't skip a legitimate finding because it looks like a nitpick, and
  don't apply one the project's conventions refute.
- Don't skip a finding silently — in `pr-loop`, every skip gets a
  `codecanary reply` with the rationale (step 8). In local modes,
  every skip adds its `fix_ref` to `DEFERRED_FIX_REFS` so the next
  cycle doesn't surface it again.
- Don't fix in `pr-loop` with `FIX_ON_CI` while the bot is still
  working: wait, pull its commits, and take over only when it stopped
  or asked for a local fix.
- Don't guess the mode. Always read it from `codecanary mode --output json`.
- Don't silently fall back from `pr-loop` to a local mode when the
  GHA fails — surface the error, stop, let the operator decide.
- Don't write your own logic to parse `<!-- codecanary:finding ... -->`
  markers — the CLI already returns structured Findings.
- Don't `gh api` or `gh pr view` yourself — the CLI handles that
  (`codecanary findings` for reads, `codecanary reply` for thread
  replies, `codecanary mode` for mode detection, `codecanary checks`
  for required checks). The one exception is reading the bot's
  stop signal in **Waiting for the bot** when the CLI's JSON lacks it.
- Don't attempt concurrent PR work. One branch at a time.
- Don't commit to `main` or an unrelated branch; always stay on the PR's
  feature branch. Applies to both `pr-loop` and `local-loop-git`.
- Don't force-push. The loop only appends commits.
- Don't push in `local-loop-git` without asking — the push prompt at
  session end is the only authorised push.

## Mode summary

| Mode | Findings from | Who fixes | After the fix |
| --- | --- | --- | --- |
| `pr-loop`, `fix_on_ci` true | bot (`codecanary findings --watch`) | the bot on CI; you only after "stopped after N fixes" or the "fix locally" comment | pull the bot's commits and wait; if you took over, one commit + one push |
| `pr-loop`, `fix_on_ci` false | bot (`codecanary findings --watch`) | you | commit, push; the push triggers the next review; replies on skipped threads |
| `local-loop-git` | `codecanary review` | you | commit without pushing; push prompt at exit; skipped refs deferred |
| `local-loop-nogit` | `codecanary review` | you | apply in place; no git; skipped refs deferred |

## Example operator turns

```
user: handle codecanary on this PR

A: (runs `codecanary mode --output json` → pr-loop, fix_on_ci false,
    prints banner + mode line, invokes
    `codecanary findings --watch --output json`, parses JSON,
    renders the triage table, applies the legitimate findings, runs
    `codecanary reply` on each refuted finding with a rationale,
    commits, pushes, loops)
```

```
user: handle codecanary (repo with fix.enabled: true)

A: (runs `codecanary mode --output json` → pr-loop, fix_on_ci true,
    prints banner + mode line, waits with `codecanary findings --watch`
    and `codecanary checks <PR> --watch`, pulls the bot's fix commits,
    and loops on the new review. Reports clean when the head settles
    with no findings; if the status says "stopped after 2 fixes" it
    triages and applies the open findings itself, commits and pushes
    once)
```

```
user: handle codecanary

A: (runs `codecanary mode --output json` → local-loop-nogit,
    prints banner + mode line, invokes
    `codecanary review --output json`, parses JSON, renders the triage
    table, applies the legitimate findings, tracks refuted fix_refs in
    DEFERRED_FIX_REFS, loops. On empty findings, reports the summary
    and stops — no commit, no push)
```

```
user: /codecanary-fix confirm

A: (runs `codecanary mode --output json` → pr-loop, fix_on_ci false,
    prints banner, mode line and "Confirm: on (requested)", fetches the
    findings, renders the numbered triage table, asks which to apply.
    Operator picks "Choose individually" and rows 1 and 3: applies
    those, replies on the other threads — with the operator's reason
    for the ones they declined — commits, pushes, loops)
```

```
user: handle codecanary (on a branch with a PR but no GHA)

A: (runs `codecanary mode --output json` → local-loop-git,
    prints banner + mode line, invokes
    `codecanary review --output json`, parses JSON, renders the triage
    table, applies the legitimate findings, commits locally without
    pushing, tracks refuted fix_refs, loops. On empty findings, prints
    the list of accumulated commits and asks whether to push)
```
