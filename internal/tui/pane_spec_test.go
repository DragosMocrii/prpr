package tui

import (
	"testing"

	"github.com/DragosMocrii/prpr/internal/github"
)

func TestEveryPaneHasASpec(t *testing.T) {
	for _, id := range paneIDs {
		spec := paneSpecs[id]
		if spec.name == "" || spec.empty == nil || len(spec.lists) == 0 || len(spec.columns) == 0 {
			t.Errorf("pane %d spec = %+v", id, spec)
		}
		if spec.collapsible && spec.saveCollapsed == nil {
			t.Errorf("%s is collapsible but cannot save it", spec.name)
		}
	}
}

func TestToggleCollapsedIgnoresAPaneThatIsNotCollapsible(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	// H is enabled only while Merged has rows.
	updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine, ReviewRequests: review,
		Merged: []github.PullRequest{{Repository: "acme/api", Number: 100, Title: "merged"}}}})
	m.toggleCollapsed(paneMine)
	if m.collapsed[paneMine] {
		t.Fatal("My PRs collapsed")
	}
	pressMsg(m, letter("H"))
	if !m.collapsed[paneMerged] {
		t.Fatal("H did not collapse Merged")
	}
}
