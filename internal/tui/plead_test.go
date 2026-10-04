package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

// pleadRequests are two pending review requests; the first asks again.
func pleadRequests(requested time.Time) []github.PullRequest {
	prs := reviewPRs(2)
	prs[0].RequestedAgain, prs[0].WaitingSince = true, requested
	prs[1].WaitingSince = requested
	return prs
}

func reviewCell(m *model, row, column int) string {
	return ansi.Strip(m.panes[paneReview].table.Rows()[row][column])
}

func TestPleadMarksOnlyRequestsAskedAgain(t *testing.T) {
	m := newPaneModel(t, 140, 30, nil, reviewPRs(2))
	plain := m.panes[paneReview].table.Columns()
	if plain[0].Width != 1 {
		t.Fatalf("without a request asked again the mark column widened: %+v", plain)
	}
	m = newPaneModel(t, 140, 30, nil, pleadRequests(time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)))
	if columns := m.panes[paneReview].table.Columns(); len(columns) != len(plain) || columns[0].Width != pleadWidth {
		t.Fatalf("the marker should widen the mark column, not add one: %+v", columns)
	}
	if got := reviewCell(m, 0, 0); got != pleadIcon {
		t.Fatalf("first cell of a request asked again = %q, want %q", got, pleadIcon)
	}
	if got := strings.TrimSpace(reviewCell(m, 1, 0)); got != "" {
		t.Fatalf("first cell of a first request = %q, want blank", got)
	}
	if !strings.Contains(m.View().Content, pleadIcon) {
		t.Fatal("the list does not draw the marker")
	}
}

func TestPleadBlinksOnItsOwnTick(t *testing.T) {
	m := newPaneModel(t, 140, 30, nil, pleadRequests(time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)))
	stale := pleadTickMsg{generation: m.pleadGeneration - 1}
	m.Update(stale)
	if got := reviewCell(m, 0, 0); got != pleadIcon {
		t.Fatalf("a stale tick changed the marker to %q", got)
	}
	m.Update(pleadTickMsg{generation: m.pleadGeneration})
	if got := strings.TrimSpace(reviewCell(m, 0, 0)); got != "" {
		t.Fatalf("after one tick the marker = %q, want blank", got)
	}
	m.Update(pleadTickMsg{generation: m.pleadGeneration})
	if got := reviewCell(m, 0, 0); got != pleadIcon {
		t.Fatalf("after two ticks the marker = %q, want %q", got, pleadIcon)
	}
}

func TestPleadAlternatesWithTheChangeMark(t *testing.T) {
	m := newPaneModel(t, 140, 30, nil, nil)
	updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: pleadRequests(time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))}})
	if got := reviewCell(m, 0, 0); got != pleadIcon {
		t.Fatalf("mark cell of a new request asked again = %q, want %q", got, pleadIcon)
	}
	m.Update(pleadTickMsg{generation: m.pleadGeneration})
	if got := strings.TrimSpace(reviewCell(m, 0, 0)); got != "+" {
		t.Fatalf("while the marker blinks off the mark cell = %q, want the change mark", got)
	}
	if got := strings.TrimSpace(reviewCell(m, 1, 0)); got != "+" {
		t.Fatalf("mark cell of a new first request = %q, want the change mark", got)
	}
}

func TestDismissingPleadSavesItUntilANewerRequest(t *testing.T) {
	requested := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	m := newPaneModel(t, 140, 30, nil, pleadRequests(requested))
	m.setFocus(paneReview)
	press(m, tea.Key{Code: 'X', Text: "X"})
	if columns := m.panes[paneReview].table.Columns(); columns[0].Width != 1 {
		t.Fatalf("the marker column stayed after its only marker was dismissed: %+v", columns)
	}
	saved := m.preferences.Dismissals("alice")
	if len(saved) != 1 || saved[0].Number != 100 || !saved[0].Requested.Equal(requested) {
		t.Fatalf("saved dismissals = %+v", saved)
	}

	// The same request stays dismissed.
	updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: pleadRequests(requested)}})
	if strings.Contains(m.View().Content, pleadIcon) {
		t.Fatal("a dismissed request shows its marker again")
	}
	// A newer request asks again.
	updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: pleadRequests(requested.Add(time.Hour))}})
	if got := reviewCell(m, 0, 0); got != pleadIcon {
		t.Fatalf("a newer request's marker = %q, want %q", got, pleadIcon)
	}
}

func TestPleadEndsWithAReviewAndDismissalsArePruned(t *testing.T) {
	requested := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	m := newPaneModel(t, 140, 30, nil, pleadRequests(requested))
	m.setFocus(paneReview)
	press(m, tea.Key{Code: 'X', Text: "X"})

	reviewed := pleadRequests(requested)
	reviewed[0].RequestedAgain, reviewed[0].ReviewStatus = false, github.ReviewWaitingOnAuthor
	updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: reviewed}})
	if strings.Contains(m.View().Content, pleadIcon) {
		t.Fatal("a reviewed pull request still shows the marker")
	}
	if saved := m.preferences.Dismissals("alice"); len(saved) != 0 {
		t.Fatalf("a dismissal outlived its pending request: %+v", saved)
	}
}

func TestPreviewRowsNeverPlead(t *testing.T) {
	store := testPreferences(t)
	if err := store.Save("alice", ""); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 140, 30)
	m.Init()
	m.Update(previewMsg{generation: m.refreshGeneration, account: m.accountGeneration,
		snapshot: github.Snapshot{Login: "alice", Preview: true, ReviewRequests: pleadRequests(time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))}})
	if !m.snapshot.Preview || len(m.panes[paneReview].visible) != 2 {
		t.Fatalf("preview not shown: preview %t rows %d", m.snapshot.Preview, len(m.panes[paneReview].visible))
	}
	if strings.Contains(m.View().Content, pleadIcon) || m.panes[paneReview].table.Columns()[0].Width != 1 {
		t.Fatal("a preview row shows the marker")
	}
}
