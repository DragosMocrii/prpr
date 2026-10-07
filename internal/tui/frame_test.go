package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

func frameModel(t *testing.T, width, height int) *model {
	t.Helper()
	m := newPaneModel(t, width, height, manyPRs(3), reviewPRs(2))
	updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: manyPRs(3), ReviewRequests: reviewPRs(2), Merged: mergedPRs(2)}})
	return m
}

func TestFramePlanMatchesTheDrawnLines(t *testing.T) {
	// minimumHeight is 8; below it the list is not drawn.
	for _, height := range []int{10, 14, 18, 24, 30, 40} {
		for _, collapsed := range []bool{false, true} {
			m := frameModel(t, 120, height)
			if collapsed {
				m.toggleCollapsed(paneMerged)
			}
			lines := strings.Split(ansi.Strip(m.View().Content), "\n")
			for _, p := range m.framePlan().screen {
				title := lines[p.top]
				if !strings.Contains(title, paneSpecs[p.id].name) {
					t.Errorf("h%d collapsed %v: line %d = %q, want %s's title", height, collapsed, p.top, title, paneSpecs[p.id].name)
				}
				if hit, ok := m.hitTest(p.top); !ok || hit.pane != p.id || !hit.title {
					t.Errorf("h%d: hit at %d = %+v", height, p.top, hit)
				}
				if p.body > 0 {
					if hit, ok := m.hitTest(p.top + p.body); !ok || hit.pane != p.id || hit.title {
						t.Errorf("h%d: last body line hit = %+v", height, hit)
					}
				}
			}
		}
	}
}

func TestTabCyclesEveryPaneInSinglePaneMode(t *testing.T) {
	// As TestShortTerminalShowsOnlyFocusedPane: 10 lines, two full lists.
	m := newPaneModel(t, 80, 10, manyPRs(30), reviewPRs(30))
	if !m.framePlan().single {
		t.Fatal("80x10 with two full lists should be single-pane")
	}
	seen := map[paneID]bool{m.focus: true}
	for range 4 {
		pressMsg(m, tea.KeyPressMsg{Code: tea.KeyTab})
		seen[m.focus] = true
	}
	if !seen[paneMine] || !seen[paneReview] {
		t.Fatalf("Tab reached %v", seen)
	}
}

func TestSetFocusRefusesACollapsedPane(t *testing.T) {
	m := frameModel(t, 120, 40)
	m.toggleCollapsed(paneMerged)
	m.setFocus(paneMerged)
	if m.focus == paneMerged || slices.Contains(m.framePlan().focusable(m), paneMerged) {
		t.Fatal("a collapsed pane took the focus")
	}
}
