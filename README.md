# prpr

`prpr` is a terminal app for monitoring open pull requests authored by the active GitHub CLI account. It includes drafts and pulls across repositories visible to that account.

## Requirements

- Go 1.26 or later, or the configured devcontainer Go feature.
- GitHub CLI (`gh`) on `PATH`, authenticated to `github.com`.

The app uses GitHub CLI's existing credentials and does not store tokens itself. GitHub CLI manages credential storage and may use its own plaintext fallback when no operating-system credential store is available. To connect or change accounts, use `l` from the app's authentication/error screen, or run:

```sh
gh auth login --hostname github.com --web
```

Complete the displayed device-code flow in a browser if GitHub CLI cannot open one. The devcontainer intentionally disables automatic browser launch.

## Run

From the repository root:

```sh
go run ./cmd/prpr
```

Build a binary or install it into Go's bin directory with:

```sh
go build -o /tmp/prpr ./cmd/prpr
go install ./cmd/prpr
```

## Controls

- `j` / Down: select the next pull request.
- `k` / Up: select the previous pull request.
- `p`: find and select a repository to filter the authored open PR list; choose `All repositories` to clear the filter.
- `c`: clear the repository filter and show all authored open PRs.
- In the repository picker, type to search, Enter to apply, Esc to cancel, Ctrl+U to clear, and Ctrl+R to reload accessible repositories. Enter `owner/repo` to check a repository outside the browsed list.
- `r`: refresh using the currently active GitHub CLI account.
- `l`: start GitHub CLI login from an error screen.
- `q` / Ctrl+C: quit.

Repository filtering affects only the active account's authored open pull requests and preserves their updated-descending order and draft status. The picker browses repositories associated with the account and repositories in the current PR list; a valid `owner/repo` lookup checks other repositories through GitHub. A repository with no authored open PRs remains selectable and shows an empty scoped list. Repository and All selections are saved separately for each GitHub account in the user configuration directory.

The pull request table shows draft/open state and merge-conflict status (`MERGEABLE`, `CONFLICTING`, or unknown); mergeability does not represent checks or review readiness. PR numbers use OSC 8 links in supporting terminals. The selected pull request URL is also shown below the table for copying. A refresh replaces the visible account and list together; failed refreshes do not leave stale results displayed.
