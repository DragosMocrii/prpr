# Contributing

## Setup

Install Go 1.26 or later. The application also requires GitHub CLI (`gh`) on `PATH`, authenticated to `github.com`, when you run it interactively. Tests and static checks do not need GitHub credentials or a live GitHub connection.

From the repository root:

```sh
go test ./...
go test -race ./...
go vet ./...
gofmt -l cmd internal
go build -o /tmp/prpr ./cmd/prpr
```

`gofmt -l` reports files that need formatting; format changed Go files with `gofmt -w <files>` before submitting. CI runs the format check, vet, race-enabled tests, and build.

For focused checks, use the affected package, for example:

```sh
go test ./internal/github
go test ./internal/preferences
go test ./internal/tui
```

## Interactive smoke checks

For changes to the terminal interface or GitHub-client wiring, run the built application in a Linux PTY with a fake `gh` earlier on `PATH` and a temporary absolute `XDG_CONFIG_HOME`. Stub the needed `gh auth status` and API responses; do not use real credentials or modify the developer's GitHub configuration. Exercise the changed interaction, including cancellation, empty results, or terminal resizing when relevant. A real read-only account smoke is optional; if used, preserve the existing GitHub CLI configuration with `GH_CONFIG_DIR` and avoid actions that mutate GitHub state.

## Pull requests and bug reports

Describe user-visible behavior changes and the checks or interactive scenarios you ran. Keep `go.mod` and `go.sum` consistent when changing dependencies.

Bug reports should include the operating system, terminal, Go version, GitHub CLI version when relevant, exact action or reproduction steps, and expected versus actual behavior. Include redacted error output where useful. Do not include access tokens, credential files, private pull-request links, or other private repository data.
