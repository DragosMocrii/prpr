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
		store.BotsText() != github.DefaultBots || store.SettingsErr() != nil {
		t.Fatalf("defaults: refresh %v bots %+v queues %v notify %v mouse %v title %v",
			store.Refresh(), store.Bots(), store.Queues(), store.Notify(), store.Mouse(), store.Title())
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
		!reopened.Notify() || !reopened.Mouse() || reopened.Title() {
		t.Fatalf("reopened: refresh %v bots %+v queues %v notify %v mouse %v title %v", reopened.Refresh(),
			reopened.Bots(), reopened.Queues(), reopened.Notify(), reopened.Mouse(), reopened.Title())
	}
	// Back to the defaults: the keys leave the file.
	for _, save := range []func() error{
		func() error { return reopened.SaveRefresh(DefaultRefresh) },
		func() error { return reopened.SaveBots(github.DefaultBots) },
		func() error { return reopened.SaveQueues([]github.Queue{github.QueueGitHub, github.QueueTrunk}) },
		func() error { return reopened.SaveNotify(false) },
		func() error { return reopened.SaveMouse(false) },
		func() error { return reopened.SaveTitle(true) },
	} {
		if err := save(); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"refresh"`, `"bots"`, `"queues"`, `"notify"`, `"mouse"`, `"title"`} {
		if strings.Contains(string(data), key) {
			t.Errorf("default %s was written: %s", key, data)
		}
	}
}

func TestUnreadableSettingsFallBackAndStayAsWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	contents := `{"app":{"refresh":"10s","bots":"no-equals","queues":["bors"],"legend":true}}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defaults, _ := github.ParseBots(github.DefaultBots)
	if store.Refresh() != DefaultRefresh || !reflect.DeepEqual(store.Bots(), defaults) || len(store.Queues()) != 2 {
		t.Fatalf("fallbacks: refresh %v bots %+v queues %v", store.Refresh(), store.Bots(), store.Queues())
	}
	for _, key := range []string{"refresh", "bots", "queues"} {
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
	if string(saved.App["refresh"]) != `"10s"` || string(saved.App["bots"]) != `"no-equals"` ||
		json.Unmarshal(saved.App["queues"], &queues) != nil || !reflect.DeepEqual(queues, []string{"bors"}) {
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
	if store.Refresh() != DefaultRefresh || store.BotsText() != github.DefaultBots {
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
