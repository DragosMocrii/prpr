package tui

import (
	"fmt"
	"net/url"
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
	layout := m.layoutPanes()
	for _, id := range paneIDs {
		// Hidden and empty panes keep a minimal table so cursors survive.
		m.rebuildPane(id, max(minTableHeight, layout.tables[id]), resetSelection)
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
	stats            []statColumn
	fits             bool
}

func (m *model) paneLayout(id paneID) tableLayout {
	pane := &m.panes[id]
	review := id == paneReview
	maxNumberWidth := 6
	for row := range rowCount(pane) {
		if pr, _, ok := m.paneRow(id, row); ok {
			maxNumberWidth = max(maxNumberWidth, ansi.StringWidth(fmt.Sprintf("#%d", pr.Number)))
		}
	}
	width := max(1, m.width)
	var layout tableLayout
	layout.repositoryColumn = m.selectedRepository == "" && width >= 80
	repositoryWidth := 0
	if layout.repositoryColumn {
		repositoryWidth = min(28, max(12, width/4))
	}
	lastTitle, lastWidth := "Merge", 5
	if review {
		lastTitle, lastWidth = "Author", min(16, max(8, width/6))
	}
	columns := make([]table.Column, 0, 6+len(statColumns))
	columns = append(columns, table.Column{Title: "", Width: 1})
	if layout.repositoryColumn {
		columns = append(columns, table.Column{Title: "Repository", Width: repositoryWidth})
	}
	nameColumn := len(columns)
	columns = append(columns,
		table.Column{Title: "PR name"},
		table.Column{Title: "Number", Width: maxNumberWidth},
		table.Column{Title: "State", Width: 5},
		table.Column{Title: lastTitle, Width: lastWidth},
	)
	nameWidth := remainingWidth(width, columns)
	// Statistics columns are added in priority order while the name keeps room.
	for _, stat := range statColumns {
		if stat.bots && !m.bots {
			continue
		}
		candidate := append(columns, table.Column{Title: stat.title, Width: stat.width})
		if remainingWidth(width, candidate) < minStatsNameWidth {
			break
		}
		columns = candidate
		nameWidth = remainingWidth(width, columns)
		layout.stats = append(layout.stats, stat)
	}
	layout.fits = nameWidth >= 8
	columns[nameColumn].Width = max(8, nameWidth)
	layout.columns = columns
	return layout
}

// Change-mark styles. Each is turned back on after the resets inside a cell.
const (
	reverseOn  = "\x1b[7m"
	reverseOff = "\x1b[27m"
	goneOn     = "\x1b[2;9m"
	goneOff    = "\x1b[22;29m"
)

func (m *model) paneRows(id paneID, layout tableLayout) []table.Row {
	pane := &m.panes[id]
	review := id == paneReview
	all := m.selectedRepository == ""
	now := m.now()
	rows := make([]table.Row, 0, rowCount(pane))
	for row := range rowCount(pane) {
		pr, gone, ok := m.paneRow(id, row)
		if !ok {
			continue
		}
		mark := m.changes[id].mark(pr)
		changed := func(cell changedCells, text string) string {
			if mark.kind == markChanged && mark.cells&cell != 0 {
				return restyle(text, reverseOn, reverseOff)
			}
			return text
		}
		name := singleLine(pr.Title)
		if all && !layout.repositoryColumn {
			name = singleLine(pr.Repository) + " — " + name
		}
		state := "open"
		if pr.Draft {
			state = "draft"
		}
		last, lastCell := mergeIcon(pr.Draft, pr.Mergeable, pr.MergeState), cellMerge
		if review {
			last, lastCell = singleLine(pr.Author), cellAuthor
		}
		cells := make(table.Row, 0, len(layout.columns))
		cells = append(cells, markText(mark.kind, gone))
		if layout.repositoryColumn {
			cells = append(cells, singleLine(pr.Repository))
		}
		cells = append(cells, changed(cellName, name), prNumberLink(pr.Number, pr.URL),
			changed(cellState, state), changed(lastCell, last))
		for _, stat := range layout.stats {
			cells = append(cells, changed(stat.cell, stat.text(pr, now)))
		}
		if gone {
			for i := 1; i < len(cells); i++ {
				cells[i] = restyle(cells[i], goneOn, goneOff)
			}
		}
		rows = append(rows, cells)
	}
	return rows
}

// markText is a row's change marker.
func markText(kind markKind, gone bool) string {
	switch {
	case gone:
		return lipgloss.NewStyle().Faint(true).Render("−")
	case kind == markNew:
		return coloredIcon("+", "2")
	case kind == markChanged:
		return coloredIcon("•", "3")
	case kind == markActivity:
		return lipgloss.NewStyle().Faint(true).Render("·")
	default:
		return " "
	}
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
	text  func(pr *github.PullRequest, now time.Time) string
	// cell is the change bit that highlights this column.
	cell changedCells
	// bots columns are shown only when review bots are configured.
	bots bool
}

// statColumns are listed in the order they are dropped last to first as the
// terminal narrows.
var statColumns = []statColumn{
	{"Age", 4, func(pr *github.PullRequest, now time.Time) string { return ageText(pr.WaitingSince, now) }, 0, false},
	{"Bots", 4, func(pr *github.PullRequest, _ time.Time) string { return botsText(pr.Bots) }, cellBots, true},
	{"CI", 2, func(pr *github.PullRequest, _ time.Time) string { return checksIcon(pr.Checks) }, cellCI, false},
	{"Review", 6, func(pr *github.PullRequest, _ time.Time) string { return reviewText(pr.ReviewDecision, pr.Approvals) }, cellReview, false},
	{"Size", 11, func(pr *github.PullRequest, _ time.Time) string { return sizeText(pr.Additions, pr.Deletions) }, cellSize, false},
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

func checksIcon(state string) string {
	switch state {
	case "SUCCESS":
		return coloredIcon("✓", "2")
	case "FAILURE", "ERROR":
		return coloredIcon("✗", "1")
	case "PENDING", "EXPECTED":
		return coloredIcon("●", "3")
	default:
		return "–"
	}
}

// reviewText shows the review decision followed by the current approval count.
func reviewText(decision string, approvals int) string {
	var icon string
	switch decision {
	case "APPROVED":
		icon = coloredIcon("✓", "2")
	case "CHANGES_REQUESTED":
		icon = coloredIcon("✗", "1")
	case "REVIEW_REQUIRED":
		icon = coloredIcon("●", "3")
	default:
		icon = "–"
	}
	if approvals > 0 {
		icon += strconv.Itoa(approvals)
	}
	return icon
}

// botsText shows the most attention-worthy bot state; concerns are summed
// across bots.
func botsText(reviews []github.BotReview) string {
	worst := github.BotReview{}
	concerns := 0
	for _, review := range reviews {
		worst.State = max(worst.State, review.State)
		concerns += review.Concerns
	}
	worst.Concerns = concerns
	return botStateText(worst)
}

func botStateText(review github.BotReview) string {
	switch review.State {
	case github.BotConcerns:
		return coloredIcon("✗"+strconv.Itoa(review.Concerns), "1")
	case github.BotFailed:
		return coloredIcon("!", "1")
	case github.BotRunning:
		return coloredIcon("◌", "3")
	case github.BotStale:
		return coloredIcon("✓", "2") + "*"
	case github.BotPassed:
		return coloredIcon("✓", "2")
	default:
		return "–"
	}
}

// botBreakdown lists each configured bot's state for the selected PR.
func botBreakdown(reviews []github.BotReview) string {
	parts := make([]string, len(reviews))
	for i, review := range reviews {
		parts[i] = singleLine(review.Name) + " " + botStateText(review)
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

// listHelpHeight counts help lines from the binding layout rather than the
// current enabled state, which is stale while the picker or scope prompt is
// shown.
func (m *model) listHelpHeight() int {
	keys := m.keys.listHelp()
	if !m.help.ShowAll || !m.fullHelpFits(keys) {
		return 1
	}
	rows := 1
	for _, column := range keys.full {
		rows = max(rows, len(column))
	}
	return rows
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
	for _, r := range rawURL {
		if unicode.IsControl(r) {
			return false
		}
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

// mergeIcon is green exactly when mergeReady holds.
func mergeIcon(draft bool, mergeable, state string) string {
	if mergeReady(draft, mergeable, state) {
		return coloredIcon("✓", "2")
	}
	if mergeable == "CONFLICTING" || state == "DIRTY" {
		return coloredIcon("✗", "1")
	}
	if draft {
		return "–"
	}
	switch state {
	case "BLOCKED":
		return coloredIcon("●", "3")
	case "BEHIND":
		return coloredIcon("↓", "3")
	default:
		return coloredIcon("?", "3")
	}
}
