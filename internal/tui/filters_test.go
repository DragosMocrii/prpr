package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

func filterPRs() []github.PullRequest {
	return []github.PullRequest{
		{Number: 412, Repository: "acme/api", Title: "Add rate limiting", Author: "alice", URL: "https://github.com/acme/api/pull/412",
			Mergeable: "MERGEABLE", MergeState: "CLEAN", Checks: "SUCCESS"},
		{Number: 88, Repository: "acme/infra", Title: "Split Terraform state", Author: "alice", URL: "https://github.com/acme/infra/pull/88",
			Mergeable: "MERGEABLE", MergeState: "BLOCKED", Checks: "FAILURE"},
		{Number: 419, Repository: "acme/api", Title: "Prototype webhook retries", Author: "alice", URL: "https://github.com/acme/api/pull/419",
			Draft: true, Mergeable: "MERGEABLE", MergeState: "CLEAN", Checks: "ERROR"},
	}
}

func filterReviews() []github.PullRequest {
	return []github.PullRequest{
		{Number: 1290, Repository: "acme/web", Title: "Cache avatars", Author: "sam-lee", URL: "https://github.com/acme/web/pull/1290", Checks: "PENDING"},
	}
}

func shownNumbers(m *model, id paneID) []int {
	var numbers []int
	for row := range rowCount(&m.panes[id]) {
		if pr, _, ok := m.paneRow(id, row); ok {
			numbers = append(numbers, pr.Number)
		}
	}
	return numbers
}

func typeText(m *model, text string) {
	for _, r := range text {
		press(m, tea.Key{Code: r, Text: string(r)})
	}
}

func TestSearchMatchesTitleRepositoryAuthorAndNumber(t *testing.T) {
	pr := &filterPRs()[0]
	for search, want := range map[string]bool{
		"":             true,
		"RATE":         true,
		"acme/api":     true,
		"alice":        true,
		"412":          true,
		"#41":          true,
		"rate api":     true,
		"rate infra":   false,
		"#88":          false,
		"bob":          false,
		"  limiting  ": true,
	} {
		if got := searchMatches(pr, search); got != want {
			t.Errorf("searchMatches(%q) = %t, want %t", search, got, want)
		}
	}
}

func TestSearchFiltersBothListsLiveAndEnterKeepsIt(t *testing.T) {
	m := newPaneModel(t, 120, 30, filterPRs(), filterReviews())
	press(m, tea.Key{Code: '/', Text: "/"})
	if m.searching == nil {
		t.Fatal("/ did not open the search")
	}
	// Keys that act on the list are text here.
	typeText(m, "q j")
	if m.search != "q j" {
		t.Fatalf("search = %q", m.search)
	}
	press(m, tea.Key{Code: 'u', Mod: tea.ModCtrl})
	typeText(m, "sam")
	if got := shownNumbers(m, paneMine); len(got) != 0 {
		t.Fatalf("My PRs while searching sam = %v", got)
	}
	if got := shownNumbers(m, paneReview); !slices.Equal(got, []int{1290}) {
		t.Fatalf("Review requested while searching sam = %v", got)
	}
	press(m, tea.Key{Code: tea.KeyEnter})
	view := ansi.Strip(m.View().Content)
	if m.searching != nil || !strings.Contains(view, `search "sam"`) || !strings.Contains(view, "My PRs (0 of 3)") ||
		!strings.Contains(view, "No pull requests match the filters") {
		t.Fatalf("kept search not shown:\n%s", view)
	}

	// Esc while editing restores the kept search.
	press(m, tea.Key{Code: '/', Text: "/"})
	typeText(m, "zzz")
	press(m, tea.Key{Code: tea.KeyEsc})
	if m.search != "sam" || m.searching != nil {
		t.Fatalf("esc while editing: search %q searching %v", m.search, m.searching)
	}
	press(m, tea.Key{Code: tea.KeyEsc})
	if m.filtersActive() || len(shownNumbers(m, paneMine)) != 3 {
		t.Fatalf("esc on the list did not clear filters: %q %v", m.search, shownNumbers(m, paneMine))
	}
}

func TestQuickFiltersToggleReplaceAndCombine(t *testing.T) {
	m := newPaneModel(t, 120, 30, filterPRs(), filterReviews())
	for _, tc := range []struct {
		key  rune
		want []int
	}{
		{'D', []int{419}},
		{'F', []int{88, 419}},
		{'M', []int{412}},
		{'M', []int{412, 88, 419}},
	} {
		press(m, tea.Key{Code: tc.key, Text: string(tc.key)})
		got := shownNumbers(m, paneMine)
		slices.Sort(got)
		want := slices.Clone(tc.want)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("after %c: %v, want %v", tc.key, got, want)
		}
	}
	press(m, tea.Key{Code: 'F', Text: "F"})
	press(m, tea.Key{Code: '/', Text: "/"})
	typeText(m, "terraform")
	press(m, tea.Key{Code: tea.KeyEnter})
	if got := shownNumbers(m, paneMine); !slices.Equal(got, []int{88}) {
		t.Fatalf("failing CI + search = %v", got)
	}
	if got := shownNumbers(m, paneReview); len(got) != 0 {
		t.Fatalf("pending review request matched failing CI: %v", got)
	}
	if title := ansi.Strip(m.listLines()[0]); !strings.Contains(title, `search "terraform" · failing CI`) {
		t.Fatalf("title = %q", title)
	}
	// c clears only the repository scope.
	press(m, tea.Key{Code: 'c', Text: "c"})
	if !m.filtersActive() {
		t.Fatal("c cleared the search and quick filter")
	}
}

func TestQuickFiltersNeverMatchUnknownPreviewStates(t *testing.T) {
	m := newPaneModel(t, 120, 30, nil, nil)
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", Preview: true, PullRequests: filterPRs()}})
	press(m, tea.Key{Code: 'M', Text: "M"})
	if got := shownNumbers(m, paneMine); len(got) != 0 {
		t.Fatalf("ready filter matched preview rows: %v", got)
	}
	press(m, tea.Key{Code: 'F', Text: "F"})
	if got := shownNumbers(m, paneMine); len(got) != 0 {
		t.Fatalf("failing filter matched preview rows: %v", got)
	}
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: filterPRs()}})
	if got := shownNumbers(m, paneMine); len(got) != 2 {
		t.Fatalf("failing filter after details = %v", got)
	}
}

func TestFiltersKeepSelectionAndApplyToGoneRows(t *testing.T) {
	m := newPaneModel(t, 120, 30, filterPRs(), nil)
	m.selectPR(paneMine, "acme/infra", 88)
	press(m, tea.Key{Code: 'F', Text: "F"})
	if pr, _ := m.selectedPR(); pr.Number != 88 {
		t.Fatalf("selection after F = #%d", pr.Number)
	}
	// #88 and #419 leave; their gone rows still fail CI.
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: filterPRs()[:1]}})
	if got := shownNumbers(m, paneMine); !slices.Equal(got, []int{88, 419}) {
		t.Fatalf("gone rows under failing CI = %v", got)
	}
	press(m, tea.Key{Code: 'D', Text: "D"})
	if got := shownNumbers(m, paneMine); !slices.Equal(got, []int{419}) {
		t.Fatalf("gone rows under drafts = %v", got)
	}
}

func TestSearchClosesOnFetchErrorAndStaysBounded(t *testing.T) {
	m := newPaneModel(t, 40, 8, filterPRs(), filterReviews())
	press(m, tea.Key{Code: '/', Text: "/"})
	typeText(m, strings.Repeat("long ", 20))
	assertBounded(t, m, 40, 8)
	m.Update(fetchFinishedMsg{err: errors.New("boom")})
	if m.searching != nil {
		t.Fatal("search input survived a failed fetch")
	}
	press(m, tea.Key{Code: 'q', Text: "q"})
}
