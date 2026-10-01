package preferences

import (
	"os"
	"path/filepath"
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
	if got, found := reopened.Lookup("aLiCe"); !found || got != "acme/repo" {
		t.Fatalf("Alice choice = %q, %v", got, found)
	}
	if got, found := reopened.Lookup("BOB"); !found || got != "" {
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
	if got, found := store.Lookup("alice"); !found || got != "acme/three" {
		t.Fatalf("Alice choice = %q, %v", got, found)
	}
	if got, found := store.Lookup("bob"); !found || got != "acme/two" {
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
	if got, found := store.Lookup("Alice"); !found || got != "acme/original" {
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
