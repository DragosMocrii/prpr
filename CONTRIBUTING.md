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

## Releases

Pushing a version tag such as `v0.1.0` runs the release workflow. It repeats vet and race-enabled tests, builds Linux, macOS, and Windows archives for amd64 and arm64 with the tag stamped into `prpr --version`, and publishes them with `checksums.txt` as a GitHub Release with generated notes. Tags with a suffix, such as `v0.2.0-rc.1`, become prereleases.

```sh
git tag v0.1.0
git push origin v0.1.0
```

To rebuild an existing tag, run the workflow manually with that tag; it replaces the release's archives.

After publishing a non-prerelease, the workflow writes the Homebrew formula (`Formula/prpr.rb`) and Scoop manifest (`bucket/prpr.json`) to the owner's `homebrew-tap` repository with [`.github/scripts/package-manifests.sh`](.github/scripts/package-manifests.sh). Set it up once:

1. Create a public `homebrew-tap` repository under the same owner, initialized with a README so it has a default branch.
2. Create a fine-grained token with read and write access to that repository's contents only.
3. Save the token as the `TAP_GITHUB_TOKEN` secret of this repository.

Without the secret the step is skipped. A manual run for an older tag leaves a newer published version in place. Tags made before the script existed cannot fill the tap, because the workflow runs the script from the tag's own checkout.

## Demo recording

`docs/demo.gif` and `docs/demo-nerd.gif` are recorded by a separate Go module in `docs/demo`, which runs the app in a pseudo-terminal against a fake `gh` with made-up pull requests. Re-record both after visible interface changes:

```sh
cd docs/demo
FONT=/path/to/JetBrainsMonoNerdFontMono
go run . -font $FONT-Regular.ttf -bold $FONT-Bold.ttf -emoji /path/to/noto-emoji/2D/png/72
go run . -font $FONT-Regular.ttf -bold $FONT-Bold.ttf -emoji /path/to/noto-emoji/2D/png/72 -icons nerd -o ../demo-nerd.gif
```

`-icons nerd` saves `"icons": "nerd"` in the run's temporary preferences.

The recordings use [JetBrains Mono Nerd Font](https://www.nerdfonts.com/font-downloads) (the Mono variant, which keeps icons one cell wide), with DejaVu Sans (`fonts-dejavu-core` on Debian and Ubuntu) for glyphs it lacks, such as the braille spinner; `-fallback` takes another path. Without `-font` and `-bold`, DejaVu Sans Mono is used, which has no Nerd Font icons. No font here draws the nudge emoji (👋 🔔 🚨) in color, so `-emoji` takes a directory of [Noto Emoji](https://github.com/googlefonts/noto-emoji) images (`2D/png/72`, named such as `emoji_u1f6a8.png`); without it, they are boxes. Keep the recording free of real accounts, repositories, and pull requests. The fake `gh` tells queries apart by text they contain (`rateLimit`, `reviewed-by:@me`, `mentions:@me`, which gh reads as one page without `--slurp`, so it answers one object rather than a list, `combinedSlug`, which only `R`'s reviewer query selects, the `POST` to `/comments` that sends a nudge, `search(`, `states: [MERGED]`, which only the merged query selects, and `mergeStateStatus`, which only the full query selects), so update it when those queries change.

## Pull requests and bug reports

Describe user-visible behavior changes and the checks or interactive scenarios you ran. Keep `go.mod` and `go.sum` consistent when changing dependencies.

Bug reports should include the operating system, terminal, Go version, GitHub CLI version when relevant, exact action or reproduction steps, and expected versus actual behavior. Include redacted error output where useful. Do not include access tokens, credential files, private pull-request links, or other private repository data.
