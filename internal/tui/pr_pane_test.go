package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

func reviewPRs(n int) []github.PullRequest {
	prs := make([]github.PullRequest, n)
	for i := range prs {
		prs[i] = github.PullRequest{Number: 100 + i, Repository: "acme/b", URL: fmt.Sprintf("https://github.com/acme/b/pull/%d", 100+i), Title: "review me", Author: "bob"}
	}
	return prs
}

// newPaneModel loads an All-repositories account with both lists in a single
// first fetch, so first-load focus rules see these lists.
func newPaneModel(t *testing.T, width, height int, mine, review []github.PullRequest) *model {
	t.Helper()
	store := testPreferences(t)
	if err := store.Save("alice", ""); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, width, height)
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine, ReviewRequests: review}})
	return m
}

func assertBounded(t *testing.T, m *model, width, height int) []string {
	t.Helper()
	lines := strings.Split(m.View().Content, "\n")
	if len(lines) > height {
		t.Fatalf("rendered %d lines at %dx%d", len(lines), width, height)
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > width {
			t.Fatalf("line width %d at %dx%d: %q", ansi.StringWidth(line), width, height, ansi.Strip(line))
		}
	}
	return lines
}

func TestBothPanesRenderWithOwnRowsAndColumns(t *testing.T) {
	m := newPaneModel(t, 100, 24, manyPRs(3), reviewPRs(2))
	if len(m.panes[paneMine].visible) != 3 || len(m.panes[paneReview].visible) != 2 {
		t.Fatalf("visible = %v / %v", m.panes[paneMine].visible, m.panes[paneReview].visible)
	}
	mineCols, reviewCols := m.panes[paneMine].table.Columns(), m.panes[paneReview].table.Columns()
	if mineCols[len(mineCols)-1].Title != "Merge" || reviewCols[len(reviewCols)-1].Title != "Author" {
		t.Fatalf("last columns = %q / %q", mineCols[len(mineCols)-1].Title, reviewCols[len(reviewCols)-1].Title)
	}
	row := m.panes[paneReview].table.Rows()[0]
	if row[len(row)-1] != "bob" || !strings.Contains(row[len(row)-3], "\x1b]8;;https://github.com/acme/b/pull/100") {
		t.Fatalf("review row = %q", row)
	}
	view := ansi.Strip(strings.Join(assertBounded(t, m, 100, 24), "\n"))
	if !strings.Contains(view, "My PRs (3)") || !strings.Contains(view, "Review requested (2)") {
		t.Fatalf("pane titles missing:\n%s", view)
	}
	if selected, ok := m.selectedPR(); !ok || selected.Number != 1 {
		t.Fatalf("focused selection = %+v, %v", selected, ok)
	}
}

func TestEmptyReviewPaneShowsEmptyLineAndGivesRowsToOtherPane(t *testing.T) {
	m := newPaneModel(t, 80, 24, manyPRs(30), nil)
	both := newPaneModel(t, 80, 24, manyPRs(30), reviewPRs(30))
	if m.panes[paneMine].table.Height() <= both.panes[paneMine].table.Height() {
		t.Fatalf("empty review pane did not give rows away: %d vs %d", m.panes[paneMine].table.Height(), both.panes[paneMine].table.Height())
	}
	view := ansi.Strip(strings.Join(assertBounded(t, m, 80, 24), "\n"))
	if !strings.Contains(view, "No review requests.") {
		t.Fatalf("empty review line missing:\n%s", view)
	}
	m.applyRepository("acme/zzz")
	view = ansi.Strip(m.View().Content)
	if !strings.Contains(view, "No open pull requests in acme/zzz.") || !strings.Contains(view, "No review requests in acme/zzz.") {
		t.Fatalf("scoped empty lines missing:\n%s", view)
	}
}

func TestRepositoryFilterAppliesToBothPanes(t *testing.T) {
	mine := []github.PullRequest{{Number: 1, Repository: "acme/a"}, {Number: 2, Repository: "acme/b"}}
	review := []github.PullRequest{{Number: 3, Repository: "acme/b", Author: "bob"}, {Number: 4, Repository: "acme/c", Author: "bob"}}
	m := newPaneModel(t, 100, 24, mine, review)
	m.applyRepository("ACME/B")
	if len(m.panes[paneMine].visible) != 1 || m.panes[paneMine].visible[0] != 1 {
		t.Fatalf("mine visible = %v", m.panes[paneMine].visible)
	}
	if len(m.panes[paneReview].visible) != 1 || m.panes[paneReview].visible[0] != 0 {
		t.Fatalf("review visible = %v", m.panes[paneReview].visible)
	}
}

func TestPanesStayBoundedAcrossSizes(t *testing.T) {
	m := newPaneModel(t, 100, 24, manyPRs(30), reviewPRs(30))
	for _, size := range [][2]int{{40, 8}, {40, 11}, {40, 12}, {80, 14}, {120, 40}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		assertBounded(t, m, size[0], size[1])
	}
	press(m, tea.Key{Code: '?', Text: "?"})
	for _, size := range [][2]int{{40, 12}, {80, 14}, {120, 40}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		assertBounded(t, m, size[0], size[1])
	}
}

func TestShortTerminalShowsOnlyFocusedPane(t *testing.T) {
	m := newPaneModel(t, 80, 10, manyPRs(30), reviewPRs(30))
	if !m.layoutPanes().single {
		t.Fatal("10 rows with two filled panes should use the single-pane layout")
	}
	view := ansi.Strip(strings.Join(assertBounded(t, m, 80, 10), "\n"))
	if !strings.Contains(view, "My PRs (30)") || strings.Contains(view, "Review requested") || !strings.Contains(view, "tab: other list") {
		t.Fatalf("single-pane view:\n%s", view)
	}
}

func TestNarrowReviewPaneFitsWithNullAuthor(t *testing.T) {
	review := []github.PullRequest{{Number: 9, Repository: "acme/lib", URL: "https://github.com/acme/lib/pull/9", Title: "ghost author", Author: ""}}
	m := newPaneModel(t, 40, 12, nil, review)
	if !m.panes[paneReview].fits {
		t.Fatal("review pane does not fit at 40 columns")
	}
	view := ansi.Strip(strings.Join(assertBounded(t, m, 40, 12), "\n"))
	// The 13-column name cell truncates the title, so check the number cell.
	if strings.Contains(view, "Terminal too small") || !strings.Contains(view, "#9") {
		t.Fatalf("narrow review view:\n%s", view)
	}
}
