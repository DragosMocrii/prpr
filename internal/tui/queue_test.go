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

func TestRemovedPullRequestsAreTaggedInMyPRs(t *testing.T) {
	mine := manyPRs(3)
	mine[0].Queue = &github.QueueEntry{Provider: "Trunk", State: github.QueueRemovedFailed}
	mine[1].Queue = &github.QueueEntry{Provider: "Trunk", State: github.QueueRemovedCanceled}
	m := newPaneModel(t, 140, 30, mine, nil)
	text := ansi.Strip(strings.Join(m.listLines(), "\n"))
	if !strings.Contains(text, "queue failed · ") || !strings.Contains(text, "queue canceled · ") || len(m.panes[paneQueue].visible) != 0 {
		t.Fatalf("removed tags:\n%s", text)
	}
}

func TestQueueChangeUnderlinesTheNameInMyPRs(t *testing.T) {
	m := newPaneModel(t, 140, 30, manyPRs(2), nil)
	snapshot := m.snapshot
	snapshot.PullRequests = manyPRs(2)
	snapshot.PullRequests[0].Queue = &github.QueueEntry{Provider: "Trunk", State: github.QueueRemovedFailed}
	m.applySnapshot(snapshot)
	var found bool
	for _, row := range m.paneRows(paneMine, m.paneLayout(paneMine)) {
		for _, cell := range row {
			if strings.Contains(cell, "queue failed") && strings.Contains(cell, underlined) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("name is not underlined after a queue change")
	}
}

func TestQueueRowsCountInNoCategoryAndNotInTheTitle(t *testing.T) {
	mine := manyPRs(2)
	for i := range mine {
		mine[i].Mergeable, mine[i].MergeState = "MERGEABLE", "CLEAN"
	}
	mine[1].Queue = &github.QueueEntry{Provider: "Trunk", State: github.QueuePassed}
	m := newPaneModel(t, 140, 30, mine, nil)
	if got := m.categoryCount(1); got != 1 {
		t.Fatalf("ready count = %d, want 1 (the queued one is not counted)", got)
	}
	if got := m.needYouCount(); got != 1 {
		t.Fatalf("need you = %d", got)
	}
}

func TestRemovalForFailedTestsAlertsAndMovesDoNot(t *testing.T) {
	m := queueModel(t, 120, 40)
	snapshot := m.snapshot
	snapshot.PullRequests = append([]github.PullRequest(nil), snapshot.PullRequests...)
	pr := snapshot.PullRequests[1]
	pr.Queue = &github.QueueEntry{Provider: "Trunk", State: github.QueueRemovedFailed}
	snapshot.PullRequests[1] = pr
	moved := snapshot.PullRequests[0]
	moved.Queue = &github.QueueEntry{Provider: "Trunk", State: github.QueueSubmitted}
	snapshot.PullRequests[0] = moved
	m.snapshot = snapshot
	alerts := m.alerts()
	if len(alerts) != 1 || alerts[0].pr.Number != pr.Number || !strings.Contains(strings.Join(alerts[0].kinds, ","), "merge queue") {
		t.Fatalf("alerts = %+v", alerts)
	}
}

func TestDetailsShowTheQueue(t *testing.T) {
	m := queueModel(t, 140, 40)
	m.setFocus(paneQueue)
	var queueRow []string
	pr, _ := m.selectedPR()
	for _, row := range m.detailRows(pr, false) {
		if row.label == "Queue" {
			queueRow = row.values
		}
	}
	if len(queueRow) == 0 || !strings.Contains(queueRow[0], "Trunk") {
		t.Fatalf("queue row = %q", queueRow)
	}
}

func TestMyPRsEmptyLineNamesTheQueue(t *testing.T) {
	mine := manyPRs(1)
	mine[0].Queue = &github.QueueEntry{Provider: "Trunk", State: github.QueueTesting}
	m := newPaneModel(t, 140, 30, mine, nil)
	if line := m.emptyPaneLine(paneMine); !strings.Contains(line, "outside the merge queue") {
		t.Fatalf("line = %q", line)
	}
	if line := m.emptyPaneLine(paneReview); strings.Contains(line, "queue") {
		t.Fatalf("review line = %q", line)
	}
}

func TestQueueEmptyingFocusGoesToThePaneWithRows(t *testing.T) {
	mine := manyPRs(1)
	mine[0].Queue = &github.QueueEntry{Provider: "Trunk", State: github.QueueTesting}
	m := newPaneModel(t, 140, 40, mine, reviewPRs(2))
	m.setFocus(paneQueue)
	// The pull request leaves the queue as a hidden draft, so My PRs stays empty.
	snapshot := m.snapshot
	snapshot.PullRequests = manyPRs(1)
	snapshot.PullRequests[0].Draft = true
	m.applySnapshot(snapshot)
	if len(m.panes[paneMine].visible) != 0 || m.focus != paneReview {
		t.Fatalf("focus = %v, want review", m.focus)
	}
}

func TestQueuePaneDropsDetailBeforeItsNameGetsTooNarrow(t *testing.T) {
	titles := func(m *model) string {
		var names []string
		for _, column := range m.paneLayout(paneQueue).columns {
			names = append(names, column.Title)
		}
		return strings.Join(names, ",")
	}
	wide := queueModel(t, 140, 30)
	if layout := wide.paneLayout(paneQueue); !layout.detail || !strings.Contains(titles(wide), "Detail") {
		t.Fatalf("width 140 columns %v", titles(wide))
	}
	narrow := queueModel(t, 40, 10)
	layout := narrow.paneLayout(paneQueue)
	if !layout.fits || layout.detail || strings.Contains(titles(narrow), "Detail") {
		t.Fatalf("width 40 fits %v detail %v columns %v", layout.fits, layout.detail, titles(narrow))
	}
	for _, row := range narrow.paneRows(paneQueue, layout) {
		if len(row) != len(layout.columns) {
			t.Fatalf("row has %d cells for %d columns", len(row), len(layout.columns))
		}
	}
	press(narrow, tea.Key{Code: tea.KeyTab})
	if narrow.focus != paneQueue {
		t.Fatalf("focus = %v", narrow.focus)
	}
	view := ansi.Strip(strings.Join(assertBounded(t, narrow, 40, 10), "\n"))
	if strings.Contains(view, "too small") || !strings.Contains(view, "Merge queue") {
		t.Fatalf("queue pane at 40x10:\n%s", view)
	}
}

func TestSelectionFollowsAPullRequestBetweenMyPRsAndTheQueue(t *testing.T) {
	for _, size := range [][2]int{{140, 40}, {40, 10}} {
		m := queueModel(t, size[0], size[1])
		// The queue pane lists #2 (testing), then #4 (queued).
		press(m, tea.Key{Code: tea.KeyTab})
		press(m, tea.Key{Code: tea.KeyDown})
		press(m, tea.Key{Code: tea.KeyEnter})
		if pr, ok := m.selectedPR(); !ok || pr.Number != 4 || m.focus != paneQueue || !m.details {
			t.Fatalf("%v: setup selected %+v focus %v details %v", size, pr, m.focus, m.details)
		}
		snapshot := m.snapshot
		snapshot.PullRequests = append([]github.PullRequest(nil), snapshot.PullRequests...)
		snapshot.PullRequests[3].Queue = &github.QueueEntry{Provider: "GitHub", State: github.QueueRemovedFailed}
		m.applySnapshot(snapshot)
		if pr, ok := m.selectedPR(); !ok || pr.Number != 4 || m.focus != paneMine || !m.details {
			t.Fatalf("%v: out of the queue selected %+v focus %v details %v", size, pr, m.focus, m.details)
		}
		assertBounded(t, m, size[0], size[1])
		if m.layoutPanes().tables[m.focus] == 0 {
			t.Fatalf("%v: focused pane %v is not drawn", size, m.focus)
		}

		// Back into the queue.
		snapshot.PullRequests = append([]github.PullRequest(nil), snapshot.PullRequests...)
		snapshot.PullRequests[3].Queue = &github.QueueEntry{Provider: "GitHub", State: github.QueueSubmitted}
		m.applySnapshot(snapshot)
		if pr, ok := m.selectedPR(); !ok || pr.Number != 4 || m.focus != paneQueue || !m.details {
			t.Fatalf("%v: into the queue selected %+v focus %v details %v", size, pr, m.focus, m.details)
		}
		assertBounded(t, m, size[0], size[1])
		if m.layoutPanes().tables[m.focus] == 0 {
			t.Fatalf("%v: focused pane %v is not drawn", size, m.focus)
		}
	}
}

func TestSelectionFollowsTheLastQueuedPullRequestOut(t *testing.T) {
	mine := manyPRs(3)
	mine[2].Queue = &github.QueueEntry{Provider: "Trunk", State: github.QueueTesting}
	m := newPaneModel(t, 140, 40, mine, reviewPRs(2))
	m.setFocus(paneQueue)
	press(m, tea.Key{Code: tea.KeyEnter})
	snapshot := m.snapshot
	snapshot.PullRequests = manyPRs(3)
	snapshot.PullRequests[2].Queue = &github.QueueEntry{Provider: "Trunk", State: github.QueueRemovedFailed}
	m.applySnapshot(snapshot)
	if pr, ok := m.selectedPR(); !ok || pr.Number != 3 || m.focus != paneMine || !m.details {
		t.Fatalf("selected %+v focus %v", pr, m.focus)
	}
}

func TestLeavingTheQueueIsMarkedWithoutAnEntry(t *testing.T) {
	mine := manyPRs(2)
	mine[0].Queue = &github.QueueEntry{Provider: "GitHub", State: github.QueueQueued}
	m := newPaneModel(t, 140, 30, mine, nil)
	snapshot := m.snapshot
	snapshot.PullRequests = manyPRs(2)
	m.applySnapshot(snapshot)
	pr := &m.snapshot.PullRequests[0]
	if mark := m.tracker(paneMine).mark(pr); mark.kind != markChanged || mark.cells&cellQueue == 0 {
		t.Fatalf("mark = %+v, want changed queue cell", mark)
	}
	var reversed bool
	for _, row := range m.paneRows(paneMine, m.paneLayout(paneMine)) {
		for _, cell := range row {
			if strings.Contains(cell, "title") && strings.Contains(cell, underlined) {
				reversed = true
			}
		}
	}
	if !reversed {
		t.Fatal("name is not underlined after leaving the queue")
	}
}

func TestQueuePaneTakesOnlyTheRowsItNeeds(t *testing.T) {
	mine := manyPRs(30)
	mine[0].Queue = &github.QueueEntry{Provider: "Trunk", State: github.QueueTesting}
	m := newPaneModel(t, 120, 40, mine, reviewPRs(30))
	layout := m.layoutPanes()
	if layout.single || layout.tables[paneQueue] != minDualTableHeight {
		t.Fatalf("queue table = %d, want %d", layout.tables[paneQueue], minDualTableHeight)
	}
	rows := m.height - m.listChromeHeight() - len(m.drawnPanes())
	if total := layout.tables[paneMine] + layout.tables[paneQueue] + layout.tables[paneReview]; total != rows {
		t.Fatalf("tables %v use %d of %d lines", layout.tables, total, rows)
	}
	if layout.tables[paneMine] < layout.tables[paneReview] || layout.tables[paneMine]-layout.tables[paneReview] > 1 {
		t.Fatalf("mine %d review %d do not share the rest", layout.tables[paneMine], layout.tables[paneReview])
	}
	assertBounded(t, m, 120, 40)
}

func TestPreviewDrawsNoQueuePane(t *testing.T) {
	store := testPreferences(t)
	if err := store.Save("alice", ""); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 120, 40)
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", Preview: true, PullRequests: manyPRs(4), ReviewRequests: reviewPRs(2)}})
	if len(m.panes[paneMine].visible) != 4 || len(m.panes[paneQueue].visible) != 0 || slicesContains(m.drawnPanes(), paneQueue) {
		t.Fatalf("preview mine %v queue %v drawn %v", m.panes[paneMine].visible, m.panes[paneQueue].visible, m.drawnPanes())
	}
	if view := ansi.Strip(strings.Join(assertBounded(t, m, 120, 40), "\n")); strings.Contains(view, "Merge queue") {
		t.Fatalf("preview draws the queue pane:\n%s", view)
	}
}

func TestSubmittedRowsStillAlertOnCIAndReviews(t *testing.T) {
	mine := manyPRs(2)
	mine[0].Queue = &github.QueueEntry{Provider: "Trunk", State: github.QueueSubmitted}
	mine[1].Queue = &github.QueueEntry{Provider: "Trunk", State: github.QueueTesting}
	m := newPaneModel(t, 140, 30, mine, nil)
	snapshot := m.snapshot
	snapshot.PullRequests = append([]github.PullRequest(nil), mine...)
	for i := range snapshot.PullRequests {
		snapshot.PullRequests[i].Checks = "FAILURE"
	}
	m.snapshot = snapshot
	alerts := m.alerts()
	if len(alerts) != 1 || alerts[0].pr.Number != 1 || strings.Join(alerts[0].kinds, ",") != "failing CI" {
		t.Fatalf("alerts = %+v, want failing CI for the submitted row only", alerts)
	}
	snapshot.PullRequests = append([]github.PullRequest(nil), mine...)
	for i := range snapshot.PullRequests {
		snapshot.PullRequests[i].ReviewDecision = "CHANGES_REQUESTED"
	}
	m.snapshot = snapshot
	if alerts := m.alerts(); len(alerts) != 1 || alerts[0].pr.Number != 1 || strings.Join(alerts[0].kinds, ",") != "changes requested" {
		t.Fatalf("alerts = %+v, want changes requested for the submitted row only", alerts)
	}
}

func TestSubmittedRowsNeverAlertReady(t *testing.T) {
	mine := manyPRs(1)
	mine[0].Queue = &github.QueueEntry{Provider: "Trunk", State: github.QueueSubmitted}
	mine[0].Mergeable, mine[0].MergeState = "MERGEABLE", "BLOCKED"
	m := newPaneModel(t, 140, 30, mine, nil)
	snapshot := m.snapshot
	snapshot.PullRequests = append([]github.PullRequest(nil), mine...)
	snapshot.PullRequests[0].MergeState = "CLEAN"
	m.snapshot = snapshot
	if alerts := m.alerts(); len(alerts) != 0 {
		t.Fatalf("alerts = %+v, want none", alerts)
	}
}

func TestQueueLegendOnlyWithQueueRows(t *testing.T) {
	m := newPaneModel(t, 240, 80, manyPRs(2), reviewPRs(1))
	m.icons = &nerdIcons
	pressL(m)
	queueIcon := nerdIcons.header("Queue")
	for _, section := range m.legendSections() {
		if section.label == "Queue" {
			t.Fatal("Queue section without queue entries")
		}
		for _, item := range section.items {
			if strings.HasPrefix(ansi.Strip(item), queueIcon) {
				t.Fatalf("Queue column entry without queue entries: %q", ansi.Strip(item))
			}
		}
	}
	// A removed tag on screen brings the section back.
	snapshot := m.snapshot
	snapshot.PullRequests = manyPRs(2)
	snapshot.PullRequests[1].Queue = &github.QueueEntry{Provider: "Trunk", State: github.QueueRemovedCanceled}
	m.applySnapshot(snapshot)
	if !strings.Contains(legendText(m), "removed: canceled") {
		t.Fatalf("legend without the removed tag's entry:\n%s", legendText(m))
	}
}
