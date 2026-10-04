package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

var mouseKey = tea.Key{Code: 'm', Text: "m"}

func click(m *model, y int) {
	m.Update(tea.MouseClickMsg{X: 10, Y: y, Button: tea.MouseLeft})
}

var numberCell = regexp.MustCompile(`#(\d+)`)

// numberAt returns the PR number drawn on screen line y, or 0.
func numberAt(m *model, y int) int {
	lines := strings.Split(m.View().Content, "\n")
	if y >= len(lines) {
		return 0
	}
	match := numberCell.FindStringSubmatch(ansi.Strip(lines[y]))
	if match == nil {
		return 0
	}
	number, _ := strconv.Atoi(match[1])
	return number
}

// lineOf returns the screen line that draws PR number, or -1.
func lineOf(m *model, number int) int {
	for y := range strings.Count(m.View().Content, "\n") + 1 {
		if numberAt(m, y) == number {
			return y
		}
	}
	return -1
}

func TestMouseModeIsOffByDefaultAndToggles(t *testing.T) {
	m := newPaneModel(t, 120, 30, manyPRs(5), reviewPRs(5))
	if mode := m.View().MouseMode; mode != tea.MouseModeNone {
		t.Fatalf("mouse mode at start = %v", mode)
	}
	click(m, lineOf(m, 3))
	if selected, _ := m.selectedPR(); selected.Number != 1 {
		t.Fatalf("click with mouse mode off selected #%d", selected.Number)
	}
	help := func() string { return ansi.Strip(strings.Join(m.helpBody(200), "\n")) }
	if !strings.Contains(help(), "enable mouse") {
		t.Fatalf("help does not offer mouse mode: %s", help())
	}

	press(m, mouseKey)
	if mode := m.View().MouseMode; mode != tea.MouseModeAllMotion {
		t.Fatalf("mouse mode after m = %v", mode)
	}
	if !strings.Contains(help(), "disable mouse") {
		t.Fatalf("help does not offer turning mouse mode off: %s", help())
	}
	// Other screens leave the mouse to the terminal.
	openPicker(m)
	if mode := m.View().MouseMode; mode != tea.MouseModeNone {
		t.Fatalf("mouse mode in the picker = %v", mode)
	}
	press(m, tea.Key{Code: tea.KeyEscape})
	if mode := m.View().MouseMode; mode != tea.MouseModeAllMotion {
		t.Fatalf("mouse mode after closing the picker = %v", mode)
	}
	press(m, mouseKey)
	if mode := m.View().MouseMode; mode != tea.MouseModeNone {
		t.Fatalf("mouse mode after second m = %v", mode)
	}
}

func TestClickSelectsTheRowDrawnUnderThePointer(t *testing.T) {
	for _, size := range []struct{ width, height int }{{120, 30}, {80, 10}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m := newPaneModel(t, size.width, size.height, manyPRs(40), reviewPRs(40))
			press(m, mouseKey)
			// Scroll so the first drawn row is not the first row.
			for range 25 {
				press(m, tea.Key{Code: 'j', Text: "j"})
			}
			clicked := 0
			for y := range size.height {
				number := numberAt(m, y)
				if number == 0 || number >= 100 {
					continue
				}
				click(m, y)
				if selected, ok := m.selectedPR(); !ok || selected.Number != number || m.focus != paneMine {
					t.Fatalf("click on line %d (#%d) selected %+v in pane %d", y, number, selected, m.focus)
				}
				clicked++
			}
			if clicked < 2 {
				t.Fatalf("only %d rows clicked", clicked)
			}
		})
	}
}

func TestClickFocusesTheOtherPane(t *testing.T) {
	m := newPaneModel(t, 120, 30, manyPRs(5), reviewPRs(5))
	press(m, mouseKey)
	y := lineOf(m, 102)
	click(m, y)
	if selected, ok := m.selectedPR(); !ok || m.focus != paneReview || selected.Number != 102 {
		t.Fatalf("click on the review pane: focus %d, selected %+v", m.focus, selected)
	}
	// The pane title focuses without moving that pane's cursor.
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	for title, line := range lines {
		if strings.Contains(line, "My PRs") {
			click(m, title)
			break
		}
	}
	if selected, _ := m.selectedPR(); m.focus != paneMine || selected.Number != 1 {
		t.Fatalf("title click: focus %d, selected #%d", m.focus, selected.Number)
	}
}

func TestHoverHighlightsWithoutMovingOrClearingMarks(t *testing.T) {
	one, two := changePR(1, "acme/a"), changePR(2, "acme/a")
	m := changesModel(t, one)
	updateSnapshot(m, "alice", one, two)
	press(m, mouseKey)
	y := lineOf(m, 2)
	m.Update(tea.MouseMotionMsg{X: 5, Y: y})
	if selected, _ := m.selectedPR(); selected.Number != 1 || markers(m, paneMine) != " +" {
		t.Fatalf("hover moved or cleared: selected #%d, markers %q", selected.Number, markers(m, paneMine))
	}
	hover := fmt.Sprintf("\x1b[48;5;%dm", darkHover)
	if line := strings.Split(m.View().Content, "\n")[y]; !strings.Contains(line, hover) {
		t.Fatalf("hovered row not highlighted: %q", line)
	}
	m.Update(tea.MouseMotionMsg{X: 5, Y: 0})
	if strings.Contains(m.View().Content, hover) {
		t.Fatal("highlight kept after the pointer left the table")
	}
	m.Update(tea.MouseMotionMsg{X: 5, Y: y})
	press(m, mouseKey)
	if strings.Contains(m.View().Content, hover) {
		t.Fatal("highlight kept after mouse mode was turned off")
	}
}

func TestClickingAwayFromAGoneRowDropsIt(t *testing.T) {
	one, two, three := changePR(1, "acme/a"), changePR(2, "acme/a"), changePR(3, "acme/a")
	m := changesModel(t, one, two, three)
	updateSnapshot(m, "alice", two, three)
	// Selection stayed on #1, now a gone row after the others.
	press(m, mouseKey)
	rest(m)
	click(m, lineOf(m, 2))
	if selected, _ := m.selectedPR(); selected.Number != 2 || markers(m, paneMine) != "  " {
		t.Fatalf("after clicking away from gone #1: selected #%d, markers %q", selected.Number, markers(m, paneMine))
	}
}

func TestWheelMovesTheCursorInThePaneUnderThePointer(t *testing.T) {
	m := newPaneModel(t, 120, 30, manyPRs(5), reviewPRs(5))
	press(m, mouseKey)
	y := lineOf(m, 101)
	wheel := func(button tea.MouseButton) { m.Update(tea.MouseWheelMsg{X: 5, Y: y, Button: button}) }
	wheel(tea.MouseWheelDown)
	wheel(tea.MouseWheelDown)
	if selected, _ := m.selectedPR(); m.focus != paneReview || selected.Number != 102 {
		t.Fatalf("wheel down: focus %d, selected #%d", m.focus, selected.Number)
	}
	wheel(tea.MouseWheelUp)
	if selected, _ := m.selectedPR(); selected.Number != 101 {
		t.Fatalf("wheel up selected #%d", selected.Number)
	}
	if cursor := m.panes[paneMine].table.Cursor(); cursor != 0 {
		t.Fatalf("other pane's cursor moved to %d", cursor)
	}
}
