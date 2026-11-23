---
description: Writing Go tests that pass in CI (Ubuntu, bwrap) and catch real regressions
paths:
  - "**/*.go"
---

# Go tests

```sh
go test ./... && go vet ./...
```

**CI runs on `ubuntu-latest`, not macOS.** The test job runs `scripts/setup-bwrap.sh` and then `go test -race -count=1 ./...`. A test that passes on a Mac can still fail there:

- `/tmp` is a real `/tmp` on Linux. On macOS `t.TempDir()` lives under `/var/folders` and `/tmp` resolves to `/private/tmp`. Code that treats `/tmp` specially (the fix sandbox denies it) needs a test that holds on Linux too. Don't build the "allowed" case from a `t.TempDir()` path.
- bubblewrap needs unprivileged user namespaces, which Ubuntu restricts through AppArmor (`setup-bwrap.sh` handles it in CI). A sandbox test has to either skip cleanly when namespaces aren't available or be run on Linux before you push.
- A test that "fixes" a platform failure must still assert something on that platform. Check that every condition can actually fail. `if x != want && err == nil` passes silently whenever `err != nil`.

**Assert the full rendered string for user-facing text.** This covers PR comments, failure notices, commit statuses and CLI messages. `strings.Contains` checks let duplicated lines, stray punctuation (`...output:.`) and doubled prefixes ship.
