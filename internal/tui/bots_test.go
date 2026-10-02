package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

func botPRs() []github.PullRequest {
	return []github.PullRequest{
		{Number: 1, Repository: "acme/a", Title: "concerns", URL: "https://github.com/acme/a/pull/1", Bots: []github.BotReview{
			{Name: "Claude", State: github.BotPassed},
			{Name: "Codex", State: github.BotConcerns, Concerns: 2},
			{Name: "Copilot", State: github.BotConcerns, Concerns: 1},
		}},
		{Number: 2, Repository: "acme/a", Title: "stale", URL: "https://github.com/acme/a/pull/2", Bots: []github.BotReview{
			{Name: "Claude", State: github.BotPassed},
			{Name: "Codex", State: github.BotStale},
			{Name: "Copilot", State: github.BotNotRun},
		}},
		{Number: 3, Repository: "acme/a", Title: "quiet", URL: "https://github.com/acme/a/pull/3", Bots: []github.BotReview{
			{Name: "Claude", State: github.BotNotRun},
		}},
	}
}

func newBotsModel(t *testing.T, width int) *model {
	t.Helper()
	m := newTableModel(t, width, 16, botPRs())
	m.bots = true
	m.rebuildPRTable(false)
	return m
}

func TestBotsColumnShowsWorstStateAcrossBots(t *testing.T) {
	m := newBotsModel(t, 140)
	columns := m.panes[paneMine].table.Columns()
	index := columnIndex(columns, "Bots")
	if index < 0 || columns[index-1].Title != "Age" {
		t.Fatalf("Bots column should follow Age: %+v", columns)
	}
	var got []string
	for _, row := range m.panes[paneMine].table.Rows() {
		got = append(got, ansi.Strip(row[index]))
	}
	if strings.Join(got, ",") != "✗3,✓*,–" {
		t.Fatalf("Bots cells = %q", got)
	}
}

func TestBotsColumnDropsBeforeAgeButAfterOtherStatistics(t *testing.T) {
	m := newBotsModel(t, 140)
	want := []string{"Age", "Bots", "CI", "Review", "Comments", "Size"}
	previous := len(want)
	// Below 80 columns the Repository column folds into the name and frees room.
	for width := 140; width >= 80; width-- {
		m.width = width
		m.rebuildPRTable(false)
		columns := m.panes[paneMine].table.Columns()
		var got []string
		for _, column := range columns[columnIndex(columns, "State")+1:] {
			got = append(got, column.Title)
		}
		if len(got) > previous || strings.Join(got, ",") != strings.Join(want[:len(got)], ",") {
			t.Fatalf("width %d statistics columns = %v", width, got)
		}
		previous = len(got)
	}
}

func TestBotsHiddenWhenNoneConfigured(t *testing.T) {
	m := newTableModel(t, 140, 16, botPRs())
	if columnIndex(m.panes[paneMine].table.Columns(), "Bots") >= 0 {
		t.Fatal("Bots column shown with no bots configured")
	}
	if view := ansi.Strip(m.View().Content); strings.Contains(view, "Codex") {
		t.Fatalf("bot breakdown shown with no bots configured:\n%s", view)
	}
}

func TestSelectedPRShowsBotBreakdownWhenItFits(t *testing.T) {
	m := newBotsModel(t, 140)
	const breakdown = "Claude ✓ · Codex ✗2 · Copilot ✗1"
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "https://github.com/acme/a/pull/1  "+breakdown) {
		t.Fatalf("breakdown missing beside URL:\n%s", view)
	}
	m.width = 60
	m.rebuildPRTable(false)
	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, "Codex") || !strings.Contains(view, "https://github.com/acme/a/pull/1") {
		t.Fatalf("narrow view should keep the whole URL and drop the breakdown:\n%s", view)
	}
}
