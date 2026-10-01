package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"charm.land/bubbles/v2/table"
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

func columnIndex(columns []table.Column, title string) int {
	return slices.IndexFunc(columns, func(column table.Column) bool { return column.Title == title })
}

func TestBothPanesRenderWithOwnRowsAndColumns(t *testing.T) {
	m := newPaneModel(t, 100, 24, manyPRs(3), reviewPRs(2))
	if len(m.panes[paneMine].visible) != 3 || len(m.panes[paneReview].visible) != 2 {
		t.Fatalf("visible = %v / %v", m.panes[paneMine].visible, m.panes[paneReview].visible)
	}
	mineCols, reviewCols := m.panes[paneMine].table.Columns(), m.panes[paneReview].table.Columns()
	author, number := columnIndex(reviewCols, "Author"), columnIndex(reviewCols, "Number")
	if columnIndex(mineCols, "Merge") < 0 || columnIndex(mineCols, "Author") >= 0 || author < 0 || columnIndex(reviewCols, "Merge") >= 0 {
		t.Fatalf("columns = %+v / %+v", mineCols, reviewCols)
	}
	row := m.panes[paneReview].table.Rows()[0]
	if row[author] != "bob" || !strings.Contains(row[number], "\x1b]8;;https://github.com/acme/b/pull/100") {
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

func TestTabSwitchesFocusAndPanesKeepCursors(t *testing.T) {
	m := newPaneModel(t, 100, 30, manyPRs(10), reviewPRs(10))
	press(m, tea.Key{Code: 'j', Text: "j"})
	press(m, tea.Key{Code: 'j', Text: "j"})
	press(m, tea.Key{Code: tea.KeyTab})
	if m.focus != paneReview {
		t.Fatalf("tab focus = %d", m.focus)
	}
	press(m, tea.Key{Code: 'G', Text: "G"})
	if selected, ok := m.selectedPR(); !ok || selected.Number != 109 {
		t.Fatalf("review selection = %+v, %v", selected, ok)
	}
	if m.panes[paneMine].table.Cursor() != 2 {
		t.Fatalf("G in review pane moved mine cursor to %d", m.panes[paneMine].table.Cursor())
	}
	if !strings.Contains(m.View().Content, "https://github.com/acme/b/pull/109") {
		t.Fatal("URL line does not follow the focused pane")
	}
	press(m, tea.Key{Code: tea.KeyTab, Mod: tea.ModShift})
	if m.focus != paneMine {
		t.Fatalf("shift+tab focus = %d", m.focus)
	}
	if selected, ok := m.selectedPR(); !ok || selected.Number != 3 {
		t.Fatalf("mine selection after switching back = %+v, %v", selected, ok)
	}
}

func TestFocusedEmptyPaneIgnoresTableKeys(t *testing.T) {
	m := newPaneModel(t, 80, 24, manyPRs(5), nil)
	press(m, tea.Key{Code: tea.KeyTab})
	for _, k := range []tea.Key{{Code: 'G', Text: "G"}, {Code: 'j', Text: "j"}, {Code: tea.KeyPgDown}, {Code: tea.KeyRight}} {
		press(m, k)
	}
	if _, ok := m.selectedPR(); ok {
		t.Fatal("empty review pane exposed a selection")
	}
	if m.panes[paneMine].table.Cursor() != 0 {
		t.Fatalf("keys in empty review pane moved mine cursor to %d", m.panes[paneMine].table.Cursor())
	}
	if strings.Contains(ansi.Strip(m.View().Content), "https://github.com/acme/a/pull/1") {
		t.Fatal("URL line shows the unfocused pane's PR")
	}
}

func TestSinglePaneLayoutTabShowsOtherPaneAndKeepsCursor(t *testing.T) {
	m := newPaneModel(t, 80, 10, manyPRs(30), reviewPRs(30))
	press(m, tea.Key{Code: 'j', Text: "j"})
	press(m, tea.Key{Code: tea.KeyTab})
	view := ansi.Strip(strings.Join(assertBounded(t, m, 80, 10), "\n"))
	if !strings.Contains(view, "Review requested (30)") || strings.Contains(view, "My PRs") {
		t.Fatalf("single-pane view after tab:\n%s", view)
	}
	press(m, tea.Key{Code: tea.KeyTab})
	if selected, ok := m.selectedPR(); !ok || selected.Number != 2 {
		t.Fatalf("hidden pane lost its cursor: %+v, %v", selected, ok)
	}
}

func TestHelpListsPaneSwitch(t *testing.T) {
	m := newPaneModel(t, 120, 24, manyPRs(3), reviewPRs(3))
	if !strings.Contains(ansi.Strip(strings.Join(m.helpLines(keyMap.listHelp), "\n")), "tab") {
		t.Fatal("short help does not list tab")
	}
}

func TestRefreshRestoresEachPaneAndKeepsFocus(t *testing.T) {
	shared := github.PullRequest{Number: 7, Repository: "acme/a", URL: "https://github.com/acme/a/pull/7"}
	mine := []github.PullRequest{{Number: 1, Repository: "acme/a"}, shared}
	review := []github.PullRequest{{Number: 2, Repository: "acme/b", Author: "bob"}, shared, {Number: 3, Repository: "acme/c", Author: "bob"}}
	m := newPaneModel(t, 100, 30, mine, review)
	press(m, tea.Key{Code: 'j', Text: "j"}) // mine: shared #7
	press(m, tea.Key{Code: 'r', Text: "r"})
	press(m, tea.Key{Code: tea.KeyTab})     // switching focus during a refresh is allowed
	press(m, tea.Key{Code: 'G', Text: "G"}) // review: acme/c #3
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{
		Login:          "alice",
		PullRequests:   []github.PullRequest{shared, mine[0]},
		ReviewRequests: []github.PullRequest{review[2], review[0], shared},
	}})
	if m.focus != paneReview || m.refreshing() {
		t.Fatalf("focus %d refreshing %t after refresh", m.focus, m.refreshing())
	}
	if pr, ok := m.paneSelectedPR(paneReview); !ok || pr.Repository != "acme/c" || pr.Number != 3 {
		t.Fatalf("review selection = %+v, %v", pr, ok)
	}
	if pr, ok := m.paneSelectedPR(paneMine); !ok || pr.Repository != "acme/a" || pr.Number != 7 {
		t.Fatalf("mine selection = %+v, %v", pr, ok)
	}
}

func TestFailedRefreshClearsBothPanes(t *testing.T) {
	m := newPaneModel(t, 100, 24, manyPRs(2), reviewPRs(2))
	m.startFetch()
	if len(m.panes[paneReview].visible) != 2 {
		t.Fatal("refresh hid review rows before finishing")
	}
	m.Update(fetchFinishedMsg{err: errors.New("offline")})
	if len(m.panes[paneMine].visible) != 0 || len(m.panes[paneReview].visible) != 0 || len(m.snapshot.ReviewRequests) != 0 {
		t.Fatalf("failed refresh kept rows: %v / %v", m.panes[paneMine].visible, m.panes[paneReview].visible)
	}
	if _, ok := m.selectedPR(); ok {
		t.Fatal("failed refresh exposed a selection")
	}
}

func TestFirstLoadFocusesReviewsWhenOwnListIsEmpty(t *testing.T) {
	m := newPaneModel(t, 100, 24, nil, reviewPRs(2))
	if m.focus != paneReview {
		t.Fatalf("focus = %d, want review pane", m.focus)
	}
	press(m, tea.Key{Code: tea.KeyTab})
	press(m, tea.Key{Code: 'r', Text: "r"})
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: reviewPRs(2)}})
	if m.focus != paneMine {
		t.Fatal("same-account refresh moved focus")
	}
}

func visibleIn(m *model, id paneID, number int) (string, bool) {
	view := ansi.Strip(m.panes[id].table.View())
	return view, strings.Contains(view, fmt.Sprintf("#%d", number))
}

func TestSelectedRowStaysVisibleAfterPageJump(t *testing.T) {
	review := reviewPRs(25)
	m := newPaneModel(t, 100, 16, manyPRs(40), review)
	press(m, tea.Key{Code: tea.KeyTab})
	press(m, tea.Key{Code: tea.KeyRight})
	selected := review[m.panes[paneReview].table.Cursor()]
	if view, ok := visibleIn(m, paneReview, selected.Number); !ok {
		t.Fatalf("review selection #%d not visible after page jump:\n%s", selected.Number, view)
	}
}

func TestSelectedRowStaysVisibleAfterRebuildRestore(t *testing.T) {
	mine, review := manyPRs(40), reviewPRs(25)
	m := newPaneModel(t, 100, 16, mine, review)
	press(m, tea.Key{Code: 'G', Text: "G"})
	m.startFetch()
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine, ReviewRequests: review}})
	if view, ok := visibleIn(m, paneMine, 40); !ok {
		t.Fatalf("mine selection #40 not visible after refresh:\n%s", view)
	}
}

func TestSelectedRowStaysVisibleAfterSelectPR(t *testing.T) {
	mine, review := manyPRs(40), reviewPRs(25)
	m := newPaneModel(t, 100, 16, mine, review)
	press(m, tea.Key{Code: 'G', Text: "G"})
	reordered := append([]github.PullRequest(nil), mine...)
	moved := reordered[39]
	reordered = append(reordered[:39], reordered[40:]...)
	reordered = append(reordered[:30], append([]github.PullRequest{moved}, reordered[30:]...)...)
	m.startFetch()
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: reordered, ReviewRequests: review}})
	pr, ok := m.paneSelectedPR(paneMine)
	if !ok || pr.Number != 40 {
		t.Fatalf("selected = %+v, %v; want #40", pr, ok)
	}
	if view, ok := visibleIn(m, paneMine, 40); !ok {
		t.Fatalf("selection #40 not visible after reorder:\n%s", view)
	}
}

func TestSelectedRowStaysVisibleAfterMultiRowKeys(t *testing.T) {
	mine := manyPRs(40)
	m := newPaneModel(t, 100, 16, mine, reviewPRs(25))
	keys := []tea.Key{{Code: tea.KeyPgDown}, {Code: 'd', Text: "d"}, {Code: tea.KeyPgDown}, {Code: 'd', Text: "d"}, {Code: 'd', Text: "d"}, {Code: tea.KeyPgDown},
		{Code: tea.KeyPgUp}, {Code: 'u', Text: "u"}, {Code: tea.KeyPgUp}, {Code: 'u', Text: "u"}}
	for i, k := range keys {
		press(m, k)
		pr, ok := m.paneSelectedPR(paneMine)
		if !ok {
			t.Fatalf("press %d: no selection", i)
		}
		if view, ok := visibleIn(m, paneMine, pr.Number); !ok {
			t.Fatalf("press %d (%v): selection #%d not visible:\n%s", i, k, pr.Number, view)
		}
	}
}

func TestSelectedRowStaysVisibleAfterResize(t *testing.T) {
	m := newPaneModel(t, 100, 16, manyPRs(40), reviewPRs(25))
	press(m, tea.Key{Code: 'G', Text: "G"})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 18})
	if view, ok := visibleIn(m, paneMine, 40); !ok {
		t.Fatalf("mine selection #40 not visible after resize:\n%s", view)
	}
}

func TestDualLayoutGivesEachPaneHeaderPlusTwoRows(t *testing.T) {
	const width = 80
	for height := 8; height <= 20; height++ {
		m := newPaneModel(t, width, height, manyPRs(30), reviewPRs(30))
		layout := m.layoutPanes()
		if !layout.single {
			for _, id := range paneIDs {
				// Height is the row viewport; the header takes two more lines.
				if got := m.panes[id].table.Height(); got < 2 {
					t.Fatalf("height %d: pane %d shows %d rows, want at least 2", height, id, got)
				}
				if got := layout.tables[id]; got < 4 {
					t.Fatalf("height %d: pane %d table lines = %d, want at least 4", height, id, got)
				}
			}
		}
		assertBounded(t, m, width, height)
	}
	if m := newPaneModel(t, width, 12, manyPRs(30), reviewPRs(30)); !m.layoutPanes().single {
		t.Fatal("80x12 should use the single-pane layout")
	}
}

func TestMergeLegendOnlyWhenMyPRsIsDrawnWithRows(t *testing.T) {
	const legend = "✓ ready"
	m := newPaneModel(t, 80, 10, manyPRs(30), reviewPRs(30))
	press(m, tea.Key{Code: tea.KeyTab})
	if !m.layoutPanes().single || m.focus != paneReview {
		t.Fatalf("want single layout on review pane, got %+v focus %d", m.layoutPanes(), m.focus)
	}
	if view := ansi.Strip(strings.Join(assertBounded(t, m, 80, 10), "\n")); strings.Contains(view, legend) {
		t.Fatalf("legend shown without My PRs pane:\n%s", view)
	}
	m = newPaneModel(t, 100, 24, manyPRs(3), reviewPRs(2))
	if view := ansi.Strip(strings.Join(assertBounded(t, m, 100, 24), "\n")); !strings.Contains(view, legend) {
		t.Fatalf("legend missing in dual layout:\n%s", view)
	}
}

func TestReviewPaneKeepsAgeAtStandardWidth(t *testing.T) {
	m := newPaneModel(t, 80, 24, manyPRs(3), reviewPRs(2))
	if columnIndex(m.panes[paneMine].table.Columns(), "Age") < 0 || columnIndex(m.panes[paneReview].table.Columns(), "Age") < 0 {
		t.Fatalf("Age column missing at 80 columns: %+v / %+v", m.panes[paneMine].table.Columns(), m.panes[paneReview].table.Columns())
	}
}
