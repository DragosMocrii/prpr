# prpr

`prpr` is a terminal app for monitoring open pull requests authored by the active GitHub CLI account. It includes drafts and pulls across repositories visible to that account, and lists open pull requests that request a review from you directly in a second pane; requests to your teams, such as code-owner teams, are left out.

## Requirements

- Go 1.26 or later.
- GitHub CLI (`gh`) on `PATH`, authenticated to `github.com`.

The app uses GitHub CLI's existing credentials and does not store tokens itself. GitHub CLI manages credential storage and may use its own plaintext fallback when no operating-system credential store is available. To connect or change accounts, use `l` from the app's authentication/error screen, or run:

```sh
gh auth login --hostname github.com --web
```

Complete the displayed device-code flow in a browser if GitHub CLI cannot open one.

## Install

Each [release](https://github.com/DragosMocrii/prpr/releases) has prebuilt archives for Linux, macOS, and Windows on amd64 and arm64, plus `checksums.txt`. Unpack the archive for your platform and put `prpr` (`prpr.exe` on Windows) on `PATH`. Run `uname -m` on macOS or Linux to choose: `arm64`/`aarch64` is arm64, and `x86_64` is amd64.

The macOS binaries are not signed or notarized, so macOS blocks them on first run. After checking the archive against `checksums.txt`, clear the quarantine flag:

```sh
xattr -d com.apple.quarantine prpr
```

`prpr --version` prints the installed version.

### Install with Go

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

Both lists refresh automatically 5 minutes after each fetch finishes. Change the interval with `--refresh`, which takes a Go duration of at least `30s`, or turn it off with `0`:

```sh
prpr --refresh 10m
prpr --refresh 0
```

The title shows the active interval, and its top-right corner counts down to the next refresh. A manual refresh restarts the timer. Auto-refresh waits while the repository picker, the first-run repository prompt, or GitHub login is open, and retries failed fetches except authentication failures, which need `l`.

The status line shows the remaining GitHub GraphQL quota and its reset time (for example `API 4,981/5,000 · resets 06:14`), read every 10 seconds with a GraphQL query for the rate limit alone, which GitHub does not count against the quota. The quota is shared by everything using your GitHub account. It turns yellow below 20% and red below 5%, is marked `?` when the latest read failed. On narrow terminals the merge legend is dropped first, then the reset time, then the quota shortens to bare numbers and finally hides.

The **Bots** column reports automated review bots. By default it covers GitHub Copilot code review, OpenAI Codex, and Claude; `--bots` replaces that list with comma-separated `Name=login` entries, each optionally followed by `:check`, a substring of the bot's check-run names. An empty value hides the column and skips the extra GitHub fields:

```sh
prpr --bots 'Copilot=copilot-pull-request-reviewer:copilot-pull-request-reviewer,Codex=chatgpt-codex-connector,Claude=claude:Claude Code Review'
prpr --bots 'Rabbit=coderabbitai'
prpr --bots ''
```

Bot reporting costs more GraphQL quota, mostly to read review threads: about 28 points per 100 pull requests listed, against about 3 without bots.

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
- `r`: refresh using the currently active GitHub CLI account, and restart the auto-refresh timer.
- `x`: clear every change mark and drop gone rows (shown only while there are marks).
- `l`: start GitHub CLI login from an error screen.
- `q` / Ctrl+C: quit.

Repository filtering applies to both lists: the active account's authored open pull requests, and open pull requests requesting the account's review (`user-review-requested:@me`, which excludes requests to the account's teams). Both keep updated-descending order and draft status. The picker browses repositories associated with the account and repositories in the current PR list; a valid `owner/repo` lookup checks other repositories through GitHub. A repository with no matching pull requests in either list remains selectable and shows empty scoped lists. Repository and All selections are saved separately for each GitHub account.

The pull request table shows draft/open state and whether GitHub would allow a merge now, branch protection included: `✓` ready (optional checks may still be failing), `●` blocked by required reviews or checks, `↓` behind the base branch, `✗` conflicts, `–` draft, and `?` not yet computed. The review list shows the PR author instead of merge status. 

Both lists also show statistics columns, which narrow terminals drop in this order: Size, Review, CI, Bots, Age.

- **Age**: how long the PR has waited. In **My PRs** it counts from when the PR was last marked ready for review, or from when it was opened if it was never a draft; drafts show `—`. In **Review requested** it counts from the latest review request naming you directly, and falls back to the ready-for-review time when that request is not found. Ages are computed when the lists load, refresh, resize, or change scope, so between refreshes they show the age as of the last update.
- **CI**: the head commit's check rollup — passing, failing, pending, or `–` when there are no checks.
- **Review**: the review decision (approved, changes requested, review required, or `–` when none applies) followed by the number of current approvals.
- **Size**: lines added and removed.
- **Bots**: the most pressing state among the configured review bots. The same rule applies to every bot; severity is not read.
  - `✗n`: n review threads the bots started are unresolved and not outdated.
  - `!`: a bot's check run failed on the head commit, for example when Copilot is out of quota.
  - `◌`: a bot's check run is queued or running on the head commit.
  - `✓*`: no open threads, but a bot last acted before the head commit.
  - `✓`: no open threads, and the bots acted since the head commit with a review, a comment or comment edit, or a reaction other than 👀 on the pull request.
  - `–`: no bot has acted on the pull request.

  The line under the table lists each bot's state for the selected pull request when it fits beside the URL. Findings a bot writes only in a summary comment are not counted, and only the 20 most recent review threads are read. The head commit is dated by when it was committed, not pushed, so a commit pushed long after it was made can leave an earlier bot review showing `✓` instead of `✓*`.

### Change marks

After each refresh, the column at the left of each list marks what changed since the previous successful refresh:

- `+`: the pull request is new in this list.
- `•`: a shown column changed; the changed cells are drawn in reverse video. Age is never compared.
- `·`: GitHub reports new activity, such as a comment, but no shown column changed.
- `−`: the pull request left the list (merged, closed, or the review request was withdrawn). It stays as a dimmed, struck-through row at the bottom of the list, and its link still opens it.

Marks pile up across refreshes until you look: a row's mark clears when the cursor leaves it, and a gone row is removed the same way. `x` clears them all, and is the only way to clear a list's last remaining row. Each list's title counts its new, changed, and gone rows when that fits. A failed refresh does not reset the comparison, and switching GitHub accounts starts over. Changes are kept in memory only.

On short terminals only the focused list is shown. PR numbers use OSC 8 links in supporting terminals. The selected pull request URL is also shown below the table for copying. A refresh replaces the visible account and both lists together; failed refreshes do not leave stale results displayed. Gone rows are the one exception: they are kept on purpose and always marked as gone.

See [contributing](CONTRIBUTING.md), the [MIT license](LICENSE), the [CI workflow](.github/workflows/ci.yml), and the [release workflow](.github/workflows/release.yml).
