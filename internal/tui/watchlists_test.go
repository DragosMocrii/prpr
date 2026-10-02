package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/preferences"
)

func ctrl(m *model, r rune) {
	press(m, tea.Key{Code: r, Mod: tea.ModCtrl})
}

func space(m *model) {
	press(m, tea.Key{Code: tea.KeySpace, Text: " "})
}

func esc(m *model) {
	press(m, tea.Key{Code: tea.KeyEsc})
}

// watchlistModel lists pull requests in three repositories.
func watchlistModel(t *testing.T) *model {
	t.Helper()
	m := pickerModel(t)
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: []github.PullRequest{
		{Number: 1, Repository: "acme/a", URL: "https://example.test/a/1"},
		{Number: 2, Repository: "acme/b", URL: "https://example.test/b/2"},
		{Number: 3, Repository: "acme/c", URL: "https://example.test/c/3"},
	}, ReviewRequests: []github.PullRequest{
		{Number: 4, Repository: "acme/c", URL: "https://example.test/c/4"},
	}}})
	openPicker(m)
	finishDiscovery(m, []string{"acme/a", "acme/b", "acme/c"}, nil)
	return m
}

func markRepository(t *testing.T, m *model, repository string) {
	t.Helper()
	m.picker.setQuery(repository)
	if got := m.picker.selectedCandidate().repository; got != repository {
		t.Fatalf("selected candidate = %q, want %s", got, repository)
	}
	space(m)
}

func visibleRepositories(m *model, id paneID) []string {
	var repositories []string
	for _, index := range m.panes[id].visible {
		repositories = append(repositories, m.source(id)[index].Repository)
	}
	return repositories
}

func TestMarkedRepositoriesBecomeASavedWatchlistScope(t *testing.T) {
	m := watchlistModel(t)
	markRepository(t, m, "acme/a")
	markRepository(t, m, "acme/c")
	// Marks survive changing the search.
	m.picker.setQuery("")
	if got := m.picker.markedRepositories(); !slices.Equal(got, []string{"acme/a", "acme/c"}) {
		t.Fatalf("marked = %q", got)
	}
	enter(m)
	if m.picker == nil || m.picker.naming == nil {
		t.Fatal("enter with marks did not ask for a name")
	}
	// Esc goes back to the marks.
	esc(m)
	if m.picker == nil || m.picker.naming != nil || len(m.picker.marked) != 2 {
		t.Fatalf("esc from the name lost the marks: %+v", m.picker)
	}
	enter(m)
	typeText(m, "My services")
	enter(m)
	if m.picker != nil {
		t.Fatalf("saving the watchlist kept the picker open: %q", m.picker.diagnostic)
	}
	if got := visibleRepositories(m, paneMine); !slices.Equal(got, []string{"acme/a", "acme/c"}) {
		t.Fatalf("My PRs repositories = %q, want the watchlist's", got)
	}
	if got := visibleRepositories(m, paneReview); !slices.Equal(got, []string{"acme/c"}) {
		t.Fatalf("review repositories = %q, want the watchlist's", got)
	}
	if title := m.listLines()[0]; !strings.Contains(title, "My services") {
		t.Fatalf("title %q does not name the watchlist", title)
	}
	if got, found := m.preferences.Lookup("alice"); !found || got != (preferences.Scope{Watchlist: "My services"}) {
		t.Fatalf("saved scope = %+v, %v", got, found)
	}
	lists := m.preferences.Watchlists("alice")
	if len(lists) != 1 || !slices.Equal(lists[0].Repositories, []string{"acme/a", "acme/c"}) {
		t.Fatalf("saved watchlists = %+v", lists)
	}
}

func TestWatchlistIsChosenEditedRenamedAndDeleted(t *testing.T) {
	m := watchlistModel(t)
	if err := m.preferences.SaveWatchlist("alice", "", preferences.Watchlist{Name: "Web", Repositories: []string{"acme/a", "acme/b"}}); err != nil {
		t.Fatal(err)
	}
	esc(m)
	openPicker(m)
	finishDiscovery(m, []string{"acme/a", "acme/b", "acme/c"}, nil)
	m.picker.setQuery("web")
	if got := m.picker.selectedCandidate(); got.kind != watchlistCandidate || got.watchlist != "Web" {
		t.Fatalf("selected candidate = %+v, want the Web watchlist", got)
	}
	enter(m)
	if got := visibleRepositories(m, paneMine); !slices.Equal(got, []string{"acme/a", "acme/b"}) {
		t.Fatalf("Web shows %q", got)
	}

	openPicker(m)
	finishDiscovery(m, []string{"acme/a", "acme/b", "acme/c"}, nil)
	m.picker.setQuery("web")
	ctrl(m, 'e')
	if m.picker.editing != "Web" || !slices.Equal(m.picker.markedRepositories(), []string{"acme/a", "acme/b"}) {
		t.Fatalf("edit marked %q for %q", m.picker.markedRepositories(), m.picker.editing)
	}
	markRepository(t, m, "acme/b")
	markRepository(t, m, "acme/c")
	enter(m)
	if got := m.picker.naming.Value(); got != "Web" {
		t.Fatalf("name prompt starts with %q, want the edited name", got)
	}
	ctrl(m, 'u')
	typeText(m, "Front")
	enter(m)
	lists := m.preferences.Watchlists("alice")
	if len(lists) != 1 || lists[0].Name != "Front" || !slices.Equal(lists[0].Repositories, []string{"acme/a", "acme/c"}) {
		t.Fatalf("watchlists after rename = %+v", lists)
	}
	if got := visibleRepositories(m, paneMine); !slices.Equal(got, []string{"acme/a", "acme/c"}) {
		t.Fatalf("renamed watchlist shows %q", got)
	}

	openPicker(m)
	finishDiscovery(m, []string{"acme/a", "acme/b", "acme/c"}, nil)
	m.picker.setQuery("front")
	ctrl(m, 'd')
	if len(m.preferences.Watchlists("alice")) != 1 {
		t.Fatal("first ctrl+d deleted without confirmation")
	}
	// Another key cancels the pending delete.
	press(m, tea.Key{Code: tea.KeyDown})
	ctrl(m, 'd')
	if len(m.preferences.Watchlists("alice")) != 1 {
		t.Fatal("ctrl+d after another key deleted without a new confirmation")
	}
	ctrl(m, 'd')
	if len(m.preferences.Watchlists("alice")) != 0 {
		t.Fatal("second ctrl+d did not delete")
	}
	if m.watchlist.Name != "" || len(m.panes[paneMine].visible) != 3 {
		t.Fatalf("deleting the shown watchlist left scope %q with %d rows", m.watchlist.Name, len(m.panes[paneMine].visible))
	}
	if got, found := m.preferences.Lookup("alice"); !found || got != (preferences.Scope{}) {
		t.Fatalf("scope after delete = %+v, %v; want All", got, found)
	}
}

func TestTakingAnotherWatchlistsNameNeedsASecondEnter(t *testing.T) {
	m := watchlistModel(t)
	if err := m.preferences.SaveWatchlist("alice", "", preferences.Watchlist{Name: "Web", Repositories: []string{"acme/a"}}); err != nil {
		t.Fatal(err)
	}
	m.picker.watchlists = m.preferences.Watchlists("alice")
	markRepository(t, m, "acme/b")
	enter(m)
	typeText(m, "web")
	enter(m)
	if m.picker == nil || m.picker.confirmReplace == "" {
		t.Fatal("first enter replaced another watchlist")
	}
	if lists := m.preferences.Watchlists("alice"); !slices.Equal(lists[0].Repositories, []string{"acme/a"}) {
		t.Fatalf("watchlist changed before confirmation: %+v", lists)
	}
	enter(m)
	lists := m.preferences.Watchlists("alice")
	if len(lists) != 1 || lists[0].Name != "web" || !slices.Equal(lists[0].Repositories, []string{"acme/b"}) {
		t.Fatalf("watchlists after replace = %+v", lists)
	}
}

func TestInvalidNamesAndEscKeepTheMarks(t *testing.T) {
	m := watchlistModel(t)
	markRepository(t, m, "acme/a")
	enter(m)
	typeText(m, "   ")
	enter(m)
	if m.picker == nil || m.picker.naming == nil || len(m.preferences.Watchlists("alice")) != 0 {
		t.Fatal("a blank name saved a watchlist")
	}
	m.Update(tea.PasteMsg{Content: "Ops\x1b[31m\nteam"})
	if got := m.picker.naming.Value(); strings.ContainsFunc(got, func(r rune) bool { return r < ' ' }) {
		t.Fatalf("pasted name kept control characters: %q", got)
	}
	esc(m)
	esc(m)
	if m.picker == nil || len(m.picker.marked) != 0 {
		t.Fatal("esc with marks did not clear them first")
	}
	esc(m)
	if m.picker != nil {
		t.Fatal("esc without marks did not close the picker")
	}
}

func TestSpaceOnOwnerRepoChecksAccessThenMarks(t *testing.T) {
	m := watchlistModel(t)
	m.picker.setQuery("public/other")
	if m.picker.selectedCandidate().kind != lookupRepositoryCandidate {
		t.Fatalf("candidate = %+v, want the lookup", m.picker.selectedCandidate())
	}
	space(m)
	if !m.picker.busy || !m.picker.lookup {
		t.Fatal("space did not check the repository")
	}
	stale := m.repositoryRequestID - 1
	m.Update(repositoryLookupFinishedMsg{requestID: stale, repository: "Wrong/Repo"})
	m.Update(repositoryLookupFinishedMsg{requestID: m.repositoryRequestID, repository: "Public/Other"})
	if m.picker == nil || !m.picker.isMarked("Public/Other") || m.picker.isMarked("Wrong/Repo") {
		t.Fatalf("lookup did not mark the checked repository: %+v", m.picker)
	}
	if m.watchlist.Name != "" || m.selectedRepository != "" {
		t.Fatal("marking a looked-up repository changed the scope")
	}
}

func TestAttentionAndNotificationsFollowTheWatchlist(t *testing.T) {
	m := watchlistModel(t)
	if err := m.preferences.SaveWatchlist("alice", "", preferences.Watchlist{Name: "C", Repositories: []string{"acme/c"}}); err != nil {
		t.Fatal(err)
	}
	esc(m)
	m.chooseScope(preferences.Scope{Watchlist: "C"})
	if got := m.categoryCount(6); got != 1 {
		t.Fatalf("awaiting review count = %d, want the watchlist's one", got)
	}
	m.notify = true
	reviews := []github.PullRequest{{Number: 4, Repository: "acme/c"}, {Number: 5, Repository: "acme/a"}}
	_, cmd := m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: reviews}})
	if got := notifications(cmd); len(got) != 0 {
		t.Fatalf("a review request outside the watchlist notified: %q", got)
	}
}
