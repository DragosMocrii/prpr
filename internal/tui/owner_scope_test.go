package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/preferences"
)

func ownerSnapshot() github.Snapshot {
	return github.Snapshot{Login: "alice", PullRequests: []github.PullRequest{
		{Number: 1, Repository: "acme/a", URL: "https://example.test/a/1"},
		{Number: 2, Repository: "Acme/b", URL: "https://example.test/b/2"},
		{Number: 3, Repository: "other/c", URL: "https://example.test/c/3"},
		{Number: 4, Repository: "acme-labs/d", URL: "https://example.test/d/4"},
	}, ReviewRequests: []github.PullRequest{
		{Number: 5, Repository: "acme/e", URL: "https://example.test/e/5"},
		{Number: 6, Repository: "other/f", URL: "https://example.test/f/6"},
	}}
}

func TestPickerOffersEachOwnerAndChoosingOneScopesEveryPane(t *testing.T) {
	m := pickerModel(t)
	m.Update(fetchFinishedMsg{snapshot: ownerSnapshot()})
	openPicker(m)
	finishDiscovery(m, []string{"acme/a", "acme/b", "acme/z", "acme-labs/d", "other/c"}, nil)
	var owners []string
	for _, candidate := range m.picker.candidates {
		if candidate.kind == ownerCandidate {
			owners = append(owners, candidate.owner)
		}
	}
	if !slices.Equal(owners, []string{"acme", "acme-labs", "other"}) {
		t.Fatalf("owner candidates = %q", owners)
	}
	m.picker.setQuery("acme/")
	if got := m.picker.selectedCandidate(); got.kind != ownerCandidate || got.owner != "acme" {
		t.Fatalf("typing acme/ selected %+v, want the acme owner", got)
	}
	enter(m)
	if m.picker != nil {
		t.Fatal("enter on an owner did not close the picker")
	}
	if got := visibleRepositories(m, paneMine); !slices.Equal(got, []string{"acme/a", "Acme/b"}) {
		t.Fatalf("My PRs in acme/* = %q", got)
	}
	if got := visibleRepositories(m, paneReview); !slices.Equal(got, []string{"acme/e"}) {
		t.Fatalf("Review requested in acme/* = %q", got)
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "acme/*") {
		t.Fatalf("title does not name the owner scope:\n%s", view)
	}
	if scope, found := m.preferences.Lookup("alice"); !found || scope != (preferences.Scope{Owner: "acme"}) {
		t.Fatalf("saved scope = %+v, %v", scope, found)
	}
}

func TestSavedOwnerScopeIsRestoredWithoutWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	contents := []byte(`{"github.com/alice":{"owner":"acme"}}`)
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := preferences.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 80, 20)
	m.Update(fetchFinishedMsg{snapshot: ownerSnapshot()})
	if !m.scopeChosen || m.owner != "acme" {
		t.Fatalf("restored scope chosen %v owner %q", m.scopeChosen, m.owner)
	}
	if got := visibleRepositories(m, paneMine); !slices.Equal(got, []string{"acme/a", "Acme/b"}) {
		t.Fatalf("restored My PRs = %q", got)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != string(contents) {
		t.Fatalf("restoring wrote preferences: %s, %v", data, err)
	}
}
