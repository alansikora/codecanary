# Prompt evaluation corpus

Frozen review inputs. Each `*.json` here is everything a review needs to build
its prompt — PR metadata, diff, the file contents the reviewer would have read,
project docs, and the review config in force — captured from a real pull
request. Rendering a prompt from one touches neither the network nor a working
tree, so the same fixture produces the same prompt on any machine.

`TestPromptGolden` in `internal/review/prompt_golden_test.go` renders each
fixture and compares the result against its `*.prompt.golden` file.

## What this catches, and what it does not

It catches **unintended changes to prompt construction**, and it puts a number
on intended ones. `SIZES.txt` records the rendered size of every prompt, so a
change that adds 4KB of instruction has to show that in its diff.

That number is worth watching. Instruction overhead is paid on every review
forever, and prompt edits are otherwise invisible in code review — their effect
surfaces later as a change in the findings, by which point the cause is hard to
attribute.

It does **not** measure review quality. No model runs and nothing is judged.
Whether a prompt change makes reviews better or worse is a separate question
that needs labelled findings, repeated runs to establish variance, and real
model calls.

## Updating the goldens

When a prompt change is intentional:

```bash
go test ./internal/review/ -run Golden -update
```

Commit the regenerated goldens together with the change, and say what the size
delta is in the PR description.

## Capturing a fixture

`evalsnap` reuses the review package's own fetch path, so a fixture is what a
real review would have seen rather than an approximation. Because file contents
and project docs are read from the working tree, it must run from a checkout of
the target repository at the PR's head commit — it verifies this rather than
trusting you:

```bash
cd /path/to/target-repo
git fetch origin pull/1234/head && git checkout FETCH_HEAD
go run github.com/alansikora/codecanary/cmd/evalsnap \
  --repo owner/name --pr 1234 --out /path/to/corpus
```

## Private repositories

**Fixtures committed here must come from public repositories only.** A fixture
embeds the PR's diff and the full contents of every file the reviewer read;
committing one captured from a private repository publishes that source to this
public repo, and git history makes it permanent.

`evalsnap` refuses to write a fixture from a private repository into any
directory git tracks. Keep those outside this repository and point the harness
at them:

```bash
export CODECANARY_EVAL_CORPUS=/path/to/private/corpus
go test ./internal/review/ -run Golden
```

When that variable is set it replaces this directory for the run. The fixtures
committed here are deliberately small and few — enough to exercise the harness
in CI and to show what a fixture looks like, not enough to be a serious
evaluation set.
