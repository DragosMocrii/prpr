package tui

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/preferences"
)

func pickerModel(t *testing.T) *model {
	t.Helper()
	store, err := preferences.Open(filepath.Join(t.TempDir(), "preferences.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("alice", ""); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 80, 12)
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: []github.PullRequest{
		{Number: 1, Repository: "acme/a", URL: "https://example.test/a/1"},
		{Number: 2, Repository: "acme/b", URL: "https://example.test/b/2"},
	}}})
	return m
}

func press(m *model, key tea.Key) {
	m.Update(tea.KeyPressMsg(key))
}

func openPicker(m *model) {
	press(m, tea.Key{Code: 'p', Text: "p"})
	if m.picker == nil || !m.picker.busy {
		panic("picker did not open repository discovery")
	}
}

func finishDiscovery(m *model, names []string, err error) {
	m.Update(repositoryListFinishedMsg{requestID: m.repositoryRequestID, repositories: names, err: err})
}

func TestPickerSelectsRepositoryAndAllRepositories(t *testing.T) {
	m := pickerModel(t)
	openPicker(m)
	finishDiscovery(m, []string{"acme/a", "acme/b", "acme/empty"}, nil)
	m.picker.setQuery("acme/b")
	if got := m.picker.selectedCandidate().repository; got != "acme/b" {
		t.Fatalf("selected candidate = %q, want acme/b", got)
	}
	press(m, tea.Key{Code: tea.KeyEnter})
	if m.picker != nil || m.selectedRepository != "acme/b" || len(m.panes[paneMine].visible) != 1 {
		t.Fatalf("repository selection did not apply: picker %v filter %q visible %v", m.picker, m.selectedRepository, m.panes[paneMine].visible)
	}
	if got, found := m.preferences.Lookup("alice"); !found || got != "acme/b" {
		t.Fatalf("known repository choice not persisted: %q, %v", got, found)
	}
	openPicker(m)
	finishDiscovery(m, []string{"acme/a", "acme/b"}, nil)
	m.picker.setQuery("no-match")
	if len(m.picker.candidates) != 1 || m.picker.selectedCandidate().kind != allRepositoriesCandidate {
		t.Fatalf("no-match candidates = %+v, want All repositories", m.picker.candidates)
	}
	press(m, tea.Key{Code: tea.KeyEnter})
	if m.selectedRepository != "" || len(m.panes[paneMine].visible) != 2 {
		t.Fatalf("All repositories selection = %q with %v visible", m.selectedRepository, m.panes[paneMine].visible)
	}
	if got, found := m.preferences.Lookup("alice"); !found || got != "" {
		t.Fatalf("All choice not persisted: %q, %v", got, found)
	}
}

func TestPickerCancelPreservesFilterAndPRSelection(t *testing.T) {
	m := pickerModel(t)
	m.snapshot.PullRequests[1].Repository = "acme/a"
	m.rebuildVisiblePRs()
	m.applyRepository("acme/a")
	press(m, tea.Key{Code: tea.KeyDown})
	previousCursor := m.panes[paneMine].table.Cursor()
	openPicker(m)
	press(m, tea.Key{Code: tea.KeyEsc})
	if m.picker != nil || m.selectedRepository != "acme/a" || m.panes[paneMine].table.Cursor() != previousCursor || m.panes[paneMine].table.Cursor() != 1 {
		t.Fatalf("cancel changed list state: picker %v filter %q cursor %d", m.picker, m.selectedRepository, m.panes[paneMine].table.Cursor())
	}
}

func TestPickerTextInputDoesNotInvokeMainKeys(t *testing.T) {
	m := pickerModel(t)
	openPicker(m)
	for _, text := range []string{"q", "j", "r"} {
		press(m, tea.Key{Code: rune(text[0]), Text: text})
	}
	if m.picker.query() != "qjr" || m.loading || m.snapshot.Login != "alice" {
		t.Fatalf("picker input changed main state or lost text: query %q loading %t", m.picker.query(), m.loading)
	}
	press(m, tea.Key{Code: tea.KeyBackspace})
	if m.picker.query() != "qj" {
		t.Fatalf("backspace query = %q, want qj", m.picker.query())
	}
	m.Update(tea.PasteMsg{Content: "\nowner/repo\x1b"})
	if m.picker.query() != "qjowner/repo" {
		t.Fatalf("paste query = %q, want control characters discarded", m.picker.query())
	}
	press(m, tea.Key{Code: 'u', Mod: tea.ModCtrl})
	if m.picker.query() != "" || m.picker == nil {
		t.Fatalf("Ctrl+U state: picker %v query %q", m.picker, m.picker.query())
	}
}

func TestPickerDirectLookupFailureThenCanonicalSuccess(t *testing.T) {
	m := pickerModel(t)
	m.applyRepository("acme/b")
	openPicker(m)
	finishDiscovery(m, nil, errors.New("listing denied"))
	m.picker.setQuery("public/other")
	press(m, tea.Key{Code: tea.KeyEnter})
	failedID := m.repositoryRequestID
	m.Update(repositoryLookupFinishedMsg{requestID: failedID, err: errors.New("GitHub repository lookup failed: not found")})
	if m.picker == nil || m.selectedRepository != "acme/b" || m.picker.busy || m.picker.diagnostic == "" {
		t.Fatalf("failed lookup changed filter or lost diagnostic: picker %+v filter %q", m.picker, m.selectedRepository)
	}
	if got, found := m.preferences.Lookup("alice"); !found || got != "" {
		t.Fatalf("failed lookup changed persisted choice: %q, %v", got, found)
	}
	press(m, tea.Key{Code: tea.KeyEnter})
	m.Update(repositoryLookupFinishedMsg{requestID: m.repositoryRequestID, repository: "Public/Other"})
	if m.picker != nil || m.selectedRepository != "Public/Other" || len(m.panes[paneMine].visible) != 0 {
		t.Fatalf("canonical lookup result not applied: picker %v filter %q visible %v", m.picker, m.selectedRepository, m.panes[paneMine].visible)
	}
	if got, found := m.preferences.Lookup("alice"); !found || got != "Public/Other" {
		t.Fatalf("canonical lookup choice not saved: %q, %v", got, found)
	}
}

func TestPickerIgnoresLateResultsAfterCloseAndReopen(t *testing.T) {
	m := pickerModel(t)
	openPicker(m)
	oldID := m.repositoryRequestID
	press(m, tea.Key{Code: tea.KeyEsc})
	openPicker(m)
	m.Update(repositoryListFinishedMsg{requestID: oldID, repositories: []string{"late/repo"}})
	finishDiscovery(m, nil, nil)
	if len(m.picker.repositories) != 2 || m.picker.repositories[0] != "acme/a" || m.picker.repositories[1] != "acme/b" {
		t.Fatalf("stale discovery populated reopened picker: %v", m.picker.repositories)
	}
	currentID := m.repositoryRequestID
	m.picker.setQuery("public/other")
	press(m, tea.Key{Code: tea.KeyEnter})
	lookupID := m.repositoryRequestID
	press(m, tea.Key{Code: tea.KeyEsc})
	openPicker(m)
	m.Update(repositoryLookupFinishedMsg{requestID: lookupID, repository: "Public/Other"})
	if m.selectedRepository != "" || m.repositoryRequestID == currentID || m.picker == nil {
		t.Fatalf("stale lookup affected reopened picker: filter %q id %d picker %v", m.selectedRepository, m.repositoryRequestID, m.picker)
	}
	if got, found := m.preferences.Lookup("alice"); !found || got != "" {
		t.Fatalf("stale lookup changed persisted choice: %q, %v", got, found)
	}
}

func TestPickerNavigationAndResizeStayInBounds(t *testing.T) {
	m := pickerModel(t)
	openPicker(m)
	finishDiscovery(m, []string{"org/a", "org/b", "org/c", "org/d", "org/e", "org/f"}, nil)
	for i := 0; i < 20; i++ {
		press(m, tea.Key{Code: tea.KeyDown})
	}
	if m.picker.cursor >= len(m.picker.candidates) || m.picker.offset+m.pickerViewportHeight() <= m.picker.cursor {
		t.Fatalf("picker selection not visible: cursor %d offset %d height %d", m.picker.cursor, m.picker.offset, m.pickerViewportHeight())
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	if m.picker.cursor >= len(m.picker.candidates) || m.picker.offset < 0 {
		t.Fatalf("small resize left invalid selection: %+v", m.picker)
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.picker.cursor >= len(m.picker.candidates) || m.picker.offset < 0 {
		t.Fatalf("large resize left invalid selection: %+v", m.picker)
	}
}

func TestPickerCursorMovementKeepsCandidateSelection(t *testing.T) {
	m := pickerModel(t)
	openPicker(m)
	finishDiscovery(m, []string{"acme/a", "acme/b"}, nil)
	m.picker.setQuery("acme")
	press(m, tea.Key{Code: tea.KeyDown})
	want := m.picker.selectedCandidate()
	press(m, tea.Key{Code: tea.KeyLeft})
	press(m, tea.Key{Code: tea.KeyLeft})
	press(m, tea.Key{Code: '/', Text: "/"})
	if m.picker.query() != "ac/me" {
		t.Fatalf("mid-query insert = %q, want ac/me", m.picker.query())
	}
	m.picker.setQuery("acme")
	press(m, tea.Key{Code: tea.KeyDown})
	press(m, tea.Key{Code: tea.KeyHome})
	if got := m.picker.selectedCandidate(); got != want {
		t.Fatalf("cursor movement changed selection: %+v, want %+v", got, want)
	}
}

func TestPickerLookupBlocksTypingUntilFailure(t *testing.T) {
	m := pickerModel(t)
	openPicker(m)
	finishDiscovery(m, nil, nil)
	m.picker.setQuery("public/other")
	press(m, tea.Key{Code: tea.KeyEnter})
	if !m.picker.lookup {
		t.Fatal("lookup did not start")
	}
	press(m, tea.Key{Code: 'x', Text: "x"})
	m.Update(tea.PasteMsg{Content: "y"})
	if m.picker.query() != "public/other" {
		t.Fatalf("input changed during lookup: %q", m.picker.query())
	}
	m.Update(repositoryLookupFinishedMsg{requestID: m.repositoryRequestID, err: errors.New("not found")})
	press(m, tea.Key{Code: 'x', Text: "x"})
	if m.picker.query() != "public/otherx" {
		t.Fatalf("input not editable after failed lookup: %q", m.picker.query())
	}
}

func TestPickerOffersReviewRequestRepositories(t *testing.T) {
	m := newPaneModel(t, 100, 24, []github.PullRequest{{Number: 1, Repository: "acme/a"}}, []github.PullRequest{{Number: 2, Repository: "other/z", Author: "bob"}})
	openPicker(m)
	if !slices.Contains(m.picker.repositories, "other/z") || !slices.Contains(m.picker.repositories, "acme/a") {
		t.Fatalf("picker repositories = %v", m.picker.repositories)
	}
}
