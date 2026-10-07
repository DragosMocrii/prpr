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
	if _, ok := m.snoozes[keyOf(&pr)]; ok || len(m.preferences.Snoozes("alice")) != 0 {
		t.Fatalf("snooze kept: %v, saved %v", m.snoozes, m.preferences.Snoozes("alice"))
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

func TestRecentlyMergedSettingSavesAndResetsTheBaseline(t *testing.T) {
	m := notifyModel(t, "")
	fetchMerged(m, []github.PullRequest{changePR(1, "acme/a")}, nil)
	if m.merged != preferences.DefaultMerged {
		t.Fatalf("session merged %d", m.merged)
	}
	if nextMerged(5) != 10 || nextMerged(20) != 0 || nextMerged(0) != 3 || nextMerged(7) != 10 {
		t.Fatal("choices do not cycle 0, 3, 5, 10, 20")
	}
	if formatMerged(0) != "off" || formatMerged(10) != "10" {
		t.Fatalf("format %q %q", formatMerged(0), formatMerged(10))
	}
	m.applyMerged(10)
	if m.merged != 10 || m.preferences.Merged() != 10 || !m.settingsBaseline {
		t.Fatalf("merged %d saved %d baseline %v", m.merged, m.preferences.Merged(), m.settingsBaseline)
	}
}

func TestTurningMergedOffMovesFocus(t *testing.T) {
	m := notifyModel(t, "")
	fetchMerged(m, []github.PullRequest{changePR(1, "acme/a")}, []github.PullRequest{mergedPR(changePR(2, "acme/a"), time.Hour)})
	m.setFocus(paneMerged)
	m.applyMerged(0)
	fetchMerged(m, []github.PullRequest{changePR(1, "acme/a")}, nil)
	if m.focus == paneMerged || slices.Contains(m.drawnPanes(), paneMerged) {
		t.Fatalf("focus %d, drawn %v", m.focus, m.drawnPanes())
	}
}

// mergedLayoutModel is a model at width x height after a full fetch of
// mine, review, and merged.
func mergedLayoutModel(t *testing.T, width, height int, mine, review, merged []github.PullRequest) *model {
	t.Helper()
	m := notifyModel(t, "")
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine, ReviewRequests: review, Merged: merged}})
	return m
}

func mergedPRs(n int) []github.PullRequest {
	prs := make([]github.PullRequest, n)
	for i := range prs {
		prs[i] = mergedPR(changePR(200+i, "acme/m"), time.Duration(i+1)*time.Hour)
	}
	return prs
}

func TestMergedPaneTakesOnlyRoomTheOtherListsLeave(t *testing.T) {
	mine, review := manyPRs(8), reviewPRs(8)
	short := mergedLayoutModel(t, 80, 24, mine, review, mergedPRs(5))
	without := mergedLayoutModel(t, 80, 24, mine, review, nil)
	if slices.Contains(short.drawnPanes(), paneMerged) {
		t.Fatalf("Merged drawn at 80x24: %v", short.drawnPanes())
	}
	got, want := short.layoutPanes(), without.layoutPanes()
	if got.single || got.tables[paneMine] != want.tables[paneMine] || got.tables[paneReview] != want.tables[paneReview] {
		t.Fatalf("80x24 layout %+v, without Merged %+v", got, want)
	}
	shorter := mergedLayoutModel(t, 80, 18, mine, review, mergedPRs(5))
	if got, want := shorter.layoutPanes().single, mergedLayoutModel(t, 80, 18, mine, review, nil).layoutPanes().single; got != want {
		t.Fatalf("80x18 single %v, without Merged %v", got, want)
	}

	tall := mergedLayoutModel(t, 140, 40, manyPRs(3), reviewPRs(2), mergedPRs(5))
	if !slices.Contains(tall.drawnPanes(), paneMerged) {
		t.Fatalf("Merged not drawn at 140x40: %v", tall.drawnPanes())
	}
	layout := tall.layoutPanes()
	if layout.tables[paneMerged] != tableHeaderLen+5 || layout.tables[paneMine] < tableHeaderLen+3 || layout.tables[paneReview] < tableHeaderLen+2 {
		t.Fatalf("140x40 tables %v", layout.tables)
	}
}

func TestResizingLeavesMergedOutAndMovesFocus(t *testing.T) {
	m := mergedLayoutModel(t, 140, 40, manyPRs(8), reviewPRs(8), mergedPRs(5))
	m.setFocus(paneMerged)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.focus == paneMerged || !slices.Contains(m.drawnPanes(), m.focus) {
		t.Fatalf("focus %d, drawn %v", m.focus, m.drawnPanes())
	}
}

func TestMergedPaneFitsBesideFourPanes(t *testing.T) {
	mine, review := snoozePRs()
	mine = append(mine, github.PullRequest{Repository: "acme/api", Number: 3, Title: "Queued",
		Queue: &github.QueueEntry{Provider: "Trunk", State: github.QueueTesting}})
	for _, size := range [][2]int{{40, 8}, {60, 12}, {80, 20}, {140, 40}, {200, 60}} {
		m := mergedLayoutModel(t, size[0], size[1], mine, review, mergedPRs(3))
		snoozeFor(m, preferences.Snooze{Repository: "acme/api", Number: 1, List: preferences.SnoozeMine,
			Until: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)})
		assertBounded(t, m, size[0], size[1])
		for range 5 {
			m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			assertBounded(t, m, size[0], size[1])
		}
		if size[1] == 60 {
			if len(m.drawnPanes()) != 5 {
				t.Fatalf("200x60 draws %v, want five panes", m.drawnPanes())
			}
			// Hit testing follows the layout with Merged drawn last.
			m.mouse = true
			for _, want := range []struct {
				pane   paneID
				number int
			}{{paneReview, 7}, {paneMerged, 201}, {paneQueue, 3}} {
				click(m, lineOf(m, want.number))
				if pr, ok := m.selectedPR(); m.focus != want.pane || !ok || pr.Number != want.number {
					t.Fatalf("click on #%d selected focus %d %+v", want.number, m.focus, pr)
				}
			}
		}
	}
}

func TestMergedRowDetails(t *testing.T) {
	m := notifyModel(t, "")
	pr := mergedPR(changePR(2, "acme/a"), time.Hour)
	pr.Additions, pr.Deletions = 12, 3
	fetchMerged(m, []github.PullRequest{changePR(1, "acme/a")}, []github.PullRequest{pr})
	m.setFocus(paneMerged)
	m.details = true
	m.syncKeys()
	if !m.detailsShown() {
		t.Fatal("details not shown on a merged row")
	}
	view := ansi.Strip(strings.Join(m.detailsView(), "\n"))
	if merged := strings.TrimPrefix(mergedDetail(&pr, m.now()), "Merged "); !strings.Contains(view, merged) || !strings.Contains(view, "+12 −3") {
		t.Fatalf("details:\n%s", view)
	}
	if !m.keys.Editor.Enabled() {
		t.Fatal("e disabled on a merged row's details")
	}
}

func TestDroppingAGoneRowThatMakesRoomForMergedStaysBounded(t *testing.T) {
	m := notifyModel(t, "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 23})
	var mine, merged []github.PullRequest
	for n := 1; n <= 8; n++ {
		mine = append(mine, changePR(n, "acme/a"))
	}
	for n := 11; n <= 15; n++ {
		merged = append(merged, mergedPR(changePR(n, "acme/m"), time.Duration(n)*time.Hour))
	}
	fetchMerged(m, mine, merged)
	// #8 closes without merging and stays behind as a gone row.
	fetchMerged(m, mine[:7], merged)
	if slices.Contains(m.drawnPanes(), paneMerged) {
		t.Fatal("Merged drawn before the gone row is dropped; the test needs a smaller terminal")
	}
	down, up := tea.Key{Code: 'j', Text: "j"}, tea.Key{Code: 'k', Text: "k"}
	for range 7 {
		press(m, down)
	}
	if pr, gone, _ := m.paneRow(paneMine, m.panes[paneMine].table.Cursor()); !gone || pr.Number != 8 {
		t.Fatalf("cursor on %+v (gone %v), want gone #8", pr, gone)
	}
	rest(m)
	press(m, up)
	if !slices.Contains(m.drawnPanes(), paneMerged) {
		t.Fatal("dropping the gone row left no room for Merged; the test needs a larger terminal")
	}
	assertBounded(t, m, 140, 23)
	if !strings.Contains(ansi.Strip(m.View().Content), "Merged (5)") {
		t.Fatal("Merged pane not laid out after it gained room")
	}
}
