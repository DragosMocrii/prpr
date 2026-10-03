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

func snoozePRs() ([]github.PullRequest, []github.PullRequest) {
	mine := []github.PullRequest{
		{Repository: "acme/api", Number: 1, Title: "First", Mergeable: "MERGEABLE", MergeState: "BLOCKED"},
		{Repository: "acme/api", Number: 2, Title: "Second", Mergeable: "MERGEABLE", MergeState: "BLOCKED"},
	}
	review := []github.PullRequest{
		{Repository: "acme/web", Number: 7, Title: "Review me", Author: "bob", Mergeable: "MERGEABLE", MergeState: "BLOCKED"},
	}
	return mine, review
}

// snoozeFor puts snoozes in place directly; Task 4 adds the key that does.
func snoozeFor(m *model, snoozes ...preferences.Snooze) {
	if m.snoozes == nil {
		m.snoozes = make(map[prKey]preferences.Snooze)
	}
	for _, s := range snoozes {
		m.snoozes[prKey{strings.ToLower(s.Repository), s.Number}] = s
	}
	m.keepingSelection(m.rebuildVisiblePRs)
}

func paneNumbers(m *model, id paneID) []int {
	var numbers []int
	for row := range rowCount(&m.panes[id]) {
		if pr, _, ok := m.paneRow(id, row); ok {
			numbers = append(numbers, pr.Number)
		}
	}
	return numbers
}

func TestSnoozedPaneIndexesBothLists(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	if slices.Contains(m.drawnPanes(), paneSnoozed) {
		t.Fatal("Snoozed pane drawn without snoozes")
	}
	until := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	// The review snooze saw the pending request, so a fetch does not wake it.
	snoozeFor(m,
		preferences.Snooze{Repository: "acme/web", Number: 7, List: preferences.SnoozeReview, Until: until,
			Seen: []string{signalRequested}},
		preferences.Snooze{Repository: "acme/api", Number: 2, List: preferences.SnoozeMine, Until: until.Add(time.Hour)})
	if got := paneNumbers(m, paneSnoozed); !slices.Equal(got, []int{7, 2}) {
		t.Fatalf("Snoozed rows = %v, want [7 2] (soonest first)", got)
	}
	if got := paneNumbers(m, paneMine); !slices.Equal(got, []int{1}) {
		t.Fatalf("My PRs = %v, want [1]", got)
	}
	if got := paneNumbers(m, paneReview); len(got) != 0 {
		t.Fatalf("Review requested = %v, want none", got)
	}
	// An empty authored list still maps review rows. The fixed clock keeps
	// the snoozes from ending.
	m.now = func() time.Time { return snoozeNow }
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: review}})
	if got := paneNumbers(m, paneSnoozed); !slices.Contains(got, 7) {
		t.Fatalf("Snoozed rows with no authored list = %v", got)
	}
}

func TestSnoozedRowsCountNowhere(t *testing.T) {
	mine, review := snoozePRs()
	mine[1].MergeState = "CLEAN"
	m := newPaneModel(t, 140, 40, mine, review)
	before := m.needYouCount()
	until := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	snoozeFor(m,
		preferences.Snooze{Repository: "acme/api", Number: 2, List: preferences.SnoozeMine, Until: until},
		preferences.Snooze{Repository: "acme/web", Number: 7, List: preferences.SnoozeReview, Until: until})
	if got := m.needYouCount(); got != before-2 {
		t.Fatalf("title count = %d, want %d", got, before-2)
	}
	for number := 1; number <= len(attentionCategories); number++ {
		if m.categoryMatches(number, paneSnoozed, &m.snapshot.PullRequests[1], false) {
			t.Fatalf("category %d covers the Snoozed pane", number)
		}
	}
}

func TestFourPanesFitEverySize(t *testing.T) {
	mine, review := snoozePRs()
	mine = append(mine, github.PullRequest{Repository: "acme/api", Number: 3, Title: "Queued",
		Queue: &github.QueueEntry{Provider: "Trunk", State: github.QueueTesting}})
	for _, size := range [][2]int{{40, 8}, {60, 12}, {80, 20}, {140, 40}} {
		m := newPaneModel(t, size[0], size[1], mine, review)
		snoozeFor(m, preferences.Snooze{Repository: "acme/api", Number: 1, List: preferences.SnoozeMine,
			Until: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)})
		assertBounded(t, m, size[0], size[1])
		for range 4 {
			m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			assertBounded(t, m, size[0], size[1])
		}
	}
}

func TestTabReachesTheSnoozedPane(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	snoozeFor(m, preferences.Snooze{Repository: "acme/api", Number: 1, List: preferences.SnoozeMine,
		Until: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)})
	seen := map[paneID]bool{}
	for range 3 {
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		seen[m.focus] = true
	}
	if !seen[paneSnoozed] {
		t.Fatalf("Tab never focused the Snoozed pane: %v", seen)
	}
}

func TestSnoozedPaneColumnsAndDimRows(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	m.now = func() time.Time { return snoozeNow }
	snoozeFor(m,
		preferences.Snooze{Repository: "acme/api", Number: 1, List: preferences.SnoozeMine, Until: time.Date(2026, 10, 6, 9, 0, 0, 0, snoozeZone)},
		preferences.Snooze{Repository: "acme/web", Number: 7, List: preferences.SnoozeReview, Until: snoozeNow.Add(activityLimit), Activity: true})
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Snoozed (2)", "Wakes", "From", "Tue 09:00", "activity", "mine"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q", want)
		}
	}
}

func TestWokeRowsAreMarkedAndTagged(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	m.woke = map[prKey]string{{"acme/api", 2}: "changes requested"}
	m.rebuildVisiblePRs()
	pr := &m.snapshot.PullRequests[1]
	if mark := m.rowMark(paneMine, pr); mark.kind != markChanged {
		t.Fatalf("woken row mark = %v, want changed", mark.kind)
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "woke: changes requested") {
		t.Fatal("woken row has no tag")
	}
	// Leaving the row clears the tag.
	m.setFocus(paneMine)
	m.selectPR(paneMine, "acme/api", 2)
	m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if _, ok := m.woke[prKey{"acme/api", 2}]; ok {
		t.Fatal("leaving the row kept the woke tag")
	}
}

func TestSelectionFollowsAWokenPullRequestOnlyWhenAsked(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	snoozeFor(m, preferences.Snooze{Repository: "acme/api", Number: 2, List: preferences.SnoozeMine,
		Until: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)})
	m.setFocus(paneSnoozed)
	m.selectPR(paneSnoozed, "acme/api", 2)
	m.keepSelection(func() { delete(m.snoozes, prKey{"acme/api", 2}); m.rebuildVisiblePRs() }, true)
	if pr, ok := m.selectedPR(); !ok || m.focus != paneMine || pr.Number != 2 {
		t.Fatalf("focus %v on %v, want My PRs on #2", m.focus, pr)
	}
}

func TestSelectionStaysWithoutFollow(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	m.setFocus(paneMine)
	m.selectPR(paneMine, "acme/api", 2)
	m.keepSelection(func() {
		m.snoozes = map[prKey]preferences.Snooze{{"acme/api", 2}: {Repository: "acme/api", Number: 2,
			List: preferences.SnoozeMine, Until: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)}}
		m.rebuildVisiblePRs()
	}, false)
	if m.focus != paneMine {
		t.Fatalf("focus moved to %v without follow", m.focus)
	}
}

func TestSnoozedGoneRowsIndexBothGoneLists(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	until := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	snoozeFor(m,
		preferences.Snooze{Repository: "acme/web", Number: 7, List: preferences.SnoozeReview, Until: until},
		preferences.Snooze{Repository: "acme/api", Number: 2, List: preferences.SnoozeMine, Until: until})
	// Both snoozed pull requests close; #1 closes too.
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice"}})
	if got := paneNumbers(m, paneSnoozed); !slices.Equal(got, []int{2, 7}) {
		t.Fatalf("Snoozed gone rows = %v, want [2 7]", got)
	}
	if got := paneNumbers(m, paneMine); !slices.Equal(got, []int{1}) {
		t.Fatalf("My PRs gone rows = %v, want [1]", got)
	}
	// The closed snoozes are deleted; their gone rows stay through
	// snoozeClosed.
	if len(m.snoozes) != 0 || len(m.snoozeClosed) != 2 {
		t.Fatalf("snoozes %v, closed %v, want none and two", m.snoozes, m.snoozeClosed)
	}
	if got := paneNumbers(m, paneReview); len(got) != 0 {
		t.Fatalf("Review requested gone rows = %v, want none", got)
	}
	// x clears the gone rows and the closed snoozes.
	m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if len(m.snoozeClosed) != 0 || slices.Contains(m.drawnPanes(), paneSnoozed) {
		t.Fatalf("x kept closed snoozes %v or the Snoozed pane", m.snoozeClosed)
	}
}

func TestSnoozeLegendOnlyWithSnoozedOrWokeRows(t *testing.T) {
	mine, review := snoozePRs()
	for _, nerd := range []bool{false, true} {
		m := newPaneModel(t, 140, 40, mine, review)
		if nerd {
			m.icons = &nerdIcons
		}
		has := func() bool {
			for _, section := range m.legendSections() {
				if section.label == "Snoozed" {
					if nerd && (nerdIcons.woke == "" || !strings.HasPrefix(ansi.Strip(section.items[0]), nerdIcons.woke)) {
						t.Fatalf("Nerd Snoozed legend entry %q does not start with the woke icon", section.items[0])
					}
					return true
				}
			}
			return false
		}
		if has() {
			t.Fatalf("nerd=%v: Snoozed legend without snoozes", nerd)
		}
		snoozeFor(m, preferences.Snooze{Repository: "acme/api", Number: 1, List: preferences.SnoozeMine,
			Until: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)})
		if !has() {
			t.Fatalf("nerd=%v: no Snoozed legend with a snoozed row", nerd)
		}
		delete(m.snoozes, prKey{"acme/api", 1})
		m.woke = map[prKey]string{{"acme/api", 1}: "snooze ended"}
		m.rebuildVisiblePRs()
		if !has() {
			t.Fatalf("nerd=%v: no Snoozed legend with a woke tag on screen", nerd)
		}
	}
}
