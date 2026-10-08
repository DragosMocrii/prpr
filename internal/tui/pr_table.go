package tui

import (
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/table"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

// rebuildPRTable rebuilds both panes' tables for the current layout.
func (m *model) rebuildPRTable(resetSelection bool) {
	// An emptied queue pane is no longer drawn, nor a Merged pane the
	// terminal has no room for, nor a collapsed one, so none can keep the focus.
	if focusable := m.framePlan().focusable(m); !slices.Contains(focusable, m.focus) {
		m.focus = paneMine
		for _, id := range focusable {
			if len(m.panes[id].visible) > 0 {
				m.focus = id
				break
			}
		}
	}
	m.sharedRows = m.shared()
	// Planned after the focus fix-up: single-pane mode follows the focus.
	plan := m.framePlan()
	m.builtDrawn = plan.drawn
	for _, id := range paneIDs {
		// Hidden and empty panes keep a minimal table so cursors survive.
		m.rebuildPane(id, max(minTableHeight, plan.tables[id]), resetSelection)
	}
}

func (m *model) rebuildPane(id paneID, height int, resetSelection bool) {
	pane := &m.panes[id]
	previous := 0
	if !resetSelection {
		previous = pane.table.Cursor()
	}
	layout := m.layoutColumns(id)
	pane.fits = layout.fits
	pane.table = table.New(
		table.WithStyles(tableStyles(id == m.focus, m.darkBackground)),
		table.WithColumns(layout.tableColumns(m.icons)),
		table.WithRows(m.buildRows(id, layout)),
		table.WithWidth(max(1, m.width)),
		table.WithHeight(height),
		table.WithFocused(id == m.focus),
		table.WithKeyMap(m.keys.Table),
	)
	if !resetSelection && rowCount(pane) > 0 {
		moveCursor(&pane.table, previous)
	}
	m.syncPages(id)
}

// redrawRows replaces a pane's rows in place after its marks or gone rows
// change, keeping the table's cursor and scroll position. When the change
// moves the frame plan (a legend entry that goes, say), every table is
// rebuilt for it instead.
func (m *model) redrawRows(id paneID) {
	pane := &m.panes[id]
	layout := m.layoutColumns(id)
	pane.fits = layout.fits
	pane.table.SetColumns(layout.tableColumns(m.icons))
	pane.table.SetRows(m.buildRows(id, layout))
	m.syncPages(id)
	if !m.tablesFollow(m.framePlan()) {
		m.rebuildPRTable(false)
	}
}

// tablesFollow reports whether the built tables are the ones plan draws:
// the same panes, each on screen with rows at its planned height.
func (m *model) tablesFollow(plan framePlan) bool {
	if !slices.Equal(plan.drawn, m.builtDrawn) {
		return false
	}
	for _, p := range plan.screen {
		pane := &m.panes[p.id]
		if rowCount(pane) > 0 && tableHeaderLen+pane.table.Height() != max(minTableHeight, plan.tables[p.id]) {
			return false
		}
	}
	return true
}

// nameTagSeparator follows a tag in front of a pull request's name.
func nameTagSeparator(ic *iconSet) string {
	if ic.nerd {
		return " "
	}
	return " · "
}

// Change-mark styles. Each is turned back on after the resets inside a cell.
const (
	reverseOn  = "\x1b[7m"
	reverseOff = "\x1b[27m"
	// underlineOff ends underlineOn.
	underlineOff = "\x1b[24;59m"
	goneOn       = "\x1b[2;9m"
	goneOff      = "\x1b[22;29m"
	// waitingOn marks a reviewed pull request that waits on someone else.
	waitingOn  = "\x1b[2;3m"
	waitingOff = "\x1b[22;23m"
	// dimOn draws the Snoozed pane's rows.
	dimOn  = "\x1b[2m"
	dimOff = "\x1b[22m"
)

// reviewStatusTag names where a reviewed pull request stands in a word or
// two, or an icon in the Nerd set, colored when it needs the viewer again;
// it is empty for a pending review request.
func reviewStatusTag(ic *iconSet, status github.ReviewStatus, waiting bool) string {
	text, icon := "", ""
	switch status {
	case github.ReviewNewCommits:
		text, icon = "new commits", ic.newCommits
	case github.ReviewAuthorReplied:
		text, icon = "replied", ic.replied
	case github.ReviewDismissed:
		text, icon = "dismissed", ic.dismissed
	case github.ReviewNewActivity:
		text, icon = "activity", ic.activity
	case github.ReviewWaitingOnAuthor:
		text, icon = "waiting", ic.waiting
	case github.ReviewApproved:
		text, icon = "approved", ic.approved
	case github.ReviewBackInDraft:
		text, icon = "waiting", ic.backInDraft
	case github.ReviewNudged:
		text, icon = "nudged", ic.bell
	default:
		return ""
	}
	if ic.nerd {
		text = icon
	}
	if waiting {
		return text
	}
	return coloredIcon(text, "3")
}

// queueText draws a queue state: a colored word, or the Nerd icon.
func queueText(ic *iconSet, state github.QueueState) string {
	word, icon, color := "?", ic.unknown, "3"
	switch state {
	case github.QueueSubmitted:
		word, icon, color = "submitted", ic.queueSubmitted, ""
	case github.QueueQueued:
		word, icon, color = "queued", ic.queueQueued, ""
	case github.QueueTesting:
		word, icon, color = "testing", ic.queueTesting, "3"
	case github.QueueFailing:
		word, icon, color = "failing", ic.queueFailing, "1"
	case github.QueuePassed:
		word, icon, color = "passed", ic.queuePassed, "2"
	}
	text := word
	if ic.nerd && icon != "" {
		text = icon
	}
	if color == "" {
		return text
	}
	return coloredIcon(text, color)
}

// queueState is a pull request's queue state, zero without an entry.
func queueState(pr *github.PullRequest) github.QueueState {
	if pr.Queue == nil {
		return 0
	}
	return pr.Queue.State
}

// queueRank orders the queue pane: furthest along first.
func queueRank(state github.QueueState) int {
	switch state {
	case github.QueuePassed:
		return 0
	case github.QueueFailing:
		return 1
	case github.QueueTesting:
		return 2
	case github.QueueQueued:
		return 3
	case github.QueueSubmitted:
		return 4
	}
	return 5
}

// removedQueueTag marks a pull request the queue removed, shown before the
// name in My PRs: red when it failed, faint when it was canceled.
func removedQueueTag(ic *iconSet, state github.QueueState) string {
	text, color := "", ""
	switch state {
	case github.QueueRemovedFailed:
		text, color = "queue failed", "1"
	case github.QueueRemovedCanceled:
		text = "queue canceled"
	default:
		return ""
	}
	if ic.nerd {
		text = ic.queueRemoved
	}
	if color == "" {
		return lipgloss.NewStyle().Faint(true).Render(text)
	}
	return coloredIcon(text, color)
}

// wokeTag marks a pull request that woke from a snooze, shown before the
// name: yellow, as words in the Unicode set and as an icon and the reason in
// the Nerd set.
func wokeTag(ic *iconSet, reason string) string {
	if ic.nerd {
		return coloredIcon(ic.woke+" "+reason, "3")
	}
	return coloredIcon("woke: "+reason, "3")
}

// reviewStatusText names where a reviewed pull request stands, for
// notifications; it is empty for a pending review request.
func reviewStatusText(status github.ReviewStatus) string {
	switch status {
	case github.ReviewNewCommits:
		return "new commits"
	case github.ReviewAuthorReplied:
		return "author replied"
	case github.ReviewDismissed:
		return "review dismissed"
	case github.ReviewNewActivity:
		return "new activity"
	case github.ReviewWaitingOnAuthor:
		return "waiting on author"
	case github.ReviewApproved:
		return "you approved"
	case github.ReviewBackInDraft:
		return "back in draft"
	case github.ReviewNudged:
		return "nudged"
	default:
		return ""
	}
}

// pendingText fills a cell that a preview does not know yet.
var pendingText = lipgloss.NewStyle().Faint(true).Render("…")

// markText is a row's change marker. A changed row's marker shows its
// direction.
func markText(kind markKind, dir direction, gone bool) string {
	switch {
	case gone:
		return lipgloss.NewStyle().Faint(true).Render("−")
	case kind == markNew:
		return coloredIcon("+", "2")
	case kind == markChanged && dir == dirGood:
		return coloredIcon("▲", directionColor(dir))
	case kind == markChanged && dir == dirBad:
		return coloredIcon("▼", directionColor(dir))
	case kind == markChanged:
		return coloredIcon("•", directionColor(dir))
	case kind == markActivity:
		return lipgloss.NewStyle().Faint(true).Render("·")
	default:
		return " "
	}
}

// directionColor is the ANSI color of a change's direction.
func directionColor(dir direction) string {
	switch dir {
	case dirGood:
		return "2"
	case dirBad:
		return "1"
	default:
		return "3"
	}
}

// underlineOn underlines a changed cell in its direction's color.
func underlineOn(dir direction) string {
	return "\x1b[4;58;5;" + directionColor(dir) + "m"
}

// restyle applies on to the whole of text, turning it back on after every
// reset inside text.
func restyle(text, on, off string) string {
	for _, reset := range []string{"\x1b[m", "\x1b[0m"} {
		text = strings.ReplaceAll(text, reset, reset+on)
	}
	return on + text + off
}

// minStatsNameWidth is the PR name width that ranked columns may not
// squeeze below.
const minStatsNameWidth = 16

// remainingWidth is the width left for the zero-width name column; each
// column has one cell of padding on both sides.
func remainingWidth(width int, columns []table.Column) int {
	for _, column := range columns {
		width -= column.Width + 2
	}
	return width
}

// ageText formats how long a pull request has waited; zero means not waiting.
func ageText(since, now time.Time) string {
	if since.IsZero() {
		return "—"
	}
	elapsed := now.Sub(since)
	const day = 24 * time.Hour
	switch {
	case elapsed < time.Hour:
		return "<1h"
	case elapsed < day:
		return strconv.Itoa(int(elapsed/time.Hour)) + "h"
	case elapsed < 14*day:
		return strconv.Itoa(int(elapsed/day)) + "d"
	case elapsed < 63*day:
		return strconv.Itoa(int(elapsed/(7*day))) + "w"
	case elapsed < 365*day:
		return strconv.Itoa(int(elapsed/(30*day))) + "mo"
	default:
		return strconv.Itoa(int(elapsed/(365*day))) + "y"
	}
}

func checksIcon(ic *iconSet, state string) string {
	switch state {
	case "SUCCESS":
		return coloredIcon(ic.passed, "2")
	case "FAILURE", "ERROR":
		return coloredIcon(ic.failed, "1")
	case "PENDING", "EXPECTED":
		return coloredIcon(ic.pending, "3")
	default:
		return ic.none
	}
}

// reviewText shows the review decision followed by the current approval count.
func reviewText(ic *iconSet, decision string, approvals int) string {
	var icon string
	switch decision {
	case "APPROVED":
		icon = coloredIcon(ic.passed, "2")
	case "CHANGES_REQUESTED":
		icon = coloredIcon(ic.failed, "1")
	case "REVIEW_REQUIRED":
		icon = coloredIcon(ic.pending, "3")
	default:
		icon = ic.none
	}
	if approvals > 0 {
		icon += ic.gap + strconv.Itoa(approvals)
	}
	return icon
}

// botsText shows the most attention-worthy bot state; concerns are summed
// across bots.
func botsText(ic *iconSet, reviews []github.BotReview) string {
	return botStateText(ic, botsWorst(reviews))
}

func botStateText(ic *iconSet, review github.BotReview) string {
	switch review.State {
	case github.BotConcerns:
		return coloredIcon(ic.failed+ic.gap+strconv.Itoa(review.Concerns), "1")
	case github.BotFailed:
		return coloredIcon(ic.botFailed, "1")
	case github.BotRunning:
		return coloredIcon(ic.botRunning, "3")
	case github.BotStale:
		return coloredIcon(ic.passed, "2") + ic.gap + "*"
	case github.BotPassed:
		return coloredIcon(ic.passed, "2")
	default:
		return ic.none
	}
}

// botBreakdown lists each configured bot's state for the selected PR.
func botBreakdown(ic *iconSet, reviews []github.BotReview) string {
	parts := make([]string, len(reviews))
	for i, review := range reviews {
		parts[i] = singleLine(review.Name) + " " + botStateText(ic, review)
	}
	return strings.Join(parts, " · ")
}

func sizeText(additions, deletions int) string {
	return "+" + compactCount(additions) + "/-" + compactCount(deletions)
}

func compactCount(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 10000:
		return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + "k"
	default:
		return strconv.Itoa(n/1000) + "k"
	}
}

func coloredIcon(icon, color string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(icon)
}

func prNumberLink(number int, rawURL string) string {
	label := lipgloss.NewStyle().Underline(true).Render("#" + strconv.Itoa(number))
	if !safePullRequestURL(rawURL) {
		return label
	}
	return ansi.SetHyperlink(rawURL) + label + ansi.ResetHyperlink()
}

func safePullRequestURL(rawURL string) bool {
	if rawURL == "" {
		return false
	}
	if strings.ContainsFunc(rawURL, unicode.IsControl) {
		return false
	}
	parsed, err := url.Parse(rawURL)
	return err == nil && strings.EqualFold(parsed.Scheme, "https") &&
		strings.EqualFold(parsed.Hostname(), "github.com") && parsed.Port() == "" &&
		parsed.User == nil && strings.EqualFold(parsed.Host, parsed.Hostname())
}

// mergeReady reports whether GitHub would merge the pull request now, branch
// protection included. Conflicts win over every other state, and drafts are
// read from isDraft, since GitHub deprecated the DRAFT merge state.
func mergeReady(draft bool, mergeable, state string) bool {
	if mergeable == "CONFLICTING" || state == "DIRTY" || draft {
		return false
	}
	return state == "CLEAN" || state == "HAS_HOOKS" || state == "UNSTABLE"
}

// mergeIcon draws GitHub's merge state. ready is the rules' answer: green
// when they hold, and yellow when only GitHub would merge.
func mergeIcon(ic *iconSet, draft bool, mergeable, state string, ready bool) string {
	if ready {
		return coloredIcon(ic.check, "2")
	}
	if mergeReady(draft, mergeable, state) {
		return coloredIcon(ic.check, "3")
	}
	if mergeable == "CONFLICTING" || state == "DIRTY" {
		return coloredIcon(ic.cross, "1")
	}
	if draft {
		return ic.none
	}
	switch state {
	case "BLOCKED":
		return coloredIcon(ic.pending, "3")
	case "BEHIND":
		return coloredIcon(ic.behind, "3")
	default:
		return coloredIcon(ic.unknown, "3")
	}
}
