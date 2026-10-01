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

func updateSnapshot(m *model, login string, prs ...github.PullRequest) {
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: login, PullRequests: prs}})
}

func TestFreshAccountPromptsAndPickerCancellationDoesNotChoose(t *testing.T) {
	store := testPreferences(t)
	m := &model{ctx: context.Background(), preferences: store, width: 80, height: 24}
	updateSnapshot(m, "alice", github.PullRequest{Number: 1, Repository: "acme/repo"})
	if m.scopeChosen || len(m.visiblePRs) != 1 || !strings.Contains(strings.Join(m.scopeChoiceLines(), "\n"), "Pick a repository") {
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
	m := &model{ctx: context.Background(), preferences: store, width: 80, height: 24}
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
	if got, found := reopened.Lookup("ALICE"); !found || got != "" {
		t.Fatalf("saved explicit All = %q, %v", got, found)
	}
	next := &model{ctx: context.Background(), preferences: reopened, width: 80, height: 24}
	updateSnapshot(next, "Alice", github.PullRequest{Number: 2, Repository: "acme/repo"})
	if !next.scopeChosen || len(next.visiblePRs) != 1 {
		t.Fatalf("reopened account still prompted or lost rows: chosen %t visible %v", next.scopeChosen, next.visiblePRs)
	}
}

func TestAccountSwitchRestoresSavedScopeOrPrompts(t *testing.T) {
	store := testPreferences(t)
	if err := store.Save("alice", "acme/a"); err != nil {
		t.Fatal(err)
	}
	m := &model{ctx: context.Background(), preferences: store, width: 80, height: 24}
	updateSnapshot(m, "Alice", github.PullRequest{Number: 1, Repository: "acme/a"}, github.PullRequest{Number: 2, Repository: "other/b"})
	if !m.scopeChosen || m.selectedRepository != "acme/a" || len(m.visiblePRs) != 1 {
		t.Fatalf("saved Alice scope not restored: chosen %t repo %q visible %v", m.scopeChosen, m.selectedRepository, m.visiblePRs)
	}
	m.preferenceErr = errors.New("Selection not saved: disk error")
	updateSnapshot(m, "bob", github.PullRequest{Number: 3, Repository: "other/b"})
	if m.scopeChosen || m.selectedRepository != "" || m.preferenceErr != nil {
		t.Fatalf("unconfigured Bob inherited Alice state: chosen %t repo %q warning %v", m.scopeChosen, m.selectedRepository, m.preferenceErr)
	}
	m.chooseRepository("")
	if got, found := store.Lookup("bob"); !found || got != "" {
		t.Fatalf("Bob's explicit All choice was not saved: %q, %v", got, found)
	}
	updateSnapshot(m, "ALICE", github.PullRequest{Number: 4, Repository: "acme/a"})
	if !m.scopeChosen || m.selectedRepository != "acme/a" || m.preferenceErr != nil || len(m.visiblePRs) != 1 {
		t.Fatalf("switching back did not restore Alice state: chosen %t repo %q warning %v visible %v", m.scopeChosen, m.selectedRepository, m.preferenceErr, m.visiblePRs)
	}
}

func TestSameAccountRefreshRetainsChosenEmptyScope(t *testing.T) {
	store := testPreferences(t)
	if err := store.Save("alice", "acme/empty"); err != nil {
		t.Fatal(err)
	}
	m := &model{ctx: context.Background(), preferences: store, width: 80, height: 24}
	updateSnapshot(m, "alice", github.PullRequest{Number: 1, Repository: "acme/empty"})
	m.preferenceErr = errors.New("Selection not saved: transient")
	updateSnapshot(m, "alice", github.PullRequest{Number: 2, Repository: "acme/other"})
	if !m.scopeChosen || m.selectedRepository != "acme/empty" || len(m.visiblePRs) != 0 || m.preferenceErr == nil {
		t.Fatalf("same-account refresh changed choice: chosen %t repo %q visible %v warning %v", m.scopeChosen, m.selectedRepository, m.visiblePRs, m.preferenceErr)
	}
}

func TestClearFilterPersistsAll(t *testing.T) {
	store := testPreferences(t)
	if err := store.Save("alice", "acme/repo"); err != nil {
		t.Fatal(err)
	}
	m := &model{ctx: context.Background(), preferences: store, width: 80, height: 24}
	updateSnapshot(m, "alice", github.PullRequest{Number: 1, Repository: "acme/repo"})
	press(m, tea.Key{Code: 'c', Text: "c"})
	if !m.scopeChosen || m.selectedRepository != "" || len(m.visiblePRs) != 1 {
		t.Fatalf("clear filter state = chosen %t repo %q visible %v", m.scopeChosen, m.selectedRepository, m.visiblePRs)
	}
	if got, found := store.Lookup("alice"); !found || got != "" {
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
	m := &model{ctx: context.Background(), preferences: store, width: 80, height: 24}
	updateSnapshot(m, "alice", github.PullRequest{Number: 1, Repository: "acme/repo"})
	m.chooseRepository("acme/repo")
	if !m.scopeChosen || m.selectedRepository != "acme/repo" || len(m.visiblePRs) != 1 || m.err != nil {
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
	m := &model{ctx: context.Background(), preferences: store, snapshot: github.Snapshot{Login: "alice", PullRequests: []github.PullRequest{{Number: 7}}}, width: 80, height: 24}
	m.filterLogin, m.scopeChosen = "alice", true
	m.startFetch()
	if len(m.snapshot.PullRequests) != 0 || !m.loading {
		t.Fatalf("refresh did not clear old list while loading: %+v", m)
	}
	m.Update(fetchFinishedMsg{err: errors.New("offline")})
	if len(m.snapshot.PullRequests) != 0 || m.snapshot.Login != "" || m.loading || m.err == nil {
		t.Fatalf("failed refresh retained or hid stale state: %+v", m)
	}
	if got, found := store.Lookup("alice"); !found || got != "" {
		t.Fatalf("failed refresh changed saved choice: %q, %v", got, found)
	}
}
