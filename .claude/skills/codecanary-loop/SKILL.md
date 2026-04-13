---
name: codecanary-loop
description: |
  Drive a codecanary review → triage → fix → push feedback loop to convergence.
  Defaults to PR mode (watches the codecanary GitHub action, fetches findings,
  applies approved fixes, commits, pushes, and re-watches). Pass `--local` to
  run a local review against uncommitted changes instead, skipping all git
  plumbing. Always confirms every finding with the user before applying —
  never auto-applies.
---

# codecanary-loop

You are driving the CodeCanary review-fix loop. The operator runs this skill
when they want you to iterate against CodeCanary's findings until the review
is clean. Stay disciplined: you are the glue between the CLI and the
operator's decisions, not the reviewer.

## Heavy lifting lives in the CLI

All polling, fetching, parsing, and PR/repo autodetection happens in
`codecanary findings` and `codecanary review`. You never shell out to `gh`
directly from this skill. You never parse HTML comment markers. You never
poll for CI status. The CLI emits structured JSON; you consume it.

This is intentional for token-efficiency: the loop machinery runs in
subprocesses whose output is small and structured. Your conversation budget
is spent on triage judgment and fix application, not on watching CI.

## Mode selection

- **PR mode (default)**: fixes land as commits on the current branch and
  are pushed. Used when an open PR exists for the branch.
- **Local mode** — invoked when the operator passes `--local` as an argument
  to this skill: `codecanary review --output json` runs a review on the
  current dirty working tree; fixes are applied but not committed or pushed.

If you cannot tell which mode applies, ask the operator before starting.

## The loop

Track one piece of state across iterations:
- `CYCLE` — integer, starts at 0, increments at the top of every iteration.

### Iteration

1. `CYCLE = CYCLE + 1`.
2. Fetch findings:
   - **PR mode**: run
     `codecanary findings --watch --output json`.
     The command blocks until the review check completes; its stdout is
     a single JSON object. Parse it. Deduplication is handled by GitHub
     thread resolution — resolved threads are excluded by default.
   - **Local mode**: run `codecanary review --output json`. The command
     runs the review inline; its stdout is a JSON object with a
     `findings` array in the same shape.
3. **PR mode only** — check the `conclusion` field in the JSON output.
   Local mode does not have a check run, so skip only this conclusion
   check and proceed to the findings-empty check at the end of this
   step.
   If `conclusion` is `failure`
   (or any value other than `success` / `neutral` / empty), the review
   run itself broke. Tell the operator the check failed and stop. Do
   not say the review is clean, even if `findings` is empty — an empty
   list on a failed run means findings were never published, not that
   the code is fine. Do not count this as a cycle (roll `CYCLE` back).
   Wait for the operator to explicitly ask you to retry before
   starting another cycle.
   If the findings list is empty (for either mode), tell the operator
   the review is clean and exit. Do not loop further.
4. If `CYCLE > 1`, emit this reminder to the operator verbatim, before
   the triage table:
   > This is review cycle *N*. Before applying fixes, check whether the new
   > findings are caused by your previous fixes or are genuinely different
   > issues. If the bot keeps re-flagging the same `fix_ref` across cycles,
   > stop and verify your fix actually addresses what the bot meant —
   > don't keep patching symptoms.
5. Render a triage table (Markdown) summarizing the findings:
   - Columns: severity, file:line, fix_ref, title, proposed action
   - One row per finding. Keep proposed actions terse (one line each).
6. Ask the operator to confirm. Use `AskUserQuestion` with a single
   question whose options are:
   - "Apply all" *(Recommended)*
   - "Apply some (I'll specify which)"
   - "Skip this cycle" — treats all findings as deferred; exits the loop
   - "Abort" — exits the loop immediately
   Wait for the response before touching any files.
7. If the operator approved (all or some), apply the fixes. For each
   approved finding:
   - Read the file, make the minimal edit that addresses the finding,
     keeping the surrounding code intact (do not "improve" unrelated code).
   - If the suggestion in the finding is an exact code snippet and fits
     the context, prefer it verbatim; otherwise adapt it to the codebase
     conventions (existing imports, types, error-handling style).
8. Finalize the cycle:
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

- The findings list comes back empty **and** `conclusion` is healthy
  (`success`, `neutral`, or absent) — normal success.
- The operator chose "Skip this cycle" or "Abort".
- The CLI errors out (network failure, no PR detected, timeout on
  `--watch`). Surface the error verbatim and stop.
- You detect you're in a stable disagreement loop: the same `fix_ref`
  values appear in two consecutive cycles after you applied fixes for
  them. This is the signal from step 4 turning into a hard stop — tell
  the operator which fix_refs keep re-emerging and ask them to review
  whether the fix is correct before continuing.

## What not to do

- Don't iterate without operator confirmation.
- Don't auto-apply nitpicks or "obvious" fixes.
- Don't write your own logic to parse `<!-- codecanary:finding ... -->`
  markers — the CLI already returns structured Findings.
- Don't `gh api` or `gh pr view` yourself — the CLI handles that.
- Don't attempt concurrent PR work. One branch at a time.
- Don't commit to `main` or an unrelated branch; always stay on the PR's
  feature branch.
- Don't force-push. The loop only appends commits.

## Example operator turn

```
user: Run codecanary-loop on this PR

assistant: (invokes `codecanary findings --watch --output json`,
            parses JSON, renders triage table, asks for confirmation,
            applies approved fixes, commits, pushes, loops)
```
