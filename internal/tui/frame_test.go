package tui

import (
	"fmt"
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

func TestDismissingAGoneRowLaysOutTheTablesTheFramePlans(t *testing.T) {
	// A gone row's dismissal shortens My PRs, which gives Merged a line
	// without changing which panes are drawn.
	m := mergedLayoutModel(t, 140, 26, manyPRs(8), nil, mergedPRs(15))
	updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: manyPRs(7), Merged: mergedPRs(15)}})
	if len(m.panes[paneMine].gone) != 1 {
		t.Fatalf("gone %v", m.panes[paneMine].gone)
	}
	before := m.framePlan()
	press(m, tea.Key{Code: 'G', Text: "G"})
	rest(m)
	press(m, tea.Key{Code: 'k', Text: "k"})
	if len(m.panes[paneMine].gone) != 0 {
		t.Fatalf("gone row not dismissed: %v", m.panes[paneMine].gone)
	}
	plan := m.framePlan()
	if plan.tables == before.tables {
		t.Fatalf("fixture: the dismissal left the plan's tables at %v", plan.tables)
	}
	assertTablesFollowPlan(t, m)
}

// assertTablesFollowPlan checks that every pane on screen draws its title
// where the frame plan puts it, its table is built at the planned height,
// and each row line hit testing finds draws that row.
func assertTablesFollowPlan(t *testing.T, m *model) {
	t.Helper()
	plan := m.framePlan()
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	for _, p := range plan.screen {
		if !strings.Contains(lines[p.top], paneSpecs[p.id].name) {
			t.Errorf("line %d = %q, want %s's title", p.top, lines[p.top], paneSpecs[p.id].name)
		}
		pane := &m.panes[p.id]
		if rowCount(pane) == 0 {
			continue
		}
		if built := tableHeaderLen + pane.table.Height(); built != plan.tables[p.id] {
			t.Errorf("%s table built %d lines, planned %d", paneSpecs[p.id].name, built, plan.tables[p.id])
		}
		for line := p.top + 1 + tableHeaderLen; line <= p.top+p.body; line++ {
			hit, ok := m.hitTest(line)
			if !ok || hit.pane != p.id || hit.row < 0 {
				continue
			}
			pr, _, _ := m.paneRow(p.id, hit.row)
			if want := fmt.Sprintf("#%d", pr.Number); !strings.Contains(lines[line], want) {
				t.Errorf("line %d = %q, hit row %d is %s", line, lines[line], hit.row, want)
			}
		}
	}
}

func TestPleadDismissalWithTheLegendLaysOutTheTablesTheFramePlans(t *testing.T) {
	// Dismissing the only marker drops the legend's plead entry, which gives
	// the tables a line.
	for _, height := range []int{30, 40, 50, 60} {
		t.Run(fmt.Sprint(height), func(t *testing.T) {
			review := reviewPRs(21)
			review[0].RequestedAgain = true
			m := newPaneModel(t, 140, height, manyPRs(20), review)
			pressL(m)
			m.setFocus(paneReview)
			before := m.framePlan()
			press(m, tea.Key{Code: 'X', Text: "X"})
			if m.pleadShown() {
				t.Fatalf("X left the marker")
			}
			if m.framePlan().tables == before.tables {
				t.Fatalf("fixture: the dismissal left the plan's tables at %v", before.tables)
			}
			assertTablesFollowPlan(t, m)
		})
	}
}

func TestReadingAWokeRowWithTheLegendLaysOutTheTablesTheFramePlans(t *testing.T) {
	// Reading the only woke row drops the legend's woke entry, which gives
	// the tables a line.
	for _, height := range []int{30, 40, 50, 60} {
		t.Run(fmt.Sprint(height), func(t *testing.T) {
			mine := manyPRs(20)
			m := newPaneModel(t, 140, height, mine, reviewPRs(21))
			m.markWoke(keyOf(&mine[0]), "snooze ended")
			m.rebuildPRTable(false)
			pressL(m)
			m.setFocus(paneMine)
			before := m.framePlan()
			rest(m)
			press(m, tea.Key{Code: 'j', Text: "j"})
			if m.wokeShown() {
				t.Fatalf("leaving the row kept its woke tag")
			}
			if m.framePlan().tables == before.tables {
				t.Fatalf("fixture: reading the row left the plan's tables at %v", before.tables)
			}
			assertTablesFollowPlan(t, m)
		})
	}
}
