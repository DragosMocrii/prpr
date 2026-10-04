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
	if m.searching != nil || !strings.Contains(view, `search "sam"`) || !strings.Contains(view, "My PRs (0 of 2)") ||
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
	if m.filtersActive() || len(shownNumbers(m, paneMine)) != 2 {
		t.Fatalf("esc on the list did not clear filters: %q %v", m.search, shownNumbers(m, paneMine))
	}
}

func TestQuickFiltersToggleReplaceAndCombine(t *testing.T) {
	m := newPaneModel(t, 120, 30, filterPRs(), filterReviews())
	for _, tc := range []struct {
		key  rune
		want []int
	}{
		{'F', []int{88}},
		{'M', []int{412}},
		{'M', []int{412, 88}},
		// Drafts join whatever else is shown.
		{'D', []int{412, 88, 419}},
		{'F', []int{88, 419}},
		{'D', []int{88}},
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
	if got := shownNumbers(m, paneMine); len(got) != 1 {
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
	// #88 and #419 leave; their gone rows still fail CI, and the draft's
	// shows with the drafts.
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: filterPRs()[:1]}})
	if got := shownNumbers(m, paneMine); !slices.Equal(got, []int{88}) {
		t.Fatalf("gone rows under failing CI = %v", got)
	}
	press(m, tea.Key{Code: 'D', Text: "D"})
	if got := shownNumbers(m, paneMine); !slices.Equal(got, []int{88, 419}) {
		t.Fatalf("gone rows with drafts shown = %v", got)
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

func TestDraftsAreHiddenUntilDShowsThemAndTheChoiceIsSaved(t *testing.T) {
	draftReview := filterReviews()[0]
	draftReview.Number, draftReview.Draft = 1291, true
	m := newPaneModel(t, 140, 30, filterPRs(), append(filterReviews(), draftReview))
	if got := shownNumbers(m, paneMine); slices.Contains(got, 419) {
		t.Fatalf("My PRs shows the draft by default: %v", got)
	}
	if got := shownNumbers(m, paneReview); !slices.Equal(got, []int{1290}) {
		t.Fatalf("Review requested by default = %v", got)
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "My PRs (2) · 1 draft hidden") || !strings.Contains(view, "Review requested (1) · 1 draft hidden") {
		t.Fatalf("pane titles do not say drafts are hidden:\n%s", view)
	}
	// Hidden drafts are out of scope: no count points at them.
	if m.categoryCount(3) != 1 || m.categoryCount(6) != 1 {
		t.Fatalf("failing CI %d, awaiting review %d, want the drafts left out", m.categoryCount(3), m.categoryCount(6))
	}

	press(m, tea.Key{Code: 'D', Text: "D"})
	if !m.preferences.ShowDrafts() || len(shownNumbers(m, paneMine)) != 3 || len(shownNumbers(m, paneReview)) != 2 {
		t.Fatalf("D did not show and save the drafts: saved %t, %v %v", m.preferences.ShowDrafts(), shownNumbers(m, paneMine), shownNumbers(m, paneReview))
	}
	view = ansi.Strip(m.View().Content)
	if !strings.Contains(view, "drafts shown") || strings.Contains(view, "hidden") {
		t.Fatalf("view with drafts shown:\n%s", view)
	}
	if m.categoryCount(3) != 2 || m.categoryCount(6) != 2 {
		t.Fatalf("shown drafts are not counted: %d %d", m.categoryCount(3), m.categoryCount(6))
	}
	// Esc clears filters, not the saved choice.
	press(m, tea.Key{Code: tea.KeyEsc})
	if !m.showDrafts {
		t.Fatal("esc hid the drafts")
	}
	press(m, tea.Key{Code: 'D', Text: "D"})
	if m.showDrafts || m.preferences.ShowDrafts() || slices.Contains(shownNumbers(m, paneMine), 419) {
		t.Fatal("D again did not hide and save")
	}
}

func TestSavedDraftsChoiceIsRestored(t *testing.T) {
	store := testPreferences(t)
	if err := store.SaveShowDrafts(true); err != nil {
		t.Fatal(err)
	}
	client, err := github.NewClient(nil)
	if err != nil {
		t.Fatal(err)
	}
	if m := New(t.Context(), client, store).(*model); !m.showDrafts {
		t.Fatal("saved shown drafts started hidden")
	}
}

func TestAHiddenDraftReviewRequestAlertsWhenItLeavesDraft(t *testing.T) {
	m := notifyModel(t, "")
	draft := reviewedPR(9, github.ReviewRequested)
	draft.Draft = true
	fetch(m, nil, nil)
	if got := fetch(m, nil, []github.PullRequest{draft}); got != nil {
		t.Fatalf("a hidden draft request notified: %q", got)
	}
	ready := draft
	ready.Draft = false
	if got := fetch(m, nil, []github.PullRequest{ready}); len(got) != 1 || !strings.Contains(got[0], "acme/b#9 review requested") {
		t.Fatalf("leaving draft notified %q", got)
	}
}

func TestAScopeOfOnlyDraftsSaysDShowsThem(t *testing.T) {
	m := newPaneModel(t, 40, 10, filterPRs()[2:], nil)
	assertBounded(t, m, 40, 10)
	m.Update(tea.WindowSizeMsg{Width: 70, Height: 10})
	view := ansi.Strip(strings.Join(assertBounded(t, m, 70, 10), "\n"))
	if !strings.Contains(view, "1 draft hidden, D shows it") {
		t.Fatalf("empty pane does not point at the hidden draft:\n%s", view)
	}
	press(m, tea.Key{Code: 'D', Text: "D"})
	if got := shownNumbers(m, paneMine); !slices.Equal(got, []int{419}) {
		t.Fatalf("after D = %v", got)
	}
}
