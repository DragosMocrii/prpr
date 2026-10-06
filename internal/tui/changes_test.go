package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

var changeTime = time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

func changePR(number int, repository string) github.PullRequest {
	return github.PullRequest{
		Number: number, Repository: repository, Title: "change " + repository,
		URL: "https://github.com/" + repository + "/pull/" + string(rune('0'+number)), UpdatedAt: changeTime,
		Mergeable: "MERGEABLE", MergeState: "BLOCKED", Checks: "PENDING", ReviewDecision: "REVIEW_REQUIRED",
	}
}

func changesModel(t *testing.T, mine ...github.PullRequest) *model {
	t.Helper()
	store := testPreferences(t)
	if err := store.Save("alice", ""); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 140, 30)
	updateSnapshot(m, "alice", mine...)
	return m
}

// underlined marks a changed cell.
const underlined = "\x1b[4;58;5;"

// rest lets the cursor rest on its row until the row's mark counts as read.
func rest(m *model) {
	m.Update(restMsg{})
	m.Update(restMsg{m.restGeneration})
}

// markers lists the change marker of each row in a pane, top to bottom.
func markers(m *model, id paneID) string {
	var marks []string
	for _, row := range m.panes[id].table.Rows() {
		marks = append(marks, ansi.Strip(row[0]))
	}
	return strings.Join(marks, "")
}

// cellOf returns a row's cell under the column title.
func cellOf(t *testing.T, m *model, id paneID, row int, title string) string {
	t.Helper()
	index := columnIndex(m.panes[id].table.Columns(), title)
	if index < 0 {
		t.Fatalf("no %s column", title)
	}
	return m.panes[id].table.Rows()[row][index]
}

func TestRefreshMarksNewChangedActivityAndGoneRows(t *testing.T) {
	one, two, three, four := changePR(1, "acme/a"), changePR(2, "acme/a"), changePR(3, "acme/b"), changePR(4, "acme/b")
	m := changesModel(t, one, two, three, four)
	if got := markers(m, paneMine); got != "    " {
		t.Fatalf("first load markers = %q, want none", got)
	}

	ci := two
	ci.Checks, ci.Comments = "SUCCESS", 3
	ci.UpdatedAt = changeTime.Add(time.Minute)
	activity := three
	activity.UpdatedAt = changeTime.Add(time.Minute)
	aged := one
	aged.WaitingSince = changeTime.Add(-time.Hour)
	updateSnapshot(m, "alice", aged, ci, activity, changePR(5, "acme/a"))

	if got := markers(m, paneMine); got != " ▲·+−" {
		t.Fatalf("markers = %q, want unchanged, changed, activity, new, gone", got)
	}
	for _, title := range []string{"CI", "Comments"} {
		if cell := cellOf(t, m, paneMine, 1, title); !strings.Contains(cell, underlined) {
			t.Errorf("changed %s cell not highlighted: %q", title, cell)
		}
	}
	for _, title := range []string{"PR name", "Merge", "Review", "Size"} {
		if cell := cellOf(t, m, paneMine, 1, title); strings.Contains(cell, underlined) {
			t.Errorf("unchanged %s cell highlighted: %q", title, cell)
		}
	}
	gone := m.panes[paneMine].table.Rows()[4]
	for _, cell := range gone[1:] {
		if !strings.Contains(cell, goneOn) {
			t.Errorf("gone row cell not dimmed and struck through: %q", cell)
		}
	}
	if title := ansi.Strip(m.paneTitle(paneMine, false)); !strings.Contains(title, "My PRs (4) · +1 new · 1 changed · 1 gone") {
		t.Fatalf("pane title = %q", title)
	}
	m.panes[paneMine].table.MoveDown(4)
	if selected, ok := m.selectedPR(); !ok || selected.Number != 4 || selected.URL != four.URL {
		t.Fatalf("gone row selection = %+v, %v", selected, ok)
	}
}

func TestMarksAccumulateAcrossRefreshesAndFailedFetches(t *testing.T) {
	one, two := changePR(1, "acme/a"), changePR(2, "acme/a")
	m := changesModel(t, one, two)
	two.Checks = "SUCCESS"
	updateSnapshot(m, "alice", two)
	two.ReviewDecision = "APPROVED"
	updateSnapshot(m, "alice", two)
	m.Update(fetchFinishedMsg{err: errors.New("network down")})
	updateSnapshot(m, "alice", two)

	if got := markers(m, paneMine); got != "▲−" {
		t.Fatalf("markers after failed fetch = %q, want changed and gone", got)
	}
	for _, title := range []string{"CI", "Review"} {
		if cell := cellOf(t, m, paneMine, 0, title); !strings.Contains(cell, underlined) {
			t.Errorf("%s change lost across refreshes: %q", title, cell)
		}
	}

	// A gone PR that comes back is new again and leaves the gone rows.
	updateSnapshot(m, "alice", two, one)
	if got := markers(m, paneMine); got != "▲+" {
		t.Fatalf("markers after return = %q", got)
	}
}

func TestLeavingARowClearsItsMarkAndDropsGoneRows(t *testing.T) {
	one, two, three := changePR(1, "acme/a"), changePR(2, "acme/a"), changePR(3, "acme/a")
	m := changesModel(t, one, two, three)
	updateSnapshot(m, "alice", changePR(4, "acme/a"), one)
	if got := markers(m, paneMine); got != "+ −−" {
		t.Fatalf("markers = %q", got)
	}
	down, up := tea.Key{Code: 'j', Text: "j"}, tea.Key{Code: 'k', Text: "k"}

	// Selection followed #1 to the second row. The new row keeps its mark
	// while selected and loses it once the cursor leaves.
	press(m, up)
	if selected, _ := m.selectedPR(); selected.Number != 4 || markers(m, paneMine) != "+ −−" {
		t.Fatalf("on the new row: selected %+v, markers %q", selected, markers(m, paneMine))
	}
	rest(m)
	if got := markers(m, paneMine); got != "+ −−" {
		t.Fatalf("markers while resting on the new row = %q", got)
	}
	press(m, down)
	if got := markers(m, paneMine); got != "  −−" {
		t.Fatalf("markers after leaving the new row = %q", got)
	}
	press(m, down)
	rest(m)
	press(m, down)
	if selected, _ := m.selectedPR(); selected.Number != 3 || markers(m, paneMine) != "  −" {
		t.Fatalf("leaving gone #2 downward: selected %+v, markers %q", selected, markers(m, paneMine))
	}
	rest(m)
	press(m, up)
	if selected, _ := m.selectedPR(); selected.Number != 1 || markers(m, paneMine) != "  " {
		t.Fatalf("leaving gone #3 upward: selected %+v, markers %q", selected, markers(m, paneMine))
	}
}

func TestClearMarksKeyClearsEverythingAndKeepsSelection(t *testing.T) {
	one, two := changePR(1, "acme/a"), changePR(2, "acme/a")
	m := changesModel(t, one, two)
	clear := tea.Key{Code: 'x', Text: "x"}
	if help := ansi.Strip(strings.Join(m.helpLines(keyMap.listHelp), "\n")); strings.Contains(help, "clear marks") {
		t.Fatalf("clear marks offered with nothing marked: %s", help)
	}
	two.Checks = "FAILURE"
	updateSnapshot(m, "alice", changePR(3, "acme/a"), two)
	// Selection stayed on #1, now gone; moving up drops it.
	press(m, tea.Key{Code: 'k', Text: "k"})
	if help := ansi.Strip(strings.Join(m.helpLines(keyMap.listHelp), "\n")); !strings.Contains(help, "clear marks") {
		t.Fatalf("clear marks missing from help: %s", help)
	}
	press(m, clear)
	if got := markers(m, paneMine); got != "  " {
		t.Fatalf("markers after x = %q", got)
	}
	if selected, ok := m.selectedPR(); !ok || selected.Number != 2 {
		t.Fatalf("selection after x = %+v, %v", selected, ok)
	}
	if strings.Contains(ansi.Strip(m.paneTitle(paneMine, false)), "new") {
		t.Fatalf("summary kept after x: %q", m.paneTitle(paneMine, false))
	}
}

func TestAccountSwitchStartsWithoutMarks(t *testing.T) {
	m := changesModel(t, changePR(1, "acme/a"))
	if err := m.preferences.Save("bob", ""); err != nil {
		t.Fatal(err)
	}
	updateSnapshot(m, "bob", changePR(2, "acme/a"))
	if got := markers(m, paneMine); got != " " {
		t.Fatalf("markers after account switch = %q", got)
	}
}

func TestGoneRowsFollowTheRepositoryScope(t *testing.T) {
	m := changesModel(t, changePR(1, "acme/a"), changePR(2, "acme/b"))
	updateSnapshot(m, "alice")
	if got := markers(m, paneMine); got != "−−" {
		t.Fatalf("markers = %q", got)
	}
	m.applyRepository("acme/b")
	if got := markers(m, paneMine); got != "−" {
		t.Fatalf("scoped markers = %q", got)
	}
	if selected, ok := m.selectedPR(); !ok || selected.Repository != "acme/b" {
		t.Fatalf("scoped gone selection = %+v, %v", selected, ok)
	}
	// Clearing the last rows empties the pane.
	m.applyRepository("")
	press(m, tea.Key{Code: 'x', Text: "x"})
	if rowCount(&m.panes[paneMine]) != 0 {
		t.Fatalf("gone rows left after x: %q", markers(m, paneMine))
	}
	if view := ansi.Strip(strings.Join(assertBounded(t, m, 140, 30), "\n")); !strings.Contains(view, "No open pull requests") {
		t.Fatalf("emptied pane not shown as empty:\n%s", view)
	}
}

func TestReviewPaneMarksAuthorChanges(t *testing.T) {
	store := testPreferences(t)
	if err := store.Save("alice", ""); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 140, 30)
	request := changePR(7, "acme/r")
	request.Author = "bob"
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: []github.PullRequest{request}}})
	request.Author = "carol"
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: []github.PullRequest{request}}})
	if got := markers(m, paneReview); got != "•" {
		t.Fatalf("review markers = %q", got)
	}
	if cell := cellOf(t, m, paneReview, 0, "Author"); !strings.Contains(cell, underlined) {
		t.Fatalf("author change not highlighted: %q", cell)
	}
}

func TestPaneWithOnlyGoneRowsShowsThem(t *testing.T) {
	only := changePR(1, "acme/a")
	m := changesModel(t, only)
	updateSnapshot(m, "alice")
	view := ansi.Strip(strings.Join(assertBounded(t, m, 140, 30), "\n"))
	if strings.Contains(view, "No open pull requests") || !strings.Contains(view, "− ") || !strings.Contains(view, only.URL) {
		t.Fatalf("pane with only a gone row:\n%s", view)
	}
	press(m, tea.Key{Code: 'x', Text: "x"})
	if view := ansi.Strip(strings.Join(assertBounded(t, m, 140, 30), "\n")); !strings.Contains(view, "No open pull requests") {
		t.Fatalf("pane not empty after x:\n%s", view)
	}
}

func TestReviewingARowClearsItsMark(t *testing.T) {
	m := notifyModel(t, "")
	waiting := reviewedPR(8, github.ReviewWaitingOnAuthor)
	fetch(m, nil, []github.PullRequest{waiting})
	request := reviewedPR(9, github.ReviewRequested)
	newCommits := reviewedPR(8, github.ReviewNewCommits)
	newCommits.UpdatedAt = changeTime.Add(time.Hour)
	fetch(m, nil, []github.PullRequest{request, newCommits})
	reviews := m.changes[paneReview]
	if reviews.mark(&request).kind != markNew || reviews.mark(&newCommits).kind != markChanged {
		t.Fatalf("marks %+v %+v, want the request new and the new commits changed", reviews.mark(&request), reviews.mark(&newCommits))
	}
	// The viewer approves one and comments on the other.
	approved, commented := request, newCommits
	approved.ReviewStatus, approved.UpdatedAt = github.ReviewApproved, changeTime.Add(2*time.Hour)
	commented.ReviewStatus, commented.UpdatedAt = github.ReviewWaitingOnAuthor, changeTime.Add(2*time.Hour)
	commented.Comments++
	fetch(m, nil, []github.PullRequest{approved, commented})
	reviews = m.changes[paneReview]
	if reviews.mark(&approved).kind != markNone || reviews.mark(&commented).kind != markNone || m.hasMarks() {
		t.Fatalf("marks %+v %+v remain after the viewer reviewed", reviews.mark(&approved), reviews.mark(&commented))
	}
	// Turning back into a draft is the author's doing, so it still marks.
	draft := approved
	draft.ReviewStatus, draft.UpdatedAt = github.ReviewBackInDraft, changeTime.Add(3*time.Hour)
	fetch(m, nil, []github.PullRequest{draft, commented})
	if m.changes[paneReview].mark(&draft).kind != markChanged {
		t.Fatalf("back in draft mark %+v", m.changes[paneReview].mark(&draft))
	}
}
