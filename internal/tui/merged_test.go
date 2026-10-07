package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/preferences"
)

// fetchMerged applies a full fetch with a merged list; it returns the
// notifications it sent.
func fetchMerged(m *model, mine, merged []github.PullRequest) []string {
	_, cmd := updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine, Merged: merged}})
	return notifications(cmd)
}

func mergedPR(pr github.PullRequest, ago time.Duration) github.PullRequest {
	pr.MergedAt, pr.MergedBy = changeTime.Add(-ago), "bob"
	return pr
}

func TestAMergingPullRequestMovesToMerged(t *testing.T) {
	m := notifyModel(t, "")
	a, b := changePR(1, "acme/a"), changePR(2, "acme/a")
	fetchMerged(m, []github.PullRequest{a, b}, nil)
	if slices.Contains(m.drawnPanes(), paneMerged) {
		t.Fatal("an empty Merged pane is drawn")
	}
	m.selectPR(paneMine, "acme/a", 2)
	merged := mergedPR(b, 0)
	if got := fetchMerged(m, []github.PullRequest{a}, []github.PullRequest{merged}); got != nil {
		t.Fatalf("merging notified %q", got)
	}
	if len(m.panes[paneMine].gone) != 0 {
		t.Fatalf("My PRs gone rows %v, want the merged one moved", m.panes[paneMine].gone)
	}
	if got := shownNumbers(m, paneMerged); !slices.Equal(got, []int{2}) {
		t.Fatalf("Merged = %v", got)
	}
	if m.changes[paneMerged].mark(&merged).kind != markNew {
		t.Fatalf("mark %+v, want new", m.changes[paneMerged].mark(&merged))
	}
	if pr, ok := m.selectedPR(); m.focus != paneMerged || !ok || pr.Number != 2 {
		t.Fatalf("focus %d, selection %+v; want it to follow #2", m.focus, pr)
	}
	// Closing without merging is still gone.
	fetchMerged(m, nil, []github.PullRequest{merged})
	if got := len(m.panes[paneMine].gone); got != 1 {
		t.Fatalf("closed pull request: %d gone rows, want 1", got)
	}
	// Falling out of the last N is not news.
	fetchMerged(m, nil, nil)
	if len(m.panes[paneMerged].gone) != 0 || slices.Contains(m.drawnPanes(), paneMerged) {
		t.Fatalf("Merged gone %v, drawn %v", m.panes[paneMerged].gone, m.drawnPanes())
	}
}

func TestMergedRowsNeedNoOne(t *testing.T) {
	m := notifyModel(t, "")
	failing := mergedPR(changePR(3, "acme/a"), time.Hour)
	failing.Checks, failing.ReviewDecision, failing.Mergeable = "FAILURE", "CHANGES_REQUESTED", "CONFLICTING"
	fetchMerged(m, nil, []github.PullRequest{failing})
	for number := range attentionCategories {
		if got := m.categoryCount(number + 1); got != 0 {
			t.Errorf("category %d counts %d merged rows", number+1, got)
		}
	}
	if got := m.needYouCount(); got != 0 {
		t.Fatalf("need you %d", got)
	}
	m.focus = paneMerged
	m.syncKeys()
	if m.keys.Snooze.Enabled() || m.keys.Rerequest.Enabled() {
		t.Fatal("z or R enabled on a merged row")
	}
	if !m.keys.Open.Enabled() || !m.keys.CopyURL.Enabled() {
		t.Fatal("o or y disabled on a merged row")
	}
}

func TestMergedPaneFollowsScopeSearchAndFilters(t *testing.T) {
	m := notifyModel(t, "acme/a")
	inScope, other := mergedPR(changePR(4, "acme/a"), time.Hour), mergedPR(changePR(5, "acme/b"), 2*time.Hour)
	fetchMerged(m, []github.PullRequest{changePR(1, "acme/a")}, []github.PullRequest{inScope, other})
	if got := shownNumbers(m, paneMerged); !slices.Equal(got, []int{4}) {
		t.Fatalf("scoped Merged = %v", got)
	}
	m.search = "nothing matches"
	m.applyFilters()
	if slices.Contains(m.drawnPanes(), paneMerged) {
		t.Fatal("Merged drawn while the search matches none of it")
	}
	m.search = ""
	m.quick = quickFailing
	m.applyFilters()
	if len(m.panes[paneMerged].visible) != 0 {
		t.Fatal("a quick filter matched a merged row")
	}
	m.quick = quickNone
	m.category = 1
	m.applyFilters()
	if len(m.panes[paneMerged].visible) != 0 {
		t.Fatal("a category matched a merged row")
	}
}

func TestASnoozedPullRequestThatMergesMovesToMerged(t *testing.T) {
	m := notifyModel(t, "")
	pr := changePR(6, "acme/a")
	fetchMerged(m, []github.PullRequest{pr}, nil)
	m.snoozes[keyOf(&pr)] = preferences.Snooze{Repository: pr.Repository, Number: pr.Number, List: preferences.SnoozeMine, Until: changeTime.Add(24 * time.Hour)}
	m.rebuildVisiblePRs()
	fetchMerged(m, nil, []github.PullRequest{mergedPR(pr, 0)})
	if got := rowCount(&m.panes[paneSnoozed]); got != 0 {
		t.Fatalf("Snoozed rows %d, want the merged one gone from it", got)
	}
	if got := shownNumbers(m, paneMerged); !slices.Equal(got, []int{6}) {
		t.Fatalf("Merged = %v", got)
	}
}

func TestMergedPaneDrawsAndClicksWithEveryPane(t *testing.T) {
	m := notifyModel(t, "")
	m.mouse = true
	mine := []github.PullRequest{changePR(1, "acme/a"), changePR(2, "acme/a")}
	merged := []github.PullRequest{mergedPR(changePR(8, "acme/a"), time.Hour), mergedPR(changePR(9, "acme/a"), 2*time.Hour)}
	updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine,
		ReviewRequests: []github.PullRequest{reviewedPR(7, github.ReviewRequested)}, Merged: merged}})
	for _, size := range [][2]int{{40, 10}, {80, 14}, {140, 30}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		assertBounded(t, m, size[0], size[1])
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Merged (2)") || !strings.Contains(view, "bob") {
		t.Fatalf("Merged pane not drawn:\n%s", view)
	}
	click(m, lineOf(m, 9))
	if pr, ok := m.selectedPR(); m.focus != paneMerged || !ok || pr.Number != 9 {
		t.Fatalf("click selected focus %d %+v", m.focus, pr)
	}
}
