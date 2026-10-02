# prpr

`prpr` is a terminal app for monitoring open pull requests authored by a GitHub CLI account: gh's active account, or one you pin in prpr (see [GitHub accounts](#github-accounts)). It includes drafts and pulls across repositories visible to that account, and lists open pull requests that request a review from you directly in a second pane; requests to your teams, such as code-owner teams, are left out.

![prpr listing pull requests, then marking a new, a changed, and a merged pull request after a refresh](docs/demo.gif)

## Requirements

- GitHub CLI (`gh`) on `PATH`, authenticated to `github.com`. Homebrew and Scoop install it for you.
- Go 1.26 or later, only to install with `go install` or build from source. Packages and prebuilt binaries need no Go.

The app uses GitHub CLI's existing credentials and does not store tokens itself. GitHub CLI manages credential storage and may use its own plaintext fallback when no operating-system credential store is available. To connect or change accounts, use `l` from the app's authentication/error screen, or run:

```sh
gh auth login --hostname github.com --web
```

Complete the displayed device-code flow in a browser if GitHub CLI cannot open one.

## Install

### Homebrew (macOS and Linux)

```sh
brew install DragosMocrii/tap/prpr
```

### Scoop (Windows)

```powershell
scoop bucket add dragosmocrii https://github.com/DragosMocrii/homebrew-tap
scoop install prpr
```

Upgrade with `brew upgrade prpr` or `scoop update prpr`. `prpr --version` prints the installed version.

### Prebuilt binaries

Each [release](https://github.com/DragosMocrii/prpr/releases) has prebuilt archives for Linux, macOS, and Windows on amd64 and arm64, plus `checksums.txt`. Unpack the archive for your platform and put `prpr` (`prpr.exe` on Windows) on `PATH`. Run `uname -m` on macOS or Linux to choose: `arm64`/`aarch64` is arm64, and `x86_64` is amd64.

The macOS binaries are not signed or notarized, so macOS blocks a downloaded binary on first run; Homebrew installs are not affected. After checking the archive against `checksums.txt`, clear the quarantine flag:

```sh
xattr -d com.apple.quarantine prpr
```

### Install with Go

With Go 1.26 or later:

```sh
go install github.com/DragosMocrii/prpr/cmd/prpr@latest
```

Ensure Go's bin directory is on `PATH`, then run:

```sh
prpr
```

### Run from source

With Go 1.26 or later:

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

`--notify` starts with desktop notifications on; see [Notifications](#notifications).

Bot reporting costs more GraphQL quota, mostly to read review threads: about 28 points per 100 pull requests listed, against about 3 without bots.

Preferences are stored at `prpr/preferences.json` under the directory returned by Go's `os.UserConfigDir()`. On Linux, this is `$XDG_CONFIG_HOME` when it is absolute, or `$HOME/.config` otherwise. The file stores a repository or watchlist choice and the watchlists of each GitHub account, and the login of a pinned GitHub CLI account; it does not contain GitHub credentials. A new account prompts for a choice. Choosing All repositories is saved as an explicit choice. Accounts without watchlists keep the format earlier versions read; once an account saves a watchlist or an account is pinned, prpr 0.1.16 and older refuse to open the file until it is repaired, and 0.1.17 drops the pinned account the next time it saves.

If startup reports invalid preferences, back up, repair, or remove only the reported `prpr/preferences.json` file before retrying. Do not remove GitHub CLI credentials to repair app preferences.

## Controls

- `j` / Down: select the next pull request.
- `k` / Up: select the previous pull request.
- `f` / `b` / Page Down / Page Up / Space: move by a page; `d` / `u`: move by half a page; `g` / `G` / Home / End: jump to the first or last pull request.
- Left / Right: jump to the previous or next page shown in the page indicator.
- Tab / Shift+Tab: switch between **My PRs** and **Review requested**. Each list keeps its own selection and scroll position; navigation keys move only the focused list.
- `/`: search both lists; see [Search and quick filters](#search-and-quick-filters).
- `D` / `F` / `M`: show only drafts, PRs with failing CI, or PRs ready to merge; press again to show all.
- `1`–`7`: show one attention category from the summary line; press again to show all. See [Attention summary](#attention-summary).
- Esc: clear the search, quick filter, and attention category.
- Enter: show the selected pull request's details; Esc or Enter goes back. See [Details](#details).
- `?`: show or hide all key bindings.
- `p`: find and select a repository or watchlist to filter both lists; choose `All repositories` to clear the filter. See [Watchlists](#watchlists).
- `c`: clear the repository filter and show all authored open PRs and review requests.
- In the repository picker, type to search (Left/Right move within the query), Enter to apply, Esc to cancel, Ctrl+U to clear, and Ctrl+R to reload accessible repositories. Enter `owner/repo` to check a repository outside the browsed list. Space marks repositories for a watchlist, and Ctrl+E and Ctrl+D edit and delete the watchlist under the cursor.
- `r`: refresh as the pinned GitHub CLI account, or gh's active one, and restart the auto-refresh timer.
- `a`: choose the GitHub CLI account prpr uses; see [GitHub accounts](#github-accounts).
- `o`: open the selected pull request in the browser. GitHub CLI picks the browser: its `browser` setting, `GH_BROWSER`, or `BROWSER`, else the system default. With an account pinned, prpr opens the URL itself in the same order, so the browser never receives the account's token.
- `y`: copy the selected pull request's URL to the clipboard. The copy is sent as an OSC 52 terminal sequence, so it also works over SSH and in containers, but terminals without OSC 52 support, such as macOS Terminal, ignore it; copy the URL shown below the table instead.
- `x`: clear every change mark and drop gone rows (shown only while there are marks).
- `m`: turn mouse mode on or off; see [Mouse](#mouse).
- `n`: turn notifications on or off; see [Notifications](#notifications).
- `l`: start GitHub CLI login from an error screen.
- `q` / Ctrl+C: quit.

Repository filtering applies to both lists: the active account's authored open pull requests, and open pull requests requesting the account's review (`user-review-requested:@me`, which excludes requests to the account's teams). **My PRs** lists pull requests GitHub would merge now (`✓`) first, then the oldest created first; **Review requested** keeps the most recently updated first. Both show draft status. The picker browses repositories associated with the account and repositories in the current PR list; a valid `owner/repo` lookup checks other repositories through GitHub. A repository with no matching pull requests in either list remains selectable and shows empty scoped lists. Repository and All selections are saved separately for each GitHub account.

The pull request table shows draft/open state and whether GitHub would allow a merge now, branch protection included: `✓` ready (optional checks may still be failing), `●` blocked by required reviews or checks, `↓` behind the base branch, `✗` conflicts, `–` draft, and `?` not yet computed. The review list shows the PR author instead of merge status. 

Both lists also show statistics columns, which narrow terminals drop in this order: Size, Comments, Review, CI, Bots, Age.

- **Age**: how long the PR has waited. In **My PRs** it counts from when the PR was last marked ready for review, or from when it was opened if it was never a draft; drafts show `—`. In **Review requested** it counts from the latest review request naming you directly, and falls back to the ready-for-review time when that request is not found. Ages are computed when the lists load, refresh, resize, or change scope, so between refreshes they show the age as of the last update.
- **CI**: the head commit's check rollup — passing, failing, pending, or `–` when there are no checks.
- **Review**: the review decision (approved, changes requested, review required, or `–` when none applies) followed by the number of current approvals.
- **Comments**: the number of conversation comments, bots included. Code review comments are not counted.
- **Size**: lines added and removed.
- **Bots**: the most pressing state among the configured review bots. The same rule applies to every bot; severity is not read.
  - `✗n`: n review threads the bots started are unresolved and not outdated.
  - `!`: a bot's check run failed on the head commit, for example when Copilot is out of quota.
  - `◌`: a bot's check run is queued or running on the head commit.
  - `✓*`: no open threads, but a bot last acted before the head commit.
  - `✓`: no open threads, and the bots acted since the head commit with a review, a comment or comment edit, or a reaction other than 👀 on the pull request.
  - `–`: no bot has acted on the pull request.

  The line under the table lists each bot's state for the selected pull request when it fits beside the URL. Findings a bot writes only in a summary comment are not counted, and only the 20 most recent review threads are read. The head commit is dated by when it was committed, not pushed, so a commit pushed long after it was made can leave an earlier bot review showing `✓` instead of `✓*`.

### Attention summary

The line under the title counts pull requests by status, each with the number key that shows them:

```text
1 ✓ 2 ready to merge · 2 ✗ 1 changes requested · 3 ✗ 1 failing CI · 4 ✗ 1 conflicts · 5 ✗ 3 bot threads · 6 ● 3 awaiting your review · 7 ? 1 status unknown
```

1–5 cover **My PRs**: ready to merge (the green `✓`), changes requested, failing checks, conflicts, and unresolved bot threads. 6 counts every review request, and 7 counts pull requests in either list whose merge status GitHub has not computed yet. Unknown is its own category; it is never counted as ready or healthy, and null review decisions or check results are never counted as changes requested or failing. Categories with no pull requests are left out, and the numbers never change. There is no priority score.

Pressing a number shows only that category, focuses its list, and highlights it in the summary; pressing it again or Esc shows everything. It replaces the `D`/`F`/`M` quick filter and combines with search and the repository filter. Counts follow the repository filter but not the search or quick filter, and leave out gone rows. While the quick first look is loading, only conflicts are counted. Narrow terminals shorten the labels, and terminals under 14 lines drop the summary.

### Search and quick filters

`/` opens a search line in place of the status line. Both lists narrow as you type to pull requests whose title, repository, author, or `#number` contains every word typed, ignoring case: `412` and `#41` both find #412. Enter keeps the search and returns to the list; Esc while typing restores the previous search.

The quick filters show only drafts (`D`), pull requests whose checks failed (`F`), or pull requests GitHub would merge now (`M`, the same rule as the green `✓`). One is active at a time; pressing its key again turns it off. While the quick first look is loading, CI and merge states are unknown, so `F` and `M` match nothing until the details arrive.

Search and quick filters combine with the repository filter and apply to both lists, gone rows included. The title line names every active filter, list titles count shown rows against all rows in scope (`My PRs (3 of 12)`), and Esc on the list clears the search, quick filter, and attention category; `c` still clears only the repository filter. Filters are kept in memory only.

### Details

Enter opens a screen that describes the selected pull request in words: its full title, and why it can or cannot be merged, its checks, reviews, each bot's state, how long it has waited, when it was opened and updated, its size, and its comment count. On terminals at least 100 columns wide and 24 lines tall the details appear in a box over the list; smaller terminals give them the whole screen. Nothing is hidden on narrow terminals; long lines wrap.

The merge explanation names a blocker only when GitHub's data proves it. A required review or requested changes are named, since GitHub reports a review decision only when reviews are required. Failing checks are never named as the blocker, because the data does not say which checks are required; when GitHub blocks a merge for another reason, the screen says GitHub does not name the rule.

Up and Down (`k`/`j`) move to the previous or next pull request in the same list, clearing change marks as on the list. `o`, `y`, `r`, and `q` work as on the list. The screen follows the pull request across refreshes, and says so when it has left the list.

### Change marks

After each refresh, the column at the left of each list marks what changed since the previous successful refresh:

- `+`: the pull request is new in this list.
- `•`: a shown column changed; the changed cells are drawn in reverse video. Age is never compared.
- `·`: GitHub reports new activity, such as a code review comment, but no shown column changed.
- `−`: the pull request left the list (merged, closed, or the review request was withdrawn). It stays as a dimmed, struck-through row at the bottom of the list, and its link still opens it.

Marks pile up across refreshes until you look: a row's mark clears when the cursor leaves it, and a gone row is removed the same way. `x` clears them all, and is the only way to clear a list's last remaining row. Each list's title counts its new, changed, and gone rows when that fits. A failed refresh does not reset the comparison, and switching GitHub accounts starts over. Changes are kept in memory only.

### GitHub accounts

prpr follows gh's active account unless you pin one. Press `a` to list the accounts gh has stored for github.com (`gh auth status --json hosts`) and choose one; the first row, **Follow gh's active account**, unpins. A pinned account stays in use when gh's active account changes, for example after `gh auth switch` in another terminal, and the title marks it `(pinned)`. The choice is saved across runs.

prpr never stores the token. Before each fetch it reads the pinned account's token with `gh auth token --hostname github.com --user <login>`, so a new login or `gh auth refresh` of that account takes effect at the next refresh, and passes it to its own gh commands in `GH_TOKEN` only, never as an argument. It drops inherited `GH_TOKEN`, `GITHUB_TOKEN`, `GH_DEBUG`, and `DEBUG` from those commands, and never shows the token: error text has any copy of it replaced by `[redacted]`.

If the pinned account is logged out of gh or its token stops working, prpr shows that on the error screen instead of switching accounts; press `a` to choose another account or `l` to log in. It also checks once a minute whether gh has a new token for the account, reading gh's local store without contacting GitHub, and fetches again as soon as it does, so logging in again or running `gh auth refresh` in another terminal is enough. gh does not report when tokens expire; tokens from `gh auth login` do not expire unless revoked. Logging in with `l` runs `gh auth login` without the token, and makes the new account gh's active one, as it always has.

When `GH_TOKEN` or `GITHUB_TOKEN` is set in prpr's environment, gh uses that token instead of any stored account, so prpr follows it and `a` is off.

### Watchlists

A watchlist is a named group of repositories, such as "My services" or "Open source", that both lists can show instead of one repository or all of them. Watchlists are saved for each GitHub account and appear at the top of the `p` picker, marked `★`; Enter shows one, and the title names it.

- To create one, press Space on each repository in the picker. Marks stay while you change the search, and Space on `Use owner/repo (check access)` checks that repository, then marks it. Press Enter, type a name of up to 40 characters, and press Enter again to save and show the watchlist. Esc goes back to the marks; Esc on the marks clears them.
- To change one, press Ctrl+E on it: its repositories are marked, and Enter saves the marks under the name you keep or type. A new name renames it. Taking the name of another watchlist asks for a second Enter, which replaces that one.
- To delete one, press Ctrl+D on it twice. Deleting the watchlist on screen shows All repositories.

### Notifications

Notifications are off unless prpr starts with `--notify`; `n` turns them on or off for the session, and the title shows `notify` while they are on. After each refresh, prpr sends one desktop notification if, since the previous refresh:

- one of your pull requests became ready to merge (`✓`), its CI started failing, or a reviewer requested changes;
- a new review request arrived.

Only changes alert, not the current state: the first load, a switch to another GitHub account, and pull requests you just opened never do, and a failed refresh is skipped. A merge state that GitHub briefly reports as unknown is compared with the last known one. Pull requests outside the repository filter are ignored; search and quick filters are not. A notification names one pull request with its title, or the first two and a count of the rest, and the same text appears in the status line until the next key press.

Where prpr can reach your desktop, it posts the notification itself: with `osascript` on macOS, unless you are connected over SSH, and with `notify-send` on Linux when a D-Bus session bus is reachable. macOS lists these notifications under Script Editor, so allow notifications for Script Editor in System Settings if none appear. Elsewhere, such as over SSH or in a container, prpr sends an OSC 9 terminal sequence instead, which iTerm2, WezTerm, Ghostty, kitty, and Windows Terminal show as a desktop notification; inside tmux, enable `set -g allow-passthrough on`. If the desktop notifier fails, prpr reports it in the status line and uses OSC 9 for the rest of the session.

Every notification also rings the terminal bell, which is what terminals without OSC 9 support show, including the VS Code terminal in a dev container: VS Code marks the terminal tab with a bell, and plays a sound when `accessibility.signals.terminalBell` is on.

### Mouse

Mouse mode is off at start, so the terminal handles the mouse as usual. Press `m` to turn it on for the session:

- Hovering highlights the row under the pointer. It does not move the selection or clear change marks.
- Clicking a row selects it and focuses its list; clicking a list title focuses that list. Moving off a row this way clears its mark, as the keys do.
- The scroll wheel moves the selection one row in the list under the pointer.

While mouse mode is on, the terminal passes the mouse to prpr, so selecting text and clicking links need a modifier key: Shift in most terminals, Option in iTerm2. The key varies by terminal. Other screens, such as the repository picker, leave the mouse to the terminal. Press `m` again to turn it off.

When prpr starts, or after an error, it first shows both lists from a quick query while the full query runs: the Merge, Age, Bots, CI, Review, and Comments columns show `…` until the details arrive, and the corner shows "Loading details". Conflicts already show `✗`. The scope prompt and the rows can be used meanwhile. Refreshes keep the full rows on screen instead.

On short terminals only the focused list is shown. PR numbers use OSC 8 links in supporting terminals. The selected pull request URL is also shown below the table, and `o` and `y` open or copy it. A refresh replaces the visible account and both lists together; failed refreshes do not leave stale results displayed. Gone rows are the one exception: they are kept on purpose and always marked as gone.

See [contributing](CONTRIBUTING.md), the [MIT license](LICENSE), the [CI workflow](.github/workflows/ci.yml), and the [release workflow](.github/workflows/release.yml). The demo at the top is recorded with made-up data by [`docs/demo`](docs/demo/main.go).
