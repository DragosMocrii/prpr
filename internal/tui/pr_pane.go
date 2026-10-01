package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/paginator"
	"charm.land/bubbles/v2/table"
	"charm.land/lipgloss/v2"

	"github.com/DragosMocrii/prpr/internal/github"
)

type paneID int

const (
	paneMine paneID = iota
	paneReview
)

var paneIDs = [...]paneID{paneMine, paneReview}

// prPane is one pull-request list. visible holds indices into the pane's
// source slice, so a row is identified by its visible index, never by number.
type prPane struct {
	visible []int
	table   table.Model
	pages   paginator.Model
	fits    bool
}

func newPRPane() prPane {
	pages := paginator.New()
	pages.Type = paginator.Dots
	return prPane{pages: pages}
}

// minTableHeight is a table header (title and border) plus one row.
const minTableHeight = 3

// paneLayout gives each pane's table height, including its header. A zero
// height means the pane is empty or not drawn.
type paneLayout struct {
	single bool // only the focused pane is drawn
	tables [2]int
}

// listChromeHeight counts list lines outside the panes: the title, the
// selected URL, the status line, and the help.
func (m *model) listChromeHeight() int {
	return 3 + m.listHelpHeight()
}

func (m *model) layoutPanes() paneLayout {
	avail := m.height - m.listChromeHeight()
	filled := func(id paneID) bool { return len(m.panes[id].visible) > 0 }
	need := func(id paneID) int {
		if filled(id) {
			return 1 + minTableHeight
		}
		return 2 // title and empty line
	}
	var layout paneLayout
	if avail < need(paneMine)+need(paneReview) {
		layout.single = true
		if filled(m.focus) {
			layout.tables[m.focus] = max(minTableHeight, avail-1)
		}
		return layout
	}
	rows := avail - 2 // pane titles
	switch {
	case filled(paneMine) && filled(paneReview):
		layout.tables[paneMine] = rows - rows/2
		layout.tables[paneReview] = rows / 2
	case filled(paneMine):
		layout.tables[paneMine] = rows - 1
	case filled(paneReview):
		layout.tables[paneReview] = rows - 1
	}
	return layout
}

func (m *model) source(id paneID) []github.PullRequest {
	if id == paneReview {
		return m.snapshot.ReviewRequests
	}
	return m.snapshot.PullRequests
}

func (m *model) focused() *prPane {
	return &m.panes[m.focus]
}

func (m *model) paneSelectedPR(id paneID) (*github.PullRequest, bool) {
	pane := &m.panes[id]
	source := m.source(id)
	row := pane.table.Cursor()
	if row < 0 || row >= len(pane.visible) {
		return nil, false
	}
	index := pane.visible[row]
	if index < 0 || index >= len(source) {
		return nil, false
	}
	return &source[index], true
}

func (m *model) selectedPR() (*github.PullRequest, bool) {
	return m.paneSelectedPR(m.focus)
}

// selectPR moves a pane's cursor to the visible row for repository and
// number, if that pull request is still visible in the pane.
func (m *model) selectPR(id paneID, repository string, number int) {
	pane := &m.panes[id]
	source := m.source(id)
	for row, index := range pane.visible {
		pr := &source[index]
		if pr.Number == number && strings.EqualFold(pr.Repository, repository) {
			pane.table.SetCursor(row)
			m.syncPages(id)
			return
		}
	}
}

// syncPages derives a pane's page indicator from its table's visible rows and
// cursor. The table still owns scrolling; pages only report position.
func (m *model) syncPages(id paneID) {
	pane := &m.panes[id]
	pane.pages.PerPage = max(1, pane.table.Height())
	pane.pages.TotalPages = 1
	pane.pages.SetTotalPages(len(pane.visible))
	pane.pages.Page = min(pane.table.Cursor()/pane.pages.PerPage, pane.pages.TotalPages-1)
}

func (m *model) pageIndicator() string {
	pages := m.focused().pages
	if pages.TotalPages > 12 {
		pages.Type = paginator.Arabic
		pages.ArabicFormat = "page %d/%d"
	}
	return pages.View()
}

// applyFocusStyles highlights the selected row only in the focused pane and
// lets only that table take key input. Styles are swapped in place so each
// table keeps its scroll position.
func (m *model) applyFocusStyles() {
	for _, id := range paneIDs {
		pane := &m.panes[id]
		pane.table.SetStyles(tableStyles(id == m.focus))
		if id == m.focus {
			pane.table.Focus()
		} else {
			pane.table.Blur()
		}
	}
}

func tableStyles(focused bool) table.Styles {
	selected := lipgloss.NewStyle()
	if focused {
		selected = selected.Bold(true).Background(lipgloss.Color("236"))
	}
	return table.Styles{
		Header:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).Padding(0, 1).Border(lipgloss.NormalBorder(), false, false, true, false),
		Cell:     lipgloss.NewStyle().Padding(0, 1),
		Selected: selected,
	}
}

func (m *model) paneTitle(id paneID, single bool) string {
	name := "My PRs"
	if id == paneReview {
		name = "Review requested"
	}
	title := fmt.Sprintf("%s (%d)", name, len(m.panes[id].visible))
	if single {
		title += " · tab: other list"
	}
	if id != m.focus {
		return "  " + lipgloss.NewStyle().Faint(true).Render(title)
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).Render("▸ " + title)
}

func (m *model) emptyPaneLine(id paneID) string {
	text := "No open pull requests"
	if id == paneReview {
		text = "No review requests"
	}
	if m.selectedRepository != "" {
		text += " in " + singleLine(m.selectedRepository)
	}
	return "  " + text + "."
}
