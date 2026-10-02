package preferences

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestOpenSaveAndReopenDistinguishesMissingFromAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "preferences.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := store.Lookup("Alice"); found {
		t.Fatal("missing account was reported as configured")
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("Open created preference directory: %v", err)
	}
	if err := store.Save("Alice", "acme/repo"); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("Bob", ""); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, found := reopened.Lookup("aLiCe"); !found || got != (Scope{Repository: "acme/repo"}) {
		t.Fatalf("Alice choice = %q, %v", got, found)
	}
	if got, found := reopened.Lookup("BOB"); !found || got != (Scope{Repository: ""}) {
		t.Fatalf("Bob explicit All = %q, %v", got, found)
	}
}

func TestSavePreservesOtherAccounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("Alice", "acme/one"); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("Bob", "acme/two"); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("ALICE", "acme/three"); err != nil {
		t.Fatal(err)
	}
	if got, found := store.Lookup("alice"); !found || got != (Scope{Repository: "acme/three"}) {
		t.Fatalf("Alice choice = %q, %v", got, found)
	}
	if got, found := store.Lookup("bob"); !found || got != (Scope{Repository: "acme/two"}) {
		t.Fatalf("Bob choice = %q, %v", got, found)
	}
}

func TestOpenRejectsInvalidFilesWithoutChangingThem(t *testing.T) {
	for _, contents := range []string{"null", "[]", `{"alice":null}`, `{"alice":3}`, `{"alice":"owner/repo/extra"}`, "{"} {
		t.Run(contents, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "preferences.json")
			if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(path); err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("Open error = %v, want path-specific error", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != contents {
				t.Fatalf("invalid preferences changed to %q", data)
			}
		})
	}
}

func TestFailedSaveDoesNotChangeMemoryOrDisk(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "preferences.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("Alice", "acme/original"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	store.path = filepath.Join(blocked, "preferences.json")
	if err := store.Save("Alice", "acme/new"); err == nil {
		t.Fatal("Save unexpectedly succeeded through a file parent")
	}
	if got, found := store.Lookup("Alice"); !found || got != (Scope{Repository: "acme/original"}) {
		t.Fatalf("failed Save changed memory to %q, %v", got, found)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("failed Save changed old file: %s", after)
	}
}

func TestWatchlistsRoundTripRenameAndDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWatchlist("Alice", "", Watchlist{Name: "My services", Repositories: []string{"acme/web", "acme/API", "Acme/api"}}); err != nil {
		t.Fatal(err)
	}
	// A watchlist alone is not a scope choice.
	if _, found := store.Lookup("alice"); found {
		t.Fatal("saving a watchlist saved a scope")
	}
	if err := store.SaveScope("alice", Scope{Watchlist: "my SERVICES"}); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, found := reopened.Lookup("ALICE"); !found || got != (Scope{Watchlist: "My services"}) {
		t.Fatalf("scope = %+v, %v; want the watchlist", got, found)
	}
	lists := reopened.Watchlists("alice")
	if len(lists) != 1 || lists[0].Name != "My services" || !slices.Equal(lists[0].Repositories, []string{"acme/API", "acme/web"}) {
		t.Fatalf("watchlists = %+v, want one with repeats dropped", lists)
	}

	// Renaming the active watchlist keeps the scope on it.
	if err := reopened.SaveWatchlist("alice", "My services", Watchlist{Name: "Services", Repositories: []string{"acme/web"}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := reopened.Lookup("alice"); got != (Scope{Watchlist: "Services"}) {
		t.Fatalf("scope after rename = %+v", got)
	}
	if lists := reopened.Watchlists("alice"); len(lists) != 1 || lists[0].Name != "Services" {
		t.Fatalf("watchlists after rename = %+v", lists)
	}

	if err := reopened.DeleteWatchlist("alice", "services"); err != nil {
		t.Fatal(err)
	}
	if got, found := reopened.Lookup("alice"); !found || got != (Scope{}) {
		t.Fatalf("scope after deleting its watchlist = %+v, %v; want All", got, found)
	}
	if err := reopened.SaveScope("alice", Scope{Watchlist: "Services"}); err == nil {
		t.Fatal("saved a scope on a deleted watchlist")
	}
}

func TestPlainScopesKeepTheFormatOlderVersionsRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := os.WriteFile(path, []byte(`{"github.com/bob":"acme/old"}`), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("alice", ""); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil || raw["github.com/alice"] != "" || raw["github.com/bob"] != "acme/old" {
		t.Fatalf("preferences = %s, %v; want plain repository strings", data, err)
	}
}

func TestOpenRejectsInvalidWatchlists(t *testing.T) {
	for _, contents := range []string{
		`{"alice":{"watchlists":{"":["acme/a"]}}}`,
		`{"alice":{"watchlists":{" padded":["acme/a"]}}}`,
		`{"alice":{"watchlists":{"Empty":[]}}}`,
		`{"alice":{"watchlists":{"Bad":["not a repo"]}}}`,
		`{"alice":{"watchlists":{"Same":["acme/a"],"same":["acme/b"]}}}`,
		`{"alice":{"repository":"acme/a","watchlist":"Same","watchlists":{"Same":["acme/a"]}}}`,
		`{"alice":{"repository":"owner/repo/extra"}}`,
	} {
		path := filepath.Join(t.TempDir(), "preferences.json")
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(path); err == nil {
			t.Errorf("Open(%s) succeeded, want an error", contents)
		}
	}
}

func TestMissingWatchlistScopePromptsAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := os.WriteFile(path, []byte(`{"github.com/alice":{"watchlist":"Gone","watchlists":{"Kept":["acme/a"]}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := store.Lookup("alice"); found {
		t.Fatal("a scope on a missing watchlist was restored")
	}
	if lists := store.Watchlists("alice"); len(lists) != 1 || lists[0].Name != "Kept" {
		t.Fatalf("watchlists = %+v, want the kept one", lists)
	}
}
