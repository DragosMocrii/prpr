package tui

import (
	"fmt"
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
	layout := m.paneLayout(id)
	pane.fits = layout.fits
	pane.table = table.New(
		table.WithStyles(tableStyles(id == m.focus, m.darkBackground)),
		table.WithColumns(layout.columns),
		table.WithRows(m.paneRows(id, layout)),
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
// change, keeping the table's cursor and scroll position.
func (m *model) redrawRows(id paneID) {
	pane := &m.panes[id]
	layout := m.paneLayout(id)
	pane.fits = layout.fits
	pane.table.SetColumns(layout.columns)
	pane.table.SetRows(m.paneRows(id, layout))
	m.syncPages(id)
}

// tableLayout is a pane's columns for the current width and scope.
type tableLayout struct {
	columns          []table.Column
	repositoryColumn bool
	// shared is what every row has in common, which the tables leave out.
	shared   sharedRepositories
	stats    []statColumn
	detail   bool // the queue pane's Detail column is shown
	from     bool // the Snoozed pane's From column is shown
	mergedBy bool // the Merged pane's Merged by column is shown
	plead    bool // the mark column is wide enough for the asked-again marker
	fits     bool
}

func (m *model) paneLayout(id paneID) tableLayout {
	pane := &m.panes[id]
	review := id == paneReview
	// Number, Repository, and Author are as wide as their longest value.
	maxNumberWidth, maxRepository, maxAuthor, maxMergedBy := 6, 0, 0, 0
	shared := m.sharedRows
	for row := range rowCount(pane) {
		if pr, _, ok := m.paneRow(id, row); ok {
			maxNumberWidth = max(maxNumberWidth, ansi.StringWidth(fmt.Sprintf("#%d", pr.Number)))
			maxRepository = max(maxRepository, ansi.StringWidth(shared.repositoryText(pr)))
			maxAuthor = max(maxAuthor, ansi.StringWidth(singleLine(pr.Author)))
			maxMergedBy = max(maxMergedBy, ansi.StringWidth(singleLine(pr.MergedBy)))
		}
	}
	width := max(1, m.width)
	layout := tableLayout{shared: shared}
	// One repository shared by every row is named in the title instead.
	layout.repositoryColumn = m.selectedRepository == "" && shared.repository == "" && width >= 80
	repositoryWidth := 0
	if layout.repositoryColumn {
		repositoryWidth = min(28, max(12, width/4), max(len("Repository"), maxRepository))
	}
	ic := m.icons
	if id == paneQueue {
		queueWidth := 9
		if ic.nerd {
			queueWidth = 1
		}
		columns := []table.Column{{Title: "", Width: 1}, {Title: ic.header("Queue"), Width: queueWidth}}
		if layout.repositoryColumn {
			columns = append(columns, table.Column{Title: "Repository", Width: repositoryWidth})
		}
		columns = append(columns, table.Column{Title: "Number", Width: maxNumberWidth})
		nameColumn := len(columns)
		columns = append(columns, table.Column{Title: "PR name"})
		// Like statistics columns, Detail stays only while the name keeps room.
		withDetail := append(slices.Clone(columns), table.Column{Title: "Detail", Width: min(24, max(10, width/6))})
		if remainingWidth(width, withDetail) >= minStatsNameWidth {
			columns, layout.detail = withDetail, true
		}
		nameWidth := remainingWidth(width, columns)
		layout.fits = nameWidth >= 8
		columns[nameColumn].Width = max(8, nameWidth)
		layout.columns = columns
		return layout
	}
	if id == paneSnoozed {
		columns := []table.Column{{Title: "", Width: 1}, {Title: ic.header("Wakes"), Width: 9}}
		if layout.repositoryColumn {
			columns = append(columns, table.Column{Title: "Repository", Width: repositoryWidth})
		}
		columns = append(columns, table.Column{Title: "Number", Width: maxNumberWidth})
		nameColumn := len(columns)
		columns = append(columns, table.Column{Title: "PR name"})
		// Like the queue's Detail, From stays only while the name keeps room.
		withFrom := append(slices.Clone(columns), table.Column{Title: "From", Width: 12})
		if remainingWidth(width, withFrom) >= minStatsNameWidth {
			columns, layout.from = withFrom, true
		}
		nameWidth := remainingWidth(width, columns)
		layout.fits = nameWidth >= 8
		columns[nameColumn].Width = max(8, nameWidth)
		layout.columns = columns
		return layout
	}
	if id == paneMerged {
		columns := []table.Column{{Title: "", Width: 1}, {Title: ic.header("Merged"), Width: 6}}
		if layout.repositoryColumn {
			columns = append(columns, table.Column{Title: "Repository", Width: repositoryWidth})
		}
		columns = append(columns, table.Column{Title: "Number", Width: maxNumberWidth})
		nameColumn := len(columns)
		columns = append(columns, table.Column{Title: "PR name"})
		// Like the queue's Detail, Merged by stays only while the name keeps room.
		withBy := append(slices.Clone(columns), table.Column{Title: "Merged by", Width: min(16, max(len("Merged by"), maxMergedBy))})
		if remainingWidth(width, withBy) >= minStatsNameWidth {
			columns, layout.mergedBy = withBy, true
		}
		nameWidth := remainingWidth(width, columns)
		layout.fits = nameWidth >= 8
		columns[nameColumn].Width = max(8, nameWidth)
		layout.columns = columns
		return layout
	}
	lastTitle, lastWidth := ic.header("Merge"), 5
	if ic.nerd {
		lastWidth = 1
	}
	if review {
		lastTitle, lastWidth = "Author", min(16, max(8, maxAuthor))
	}
	columns := make([]table.Column, 0, 6+len(statColumns))
	// The mark column widens for the asked-again marker while one is shown.
	markWidth := 1
	if review && m.pleadShown() {
		layout.plead, markWidth = true, pleadWidth
	}
	columns = append(columns, table.Column{Title: "", Width: markWidth})
	// My PRs leads with Merge, so readiness is seen first; the review list
	// ends with its author instead.
	if !review {
		columns = append(columns, table.Column{Title: lastTitle, Width: lastWidth})
	}
	if layout.repositoryColumn {
		columns = append(columns, table.Column{Title: "Repository", Width: repositoryWidth})
	}
	columns = append(columns, table.Column{Title: "Number", Width: maxNumberWidth})
	nameColumn := len(columns)
	columns = append(columns, table.Column{Title: "PR name"})
	if review {
		columns = append(columns, table.Column{Title: lastTitle, Width: lastWidth})
	}
	// Statistics columns are kept in priority order while the name keeps room,
	// then drawn in their usual order.
	kept := make(map[changedCells]bool)
	withStats := slices.Clone(columns)
	for _, stat := range paneStatColumns(id) {
		if stat.bots && !m.bots {
			continue
		}
		candidate := append(withStats, table.Column{Width: stat.columnWidth(ic)})
		if remainingWidth(width, candidate) < minStatsNameWidth {
			break
		}
		withStats, kept[stat.cell] = candidate, true
	}
	for _, stat := range statColumns {
		if kept[stat.cell] {
			columns = append(columns, table.Column{Title: ic.header(stat.title), Width: stat.columnWidth(ic)})
			layout.stats = append(layout.stats, stat)
		}
	}
	nameWidth := remainingWidth(width, columns)
	layout.fits = nameWidth >= 8
	columns[nameColumn].Width = max(8, nameWidth)
	layout.columns = columns
	return layout
}

// nameTagSeparator follows a tag in front of a pull request's name.
func nameTagSeparator(ic *iconSet) string {
	if ic.nerd {
		return " "
	}
	return " · "
}

// paneStatColumns is statColumns in a pane's priority order: Review
// requested gives up Review first, since a pending request mostly shows it
// as required.
func paneStatColumns(id paneID) []statColumn {
	if id != paneReview {
		return statColumns
	}
	stats := make([]statColumn, 0, len(statColumns))
	var review []statColumn
	for _, stat := range statColumns {
		if stat.cell == cellReview {
			review = append(review, stat)
			continue
		}
		stats = append(stats, stat)
	}
	return append(stats, review...)
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

func (m *model) paneRows(id paneID, layout tableLayout) []table.Row {
	pane := &m.panes[id]
	review := id == paneReview
	shared := layout.shared
	all := m.selectedRepository == "" && shared.repository == ""
	now := m.now()
	ic := m.icons
	rows := make([]table.Row, 0, rowCount(pane))
	for row := range rowCount(pane) {
		pr, gone, ok := m.paneRow(id, row)
		if !ok {
			continue
		}
		mark := m.rowMark(id, pr)
		var lines []changeLine
		if !gone {
			lines = m.changeLines(id, pr)
		}
		markCell := markText(mark.kind, rowDirection(lines), gone)
		changed := func(cell changedCells, text string) string {
			if mark.kind == markChanged && mark.cells&cell != 0 {
				return restyle(text, underlineOn(cellDirection(lines, cell)), underlineOff)
			}
			return text
		}
		name := singleLine(pr.Title)
		if all && !layout.repositoryColumn {
			name = shared.repositoryText(pr) + " — " + name
		}
		// A review row back in draft already says so in its status tag.
		if pr.Draft && pr.ReviewStatus != github.ReviewBackInDraft {
			name = ic.draftTag() + nameTagSeparator(ic) + name
		}
		if review {
			if status := reviewStatusTag(ic, pr.ReviewStatus); status != "" && ic.nerd {
				name = status + " " + name
			} else if status != "" {
				name = status + " · " + name
			}
		}
		// The name carries the draft tag, so it stands for State too.
		nameCells := cellName | cellState
		if id == paneMine {
			// The name stands for the queue in My PRs, even once the entry is gone.
			nameCells |= cellQueue
			if tag := removedQueueTag(ic, queueState(pr)); tag != "" && ic.nerd {
				name = tag + " " + name
			} else if tag != "" {
				name = tag + " · " + name
			}
		}
		// Merged rows are never gone: falling out of the last N is not news.
		if id == paneMerged {
			cells := table.Row{markCell, ageText(pr.MergedAt, now)}
			if layout.repositoryColumn {
				cells = append(cells, shared.repositoryText(pr))
			}
			cells = append(cells, prNumberLink(pr.Number, pr.URL), changed(cellName, name))
			if layout.mergedBy {
				cells = append(cells, singleLine(pr.MergedBy))
			}
			rows = append(rows, cells)
			continue
		}
		if reason, ok := m.woke[keyOf(pr)]; ok && id != paneSnoozed {
			name = wokeTag(ic, singleLine(reason)) + " · " + name
		}
		if id == paneSnoozed {
			rows = append(rows, m.snoozedRow(pr, gone, layout, markCell, changed(cellName, name), now))
			continue
		}
		if id == paneQueue {
			cells := table.Row{markCell, changed(cellQueue, queueText(ic, pr.Queue.State))}
			if layout.repositoryColumn {
				cells = append(cells, shared.repositoryText(pr))
			}
			cells = append(cells, prNumberLink(pr.Number, pr.URL), changed(cellName, name))
			if layout.detail {
				cells = append(cells, changed(cellQueue, singleLine(pr.Queue.Detail)))
			}
			if gone {
				for i := 1; i < len(cells); i++ {
					cells[i] = restyle(cells[i], goneOn, goneOff)
				}
			}
			rows = append(rows, cells)
			continue
		}
		// A preview has no merge state, but conflicts are already known.
		preview := m.snapshot.Preview && !gone
		last, lastCell := m.mergeCell(pr), cellMerge
		if preview && pr.Mergeable != "CONFLICTING" {
			last = pendingText
		}
		if review {
			last, lastCell = singleLine(pr.Author), cellAuthor
		}
		cells := make(table.Row, 0, len(layout.columns))
		cells = append(cells, markCell)
		if !review {
			cells = append(cells, changed(lastCell, last))
		}
		if layout.repositoryColumn {
			cells = append(cells, shared.repositoryText(pr))
		}
		cells = append(cells, prNumberLink(pr.Number, pr.URL), changed(nameCells, name))
		if review {
			cells = append(cells, changed(lastCell, last))
		}
		for _, stat := range layout.stats {
			if preview && stat.detail {
				cells = append(cells, pendingText)
				continue
			}
			cells = append(cells, changed(stat.cell, stat.text(ic, pr, now)))
		}
		switch {
		case gone:
			for i := 1; i < len(cells); i++ {
				cells[i] = restyle(cells[i], goneOn, goneOff)
			}
		case review && pr.ReviewStatus.Waiting():
			for i := 1; i < len(cells); i++ {
				cells[i] = restyle(cells[i], waitingOn, waitingOff)
			}
		}
		if layout.plead {
			cells[0] = m.pleadMark(pr, gone, markCell)
		}
		rows = append(rows, cells)
	}
	return rows
}

// snoozedRow is a Snoozed pane row: drawn dim, or struck through when gone.
// A snooze deleted when its pull request closed has no wake time.
func (m *model) snoozedRow(pr *github.PullRequest, gone bool, layout tableLayout, markCell, name string, now time.Time) table.Row {
	wakes := "—"
	if s, ok := m.snoozes[keyOf(pr)]; ok {
		wakes = wakeText(s, now)
	}
	cells := table.Row{markCell, wakes}
	if layout.repositoryColumn {
		cells = append(cells, layout.shared.repositoryText(pr))
	}
	cells = append(cells, prNumberLink(pr.Number, pr.URL), name)
	if layout.from {
		from := "mine"
		switch {
		case queued(pr):
			from = "queue"
		case m.snoozeList(pr) == listReview:
			from = reviewStatusText(pr.ReviewStatus)
			if from == "" {
				from = "review"
			}
		}
		cells = append(cells, from)
	}
	on, off := dimOn, dimOff
	if gone {
		on, off = goneOn, goneOff
	}
	for i := 1; i < len(cells); i++ {
		cells[i] = restyle(cells[i], on, off)
	}
	return cells
}

// reviewStatusTag names where a reviewed pull request stands in a word or
// two, or an icon in the Nerd set, colored when it needs the viewer again;
// it is empty for a pending review request.
func reviewStatusTag(ic *iconSet, status github.ReviewStatus) string {
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
	default:
		return ""
	}
	if ic.nerd {
		text = icon
	}
	if status.Waiting() {
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

// minStatsNameWidth is the PR name width that statistics columns may not
// squeeze below.
const minStatsNameWidth = 16

type statColumn struct {
	title string
	width int
	// nerdWidth replaces width with the Nerd set, whose header is one icon.
	nerdWidth int
	text      func(ic *iconSet, pr *github.PullRequest, now time.Time) string
	// cell is the change bit that highlights this column.
	cell changedCells
	// bots columns are shown only when review bots are configured.
	bots bool
	// detail columns are unknown in a preview.
	detail bool
}

// statColumns are listed in the order they are dropped last to first as the
// terminal narrows.
var statColumns = []statColumn{
	{"Age", 4, 4, func(_ *iconSet, pr *github.PullRequest, now time.Time) string { return ageText(pr.WaitingSince, now) }, 0, false, true},
	{"Bots", 4, 4, func(ic *iconSet, pr *github.PullRequest, _ time.Time) string { return botsText(ic, pr.Bots) }, cellBots, true, true},
	{"CI", 2, 1, func(ic *iconSet, pr *github.PullRequest, _ time.Time) string { return checksIcon(ic, pr.Checks) }, cellCI, false, true},
	{"Review", 6, 4, func(ic *iconSet, pr *github.PullRequest, _ time.Time) string {
		return reviewText(ic, pr.ReviewDecision, pr.Approvals)
	}, cellReview, false, true},
	{"Comments", 8, 4, func(_ *iconSet, pr *github.PullRequest, _ time.Time) string { return strconv.Itoa(pr.Comments) }, cellComments, false, true},
	{"Size", 11, 11, func(_ *iconSet, pr *github.PullRequest, _ time.Time) string {
		return sizeText(pr.Additions, pr.Deletions)
	}, cellSize, false, false},
}

func (stat statColumn) columnWidth(ic *iconSet) int {
	if ic.nerd {
		return stat.nerdWidth
	}
	return stat.width
}

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
