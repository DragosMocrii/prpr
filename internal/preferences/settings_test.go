package preferences

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DragosMocrii/prpr/internal/github"
)

func TestSettingsDefaultWithoutAFile(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "preferences.json"))
	if err != nil {
		t.Fatal(err)
	}
	defaults, _ := github.ParseBots(github.DefaultBots)
	queues, _ := github.ParseQueues(github.DefaultQueues)
	if store.Refresh() != DefaultRefresh || !reflect.DeepEqual(store.Bots(), defaults) ||
		!reflect.DeepEqual(store.Queues(), queues) || store.Notify() || store.Mouse() || !store.Title() ||
		store.BotsText() != github.DefaultBots || store.Editor() != EditorVSCode || store.SettingsErr() != nil ||
		store.FullRefresh() != DefaultFullRefresh {
		t.Fatalf("defaults: refresh %v bots %+v queues %v notify %v mouse %v title %v fullRefresh %v",
			store.Refresh(), store.Bots(), store.Queues(), store.Notify(), store.Mouse(), store.Title(), store.FullRefresh())
	}
}

func TestSettingsRoundTripAndDefaultsAreNotWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, save := range []func() error{
		func() error { return store.SaveRefresh(10 * time.Minute) },
		func() error { return store.SaveBots("") },
		func() error { return store.SaveQueues([]github.Queue{github.QueueTrunk}) },
		func() error { return store.SaveNotify(true) },
		func() error { return store.SaveMouse(true) },
		func() error { return store.SaveTitle(false) },
		func() error { return store.SaveEditor(EditorGitHubDev) },
		func() error { return store.SaveFullRefresh(0) },
	} {
		if err := save(); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Refresh() != 10*time.Minute || len(reopened.Bots()) != 0 || reopened.BotsText() != "" ||
		!reflect.DeepEqual(reopened.Queues(), []github.Queue{github.QueueTrunk}) ||
		!reopened.Notify() || !reopened.Mouse() || reopened.Title() || reopened.Editor() != EditorGitHubDev ||
		reopened.FullRefresh() != 0 {
		t.Fatalf("reopened: refresh %v bots %+v queues %v notify %v mouse %v title %v fullRefresh %v", reopened.Refresh(),
			reopened.Bots(), reopened.Queues(), reopened.Notify(), reopened.Mouse(), reopened.Title(), reopened.FullRefresh())
	}
	// Back to the defaults: the keys leave the file.
	for _, save := range []func() error{
		func() error { return reopened.SaveRefresh(DefaultRefresh) },
		func() error { return reopened.SaveBots(github.DefaultBots) },
		func() error { return reopened.SaveQueues([]github.Queue{github.QueueGitHub, github.QueueTrunk}) },
		func() error { return reopened.SaveNotify(false) },
		func() error { return reopened.SaveMouse(false) },
		func() error { return reopened.SaveTitle(true) },
		func() error { return reopened.SaveEditor(EditorVSCode) },
		func() error { return reopened.SaveFullRefresh(DefaultFullRefresh) },
	} {
		if err := save(); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"refresh"`, `"bots"`, `"queues"`, `"notify"`, `"mouse"`, `"title"`, `"editor"`, `"fullRefresh"`} {
		if strings.Contains(string(data), key) {
			t.Errorf("default %s was written: %s", key, data)
		}
	}
}

func TestUnreadableSettingsFallBackAndStayAsWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	contents := `{"app":{"refresh":"10s","bots":"no-equals","queues":["bors"],"editor":"atom","fullRefresh":"5s","legend":true}}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defaults, _ := github.ParseBots(github.DefaultBots)
	if store.Refresh() != DefaultRefresh || !reflect.DeepEqual(store.Bots(), defaults) || len(store.Queues()) != 2 ||
		store.Editor() != EditorVSCode || store.FullRefresh() != DefaultFullRefresh {
		t.Fatalf("fallbacks: refresh %v bots %+v queues %v editor %q fullRefresh %v", store.Refresh(), store.Bots(), store.Queues(), store.Editor(), store.FullRefresh())
	}
	for _, key := range []string{"refresh", "bots", "queues", "editor", "fullRefresh"} {
		if store.SettingErr(key) == nil {
			t.Errorf("no error for unreadable %s", key)
		}
	}
	if store.SettingsErr() == nil {
		t.Error("SettingsErr is nil")
	}
	// Another save keeps the unreadable values as written.
	if err := store.SaveMouse(true); err != nil {
		t.Fatal(err)
	}
	var saved struct{ App map[string]json.RawMessage }
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	var queues []string
	if string(saved.App["refresh"]) != `"10s"` || string(saved.App["bots"]) != `"no-equals"` || string(saved.App["editor"]) != `"atom"` ||
		json.Unmarshal(saved.App["queues"], &queues) != nil || !reflect.DeepEqual(queues, []string{"bors"}) || string(saved.App["fullRefresh"]) != `"5s"` {
		t.Fatalf("unreadable values not kept: %s", data)
	}
	// Saving a setting replaces its unreadable value and clears its error.
	if err := store.SaveRefresh(time.Minute); err != nil {
		t.Fatal(err)
	}
	if store.SettingErr("refresh") != nil || store.Refresh() != time.Minute {
		t.Fatalf("refresh after save: %v, %v", store.Refresh(), store.SettingErr("refresh"))
	}
}

func TestSaveSettingsRejectsInvalidValues(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "preferences.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRefresh(10 * time.Second); err == nil {
		t.Error("SaveRefresh(10s) succeeded")
	}
	if err := store.SaveBots("no-equals"); err == nil {
		t.Error("SaveBots(invalid) succeeded")
	}
	if err := store.SaveEditor("atom"); err == nil {
		t.Error("SaveEditor(atom) succeeded")
	}
	if err := store.SaveFullRefresh(time.Second); err == nil {
		t.Error("SaveFullRefresh(1s) succeeded")
	}
	if store.Refresh() != DefaultRefresh || store.BotsText() != github.DefaultBots || store.Editor() != EditorVSCode {
		t.Fatal("a rejected save changed the store")
	}
}

func TestFormatRefresh(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "0", 5 * time.Minute: "5m", 90 * time.Second: "1m30s", time.Hour: "60m"} {
		if got := FormatRefresh(d); got != want {
			t.Errorf("FormatRefresh(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestUnreadableWrongTypeFallsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	// Write invalid types: refresh as number, bots as number
	contents := `{"app":{"refresh":300,"bots":5,"queues":["trunk"]}}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Should fall back to defaults
	if store.Refresh() != DefaultRefresh || store.BotsText() != github.DefaultBots {
		t.Fatalf("fallbacks: refresh %v bots %q", store.Refresh(), store.BotsText())
	}
	// Should have errors recorded
	if store.SettingErr("refresh") == nil {
		t.Error("no error for unreadable refresh")
	}
	if store.SettingErr("bots") == nil {
		t.Error("no error for unreadable bots")
	}
	// Save an unrelated setting
	if err := store.SaveMouse(true); err != nil {
		t.Fatal(err)
	}
	// Raw values should be preserved in file
	var saved struct{ App map[string]json.RawMessage }
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	var refreshVal, botsVal interface{}
	json.Unmarshal(saved.App["refresh"], &refreshVal)
	json.Unmarshal(saved.App["bots"], &botsVal)
	if refreshVal != 300.0 || botsVal != 5.0 {
		t.Fatalf("unreadable values not preserved: refresh=%v bots=%v", refreshVal, botsVal)
	}
}

func TestFailedWriteLeaveStoreUnchanged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("test requires non-root")
	}
	path := filepath.Join(t.TempDir(), "preferences.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Set up a successful save first
	if err := store.SaveRefresh(10 * time.Minute); err != nil {
		t.Fatal(err)
	}
	if store.Refresh() != 10*time.Minute {
		t.Fatal("SaveRefresh failed")
	}
	// Make the directory read-only to force write failure
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	// Try to save - should fail
	errSave := store.SaveNotify(true)
	if errSave == nil {
		t.Fatal("SaveNotify should have failed with read-only directory")
	}
	// Store state should be unchanged
	if store.Notify() {
		t.Error("store.Notify() changed after failed save")
	}
	if store.Refresh() != 10*time.Minute {
		t.Errorf("store.Refresh() changed to %v after failed save", store.Refresh())
	}
}

func TestSaveQueuesDuplicatesRejected(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "preferences.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Try to save with duplicate
	err = store.SaveQueues([]github.Queue{github.QueueTrunk, github.QueueTrunk})
	if err == nil {
		t.Error("SaveQueues with duplicates should fail")
	}
	// Store should be unchanged
	defaults, _ := github.ParseQueues(github.DefaultQueues)
	if !reflect.DeepEqual(store.Queues(), defaults) {
		t.Fatal("store.Queues() changed after failed save")
	}
}
