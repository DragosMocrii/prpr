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

	"github.com/DragosMocrii/prpr/internal/github"
)

func (m *model) rebuildPRTable(resetSelection bool) {
	previous := 0
	if !resetSelection {
		previous = m.prTable.Cursor()
	}
	rows := make([]table.Row, 0, len(m.visiblePRs))
	maxNumberWidth := 6
	for _, index := range m.visiblePRs {
		if index < 0 || index >= len(m.snapshot.PullRequests) {
			continue
		}
		pr := &m.snapshot.PullRequests[index]
		maxNumberWidth = max(maxNumberWidth, ansi.StringWidth(fmt.Sprintf("#%d", pr.Number)))
	}
	width := m.width
	if width < 1 {
		width = 1
	}
	all := m.selectedRepository == ""
	repositoryColumn := all && width >= 80
	repositoryWidth := 0
	if repositoryColumn {
		repositoryWidth = min(28, max(12, width/4))
	}
	columns := make([]table.Column, 0, 5)
	if repositoryColumn {
		columns = append(columns, table.Column{Title: "Repository", Width: repositoryWidth})
	}
	columns = append(columns, table.Column{Title: "PR name"})
	columns = append(columns,
		table.Column{Title: "Number", Width: maxNumberWidth},
		table.Column{Title: "State", Width: 5},
		table.Column{Title: "Merge", Width: 5},
	)
	nameWidth := width - 8 - maxNumberWidth - 5 - 5
	if repositoryColumn {
		nameWidth = width - 10 - repositoryWidth - maxNumberWidth - 5 - 5
	}
	m.prTableFits = nameWidth >= 8
	nameWidth = max(8, nameWidth)
	if repositoryColumn {
		columns[1].Width = nameWidth
	} else {
		columns[0].Width = nameWidth
	}
	for _, index := range m.visiblePRs {
		if index < 0 || index >= len(m.snapshot.PullRequests) {
			continue
		}
		pr := &m.snapshot.PullRequests[index]
		name := singleLine(pr.Title)
		if all && !repositoryColumn {
			name = singleLine(pr.Repository) + " — " + name
		}
		state := "open"
		if pr.Draft {
			state = "draft"
		}
		row := make(table.Row, 0, len(columns))
		if repositoryColumn {
			row = append(row, singleLine(pr.Repository))
		}
		row = append(row, name, prNumberLink(pr.Number, pr.URL), state, mergeableIcon(pr.Mergeable))
		rows = append(rows, row)
	}
	styles := table.Styles{
		Header:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).Padding(0, 1).Border(lipgloss.NormalBorder(), false, false, true, false),
		Cell:     lipgloss.NewStyle().Padding(0, 1),
		Selected: lipgloss.NewStyle().Bold(true).Background(lipgloss.Color("236")),
	}
	height := max(3, m.height-5)
	m.prTable = table.New(
		table.WithStyles(styles),
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithWidth(width),
		table.WithHeight(height),
		table.WithFocused(true),
	)
	if !resetSelection && len(rows) > 0 {
		m.prTable.MoveDown(min(max(previous, 0), len(rows)-1))
	}
}

func (m *model) selectedPR() (*github.PullRequest, bool) {
	index := m.prTable.Cursor()
	if index < 0 || index >= len(m.visiblePRs) {
		return nil, false
	}
	prIndex := m.visiblePRs[index]
	if prIndex < 0 || prIndex >= len(m.snapshot.PullRequests) {
		return nil, false
	}
	return &m.snapshot.PullRequests[prIndex], true
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
