package tui

import (
	"slices"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func pressHelp(m *model) { press(m, tea.Key{Code: '?', Text: "?"}) }

func TestHelpOverlayListsEveryListKey(t *testing.T) {
	k := defaultKeyMap()
	var grouped []string
	for _, group := range k.listGroups() {
		for _, binding := range group.keys {
			grouped = append(grouped, binding.Help().Key)
		}
	}
	list := k.listHelp()
	for _, binding := range slices.Concat(list.short, list.pinned) {
		if binding.Help().Key != "?" && !slices.Contains(grouped, binding.Help().Key) {
			t.Errorf("%s is on the help line but in no help group", binding.Help().Key)
		}
	}
	for _, binding := range []key.Binding{k.Rerequest, k.DismissPlead, k.Snooze, k.Undo, k.Icons, k.Mouse, k.Notify, k.Legend, k.Drafts,
		k.QuickFailing, k.QuickReady, k.Rules, k.Schedule, k.Wake, k.Account, k.AllRepositories, k.CopyURL, k.PrevPane} {
		if !slices.Contains(grouped, binding.Help().Key) {
			t.Errorf("%s is in no help group", binding.Help().Key)
		}
	}
}

func TestHelpOverlayFitsAndDimsKeysThatDoNothing(t *testing.T) {
	m := newPaneModel(t, 140, 40, manyPRs(5), reviewPRs(3))
	pressHelp(m)
	if !m.helpShown() {
		t.Fatal("? did not open the help overlay")
	}
	for _, size := range [][2]int{{140, 40}, {90, 26}, {60, 14}, {40, 8}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := assertBounded(t, m, size[0], size[1])
		if view := ansi.Strip(strings.Join(lines, "\n")); !strings.Contains(view, "Move") {
			t.Fatalf("help overlay missing at %dx%d:\n%s", size[0], size[1], view)
		}
	}
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Scope & settings") || !strings.Contains(view, "dismiss asked again") {
		t.Fatalf("wide help lacks groups or keys:\n%s", view)
	}
	// X does nothing on a row without 🙏, so it is drawn faint.
	for _, line := range m.helpBody(140) {
		if strings.Contains(ansi.Strip(line), "dismiss asked again") && !strings.Contains(line, "\x1b[2m") {
			t.Fatalf("X is not faint: %q", line)
		}
	}
}

func TestHelpOverlayScrollsAndIgnoresOtherKeys(t *testing.T) {
	m := newPaneModel(t, 60, 14, manyPRs(5), nil)
	pressHelp(m)
	first := ansi.Strip(m.View().Content)
	press(m, tea.Key{Code: 'j', Text: "j"})
	if ansi.Strip(m.View().Content) == first {
		t.Fatal("j did not scroll a help overlay taller than the screen")
	}
	cursor := m.panes[paneMine].table.Cursor()
	for _, text := range []string{"z", "o", "x", "/"} {
		press(m, tea.Key{Code: rune(text[0]), Text: text})
	}
	if m.snoozeEditor != nil || m.searching != nil || m.panes[paneMine].table.Cursor() != cursor || !m.helpShown() {
		t.Fatal("a key acted on the list behind the help overlay")
	}
	press(m, tea.Key{Code: tea.KeyEsc})
	if m.helpShown() {
		t.Fatal("esc did not close the help overlay")
	}
	pressHelp(m)
	pressHelp(m)
	if m.helpShown() {
		t.Fatal("a second ? did not close the help overlay")
	}
}

func TestHelpLineKeepsHelpAndQuitAndLeadsWithKeysThatApplyNow(t *testing.T) {
	m := newPaneModel(t, 140, 30, manyPRs(5), nil)
	for _, width := range []int{140, 90, 60} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		line := ansi.Strip(m.helpLines(keyMap.listHelp)[0])
		if !strings.HasSuffix(line, "? help • q quit") || ansi.StringWidth(line) > width {
			t.Fatalf("help line at %d columns = %q", width, line)
		}
		if strings.Contains(line, "esc") || strings.Contains(line, "↑") {
			t.Fatalf("help line at %d columns offers keys that do not apply: %q", width, line)
		}
	}
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	press(m, tea.Key{Code: 'F', Text: "F"})
	if line := ansi.Strip(m.helpLines(keyMap.listHelp)[0]); !strings.HasPrefix(line, "esc clear filters") {
		t.Fatalf("with a filter active the help line = %q", line)
	}
}

func TestHelpOverlayScrollsOverAnEmptyPane(t *testing.T) {
	m := newPaneModel(t, 60, 14, manyPRs(5), nil)
	m.applyRepository("acme/empty")
	pressHelp(m)
	first := ansi.Strip(m.View().Content)
	press(m, tea.Key{Code: 'j', Text: "j"})
	if ansi.Strip(m.View().Content) == first {
		t.Fatal("j did not scroll the help overlay over an empty pane")
	}
}

func TestHelpOverlayTakesNoMouseInput(t *testing.T) {
	m := newPaneModel(t, 120, 30, manyPRs(5), nil)
	press(m, mouseKey)
	line := lineOf(m, 3)
	pressHelp(m)
	if mode := m.View().MouseMode; mode != tea.MouseModeNone {
		t.Fatalf("mouse mode under the help overlay = %v", mode)
	}
	cursor := m.panes[paneMine].table.Cursor()
	click(m, line)
	m.Update(tea.MouseWheelMsg{X: 10, Y: line, Button: tea.MouseWheelDown})
	if m.panes[paneMine].table.Cursor() != cursor {
		t.Fatal("the mouse moved the cursor behind the help overlay")
	}
}
