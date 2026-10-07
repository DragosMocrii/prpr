package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

// titleLineOf is the screen line of a pane's title, or -1.
func titleLineOf(m *model, name string) int {
	for y, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if strings.Contains(line, name+" (") {
			return y
		}
	}
	return -1
}

func TestHCollapsesMergedToItsTitleAndSavesIt(t *testing.T) {
	m := mergedLayoutModel(t, 140, 30, manyPRs(2), nil, mergedPRs(3))
	m.setFocus(paneMerged)
	pressMsg(m, letter("H"))
	if !m.mergedCollapsed || !m.preferences.MergedCollapsed() {
		t.Fatalf("collapsed %t, saved %t", m.mergedCollapsed, m.preferences.MergedCollapsed())
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Merged (3)") {
		t.Fatalf("collapsed Merged lost its title and count:\n%s", view)
	}
	for _, pr := range mergedPRs(3) {
		if lineOf(m, pr.Number) >= 0 {
			t.Fatalf("collapsed Merged still draws #%d:\n%s", pr.Number, view)
		}
	}
	if m.focus == paneMerged {
		t.Fatal("focus stayed on the collapsed pane")
	}
	for range 4 {
		pressMsg(m, tea.KeyPressMsg{Code: tea.KeyTab})
		if m.focus == paneMerged {
			t.Fatal("tab focused the collapsed pane")
		}
	}
	assertBounded(t, m, 140, 30)
	pressMsg(m, letter("H"))
	if m.mergedCollapsed || m.preferences.MergedCollapsed() || lineOf(m, 200) < 0 {
		t.Fatalf("H did not show Merged again")
	}
}

func TestCollapsedMergedFitsWhereItsTableWouldNot(t *testing.T) {
	// The other lists fill the screen, so the table has no room.
	m := mergedLayoutModel(t, 100, 20, manyPRs(12), reviewPRs(12), mergedPRs(3))
	if slices.Contains(m.drawnPanes(), paneMerged) {
		t.Fatal("expanded Merged drawn without room")
	}
	m.toggleMergedCollapsed()
	if !slices.Contains(m.drawnPanes(), paneMerged) || titleLineOf(m, "Merged") < 0 {
		t.Fatalf("collapsed Merged not drawn:\n%s", ansi.Strip(m.View().Content))
	}
	for _, size := range [][2]int{{40, 10}, {80, 14}, {100, 20}, {140, 30}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		assertBounded(t, m, size[0], size[1])
	}
}

func TestCollapsedMergedCountsNewRowsAndKeepsFocusOut(t *testing.T) {
	m := notifyModel(t, "")
	a, b := changePR(1, "acme/a"), changePR(2, "acme/a")
	fetchMerged(m, []github.PullRequest{a, b}, []github.PullRequest{mergedPR(changePR(9, "acme/a"), time.Hour)})
	m.toggleMergedCollapsed()
	m.selectPR(paneMine, "acme/a", 2)
	fetchMerged(m, []github.PullRequest{a}, []github.PullRequest{mergedPR(b, 0), mergedPR(changePR(9, "acme/a"), time.Hour)})
	if m.focus == paneMerged {
		t.Fatal("focus followed a merge into the collapsed pane")
	}
	line := strings.Split(ansi.Strip(m.View().Content), "\n")[titleLineOf(m, "Merged")]
	if !strings.Contains(line, "Merged (2)") || !strings.Contains(line, "new") {
		t.Fatalf("collapsed title %q does not count the new merge", line)
	}
}

func TestClickingTheCollapsedTitleShowsMerged(t *testing.T) {
	m := mergedLayoutModel(t, 140, 30, manyPRs(2), nil, mergedPRs(3))
	m.mouse = true
	m.toggleMergedCollapsed()
	click(m, titleLineOf(m, "Merged"))
	if m.mergedCollapsed {
		t.Fatal("clicking the collapsed title did not show Merged")
	}
}
