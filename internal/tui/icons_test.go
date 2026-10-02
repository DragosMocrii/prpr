package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/preferences"
)

func pressI(m *model) {
	press(m, tea.Key{Code: 'i', Text: "i"})
}

// nameWidth is the PR name column's width in a pane.
func nameWidth(m *model, id paneID) int {
	for _, column := range m.panes[id].table.Columns() {
		if column.Title == "PR name" {
			return column.Width
		}
	}
	return 0
}

func TestNerdIconsCompactTheColumnsAndKeepWordsElsewhere(t *testing.T) {
	draft := changePR(2, "acme/a")
	draft.Draft = true
	m := changesModel(t, changePR(1, "acme/a"), draft)
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: m.snapshot.PullRequests,
		ReviewRequests: []github.PullRequest{reviewedPR(9, github.ReviewWaitingOnAuthor)}}})
	unicodeWidth := nameWidth(m, paneMine)
	row := strings.Join(m.panes[paneMine].table.Rows()[0], " ")
	if !strings.Contains(row, "open") || strings.ContainsRune(row, '') {
		t.Fatalf("unicode row = %q", row)
	}

	pressI(m)
	if !m.icons.nerd {
		t.Fatal("i did not switch to Nerd Font icons")
	}
	if got := nameWidth(m, paneMine); got <= unicodeWidth {
		t.Fatalf("PR name width = %d with Nerd icons, want more than %d", got, unicodeWidth)
	}
	rows := m.panes[paneMine].table.Rows()
	if mine := strings.Join(rows[0], " ") + strings.Join(rows[1], " "); strings.Contains(mine, "open") || strings.Contains(mine, "draft") ||
		!strings.ContainsRune(mine, '') || !strings.ContainsRune(mine, '') {
		t.Fatalf("nerd rows = %q, want state icons instead of words", mine)
	}
	if review := reviewRow(m, 0); strings.Contains(review, "waiting") || !strings.ContainsRune(review, '') {
		t.Fatalf("nerd review row = %q, want the waiting icon", review)
	}
	for _, column := range m.panes[paneMine].table.Columns() {
		if column.Title != "PR name" && column.Title != "Number" && column.Title != "Repository" && column.Title != "" && ansi.StringWidth(column.Title) != 1 {
			t.Errorf("header %q is not one icon", column.Title)
		}
	}
	// Details keep their words.
	m.details = true
	if view := m.View().Content; !strings.Contains(view, "Blocked") && !strings.Contains(view, "blocked") {
		t.Fatalf("details lost their words: %q", view)
	}
	m.details = false

	if got := m.preferences.Icons(); got != IconsNerd {
		t.Fatalf("saved icons = %q", got)
	}
	pressI(m)
	if m.icons.nerd || m.preferences.Icons() != IconsUnicode || nameWidth(m, paneMine) != unicodeWidth {
		t.Fatalf("switching back: nerd %t, saved %q, width %d", m.icons.nerd, m.preferences.Icons(), nameWidth(m, paneMine))
	}
}

func TestEveryNerdIconIsOneCell(t *testing.T) {
	ic := nerdIcons
	icons := []string{ic.check, ic.cross, ic.pending, ic.behind, ic.unknown, ic.none, ic.passed, ic.failed, ic.botRunning, ic.botFailed,
		ic.open, ic.draft, ic.newCommits, ic.replied, ic.dismissed, ic.activity, ic.waiting, ic.approved, ic.backInDraft, ic.star, ic.pin, ic.bell}
	for _, header := range ic.headers {
		icons = append(icons, header)
	}
	for _, icon := range icons {
		if ansi.StringWidth(icon) != 1 {
			t.Errorf("icon %U is %d cells wide", []rune(icon), ansi.StringWidth(icon))
		}
	}
}

func TestStartIconsPrefersTheRunsChoiceThenTheSavedOne(t *testing.T) {
	store := testPreferences(t)
	if startIcons("", store).nerd {
		t.Fatal("no choice started with Nerd icons")
	}
	if err := store.SaveIcons(IconsNerd); err != nil {
		t.Fatal(err)
	}
	if !startIcons("", store).nerd || startIcons(IconsUnicode, store).nerd {
		t.Fatal("the saved choice or the run's choice was ignored")
	}
}

func TestFailedIconSaveKeepsTheIconsAndWarns(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to a read-only directory")
	}
	dir := t.TempDir()
	store, err := preferences.Open(filepath.Join(dir, "preferences.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("alice", ""); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 140, 30)
	updateSnapshot(m, "alice", changePR(1, "acme/a"))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	pressI(m)
	if !m.icons.nerd || store.Icons() != "" || !strings.Contains(m.View().Content, "Icon choice not saved") {
		t.Fatalf("nerd %t, saved %q, view %q", m.icons.nerd, store.Icons(), m.View().Content)
	}
}

func TestNerdIconsKeepNumbersOffTheIcon(t *testing.T) {
	ic := &nerdIcons
	for _, text := range []string{
		reviewText(ic, "APPROVED", 2),
		botStateText(ic, github.BotReview{State: github.BotConcerns, Concerns: 3}),
		botStateText(ic, github.BotReview{State: github.BotStale}),
	} {
		plain := ansi.Strip(text)
		if runes := []rune(plain); len(runes) < 3 || runes[1] != ' ' {
			t.Errorf("%q does not separate the icon from what follows", plain)
		}
	}
	if got := ansi.Strip(reviewText(&unicodeIcons, "APPROVED", 2)); got != "✓2" {
		t.Errorf("unicode review = %q, want it unchanged", got)
	}
}
