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
	source := m.source(id)
	previous := 0
	if !resetSelection {
		previous = pane.table.Cursor()
	}
	review := id == paneReview
	maxNumberWidth := 6
	for _, index := range pane.visible {
		if index < 0 || index >= len(source) {
			continue
		}
		maxNumberWidth = max(maxNumberWidth, ansi.StringWidth(fmt.Sprintf("#%d", source[index].Number)))
	}
	width := max(1, m.width)
	all := m.selectedRepository == ""
	repositoryColumn := all && width >= 80
	repositoryWidth := 0
	if repositoryColumn {
		repositoryWidth = min(28, max(12, width/4))
	}
	lastTitle, lastWidth := "Merge", 5
	if review {
		lastTitle, lastWidth = "Author", min(16, max(8, width/6))
	}
	columns := make([]table.Column, 0, 5+len(statColumns))
	if repositoryColumn {
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
	stats := 0
	for _, stat := range statColumns {
		candidate := append(columns, table.Column{Title: stat.title, Width: stat.width})
		if remainingWidth(width, candidate) < minStatsNameWidth {
			break
		}
		columns = candidate
		nameWidth = remainingWidth(width, columns)
		stats++
	}
	pane.fits = nameWidth >= 8
	nameWidth = max(8, nameWidth)
	columns[nameColumn].Width = nameWidth
	now := m.now()

	rows := make([]table.Row, 0, len(pane.visible))
	for _, index := range pane.visible {
		if index < 0 || index >= len(source) {
			continue
		}
		pr := &source[index]
		name := singleLine(pr.Title)
		if all && !repositoryColumn {
			name = singleLine(pr.Repository) + " — " + name
		}
		state := "open"
		if pr.Draft {
			state = "draft"
		}
		last := mergeableIcon(pr.Mergeable)
		if review {
			last = singleLine(pr.Author)
		}
		row := make(table.Row, 0, len(columns))
		if repositoryColumn {
			row = append(row, singleLine(pr.Repository))
		}
		row = append(row, name, prNumberLink(pr.Number, pr.URL), state, last)
		for _, stat := range statColumns[:stats] {
			row = append(row, stat.cell(pr, now))
		}
		rows = append(rows, row)
	}
	pane.table = table.New(
		table.WithStyles(tableStyles(id == m.focus)),
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithWidth(width),
		table.WithHeight(height),
		table.WithFocused(id == m.focus),
		table.WithKeyMap(m.keys.Table),
	)
	if !resetSelection && len(rows) > 0 {
		moveCursor(&pane.table, previous)
	}
	m.syncPages(id)
}

// minStatsNameWidth is the PR name width that statistics columns may not
// squeeze below.
const minStatsNameWidth = 16

type statColumn struct {
	title string
	width int
	cell  func(pr *github.PullRequest, now time.Time) string
}

// statColumns are listed in the order they are dropped last to first as the
// terminal narrows.
var statColumns = []statColumn{
	{"Age", 4, func(pr *github.PullRequest, now time.Time) string { return ageText(pr.WaitingSince, now) }},
	{"CI", 2, func(pr *github.PullRequest, _ time.Time) string { return checksIcon(pr.Checks) }},
	{"Review", 6, func(pr *github.PullRequest, _ time.Time) string { return reviewText(pr.ReviewDecision, pr.Approvals) }},
	{"Size", 11, func(pr *github.PullRequest, _ time.Time) string { return sizeText(pr.Additions, pr.Deletions) }},
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

func mergeableIcon(status string) string {
	switch status {
	case "MERGEABLE":
		return coloredIcon("✓", "2")
	case "CONFLICTING":
		return coloredIcon("✗", "1")
	default:
		return coloredIcon("?", "3")
	}
}
