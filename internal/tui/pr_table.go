package tui

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/table"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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
	columns := make([]table.Column, 0, 5)
	if repositoryColumn {
		columns = append(columns, table.Column{Title: "Repository", Width: repositoryWidth})
	}
	columns = append(columns,
		table.Column{Title: "PR name"},
		table.Column{Title: "Number", Width: maxNumberWidth},
		table.Column{Title: "State", Width: 5},
		table.Column{Title: lastTitle, Width: lastWidth},
	)
	// Each column has one cell of padding on both sides.
	nameWidth := width - 2*len(columns) - repositoryWidth - maxNumberWidth - 5 - lastWidth
	pane.fits = nameWidth >= 8
	nameWidth = max(8, nameWidth)
	nameColumn := 0
	if repositoryColumn {
		nameColumn = 1
	}
	columns[nameColumn].Width = nameWidth

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
	var icon, color string
	switch status {
	case "MERGEABLE":
		icon, color = "✓", "2"
	case "CONFLICTING":
		icon, color = "✗", "1"
	default:
		icon, color = "?", "3"
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(icon)
}
