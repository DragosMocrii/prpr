package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// helpColumnGap separates the help overlay's columns.
const helpColumnGap = 4

// helpShown reports whether the help overlay is drawn: it was opened and the
// list is on screen.
func (m *model) helpShown() bool {
	return m.helpOpen && m.showingList() && !m.detailsShown() && !m.overlayOpen() &&
		m.width >= minimumWidth && m.height >= minimumHeight
}

// closeStaleHelp forgets an open help overlay once the list is gone, so it
// does not reappear when the list comes back. A terminal too small to draw
// it keeps it open.
func (m *model) closeStaleHelp() {
	if m.helpOpen && m.width >= minimumWidth && m.height >= minimumHeight && !m.showingList() {
		m.helpOpen, m.helpScroll = false, 0
	}
}

// updateHelp handles a key while the help overlay is open: ? and esc close
// it, the line keys scroll it, and every other key is ignored so it cannot
// act on a row the overlay hides.
func (m *model) updateHelp(msg tea.KeyPressMsg) tea.Cmd {
	k := m.keys
	switch {
	case key.Matches(msg, k.Help) || msg.String() == "esc":
		m.helpOpen, m.helpScroll = false, 0
	// The line keys scroll even when the focused pane has no rows to move.
	case bound(msg, k.Table.LineDown):
		m.helpScroll++
	case bound(msg, k.Table.LineUp):
		m.helpScroll = max(m.helpScroll-1, 0)
	}
	return nil
}

// helpLines draws a screen's help line.
func (m *model) helpLines(screen func(keyMap) helpKeys) []string {
	m.syncKeys()
	return []string{m.shortHelp(screen(m.keys))}
}

// shortHelp joins the enabled short keys that fit, in order, then the
// pinned keys, which are never dropped.
func (m *model) shortHelp(keys helpKeys) string {
	styles := m.help.Styles
	separator := styles.ShortSeparator.Render(" • ")
	item := func(binding key.Binding) string {
		help := binding.Help()
		return styles.ShortKey.Render(help.Key) + " " + styles.ShortDesc.Render(help.Desc)
	}
	enabled := func(bindings []key.Binding) []string {
		var items []string
		for _, binding := range bindings {
			if binding.Enabled() {
				items = append(items, item(binding))
			}
		}
		return items
	}
	short, pinned := enabled(keys.short), enabled(keys.pinned)
	for n := len(short); n >= 0; n-- {
		line := strings.Join(append(short[:n:n], pinned...), separator)
		if lipgloss.Width(line) <= m.width {
			return line
		}
	}
	return ansi.Truncate(strings.Join(pinned, separator), m.width, "…")
}

// helpGroupLines draws a group: its title, then each key beside its words,
// faint while the key does nothing.
func (m *model) helpGroupLines(group helpGroup) []string {
	keyWidth := 0
	for _, binding := range group.keys {
		keyWidth = max(keyWidth, lipgloss.Width(binding.Help().Key))
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	keyStyle := lipgloss.NewStyle().Bold(true)
	faint := lipgloss.NewStyle().Faint(true)
	lines := []string{title.Render(group.title)}
	for _, binding := range group.keys {
		help := binding.Help()
		line := keyStyle.Render(help.Key) + strings.Repeat(" ", keyWidth-lipgloss.Width(help.Key)+2) + help.Desc
		if !binding.Enabled() {
			line = faint.Render(help.Key + strings.Repeat(" ", keyWidth-lipgloss.Width(help.Key)+2) + help.Desc)
		}
		lines = append(lines, line)
	}
	return lines
}

// helpBody lays the groups out in as many columns as fit width, keeping each
// group whole and in order.
func (m *model) helpBody(width int) []string {
	m.syncKeys()
	var groups [][]string
	total := 0
	for _, group := range m.keys.listGroups() {
		lines := m.helpGroupLines(group)
		groups = append(groups, lines)
		total += len(lines) + 1
	}
	for columns := len(groups); columns > 1; columns-- {
		if body, ok := helpColumns(groups, columns, (total+columns-1)/columns, width); ok {
			return body
		}
	}
	var body []string
	for i, group := range groups {
		if i > 0 {
			body = append(body, "")
		}
		body = append(body, group...)
	}
	return body
}

// helpColumns fills columns in order, starting a new one once a column
// reaches height, and reports whether they fit width.
func helpColumns(groups [][]string, columns, height, width int) ([]string, bool) {
	var cols [][]string
	for _, group := range groups {
		if len(cols) == 0 || (len(cols[len(cols)-1]) >= height && len(cols) < columns) {
			cols = append(cols, nil)
		}
		col := &cols[len(cols)-1]
		if len(*col) > 0 {
			*col = append(*col, "")
		}
		*col = append(*col, group...)
	}
	widths := make([]int, len(cols))
	used, rows := helpColumnGap*(len(cols)-1), 0
	for i, col := range cols {
		for _, line := range col {
			widths[i] = max(widths[i], lipgloss.Width(line))
		}
		used += widths[i]
		rows = max(rows, len(col))
	}
	if used > width {
		return nil, false
	}
	body := make([]string, rows)
	for row := range rows {
		var b strings.Builder
		for i, col := range cols {
			line := ""
			if row < len(col) {
				line = col[row]
			}
			if i < len(cols)-1 {
				line += strings.Repeat(" ", widths[i]-lipgloss.Width(line)+helpColumnGap)
			}
			b.WriteString(line)
		}
		body[row] = strings.TrimRight(b.String(), " ")
	}
	return body, true
}

// helpWindow is the part of body that fits room lines from the scroll
// position, which it clamps.
func (m *model) helpWindow(body []string, room int) []string {
	m.helpScroll = max(min(m.helpScroll, len(body)-room), 0)
	end := min(m.helpScroll+room, len(body))
	return body[m.helpScroll:end]
}

// helpView draws the help as a box over the list on wide, tall terminals,
// else over the whole screen below the title.
func (m *model) helpView() []string {
	base := m.listLinesWith(keyMap.helpOverlayHelp)
	if m.detailsModal() {
		// The box keeps clear of the title line and the bottom three lines.
		room := max(m.height-1-3-2, 1)
		body := m.helpWindow(m.helpBody(m.width-8), room-2)
		box := helpBox(append([]string{lipgloss.NewStyle().Bold(true).Render("Keys"), ""}, body...))
		x := (m.width - lipgloss.Width(box)) / 2
		y := max(1, (m.height-3-lipgloss.Height(box))/2)
		return overlay(base, box, x, y)
	}
	room := max(m.height-2, 1)
	body := m.helpWindow(m.helpBody(m.width), room)
	for len(body) < room {
		body = append(body, "")
	}
	lines := append([]string{base[0]}, body...)
	return append(lines, base[len(base)-1])
}

func helpBox(content []string) string {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("6")).
		Padding(0, 1).Render(strings.Join(content, "\n"))
}

// overlay draws box over base at x, y.
func overlay(base []string, box string, x, y int) []string {
	canvas := lipgloss.NewCompositor(
		lipgloss.NewLayer(strings.Join(base, "\n")),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	)
	return strings.Split(canvas.Render(), "\n")
}
