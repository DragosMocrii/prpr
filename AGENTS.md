# Agent guidance

## Project

`prpr` monitors open pull requests authored by the active GitHub CLI account, including drafts, across repositories visible to that account.

- `cmd/prpr/main.go` wires configuration, the GitHub client, and the Bubble Tea program.
- `internal/github` invokes `gh` and decodes GitHub API responses.
- `internal/preferences` stores per-account repository scope.
- `internal/tui/model.go` owns app transitions and scope state.
- `internal/tui/repository_picker.go` owns repository-picker input and requests.
- `internal/tui/pr_table.go` owns pull-request table layout, links, and the page indicator.
- `internal/tui/keys.go` owns key bindings, their enabled state, and per-screen help.

## Development commands

Use Go 1.26 or later:

```sh
go run ./cmd/prpr
go build -o /tmp/prpr ./cmd/prpr
go test ./...
go test -race ./...
go vet ./...
gofmt -l cmd internal
```

Running the app requires `gh` on `PATH`, authenticated to `github.com`. Package checks do not require GitHub credentials or a live network connection. See [CONTRIBUTING.md](CONTRIBUTING.md) for setup and verification details. When raising the minimum Go version, update `go.mod`, the README, this guide, and the CI Go version together.

## Contracts to preserve

- Do not store GitHub tokens. Runtime GitHub access goes through the user's active `gh` account.
- Preserve the authored, open pull-request query, pagination, updated-descending ordering, and draft status.
- A missing saved account choice is different from a saved empty repository value: missing prompts for a choice; empty means the user explicitly chose All repositories. Restoring a saved choice must not write preferences. `chooseRepository` is the explicit commit-and-save path.
- Preference-save failures are nonfatal: keep the in-session selection and show the warning.
- Clear old rows when a fetch starts or fails; do not show stale results as current.
- Keep picker cancellation and request-ID checks so obsolete asynchronous results cannot change current state.
- Pull-request identity is the visible-row index mapped through `visiblePRs` to the source snapshot. Do not identify rows by PR number alone.
- Bubbles owns the pull-request table cursor and scrolling. Preserve ANSI/grapheme-aware width behavior and safe OSC 8 hyperlink targets.
- Unknown mergeability is not a clean merge state, and mergeability says nothing about checks or review readiness.

## Tests and interactive changes

- Follow existing tests: literal JSON decoder fixtures, direct TUI-message tests, and real preference stores under `t.TempDir()`.
- Assert deterministic, consumer-visible behavior. Do not pin prose, screenshots, incidental formatting, or implementation details.
- TUI behavior changes require a real terminal/PTY smoke in addition to unit tests. Exercise selection, empty scopes, resize, picker cancellation, and links as relevant to the change.
- Do not perform live login or mutate a developer's GitHub or user configuration during checks.
- For isolated Linux smoke runs, put a fake `gh` first on `PATH` and use a temporary absolute `XDG_CONFIG_HOME`. An explicitly requested live, read-only smoke must preserve the existing GitHub CLI configuration with `GH_CONFIG_DIR`.

## Change discipline and repository hygiene

- Reuse existing helpers and components. Keep changes scoped; add an SDK, alternate credential store, compatibility alias, or framework only for a concrete requirement.
- Never force-add `.devcontainer/project-adhd`, generated environment files, credentials, binaries, or preference files. Keep personal PR and repository data out of public commits.
- Do not add instructions tied to an agent vendor or local development setup. Do not make GitHub mutations or commit/push unless the user requests them.
