# prpr

`prpr` is a terminal app for monitoring open pull requests authored by the active GitHub CLI account. It includes drafts and pulls across repositories visible to that account, and lists open pull requests that request a review from you or one of your teams in a second pane.

## Requirements

- Go 1.26 or later.
- GitHub CLI (`gh`) on `PATH`, authenticated to `github.com`.

The app uses GitHub CLI's existing credentials and does not store tokens itself. GitHub CLI manages credential storage and may use its own plaintext fallback when no operating-system credential store is available. To connect or change accounts, use `l` from the app's authentication/error screen, or run:

```sh
gh auth login --hostname github.com --web
```

Complete the displayed device-code flow in a browser if GitHub CLI cannot open one.

## Install

After this repository is public and these changes have been pushed, install the latest version with:

```sh
go install github.com/DragosMocrii/prpr/cmd/prpr@latest
```

Ensure Go's bin directory is on `PATH`, then run:

```sh
prpr
```

Until then, install from a source checkout using the instructions below.

## Run from source

```sh
git clone https://github.com/DragosMocrii/prpr.git
cd prpr
go run ./cmd/prpr
```

Build a binary or install the checkout into Go's bin directory with:

```sh
go build -o /tmp/prpr ./cmd/prpr
go install ./cmd/prpr
```

## Configuration

Preferences are stored at `prpr/preferences.json` under the directory returned by Go's `os.UserConfigDir()`. On Linux, this is `$XDG_CONFIG_HOME` when it is absolute, or `$HOME/.config` otherwise. The file stores a repository choice per GitHub account; it does not contain GitHub credentials. A new account prompts for a choice. Choosing All repositories is saved as an explicit choice.

If startup reports invalid preferences, back up, repair, or remove only the reported `prpr/preferences.json` file before retrying. Do not remove GitHub CLI credentials to repair app preferences.

## Controls

- `j` / Down: select the next pull request.
- `k` / Up: select the previous pull request.
- `f` / `b` / Page Down / Page Up / Space: move by a page; `d` / `u`: move by half a page; `g` / `G` / Home / End: jump to the first or last pull request.
- Left / Right: jump to the previous or next page shown in the page indicator.
- Tab / Shift+Tab: switch between **My PRs** and **Review requested**. Each list keeps its own selection and scroll position; navigation keys move only the focused list.
- `?`: show or hide all key bindings.
- `p`: find and select a repository to filter both lists; choose `All repositories` to clear the filter.
- `c`: clear the repository filter and show all authored open PRs and review requests.
- In the repository picker, type to search (Left/Right move within the query), Enter to apply, Esc to cancel, Ctrl+U to clear, and Ctrl+R to reload accessible repositories. Enter `owner/repo` to check a repository outside the browsed list.
- `r`: refresh using the currently active GitHub CLI account.
- `l`: start GitHub CLI login from an error screen.
- `q` / Ctrl+C: quit.

Repository filtering applies to both lists: the active account's authored open pull requests, and open pull requests requesting the account's review (`review-requested:@me`, including team requests). Both keep updated-descending order and draft status. The picker browses repositories associated with the account and repositories in the current PR list; a valid `owner/repo` lookup checks other repositories through GitHub. A repository with no matching pull requests in either list remains selectable and shows empty scoped lists. Repository and All selections are saved separately for each GitHub account.

The pull request table shows draft/open state and merge-conflict status (`MERGEABLE`, `CONFLICTING`, or unknown); mergeability does not represent checks or review readiness. The review list shows the PR author instead of merge status. On short terminals only the focused list is shown. PR numbers use OSC 8 links in supporting terminals. The selected pull request URL is also shown below the table for copying. A refresh replaces the visible account and both lists together; failed refreshes do not leave stale results displayed.

See [contributing](CONTRIBUTING.md), the [MIT license](LICENSE), and the [CI workflow](.github/workflows/ci.yml).
