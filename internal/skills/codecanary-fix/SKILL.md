---
name: codecanary-fix
description: |
  Drive a codecanary review → triage → fix → push feedback loop to convergence.
  Use this whenever the operator says "handle codecanary", "handle codecanary
  reviews", or invokes /codecanary-fix. Defaults to PR mode (watches the
  codecanary GitHub action, fetches findings, applies approved fixes, commits,
  pushes, and re-watches). Falls back to local mode automatically when no PR
  is detected, reviewing uncommitted changes and skipping all git plumbing.
  Always confirms every finding with the user before applying — never
  auto-applies. Every skipped finding gets a reply posted on its review
  thread explaining the rationale.
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

- **PR mode (default)**: fixes land as commits on the current branch and
  are pushed. Used when an open PR exists for the branch and the operator
  wants CodeCanary's GitHub review cycle to drive the loop.
- **Local mode** — used when no PR exists for the branch, or when the
  operator explicitly wants a local-only pass: `codecanary review --output
  json` runs a review on the current dirty working tree; fixes are applied
  but not committed or pushed. `codecanary review` is always local unless
  `--post` is passed, so this mode works even when the branch has an open
  PR.

If you cannot tell which mode applies, ask the operator before starting.

## Startup header

Before the first iteration, run `codecanary --version` and extract the
version string from its output (e.g. `codecanary version 0.6.13` →
`0.6.13`). Then print a boxed hash-style banner to the operator.

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

## The loop

Track one piece of state across iterations:
- `CYCLE` — integer, starts at 0, increments at the top of every iteration.

### Iteration

1. `CYCLE = CYCLE + 1`.
2. Fetch findings:
   - **PR mode**: run
     `codecanary findings --watch --output json`.
     The command blocks until the review check completes; its stdout is
     a single JSON object. Parse it. Findings the bot considers handled
     are excluded by default — that includes GitHub-resolved threads
     *and* threads where the bot has recorded the author's deferral
     (ack:dismissed / ack:rebutted / ack:acknowledged). Skip replies
     posted by the skill in earlier cycles therefore stop re-surfacing
     once the next bot run has ack'd them, so you should never see the
     same deferred finding twice.
   - **Local mode**: run `codecanary review --output json`. The command
     runs the review inline; its stdout is a JSON object with a
     `findings` array in the same shape.
3. **PR mode only** — check the `conclusion` field in the JSON output.
   (Skip this step entirely for local mode — there is no check run.)
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
4. If the findings list is empty (for either mode), tell the operator
   the review is clean and exit. Do not loop further.
5. If `CYCLE > 1`, emit this reminder to the operator before the
   triage table, substituting *N* with the current value of `CYCLE`:
   > This is review cycle *N*. Before applying fixes, check whether the new
   > findings are caused by your previous fixes or are genuinely different
   > issues. If the bot keeps re-flagging the same `fix_ref` across cycles,
   > stop and verify your fix actually addresses what the bot meant —
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
9. **Post replies on every skipped finding** (PR mode only — local mode
   has no thread to reply to). A skipped finding is any finding not
   applied this cycle — that covers both "Skip this cycle" (all
   skipped) and "Apply some" (the unselected ones). For each skipped
   finding, run:

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
10. Finalize the cycle:
    - **PR mode**:
      - Run `go build ./...` and `go test ./...` if any Go files changed.
      - Commit with a message like:
        `fix: address codecanary review on #<PR> (cycle <N>)`
        plus a brief bullet list of which findings were addressed.
      - Push the branch.
      - Go back to step 1.
    - **Local mode**: stop. Report the summary of applied fixes to the
      operator. Do not commit, do not push, do not loop — a single pass
      is the contract for local mode.

## Stopping conditions

Exit the loop (and tell the operator *why*) whenever any of these hold:

- **PR mode**: the findings list comes back empty and `conclusion` is
  healthy (`success` or `neutral`) — normal success.
- **Local mode**: the findings list comes back empty — normal success.
  (There is no `conclusion` field in local mode; its absence is expected.)
- The operator chose "Skip this cycle" or "Abort". (In "Skip this cycle"
  mode, still post the skip replies from step 9 before exiting.)
- The CLI errors out (network failure, no PR detected, timeout on
  `--watch`). Surface the error verbatim and stop.
- You detect you're in a stable disagreement loop: the same `fix_ref`
  values appear in two consecutive cycles after you applied fixes for
  them. This is the signal from step 5 turning into a hard stop — tell
  the operator which fix_refs keep re-emerging and ask them to review
  whether the fix is correct before continuing.

## What not to do

- Don't iterate without operator confirmation.
- Don't auto-apply nitpicks or "obvious" fixes.
- Don't skip a finding silently — every skip gets a `codecanary reply`
  with the rationale (step 9). The only exception is local mode, which
  has no review thread to reply to.
- Don't write your own logic to parse `<!-- codecanary:finding ... -->`
  markers — the CLI already returns structured Findings.
- Don't `gh api` or `gh pr view` yourself — the CLI handles that
  (`codecanary findings` for reads, `codecanary reply` for thread
  replies).
- Don't attempt concurrent PR work. One branch at a time.
- Don't commit to `main` or an unrelated branch; always stay on the PR's
  feature branch.
- Don't force-push. The loop only appends commits.

## Example operator turn

```
user: handle codecanary on this PR

A: (invokes `codecanary findings --watch --output json`,
            parses JSON, renders triage table, asks for confirmation,
            applies approved fixes, runs `codecanary reply` on each
            skipped finding with a rationale, commits, pushes, loops)
```
