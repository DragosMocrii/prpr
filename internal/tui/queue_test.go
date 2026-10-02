package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

func queueModel(t *testing.T, width, height int) *model {
	t.Helper()
	mine := manyPRs(4)
	mine[1].Queue = &github.QueueEntry{Provider: "Trunk", State: github.QueueTesting, Detail: "testing on #9"}
	mine[3].Queue = &github.QueueEntry{Provider: "GitHub", State: github.QueueQueued, Detail: "position 2"}
	return newPaneModel(t, width, height, mine, reviewPRs(3))
}

func TestQueuedPullRequestsMoveToTheQueuePane(t *testing.T) {
	m := queueModel(t, 120, 40)
	if len(m.panes[paneMine].visible) != 2 || len(m.panes[paneQueue].visible) != 2 {
		t.Fatalf("visible mine %v queue %v", m.panes[paneMine].visible, m.panes[paneQueue].visible)
	}
	view := ansi.Strip(strings.Join(assertBounded(t, m, 120, 40), "\n"))
	mineAt, queueAt, reviewAt := strings.Index(view, "My PRs (2)"), strings.Index(view, "Merge queue (2)"), strings.Index(view, "Review requested (3)")
	if mineAt < 0 || queueAt < mineAt || reviewAt < queueAt {
		t.Fatalf("pane order:\n%s", view)
	}
}

func TestQueuePaneIsNotDrawnWhenEmpty(t *testing.T) {
	m := newPaneModel(t, 120, 40, manyPRs(4), reviewPRs(3))
	view := ansi.Strip(strings.Join(assertBounded(t, m, 120, 40), "\n"))
	if strings.Contains(view, "Merge queue") || len(m.drawnPanes()) != 2 {
		t.Fatalf("empty queue pane drawn:\n%s", view)
	}
}

func TestTabCyclesThroughDrawnPanes(t *testing.T) {
	m := queueModel(t, 120, 40)
	var order []paneID
	for range 4 {
		press(m, tea.Key{Code: tea.KeyTab})
		order = append(order, m.focus)
	}
	if order[0] != paneQueue || order[1] != paneReview || order[2] != paneMine || order[3] != paneQueue {
		t.Fatalf("tab order = %v", order)
	}
	press(m, tea.Key{Code: tea.KeyTab, Mod: tea.ModShift})
	if m.focus != paneMine {
		t.Fatalf("shift+tab from queue = %v", m.focus)
	}
}

func TestQueueEmptyingMovesFocusToADrawnPane(t *testing.T) {
	m := queueModel(t, 120, 40)
	m.setFocus(paneQueue)
	snapshot := m.snapshot
	snapshot.PullRequests = manyPRs(4)
	m.applySnapshot(snapshot)
	if m.focus == paneQueue || slicesContains(m.drawnPanes(), paneQueue) {
		t.Fatalf("focus %v on an undrawn pane", m.focus)
	}
}

func TestMovingIntoTheQueueIsNotGone(t *testing.T) {
	m := newPaneModel(t, 120, 40, manyPRs(4), reviewPRs(3))
	snapshot := m.snapshot
	snapshot.PullRequests = manyPRs(4)
	snapshot.PullRequests[0].Queue = &github.QueueEntry{Provider: "Trunk", State: github.QueueQueued}
	m.applySnapshot(snapshot)
	if len(m.panes[paneMine].gone) != 0 || len(m.panes[paneQueue].gone) != 0 {
		t.Fatalf("moved row is gone: mine %v queue %v", m.panes[paneMine].gone, m.panes[paneQueue].gone)
	}
	// Merged: the queued row leaves the fetch and is gone from the queue pane.
	snapshot.PullRequests = manyPRs(4)[1:]
	m.applySnapshot(snapshot)
	if len(m.panes[paneQueue].gone) != 1 || len(m.panes[paneMine].gone) != 0 {
		t.Fatalf("merged row gone: mine %v queue %v", m.panes[paneMine].gone, m.panes[paneQueue].gone)
	}
}

func TestThreePanesFitEverySize(t *testing.T) {
	m := queueModel(t, 120, 40)
	for _, size := range [][2]int{{40, 8}, {40, 14}, {60, 18}, {80, 24}, {140, 50}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := assertBounded(t, m, size[0], size[1])
		for row := range lines {
			if hit, ok := m.hitTest(row); ok && hit.row >= 0 {
				if _, _, ok := m.paneRow(hit.pane, hit.row); !ok {
					t.Fatalf("%v: line %d hits no row", size, row)
				}
			}
		}
	}
}

func TestQueuePaneColumnsAndOrder(t *testing.T) {
	mine := manyPRs(5)
	for i, state := range []github.QueueState{github.QueueSubmitted, github.QueuePassed, github.QueueUnknown, github.QueueTesting} {
		mine[i].Queue = &github.QueueEntry{Provider: "Trunk", State: state, Detail: "d" + string(rune('a'+i))}
	}
	m := newPaneModel(t, 140, 40, mine, reviewPRs(1))
	columns := m.panes[paneQueue].table.Columns()
	if columns[1].Title != m.icons.header("Queue") || columns[len(columns)-1].Title != "Detail" {
		t.Fatalf("queue columns = %+v", columns)
	}
	var states []string
	for _, row := range m.panes[paneQueue].table.Rows() {
		states = append(states, ansi.Strip(row[1]))
	}
	if strings.Join(states, ",") != "passed,testing,submitted,?" {
		t.Fatalf("queue order = %v", states)
	}
}

func TestMovingIntoTheQueueIsMarkedChanged(t *testing.T) {
	m := newPaneModel(t, 120, 40, manyPRs(4), reviewPRs(3))
	snapshot := m.snapshot
	snapshot.PullRequests = manyPRs(4)
	snapshot.PullRequests[0].Queue = &github.QueueEntry{Provider: "Trunk", State: github.QueueQueued}
	m.applySnapshot(snapshot)
	if mark := m.tracker(paneQueue).mark(&m.snapshot.PullRequests[0]); mark.kind != markChanged || mark.cells&cellQueue == 0 {
		t.Fatalf("moved row mark = %+v, want changed queue cell", mark)
	}
}

func TestQueueStateChangeIsMarked(t *testing.T) {
	m := queueModel(t, 120, 40)
	snapshot := m.snapshot
	snapshot.PullRequests = append([]github.PullRequest(nil), snapshot.PullRequests...)
	pr := snapshot.PullRequests[1]
	pr.Queue = &github.QueueEntry{Provider: "Trunk", State: github.QueuePassed, Detail: "tested on #9"}
	snapshot.PullRequests[1] = pr
	m.applySnapshot(snapshot)
	if mark := m.tracker(paneQueue).mark(&snapshot.PullRequests[1]); mark.kind != markChanged || mark.cells&cellQueue == 0 {
		t.Fatalf("mark = %+v", mark)
	}
}

func slicesContains(ids []paneID, id paneID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
