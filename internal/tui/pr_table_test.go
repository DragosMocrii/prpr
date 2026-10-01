package tui

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/preferences"
)

func newTableModel(t *testing.T, width, height int, prs []github.PullRequest) *model {
	t.Helper()
	store, err := preferences.Open(filepath.Join(t.TempDir(), "preferences.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("alice", ""); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, width, height)
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: prs}})
	return m
}

func TestTableFilterPreservesPRIdentityAndClickableNumber(t *testing.T) {
	m := newTableModel(t, 79, 12, []github.PullRequest{
		{Number: 1, Repository: "acme/a", URL: "https://github.com/acme/a/pull/1", Title: "first", Draft: true, Mergeable: "MERGEABLE"},
		{Number: 1, Repository: "acme/b", URL: "https://github.com/acme/b/pull/1", Title: "other"},
		{Number: 2, Repository: "acme/a", URL: "https://github.com/acme/a/pull/2", Title: "second", Mergeable: "UNKNOWN"},
	})
	m.applyRepository("acme/a")
	m.panes[paneMine].table.MoveDown(1)
	selected, ok := m.selectedPR()
	if !ok || selected.Number != 2 || selected.Repository != "acme/a" || selected.URL != "https://github.com/acme/a/pull/2" {
		t.Fatalf("selected PR = %+v, %v", selected, ok)
	}
	row := m.panes[paneMine].table.Rows()[1]
	if !strings.Contains(row[1], "\x1b]8;;https://github.com/acme/a/pull/2\x07") || !strings.HasSuffix(row[1], "\x1b]8;;\x07") {
		t.Fatalf("number hyperlink missing target/reset: %q", row[1])
	}
	if strings.Contains(row[2]+row[3], "\x1b]8;") {
		t.Fatalf("hyperlink leaked into following cells: %q %q", row[2], row[3])
	}
	if row[2] != "open" || ansi.Strip(row[3]) != "?" {
		t.Fatalf("state/unknown merge cells = %q %q", row[2], row[3])
	}
}

func TestNumberHyperlinksRejectUnsafeURLs(t *testing.T) {
	for _, rawURL := range []string{"", "http://github.com/a/b", "https://github.com:443/a/b", "https://user@github.com/a/b", "https://evil.test/github.com", "https://github.com/a/\n"} {
		got := prNumberLink(7, rawURL)
		if strings.Contains(got, "\x1b]8;") {
			t.Errorf("unsafe URL %q emitted OSC 8: %q", rawURL, got)
		}
	}
	if got := prNumberLink(7, "https://github.com/acme/repo/pull/7"); !strings.Contains(got, "\x1b]8;;https://github.com/acme/repo/pull/7\x07") {
		t.Fatalf("valid GitHub URL not linked: %q", got)
	}
}

func TestAllTableShowsRepositoryIdentityAndIndependentStatuses(t *testing.T) {
	m := newTableModel(t, 80, 12, []github.PullRequest{
		{Number: 1, Repository: "acme/a", Title: "draft", Draft: true, Mergeable: "MERGEABLE"},
		{Number: 2, Repository: "acme/b", Title: "conflicts", Mergeable: "CONFLICTING"},
		{Number: 3, Repository: "acme/c", Title: "unknown", Mergeable: "new-value"},
	})
	columns := m.panes[paneMine].table.Columns()
	if len(columns) != 5 || columns[0].Title != "Repository" || columns[1].Title != "PR name" {
		t.Fatalf("All table columns = %+v", columns)
	}
	rows := m.panes[paneMine].table.Rows()
	if rows[0][3] != "draft" || ansi.Strip(rows[0][4]) != "✓" || ansi.Strip(rows[1][4]) != "✗" || ansi.Strip(rows[2][4]) != "?" {
		t.Fatalf("state/merge rows = %+v", rows)
	}
	m.width = 79
	m.rebuildPRTable(true)
	if len(m.panes[paneMine].table.Columns()) != 4 || !strings.HasPrefix(m.panes[paneMine].table.Rows()[0][0], "acme/a — ") {
		t.Fatalf("narrow All table lost repository identity: cols %+v row %+v", m.panes[paneMine].table.Columns(), m.panes[paneMine].table.Rows()[0])
	}
}

func TestResizePreservesSelectedPRAndBoundsScreen(t *testing.T) {
	prs := make([]github.PullRequest, 12)
	for i := range prs {
		prs[i] = github.PullRequest{Number: i + 1, Repository: "acme/a", URL: fmt.Sprintf("https://github.com/acme/a/pull/%d", i+1), Title: "長い🙂 title"}
	}
	m := newTableModel(t, 120, 24, prs)
	m.panes[paneMine].table.MoveDown(8)
	wantURL := prs[8].URL
	for _, size := range [][2]int{{40, 8}, {79, 12}, {80, 12}, {120, 24}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		selected, ok := m.selectedPR()
		if !ok || selected.URL != wantURL {
			t.Fatalf("resize %dx%d changed selected PR: %+v, %v", size[0], size[1], selected, ok)
		}
		view := strings.Split(m.View().Content, "\n")
		if len(view) > size[1] {
			t.Fatalf("rendered %d lines at %dx%d", len(view), size[0], size[1])
		}
		for _, line := range view {
			if width := ansi.StringWidth(line); width > size[0] {
				t.Fatalf("rendered line width %d at %dx%d: %q", width, size[0], size[1], line)
			}
		}
	}
	m.width, m.height = 16, 5
	if !strings.Contains(strings.ReplaceAll(m.View().Content, "\n", " "), "Terminal too small") {
		t.Fatalf("too-small prompt missing: %q", m.View().Content)
	}
	m.applyRepository("acme/empty")
	if _, ok := m.selectedPR(); ok {
		t.Fatal("empty scope exposed selected PR")
	}
}

func TestLongUnicodeTitleIsSingleLineAndGraphemeSafe(t *testing.T) {
	title := strings.Repeat("🙂長", 40)
	m := newTableModel(t, 40, 8, []github.PullRequest{{Number: 1, Repository: "acme/a", Title: title}})
	m.applyRepository("acme/a")
	lines := strings.Split(m.panes[paneMine].table.View(), "\n")
	var nameCell string
	for _, line := range lines {
		if strings.Contains(ansi.Strip(line), "🙂長") {
			nameCell = line
			break
		}
	}
	if nameCell == "" || ansi.StringWidth(nameCell) > 40 || !utf8.ValidString(nameCell) {
		t.Fatalf("truncated Unicode title is invalid or oversized: %q", nameCell)
	}
	if !strings.Contains(ansi.Strip(nameCell), "…") {
		t.Fatalf("long title was not visibly truncated: %q", nameCell)
	}
}

func TestDynamicColumnBudgetShowsResizePrompt(t *testing.T) {
	m := newTableModel(t, 40, 12, []github.PullRequest{{Number: 999999999999999999, Repository: "acme/a", Title: "wide number"}})
	if m.panes[paneMine].fits {
		t.Fatal("table unexpectedly fit with no usable title width")
	}
	if !strings.Contains(m.View().Content, "Terminal too small") {
		t.Fatalf("dynamic width guard did not show resize prompt: %q", m.View().Content)
	}
}

func manyPRs(n int) []github.PullRequest {
	prs := make([]github.PullRequest, n)
	for i := range prs {
		prs[i] = github.PullRequest{Number: i + 1, Repository: "acme/a", URL: fmt.Sprintf("https://github.com/acme/a/pull/%d", i+1), Title: "title"}
	}
	return prs
}

func TestTableNavigationKeysAndPageIndicator(t *testing.T) {
	m := newTableModel(t, 80, 12, manyPRs(30))
	perPage := m.panes[paneMine].table.Height()
	if perPage < 1 || m.panes[paneMine].pages.TotalPages != (30+perPage-1)/perPage {
		t.Fatalf("pages = %d with %d rows per page", m.panes[paneMine].pages.TotalPages, perPage)
	}
	press(m, tea.Key{Code: 'G', Text: "G"})
	if selected, ok := m.selectedPR(); !ok || selected.Number != 30 || m.panes[paneMine].pages.Page != m.panes[paneMine].pages.TotalPages-1 {
		t.Fatalf("G selected %+v on page %d", selected, m.panes[paneMine].pages.Page)
	}
	press(m, tea.Key{Code: 'g', Text: "g"})
	if m.panes[paneMine].table.Cursor() != 0 || m.panes[paneMine].pages.Page != 0 {
		t.Fatalf("g cursor %d page %d", m.panes[paneMine].table.Cursor(), m.panes[paneMine].pages.Page)
	}
	press(m, tea.Key{Code: tea.KeyPgDown})
	if m.panes[paneMine].table.Cursor() != perPage {
		t.Fatalf("pgdown cursor = %d, want %d", m.panes[paneMine].table.Cursor(), perPage)
	}
	press(m, tea.Key{Code: tea.KeyRight})
	if m.panes[paneMine].table.Cursor() != 2*perPage || m.panes[paneMine].pages.Page != 2 {
		t.Fatalf("right cursor %d page %d", m.panes[paneMine].table.Cursor(), m.panes[paneMine].pages.Page)
	}
	press(m, tea.Key{Code: tea.KeyLeft})
	if m.panes[paneMine].table.Cursor() != perPage || m.panes[paneMine].pages.Page != 1 {
		t.Fatalf("left cursor %d page %d", m.panes[paneMine].table.Cursor(), m.panes[paneMine].pages.Page)
	}
	press(m, tea.Key{Code: 'G', Text: "G"})
	press(m, tea.Key{Code: tea.KeyRight})
	if m.panes[paneMine].table.Cursor() != 29 {
		t.Fatalf("right on last page moved cursor to %d", m.panes[paneMine].table.Cursor())
	}
	if !strings.Contains(m.View().Content, m.pageIndicator()) {
		t.Fatal("page indicator missing from list view")
	}
}

func TestFullHelpToggleKeepsScreenBounded(t *testing.T) {
	m := newTableModel(t, 40, 12, manyPRs(30))
	shortHeight := m.panes[paneMine].table.Height()
	press(m, tea.Key{Code: '?', Text: "?"})
	if !m.help.ShowAll || m.panes[paneMine].table.Height() >= shortHeight {
		t.Fatalf("full help did not take table rows: showAll %t height %d -> %d", m.help.ShowAll, shortHeight, m.panes[paneMine].table.Height())
	}
	for _, size := range [][2]int{{40, 12}, {40, 8}, {120, 24}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := strings.Split(m.View().Content, "\n")
		if len(view) > size[1] {
			t.Fatalf("full help rendered %d lines at %dx%d", len(view), size[0], size[1])
		}
		for _, line := range m.helpLines(keyMap.listHelp) {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("help line needs truncation at %dx%d: %q", size[0], size[1], ansi.Strip(line))
			}
		}
		for _, line := range view {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("line width %d at %dx%d: %q", ansi.StringWidth(line), size[0], size[1], line)
			}
		}
	}
	press(m, tea.Key{Code: '?', Text: "?"})
	if m.help.ShowAll {
		t.Fatal("second ? did not return to short help")
	}
}

func TestEmptyScopeIgnoresTableKeys(t *testing.T) {
	m := newTableModel(t, 80, 12, manyPRs(3))
	m.applyRepository("acme/empty")
	for _, k := range []tea.Key{{Code: 'G', Text: "G"}, {Code: tea.KeyPgDown}, {Code: tea.KeyRight}} {
		press(m, k)
	}
	if m.panes[paneMine].table.Cursor() != 0 {
		t.Fatalf("empty scope moved cursor to %d", m.panes[paneMine].table.Cursor())
	}
	if _, ok := m.selectedPR(); ok {
		t.Fatal("empty scope exposed selected PR")
	}
}

func TestFullHelpSurvivesPickerResizeAndCancel(t *testing.T) {
	m := newTableModel(t, 80, 16, manyPRs(30))
	press(m, tea.Key{Code: '?', Text: "?"})
	press(m, tea.Key{Code: 'p', Text: "p"})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 14})
	press(m, tea.Key{Code: tea.KeyEsc})
	if lines := strings.Split(m.View().Content, "\n"); len(lines) > 14 {
		t.Fatalf("list rendered %d lines at height 14 after picker resize", len(lines))
	}
}
