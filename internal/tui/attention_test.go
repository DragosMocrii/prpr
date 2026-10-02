package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

func attentionPRs() []github.PullRequest {
	return []github.PullRequest{
		{Number: 1, Repository: "acme/a", Title: "ready", Mergeable: "MERGEABLE", MergeState: "CLEAN", Checks: "SUCCESS", ReviewDecision: "APPROVED"},
		{Number: 2, Repository: "acme/a", Title: "changes", Mergeable: "MERGEABLE", MergeState: "BLOCKED", ReviewDecision: "CHANGES_REQUESTED", Checks: "FAILURE"},
		{Number: 3, Repository: "acme/b", Title: "conflict", Mergeable: "CONFLICTING", MergeState: "DIRTY",
			Bots: []github.BotReview{{Name: "Copilot", State: github.BotConcerns, Concerns: 2}}},
		// Unknown merge state, null decision, and null rollup: never healthy,
		// never blocked.
		{Number: 4, Repository: "acme/b", Title: "unknown", Mergeable: "UNKNOWN"},
	}
}

func attentionReviews() []github.PullRequest {
	return []github.PullRequest{
		{Number: 10, Repository: "acme/a", Title: "review a", Author: "bob", Mergeable: "MERGEABLE", MergeState: "CLEAN"},
		{Number: 11, Repository: "acme/b", Title: "review b", Author: "bob", Mergeable: "MERGEABLE", MergeState: "UNKNOWN"},
	}
}

func categoryCounts(m *model) []int {
	counts := make([]int, len(attentionCategories))
	for i := range counts {
		counts[i] = m.categoryCount(i + 1)
	}
	return counts
}

func TestAttentionCategoriesCountProvenStatesOnly(t *testing.T) {
	m := newPaneModel(t, 200, 30, attentionPRs(), attentionReviews())
	// ready, changes requested, failing CI, conflicts, bot threads, awaiting
	// review, unknown (one authored, one review request).
	if got, want := categoryCounts(m), []int{1, 1, 1, 1, 1, 2, 2}; !slices.Equal(got, want) {
		t.Fatalf("counts = %v, want %v", got, want)
	}
	summary := ansi.Strip(m.summaryLine())
	for _, want := range []string{"1 ✓ 1 ready to merge", "2 ✗ 1 changes requested", "6 ● 2 awaiting your review", "7 ? 2 status unknown"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary %q lacks %q", summary, want)
		}
	}

	// Counts follow the repository scope but not the search or quick filter.
	m.chooseRepository("acme/a")
	if got, want := categoryCounts(m), []int{1, 1, 1, 0, 0, 1, 0}; !slices.Equal(got, want) {
		t.Fatalf("scoped counts = %v, want %v", got, want)
	}
	press(m, tea.Key{Code: 'D', Text: "D"})
	if got, want := categoryCounts(m), []int{1, 1, 1, 0, 0, 1, 0}; !slices.Equal(got, want) {
		t.Fatalf("counts under a quick filter = %v, want %v", got, want)
	}
	if summary := ansi.Strip(m.summaryLine()); strings.Contains(summary, "conflicts") {
		t.Fatalf("empty category shown: %q", summary)
	}
}

func TestPreviewSummaryCountsOnlyConflicts(t *testing.T) {
	m := newPaneModel(t, 140, 30, nil, nil)
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", Preview: true, PullRequests: attentionPRs(), ReviewRequests: attentionReviews()}})
	if got, want := categoryCounts(m), []int{0, 0, 0, 1, 0, 0, 0}; !slices.Equal(got, want) {
		t.Fatalf("preview counts = %v, want %v", got, want)
	}
	if summary := ansi.Strip(m.summaryLine()); strings.Contains(summary, "ready") || !strings.Contains(summary, "after details load") {
		t.Fatalf("preview summary = %q", summary)
	}
}

func TestCategoryKeysFilterFocusAndToggle(t *testing.T) {
	m := newPaneModel(t, 140, 30, attentionPRs(), attentionReviews())
	press(m, tea.Key{Code: 'F', Text: "F"})
	press(m, tea.Key{Code: '6', Text: "6"})
	if m.focus != paneReview || m.quick != quickNone {
		t.Fatalf("6: focus %v quick %v", m.focus, m.quick)
	}
	if mine, review := shownNumbers(m, paneMine), shownNumbers(m, paneReview); len(mine) != 0 || !slices.Equal(review, []int{10, 11}) {
		t.Fatalf("6 shows %v / %v", mine, review)
	}
	title := ansi.Strip(m.listLines()[0])
	if !strings.Contains(title, "awaiting your review") || !strings.Contains(m.summaryLine(), reverseOn) {
		t.Fatalf("active category not shown: %q", title)
	}

	press(m, tea.Key{Code: '1', Text: "1"})
	if m.focus != paneMine || !slices.Equal(shownNumbers(m, paneMine), []int{1}) || len(shownNumbers(m, paneReview)) != 0 {
		t.Fatalf("1: focus %v shows %v / %v", m.focus, shownNumbers(m, paneMine), shownNumbers(m, paneReview))
	}
	press(m, tea.Key{Code: '1', Text: "1"})
	if m.filtersActive() {
		t.Fatal("pressing 1 again did not clear the category")
	}

	press(m, tea.Key{Code: '7', Text: "7"})
	if mine, review := shownNumbers(m, paneMine), shownNumbers(m, paneReview); !slices.Equal(mine, []int{4}) || !slices.Equal(review, []int{11}) {
		t.Fatalf("7 shows %v / %v", mine, review)
	}
	press(m, tea.Key{Code: tea.KeyEsc})
	if m.filtersActive() {
		t.Fatal("esc did not clear the category")
	}

	// An empty category does nothing.
	m.chooseRepository("acme/a")
	press(m, tea.Key{Code: '4', Text: "4"})
	if m.category != 0 {
		t.Fatal("empty category became active")
	}
}

func TestSummaryLineLayout(t *testing.T) {
	m := newPaneModel(t, 140, 30, attentionPRs(), attentionReviews())
	if lines := ansi.Strip(m.View().Content); !strings.Contains(strings.Split(lines, "\n")[1], "1 ✓ 1") {
		t.Fatalf("summary is not the second line:\n%s", lines)
	}
	for _, size := range [][2]int{{40, 8}, {40, 13}, {60, 14}, {140, 30}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := assertBounded(t, m, size[0], size[1])
		shown := strings.Contains(ansi.Strip(strings.Join(lines, "\n")), "1 ✓ 1")
		if want := size[1] >= minSummaryHeight; shown != want {
			t.Fatalf("summary shown %t at %dx%d, want %t", shown, size[0], size[1], want)
		}
	}
}

func TestNarrowSummaryUsesShortLabels(t *testing.T) {
	m := newPaneModel(t, 140, 30, attentionPRs(), attentionReviews())
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	summary := ansi.Strip(m.summaryLine())
	if !strings.Contains(summary, "7 ? 2 unknown") || strings.Contains(summary, "ready to merge") {
		t.Fatalf("narrow summary = %q", summary)
	}
}
