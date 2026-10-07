package tui

import "slices"

// framePane is a pane on screen: the line of its title and how many lines
// its body takes under it (0 collapsed, 1 for the empty line, else the
// table with its header).
type framePane struct {
	id        paneID
	top, body int
}

// framePlan is how the panes fill the list screen this frame. Drawing, hit
// testing, focus, and table sizes all read it, so they cannot disagree.
type framePlan struct {
	// single draws only the focused pane, when the smallest tables do not fit.
	single bool
	// drawn is every pane in the arrangement, in drawing order, single-pane
	// mode included: Tab cycles through them though one is on screen.
	drawn []paneID
	// screen is the panes on screen, with their lines.
	screen []framePane
	// tables is each pane's table height with its header; 0 when it has no table.
	tables [len(paneIDs)]int
}

// framePlan lays out the panes: the panes of open pull requests share the
// lines (openTables), and leftover panes (Merged) take what they leave once
// each has a title and every row, never squeezing them or switching to one
// pane at a time.
func (m *model) framePlan() framePlan {
	avail := m.height - m.listChromeHeight()
	open := m.openPanes()
	plan := framePlan{drawn: slices.Clone(open)}
	var leftover []framePane
	for _, id := range paneIDs {
		spec := &paneSpecs[id]
		rows := rowCount(&m.panes[id])
		if !spec.leftover || rows == 0 {
			continue
		}
		// Collapsed, it is a title line, which the other panes give up
		// while they keep their smallest tables.
		if m.collapsed[id] {
			if avail-1 >= m.openPanesNeed(open) {
				leftover = append(leftover, framePane{id: id})
				avail--
			}
			continue
		}
		if room := m.leftoverRoom(open, avail); room >= minDualTableHeight {
			body := min(tableHeaderLen+rows, room)
			leftover = append(leftover, framePane{id: id, body: body})
			plan.tables[id] = body
			avail -= 1 + body
		}
	}
	plan.single, plan.tables = m.openTables(open, avail, plan.tables)
	top := 1
	if m.summaryShown() {
		top++
	}
	for _, id := range open {
		if plan.single && id != m.focus {
			continue
		}
		body := plan.tables[id]
		if rowCount(&m.panes[id]) == 0 {
			body = 1
		}
		plan.screen = append(plan.screen, framePane{id: id, top: top, body: body})
		top += 1 + body
	}
	for _, p := range leftover {
		plan.drawn = append(plan.drawn, p.id)
		if plan.single {
			continue
		}
		p.top = top
		plan.screen = append(plan.screen, p)
		top += 1 + p.body
	}
	return plan
}

// leftoverRoom is how many lines a leftover pane's table may take out of
// avail: what is left once the open panes have a title and every row each
// (at least their smallest table), and the leftover pane its title.
func (m *model) leftoverRoom(open []paneID, avail int) int {
	room := avail - 1
	for _, id := range open {
		room-- // title
		if rows := rowCount(&m.panes[id]); rows > 0 {
			room -= max(minDualTableHeight, tableHeaderLen+rows)
		} else {
			room-- // empty line
		}
	}
	return room
}

// focusable lists the drawn panes that can take the focus: all but
// collapsed ones.
func (p framePlan) focusable(m *model) []paneID {
	return slices.DeleteFunc(slices.Clone(p.drawn), func(id paneID) bool { return m.collapsed[id] })
}

// at finds the pane on screen line y: its title or its body.
func (p framePlan) at(y int) (framePane, bool) {
	for _, pane := range p.screen {
		if y >= pane.top && y <= pane.top+pane.body {
			return pane, true
		}
	}
	return framePane{}, false
}
