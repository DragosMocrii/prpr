package preferences

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DragosMocrii/prpr/internal/readiness"
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

func TestPinnedAccountIsSavedApartFromAccounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if store.PinnedAccount() != "" {
		t.Fatal("a new store pins an account")
	}
	if err := store.Save("alice", "acme/a"); err != nil {
		t.Fatal(err)
	}
	if err := store.SavePinnedAccount("alice_acme"); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("bob", ""); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.PinnedAccount(); got != "alice_acme" {
		t.Fatalf("pinned account = %q", got)
	}
	if got, found := reopened.Lookup("alice"); !found || got != (Scope{Repository: "acme/a"}) {
		t.Fatalf("pinning changed alice's scope: %+v, %v", got, found)
	}
	if _, found := reopened.Lookup("app"); found {
		t.Fatal("the app settings read as an account")
	}
	if err := reopened.SavePinnedAccount(""); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"app"`) {
		t.Fatalf("following the active account kept the app settings: %s", data)
	}
	if err := reopened.SavePinnedAccount("not a login"); err == nil {
		t.Fatal("saved an invalid login")
	}
	for _, contents := range []string{`{"app":"alice"}`, `{"app":{"account":"bad login"}}`} {
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(path); err == nil {
			t.Errorf("Open(%s) succeeded", contents)
		}
	}
}

func TestIconChoiceIsSavedWithThePinnedAccount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if store.Icons() != "" {
		t.Fatal("a new store has an icon choice")
	}
	if err := store.SavePinnedAccount("alice"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveIcons("nerd"); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("alice", "acme/a"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveIcons("emoji"); err == nil {
		t.Fatal("saved an unknown icon set")
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Icons() != "nerd" || reopened.PinnedAccount() != "alice" {
		t.Fatalf("icons %q, pinned %q after reopening", reopened.Icons(), reopened.PinnedAccount())
	}
	// Unpinning keeps the icon choice.
	if err := reopened.SavePinnedAccount(""); err != nil {
		t.Fatal(err)
	}
	if again, err := Open(path); err != nil || again.Icons() != "nerd" {
		t.Fatalf("unpinning lost the icon choice: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"app":{"icons":"emoji"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Error("opened an unknown icon set")
	}
}

func TestRulesAreSavedWithTheOtherAppSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if store.Rules().Customized() || store.RulesErr() != nil {
		t.Fatal("a new store has rules of its own")
	}
	rules := readiness.DefaultRules()
	rules.Owners = map[string]readiness.Rule{"acme": {MergeButton: true, Approvals: 1, CodeOwners: true}}
	if err := store.SaveIcons("nerd"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRules(rules); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("alice", "acme/a"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if rule, own := reopened.Rules().For("acme/api"); !own || rule != rules.Owners["acme"] || reopened.Icons() != "nerd" {
		t.Fatalf("after reopening: rule %+v %t, icons %q", rule, own, reopened.Icons())
	}
	// Changing the returned rules leaves the store's alone.
	reopened.Rules().Owners["acme"] = readiness.Rule{}
	if rule, _ := reopened.Rules().For("acme/api"); rule != rules.Owners["acme"] {
		t.Fatal("the store's rules changed through a copy")
	}
}

func TestUnreadableRulesKeepTheRestAndSurviveOtherSaves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	written := `{"app":{"icons":"nerd","ready":{"default":{"merge_buton":true}}},"github.com/alice":"acme/a"}`
	if err := os.WriteFile(path, []byte(written), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if store.RulesErr() == nil || store.Rules().Customized() || store.Icons() != "nerd" {
		t.Fatalf("rules error %v, customized %t, icons %q", store.RulesErr(), store.Rules().Customized(), store.Icons())
	}
	if scope, ok := store.Lookup("alice"); !ok || scope.Repository != "acme/a" {
		t.Fatalf("scope = %+v, %t", scope, ok)
	}
	// Other saves write the unreadable rules back as they were.
	if err := store.Save("alice", "acme/b"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "merge_buton") {
		t.Fatalf("a scope save dropped the unreadable rules: %s", data)
	}
	if err := store.SaveRules(readiness.DefaultRules()); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil || reopened.RulesErr() != nil {
		t.Fatalf("after saving rules: %v, %v", err, reopened.RulesErr())
	}
}

func TestLegendAndDraftsAreSavedWithTheOtherAppSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if store.Legend() {
		t.Fatal("a new store has the legend open")
	}
	if err := store.SaveIcons("nerd"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLegend(true); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveShowDrafts(true); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("alice", "acme/a"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Legend() || !reopened.ShowDrafts() || reopened.Icons() != "nerd" {
		t.Fatalf("after reopening: legend %t, icons %q", reopened.Legend(), reopened.Icons())
	}
	if err := reopened.SaveLegend(false); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Legend() || !again.ShowDrafts() || again.Icons() != "nerd" {
		t.Fatalf("after closing: legend %t, icons %q", again.Legend(), again.Icons())
	}
}
