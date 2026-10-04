package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/preferences"
)

func testPreferences(t *testing.T) *preferences.Store {
	t.Helper()
	store, err := preferences.Open(filepath.Join(t.TempDir(), "preferences.json"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func testModel(store *preferences.Store, width, height int) *model {
	m := newModel(context.Background(), nil, store)
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return m
}

func (m *model) applyRepository(repository string) {
	m.applyScope(preferences.Scope{Repository: repository})
}

func updateFetch(m *model, msg fetchFinishedMsg) (tea.Model, tea.Cmd) {
	msg.generation = m.refreshGeneration
	msg.account = m.accountGeneration
	return m.Update(msg)
}

func updateSnapshot(m *model, login string, prs ...github.PullRequest) {
	updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: login, PullRequests: prs}})
}

func TestFreshAccountPromptsAndPickerCancellationDoesNotChoose(t *testing.T) {
	store := testPreferences(t)
	m := testModel(store, 80, 24)
	updateSnapshot(m, "alice", github.PullRequest{Number: 1, Repository: "acme/repo"})
	if m.scopeChosen || len(m.panes[paneMine].visible) != 1 || !strings.Contains(strings.Join(m.scopeChoiceLines(), "\n"), "Pick a repository") {
		t.Fatalf("fresh account did not show the uncommitted choice: %+v", m)
	}
	press(m, tea.Key{Code: tea.KeyEnter})
	if m.picker == nil {
		t.Fatal("default Pick choice did not open the picker")
	}
	press(m, tea.Key{Code: tea.KeyEsc})
	if m.scopeChosen || m.picker != nil {
		t.Fatalf("cancelling picker committed scope: chosen %t picker %v", m.scopeChosen, m.picker)
	}
	if _, found := store.Lookup("alice"); found {
		t.Fatal("cancelling picker persisted a choice")
	}
}

func TestExplicitAllPersistsAndSuppressesPromptAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	store, err := preferences.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 80, 24)
	updateSnapshot(m, "alice", github.PullRequest{Number: 1, Repository: "acme/repo"})
	press(m, tea.Key{Code: 'j', Text: "j"})
	press(m, tea.Key{Code: tea.KeyEnter})
	if !m.scopeChosen || m.selectedRepository != "" {
		t.Fatalf("All selection = chosen %t repo %q", m.scopeChosen, m.selectedRepository)
	}
	reopened, err := preferences.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, found := reopened.Lookup("ALICE"); !found || got != (preferences.Scope{Repository: ""}) {
		t.Fatalf("saved explicit All = %q, %v", got, found)
	}
	next := testModel(reopened, 80, 24)
	updateSnapshot(next, "Alice", github.PullRequest{Number: 2, Repository: "acme/repo"})
	if !next.scopeChosen || len(next.panes[paneMine].visible) != 1 {
		t.Fatalf("reopened account still prompted or lost rows: chosen %t visible %v", next.scopeChosen, next.panes[paneMine].visible)
	}
}

func TestAccountSwitchRestoresSavedScopeOrPrompts(t *testing.T) {
	store := testPreferences(t)
	if err := store.Save("alice", "acme/a"); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 80, 24)
	updateSnapshot(m, "Alice", github.PullRequest{Number: 1, Repository: "acme/a"}, github.PullRequest{Number: 2, Repository: "other/b"})
	if !m.scopeChosen || m.selectedRepository != "acme/a" || len(m.panes[paneMine].visible) != 1 {
		t.Fatalf("saved Alice scope not restored: chosen %t repo %q visible %v", m.scopeChosen, m.selectedRepository, m.panes[paneMine].visible)
	}
	m.preferenceErr = errors.New("Selection not saved: disk error")
	updateSnapshot(m, "bob", github.PullRequest{Number: 3, Repository: "other/b"})
	if m.scopeChosen || m.selectedRepository != "" || m.preferenceErr != nil {
		t.Fatalf("unconfigured Bob inherited Alice state: chosen %t repo %q warning %v", m.scopeChosen, m.selectedRepository, m.preferenceErr)
	}
	m.chooseRepository("")
	if got, found := store.Lookup("bob"); !found || got != (preferences.Scope{Repository: ""}) {
		t.Fatalf("Bob's explicit All choice was not saved: %q, %v", got, found)
	}
	updateSnapshot(m, "ALICE", github.PullRequest{Number: 4, Repository: "acme/a"})
	if !m.scopeChosen || m.selectedRepository != "acme/a" || m.preferenceErr != nil || len(m.panes[paneMine].visible) != 1 {
		t.Fatalf("switching back did not restore Alice state: chosen %t repo %q warning %v visible %v", m.scopeChosen, m.selectedRepository, m.preferenceErr, m.panes[paneMine].visible)
	}
}

func TestSameAccountRefreshRetainsChosenEmptyScope(t *testing.T) {
	store := testPreferences(t)
	if err := store.Save("alice", "acme/empty"); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 80, 24)
	updateSnapshot(m, "alice", github.PullRequest{Number: 1, Repository: "acme/empty"})
	m.preferenceErr = errors.New("Selection not saved: transient")
	updateSnapshot(m, "alice", github.PullRequest{Number: 2, Repository: "acme/other"})
	if !m.scopeChosen || m.selectedRepository != "acme/empty" || len(m.panes[paneMine].visible) != 0 || m.preferenceErr == nil {
		t.Fatalf("same-account refresh changed choice: chosen %t repo %q visible %v warning %v", m.scopeChosen, m.selectedRepository, m.panes[paneMine].visible, m.preferenceErr)
	}
}

func TestClearFilterPersistsAll(t *testing.T) {
	store := testPreferences(t)
	if err := store.Save("alice", "acme/repo"); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 80, 24)
	updateSnapshot(m, "alice", github.PullRequest{Number: 1, Repository: "acme/repo"})
	press(m, tea.Key{Code: 'c', Text: "c"})
	if !m.scopeChosen || m.selectedRepository != "" || len(m.panes[paneMine].visible) != 1 {
		t.Fatalf("clear filter state = chosen %t repo %q visible %v", m.scopeChosen, m.selectedRepository, m.panes[paneMine].visible)
	}
	if got, found := store.Lookup("alice"); !found || got != (preferences.Scope{Repository: ""}) {
		t.Fatalf("clear filter was not persisted: %q, %v", got, found)
	}
}

func TestPreferenceSaveFailureKeepsSessionChoiceUsable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	store, err := preferences.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 80, 24)
	updateSnapshot(m, "alice", github.PullRequest{Number: 1, Repository: "acme/repo"})
	m.chooseRepository("acme/repo")
	if !m.scopeChosen || m.selectedRepository != "acme/repo" || len(m.panes[paneMine].visible) != 1 || m.err != nil {
		t.Fatalf("failed save made session selection unusable: %+v", m)
	}
	if m.preferenceErr == nil || !strings.Contains(strings.Join(m.listLines(), "\n"), "Selection not saved:") {
		t.Fatalf("failed save warning missing from list: %v", m.preferenceErr)
	}
	if _, found := store.Lookup("alice"); found {
		t.Fatal("failed save mutated the stored choice")
	}
}

func TestFailedRefreshDoesNotExposeStaleRows(t *testing.T) {
	store := testPreferences(t)
	if err := store.Save("alice", ""); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 80, 24)
	updateSnapshot(m, "alice", github.PullRequest{Number: 7, Repository: "acme/a"})
	m.startFetch()
	if !m.loading || !m.refreshing() || len(m.panes[paneMine].visible) != 1 {
		t.Fatalf("refresh did not keep rows under an indicator: loading %t refreshing %t visible %v", m.loading, m.refreshing(), m.panes[paneMine].visible)
	}
	updateFetch(m, fetchFinishedMsg{err: errors.New("offline")})
	if len(m.snapshot.PullRequests) != 0 || m.snapshot.Login != "" || m.loading || m.err == nil {
		t.Fatalf("failed refresh retained or hid stale state: %+v", m)
	}
	if got, found := store.Lookup("alice"); !found || got != (preferences.Scope{Repository: ""}) {
		t.Fatalf("failed refresh changed saved choice: %q, %v", got, found)
	}
}

func TestSpinnerTicksOnlyWhileRequestsAreInFlight(t *testing.T) {
	m := testModel(testPreferences(t), 80, 24)
	m.startFetch()
	if _, cmd := m.Update(m.spinner.Tick()); cmd == nil {
		t.Fatal("spinner stopped while loading")
	}
	updateSnapshot(m, "alice")
	if _, cmd := m.Update(m.spinner.Tick()); cmd != nil {
		t.Fatal("spinner kept ticking after loading finished")
	}
}

func TestDisabledKeysDoNothing(t *testing.T) {
	m := testModel(testPreferences(t), 80, 24)
	updateSnapshot(m, "alice", github.PullRequest{Number: 1, Repository: "acme/repo"})
	for _, text := range []string{"p", "c", "l", "?"} {
		press(m, tea.Key{Code: rune(text[0]), Text: text})
	}
	if m.scopeChosen || m.picker != nil || m.loading || m.helpOpen {
		t.Fatalf("scope choice accepted list keys: chosen %t picker %v loading %t help %t", m.scopeChosen, m.picker, m.loading, m.helpOpen)
	}
}

func TestRefreshKeepsRowsNavigableAndRestoresSelection(t *testing.T) {
	store := testPreferences(t)
	if err := store.Save("alice", ""); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 80, 24)
	prs := []github.PullRequest{
		{Number: 1, Repository: "acme/a"},
		{Number: 1, Repository: "acme/b"},
		{Number: 2, Repository: "acme/a"},
	}
	updateSnapshot(m, "alice", prs...)
	press(m, tea.Key{Code: 'r', Text: "r"})
	if !m.refreshing() {
		t.Fatal("r did not start a refresh")
	}
	press(m, tea.Key{Code: 'j', Text: "j"})
	press(m, tea.Key{Code: 'p', Text: "p"})
	press(m, tea.Key{Code: 'c', Text: "c"})
	press(m, tea.Key{Code: 'r', Text: "r"})
	if m.panes[paneMine].table.Cursor() != 1 || m.picker != nil {
		t.Fatalf("refresh keys: cursor %d picker %v", m.panes[paneMine].table.Cursor(), m.picker)
	}
	// The selected acme/b #1 moves to the top; selection follows it, not the row index.
	updateSnapshot(m, "alice", prs[1], prs[0], prs[2])
	if selected, ok := m.selectedPR(); !ok || selected.Repository != "acme/b" || selected.Number != 1 || m.refreshing() {
		t.Fatalf("selection after refresh = %+v, %v (refreshing %t)", selected, ok, m.refreshing())
	}
	press(m, tea.Key{Code: 'r', Text: "r"})
	// The closed PR stays selected as a gone row.
	updateSnapshot(m, "alice", prs[0], prs[2])
	if selected, ok := m.selectedPR(); !ok || selected.Repository != "acme/b" || selected.Number != 1 || m.panes[paneMine].table.Cursor() != 2 {
		t.Fatalf("selection after selected PR closed = %+v, %v (cursor %d)", selected, ok, m.panes[paneMine].table.Cursor())
	}
}
