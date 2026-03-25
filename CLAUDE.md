# CodeCanary

AI-powered code review for GitHub pull requests.

## Project structure

```
cmd/
  review/          # Review binary (used by GitHub Action)
    main.go        # Entry point
    cli/           # Cobra commands
      root.go      # Root "codecanary" command
      review.go    # codecanary review <pr>
      generate.go  # codecanary review generate
  setup/           # Setup binary (used by curl | sh installer)
    main.go        # Interactive setup wizard (single file, no framework)
internal/
  review/          # Review engine (prompts, GitHub API, triage, formatting)
  auth/            # OAuth PKCE flow, GitHub App installation
worker/            # Cloudflare Worker — OIDC token proxy (TypeScript)
setup.sh           # Thin shell wrapper — downloads and runs codecanary-setup
```

## Two binaries

- **`codecanary`** — review binary, called by the GitHub Action. Users never install this.
- **`codecanary-setup`** — interactive setup wizard, downloaded temporarily by `setup.sh` and cleaned up after.

## Build

```sh
go build ./cmd/review    # builds codecanary
go build ./cmd/setup     # builds codecanary-setup
```

Version is set via ldflags: `-X main.version=v{version}`

## Key dependencies

- `spf13/cobra` — CLI framework (review binary only)
- `bmatcuk/doublestar` — glob pattern matching for ignore rules
- `gopkg.in/yaml.v3` — config parsing
- `golang.org/x/term` — secure password input (setup binary)

## Architecture notes

- **Config** is `.codecanary.yml` at the repo root (flat file, no directory)
- **Review flow**: fetch PR data via `gh` CLI → build prompt → call Claude via `claude` CLI → parse findings → post as PR review
- **Incremental reviews**: on re-push, triage existing threads (Go-driven classifier), only re-evaluate changed threads via Claude (haiku), then review only new code
- **Dual marker detection**: reads both `codecanary:review` and legacy `clanopy:review` HTML markers for backward compatibility
- **Anti-hallucination**: explicit file allowlist, line validation against diff, max finding distance threshold
- **Worker** (`worker/`): OIDC token exchange proxy at `oidc.codecanary.sh` — verifies GitHub Actions OIDC token, returns GitHub App installation token
- **Setup** is a standalone binary with no CLI framework — just a sequential interactive flow

## Rules

- **Minimize shell code.** `setup.sh` and the GitHub Action (`alansikora/codecanary-action`) should be kept as thin as possible. All logic must live in Go.
- No automated tests exist yet (only config unit tests). Be careful with refactors.
