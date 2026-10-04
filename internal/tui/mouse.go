package tui

import (
	"strings"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// pointer is the last position reported while mouse mode is on.
type pointer struct {
	x, y  int
	known bool
}

// paneHit is the part of a pane under a screen line. row is -1 off the
// table's rows.
type paneHit struct {
	pane  paneID
	row   int
	title bool
}

// showingList reports whether View draws the pull-request lists.
func (m *model) showingList() bool {
	return m.width >= minimumWidth && m.height >= minimumHeight &&
		!(m.loading && !m.loginActive && !m.refreshing()) &&
		(m.err == nil || m.keptWhileAsleep()) && !m.overlayOpen() && m.scopeChosen && m.panesFit() && !m.detailsShown()
}

// toggleMouse turns mouse mode on or off and saves it. Off, the terminal handles the mouse
// again, so text selection and link clicks need no modifier.
func (m *model) toggleMouse() {
	m.applyMouse(!m.mouse)
}

// hitTest maps a screen line of the list screen to a pane. It follows
// listLines: the title line and the summary when shown, then each drawn
// pane's title and its table or empty line.
func (m *model) hitTest(y int) (paneHit, bool) {
	layout := m.layoutPanes()
	top := 1
	if m.summaryShown() {
		top++
	}
	for _, id := range m.drawnPanes() {
		if layout.single && id != m.focus {
			continue
		}
		pane := &m.panes[id]
		height := 1
		if rowCount(pane) > 0 {
			height = tableHeaderLen + pane.table.Height()
		}
		switch {
		case y == top:
			return paneHit{pane: id, row: -1, title: true}, true
		case y > top && y <= top+height:
			hit := paneHit{pane: id, row: -1}
			if line := y - top - 1 - tableHeaderLen; rowCount(pane) > 0 && line >= 0 {
				if row := firstVisibleRow(pane.table) + line; row < rowCount(pane) {
					hit.row = row
				}
			}
			return hit, true
		}
		top += 1 + height
	}
	return paneHit{}, false
}

// probeBackground marks the selected row when finding a table's scroll
// position; it is never drawn.
var probeBackground = lipgloss.Color("#010203")

// probePrefix is the SGR sequence that opens a row styled with probeBackground.
var probePrefix = func() string {
	styled := lipgloss.NewStyle().Background(probeBackground).Render("x")
	return styled[:strings.IndexByte(styled, 'm')+1]
}()

// firstVisibleRow returns the row drawn first in a table. Bubbles does not
// expose its scroll position, but the selected row is always drawn, so a copy
// that marks it shows where it lands.
func firstVisibleRow(t table.Model) int {
	styles := tableStyles(false, true)
	styles.Selected = lipgloss.NewStyle().Background(probeBackground)
	t.SetStyles(styles)
	lines := strings.Split(t.View(), "\n")
	for i := tableHeaderLen; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], probePrefix) {
			return max(t.Cursor()-(i-tableHeaderLen), 0)
		}
	}
	return 0
}

// hoveredRow returns the pane's row under the pointer, or -1.
func (m *model) hoveredRow(id paneID) int {
	if !m.mouse || !m.pointer.known {
		return -1
	}
	hit, ok := m.hitTest(m.pointer.y)
	if !ok || hit.pane != id {
		return -1
	}
	return hit.row
}

// handleMouse applies mouse events while mouse mode is on and the lists are
// shown. A click or wheel turn focuses the pane under the pointer; moving the
// cursor away from a row clears its mark as a key press does.
func (m *model) handleMouse(msg tea.MouseMsg) {
	if !m.mouse {
		return
	}
	mouse := msg.Mouse()
	m.pointer = pointer{x: mouse.X, y: mouse.Y, known: true}
	// The help overlay hides the rows a click would land on.
	if !m.showingList() || m.helpShown() {
		return
	}
	switch msg := msg.(type) {
	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return
		}
		hit, ok := m.hitTest(mouse.Y)
		if !ok {
			return
		}
		if hit.pane != m.focus {
			m.setFocus(hit.pane)
		}
		if hit.row >= 0 {
			m.moveTo(hit.pane, hit.row)
		}
	case tea.MouseWheelMsg:
		hit, ok := m.hitTest(mouse.Y)
		if !ok || rowCount(&m.panes[hit.pane]) == 0 {
			return
		}
		if hit.pane != m.focus {
			m.setFocus(hit.pane)
		}
		pane := &m.panes[hit.pane]
		switch msg.Button {
		case tea.MouseWheelUp:
			m.moveTo(hit.pane, pane.table.Cursor()-1)
		case tea.MouseWheelDown:
			m.moveTo(hit.pane, pane.table.Cursor()+1)
		}
	}
}

// moveTo selects a pane's row and leaves the previous one.
func (m *model) moveTo(id paneID, row int) {
	pane := &m.panes[id]
	left := pane.table.Cursor()
	moveCursor(&pane.table, row)
	m.syncPages(id)
	m.leaveRow(id, left)
}
