package tui

import (
	"slices"
	"testing"
	"time"

	"github.com/DragosMocrii/prpr/internal/github"
)

func TestMyPRsSortReadyToMergeFirstThenOldestCreated(t *testing.T) {
	pr := func(number int, state string, created int) github.PullRequest {
		p := changePR(number, "acme/a")
		p.MergeState, p.CreatedAt = state, changeTime.Add(time.Duration(created)*time.Hour)
		return p
	}
	draft := pr(5, "CLEAN", 1)
	draft.Draft = true
	// Fetched in updated order; #6 and #7 tie on created time.
	m := changesModel(t, pr(1, "BLOCKED", 3), pr(2, "CLEAN", 4), draft, pr(3, "UNSTABLE", 2), pr(4, "BEHIND", 0), pr(6, "CLEAN", 9), pr(7, "CLEAN", 9))
	// A clean draft is never ready, so it sorts with the rest.
	m.toggleDrafts()
	order := func() []int {
		var numbers []int
		for row := range rowCount(&m.panes[paneMine]) {
			p, _, _ := m.paneRow(paneMine, row)
			numbers = append(numbers, p.Number)
		}
		return numbers
	}
	if got, want := order(), []int{3, 2, 6, 7, 4, 5, 1}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}

	// #1 becomes mergeable and moves up, still selected.
	m.selectPR(paneMine, "acme/a", 1)
	updateSnapshot(m, "alice", pr(1, "CLEAN", 3), pr(2, "CLEAN", 4), draft, pr(3, "UNSTABLE", 2), pr(4, "BEHIND", 0), pr(6, "CLEAN", 9), pr(7, "CLEAN", 9))
	if got, want := order(), []int{3, 1, 2, 6, 7, 4, 5}; !slices.Equal(got, want) {
		t.Fatalf("order after #1 became mergeable = %v, want %v", got, want)
	}
	if selected, _ := m.selectedPR(); selected.Number != 1 {
		t.Fatalf("selection = #%d, want #1", selected.Number)
	}

	// The review list keeps the fetched order.
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: []github.PullRequest{pr(9, "BLOCKED", 0), pr(8, "CLEAN", 5)}}})
	if p, _, _ := m.paneRow(paneReview, 0); p.Number != 9 {
		t.Fatalf("review list reordered: first #%d", p.Number)
	}
}
