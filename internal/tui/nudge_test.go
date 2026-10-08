package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

var nudgedAt = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// nudgedRequests are three review rows: a pending request, a waiting row
// nudged at urgency, and a plain waiting row.
func nudgedRequests(urgency github.NudgeUrgency, at time.Time) []github.PullRequest {
	prs := reviewPRs(3)
	prs[1].ReviewStatus = github.ReviewApproved
	prs[1].Nudge = &github.Nudge{Urgency: urgency, At: at, Note: "ship"}
	prs[2].ReviewStatus = github.ReviewWaitingOnAuthor
	return prs
}

func reviewCell(m *model, row, column int) string {
	return ansi.Strip(m.panes[paneReview].table.Rows()[row][column])
}

func reviewNumbers(m *model) []int {
	var out []int
	for row := range m.panes[paneReview].visible {
		if pr, _, ok := m.paneRow(paneReview, row); ok {
			out = append(out, pr.Number)
		}
	}
	return out
}

func TestNudgeMarkerWidensTheMarkColumnAndShowsItsUrgency(t *testing.T) {
	m := newPaneModel(t, 140, 30, nil, reviewPRs(2))
	if w := m.panes[paneReview].table.Columns()[0].Width; w != 1 {
		t.Fatalf("mark column without nudges = %d", w)
	}
	for _, u := range []github.NudgeUrgency{github.NudgeLow, github.NudgeNormal, github.NudgeUrgent} {
		m := newPaneModel(t, 140, 30, nil, nudgedRequests(u, nudgedAt))
		if w := m.panes[paneReview].table.Columns()[0].Width; w != nudgeWidth {
			t.Fatalf("%v: mark column = %d, want %d", u, w, nudgeWidth)
		}
		if !strings.Contains(m.View().Content, nudgeIcon(m.icons, u)) {
			t.Fatalf("%v: the list does not draw %q", u, nudgeIcon(m.icons, u))
		}
	}
}

func TestLowNudgesDoNotBlinkOthersDo(t *testing.T) {
	low := newPaneModel(t, 140, 30, nil, nudgedRequests(github.NudgeLow, nudgedAt))
	normal := newPaneModel(t, 140, 30, nil, nudgedRequests(github.NudgeNormal, nudgedAt))
	for _, m := range []*model{low, normal} {
		m.Update(nudgeTickMsg{generation: m.nudgeGeneration})
	}
	if !strings.Contains(low.View().Content, "👋") {
		t.Fatal("a low nudge blinked off")
	}
	if strings.Contains(normal.View().Content, "🔔") {
		t.Fatal("a normal nudge did not blink off")
	}
	normal.Update(nudgeTickMsg{generation: normal.nudgeGeneration - 1})
	if strings.Contains(normal.View().Content, "🔔") {
		t.Fatal("a stale tick changed the blink")
	}
}

func TestUrgentNudgesLeadAndNudgedRowsLeadTheirGroup(t *testing.T) {
	prs := nudgedRequests(github.NudgeUrgent, nudgedAt)
	m := newPaneModel(t, 140, 30, nil, prs)
	if got := reviewNumbers(m); got[0] != prs[1].Number {
		t.Fatalf("order = %v, want the urgent row first", got)
	}
	// Two pending requests: the second, nudged low, leads its group.
	requests := reviewPRs(2)
	requests[1].Nudge = &github.Nudge{Urgency: github.NudgeLow, At: nudgedAt}
	m = newPaneModel(t, 140, 30, nil, requests)
	if got := reviewNumbers(m); got[0] != requests[1].Number || got[1] != requests[0].Number {
		t.Fatalf("order = %v, want the nudged request first", got)
	}
}

func TestANudgedWaitingRowNeedsTheViewer(t *testing.T) {
	m := newPaneModel(t, 140, 30, nil, nudgedRequests(github.NudgeNormal, nudgedAt))
	nudged := &m.snapshot.ReviewRequests[1]
	if m.waiting(nudged) {
		t.Fatal("a nudged approved row still counts as waiting")
	}
	if counts := categoryCounts(m); counts[5] != 2 {
		t.Fatalf("awaiting your review = %d, want the request and the nudged row", counts[5])
	}
}

func TestDismissingANudgeUndoesEverythingItDidUntilAnotherNudge(t *testing.T) {
	m := newPaneModel(t, 140, 30, nil, nudgedRequests(github.NudgeUrgent, nudgedAt))
	m.setFocus(paneReview)
	m.selectPR(paneReview, m.snapshot.ReviewRequests[1].Repository, m.snapshot.ReviewRequests[1].Number)
	press(m, tea.Key{Code: 'X', Text: "X"})
	nudged := &m.snapshot.ReviewRequests[1]
	if m.nudge(nudged) != nil || !m.waiting(nudged) || strings.Contains(m.View().Content, "🚨") {
		t.Fatal("a dismissed nudge still shows or still lifts its row")
	}
	if counts := categoryCounts(m); counts[5] != 1 {
		t.Fatalf("awaiting your review = %d after dismissing", counts[5])
	}
	if got := reviewNumbers(m); got[0] == nudged.Number {
		t.Fatalf("order = %v, a dismissed urgent row still leads", got)
	}
	saved := m.preferences.Dismissals("alice")
	if len(saved) != 1 || !saved[0].Nudged.Equal(nudgedAt) {
		t.Fatalf("saved = %+v", saved)
	}
	// The same nudge stays dismissed; a different one shows, earlier or later.
	updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: nudgedRequests(github.NudgeUrgent, nudgedAt)}})
	if strings.Contains(m.View().Content, "🚨") {
		t.Fatal("the dismissed nudge came back")
	}
	updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: nudgedRequests(github.NudgeUrgent, nudgedAt.Add(-time.Hour))}})
	if !strings.Contains(m.View().Content, "🚨") {
		t.Fatal("a different nudge stayed hidden")
	}
}

func TestDismissalsOfEndedNudgesArePruned(t *testing.T) {
	m := newPaneModel(t, 140, 30, nil, nudgedRequests(github.NudgeNormal, nudgedAt))
	m.setFocus(paneReview)
	m.selectPR(paneReview, m.snapshot.ReviewRequests[1].Repository, m.snapshot.ReviewRequests[1].Number)
	press(m, tea.Key{Code: 'X', Text: "X"})
	ended := nudgedRequests(github.NudgeNormal, nudgedAt)
	ended[1].Nudge = nil
	updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: ended}})
	if saved := m.preferences.Dismissals("alice"); len(saved) != 0 {
		t.Fatalf("a dismissal outlived its nudge: %+v", saved)
	}
}

func TestPreviewRowsNeverShowNudges(t *testing.T) {
	store := testPreferences(t)
	if err := store.Save("alice", ""); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 140, 30)
	m.Init()
	m.Update(previewMsg{generation: m.refreshGeneration, account: m.accountGeneration,
		snapshot: github.Snapshot{Login: "alice", Preview: true, ReviewRequests: nudgedRequests(github.NudgeUrgent, nudgedAt)}})
	if !m.snapshot.Preview || len(m.panes[paneReview].visible) != 3 {
		t.Fatalf("preview not shown: preview %t rows %d", m.snapshot.Preview, len(m.panes[paneReview].visible))
	}
	if strings.Contains(m.View().Content, "🚨") || m.nudgeShown() {
		t.Fatal("a preview showed a nudge")
	}
}
