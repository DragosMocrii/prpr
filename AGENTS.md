# Agent guidance

## Project

`prpr` monitors open pull requests authored by the active GitHub CLI account, including drafts, across repositories visible to that account, and open pull requests that request the account's review (`review-requested:@me`).

- `cmd/prpr/main.go` wires configuration, the GitHub client, and the Bubble Tea program.
- `internal/github` invokes `gh` and decodes GitHub API responses; `bots.go` parses the bot list and derives each bot's review state.
- `internal/preferences` stores per-account repository scope.
- `internal/tui/model.go` owns app transitions and scope state.
- `internal/tui/repository_picker.go` owns repository-picker input and requests.
- `internal/tui/pr_pane.go` owns the two pull-request panes: focus, layout, per-pane selection, and the page indicator.
- `internal/tui/quota.go` owns rate-limit polling and the status line.
- `internal/tui/pr_table.go` owns pull-request table columns, statistics cells, and links.
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

Running the app requires `gh` on `PATH`, authenticated to `github.com`. Package checks do not require GitHub credentials or a live network connection. See [CONTRIBUTING.md](CONTRIBUTING.md) for setup and verification details. When raising the minimum Go version, update `go.mod`, the README, this guide, and the Go version in both CI and release workflows together. Release builds stamp the tag into `main.version` through `-ldflags`; keep that variable name.

## Contracts to preserve

- Do not store GitHub tokens. Runtime GitHub access goes through the user's active `gh` account.
- Preserve the authored, open pull-request query, pagination, updated-descending ordering, and draft status.
- Preserve the review-request query `is:pr is:open review-requested:@me archived:false sort:updated-desc`. Both lists are fetched and replaced together.
- A missing saved account choice is different from a saved empty repository value: missing prompts for a choice; empty means the user explicitly chose All repositories. Restoring a saved choice must not write preferences. `chooseRepository` is the explicit commit-and-save path.
- Preference-save failures are nonfatal: keep the in-session selection and show the warning.
- Keep both panes' previous rows visible during a refresh only while the refresh indicator is shown, and clear them when a fetch fails; do not show stale results as current.
- Keep picker cancellation and request-ID checks so obsolete asynchronous results cannot change current state.
- Every fetch start increments `refreshGeneration`; auto-refresh ticks from an older generation are ignored, so any refresh restarts the timer. A due tick never closes the picker or skips the scope prompt; it reschedules instead. Authentication failures are not retried automatically.
- Quota polling uses a GraphQL query that selects only `rateLimit`, which GitHub does not charge for. Do not use the REST `rate_limit` endpoint: its graphql resource does not track points spent by GraphQL queries. Polling runs as a single chain: each result schedules the next tick. It pauses during login or after an authentication failure and resumes after a successful fetch.
- Pull-request identity is the pane's visible-row index mapped through that pane's `visible` slice to its own source list (`PullRequests` or `ReviewRequests`). Do not identify rows by PR number alone.
- Bubbles owns the pull-request table cursor and scrolling. Preserve ANSI/grapheme-aware width behavior and safe OSC 8 hyperlink targets.
- Unknown mergeability is not a clean merge state, and mergeability says nothing about checks or review readiness.
- Age is ready-for-review time (last `ReadyForReviewEvent`, else `createdAt`; zero for drafts), or for review requests the latest `ReviewRequestedEvent` naming the viewer with that ready time as fallback. Null review decisions and check rollups are unknown, never approved or passing.
- Statistics columns drop in reverse priority (Size, Review, CI, Bots, Age) before squeezing the PR name below `minStatsNameWidth`.
- Every configured bot is judged by one rule, with no bot-specific text parsing: unresolved, non-outdated threads it started are concerns; then a matching head-commit check run that is unfinished or failed; then whether its latest review, comment, or non-👀 reaction is at or after the head commit date. With no bots configured, queries select none of the bot fields.

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
