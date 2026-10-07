package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

func columnTitles(m *model, id paneID) []string {
	var titles []string
	for _, column := range m.panes[id].table.Columns() {
		titles = append(titles, ansi.Strip(column.Title))
	}
	return titles
}

func TestRankedColumnsDropInRankOrder(t *testing.T) {
	m := newPaneModel(t, 200, 30, manyPRs(2), reviewPRs(2))
	wide := columnTitles(m, paneReview)
	for _, title := range []string{"Age", "CI", "Review", "Comments", "Size"} {
		if !slices.Contains(wide, title) {
			t.Fatalf("200 wide lacks %s: %v", title, wide)
		}
	}
	// Review requested gives up Review first, then Size, then Comments.
	dropped := []string{}
	// Comments goes at 58, once Repository is gone below 80.
	for width := 199; width >= 40; width-- {
		m.Update(windowSize(width, 30))
		titles := columnTitles(m, paneReview)
		for _, title := range []string{"Review", "Size", "Comments", "CI", "Age"} {
			if !slices.Contains(titles, title) && !slices.Contains(dropped, title) {
				dropped = append(dropped, title)
			}
		}
		// Drawn columns keep their order.
		if i, j := slices.Index(titles, "CI"), slices.Index(titles, "Comments"); i >= 0 && j >= 0 && i > j {
			t.Fatalf("CI drawn after Comments: %v", titles)
		}
	}
	if want := []string{"Review", "Size", "Comments"}; len(dropped) < 3 || !slices.Equal(dropped[:3], want) {
		t.Fatalf("drop order = %v, want %v first", dropped, want)
	}
}

func TestBotsColumnOnlyWithBots(t *testing.T) {
	m := newPaneModel(t, 200, 30, manyPRs(2), reviewPRs(2))
	for _, id := range []paneID{paneMine, paneReview} {
		if slices.Contains(columnTitles(m, id), "Bots") {
			t.Fatalf("pane %d draws Bots without bots: %v", id, columnTitles(m, id))
		}
	}
	m.bots = true
	m.rebuildPRTable(false)
	if !slices.Contains(columnTitles(m, paneMine), "Bots") {
		t.Fatalf("Bots missing with bots on: %v", columnTitles(m, paneMine))
	}
}

func TestPreviewCellsArePending(t *testing.T) {
	prs := []github.PullRequest{
		{Repository: "acme/api", Number: 1, Title: "plain", Mergeable: "MERGEABLE", Additions: 5},
		{Repository: "other/api", Number: 2, Title: "conflict", Mergeable: "CONFLICTING"},
	}
	store := testPreferences(t)
	if err := store.Save("alice", ""); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 200, 30)
	// As the existing preview tests do: a preview before any full fetch.
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", Preview: true, PullRequests: prs}})
	titles := columnTitles(m, paneMine)
	rows := m.panes[paneMine].table.Rows()
	if len(rows) != len(prs) {
		t.Fatalf("%d preview rows, want %d", len(rows), len(prs))
	}
	merge, size := slices.Index(titles, "Merge"), slices.Index(titles, "Size")
	// Cells are compared stripped, so pending is the stripped pendingText.
	pending := ansi.Strip(pendingText)
	for i, row := range rows {
		cells := make([]string, len(row))
		for j := range row {
			cells[j] = ansi.Strip(row[j])
		}
		conflict := strings.Contains(cells[slices.Index(titles, "PR name")], "conflict")
		if conflict == (cells[merge] == pending) {
			t.Errorf("row %d Merge = %q", i, cells[merge])
		}
		for j, title := range titles {
			stat := slices.Contains([]string{"Age", "CI", "Review", "Comments"}, title)
			if stat && cells[j] != pending {
				t.Errorf("row %d %s = %q, want pending", i, title, cells[j])
			}
		}
		if cells[size] == pending {
			t.Errorf("row %d Size pending; Size is known in a preview", i)
		}
	}
}
