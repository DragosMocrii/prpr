package preferences

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/DragosMocrii/prpr/internal/github"
)

const (
	// DefaultRefresh is how long after a fetch the lists refresh.
	DefaultRefresh = 5 * time.Minute
	// MinRefresh keeps automatic refreshes from polling GitHub too often.
	MinRefresh = 30 * time.Second
)

// Editor names, saved as the app key's editor.
const (
	EditorVSCode         = "vscode"
	EditorVSCodeInsiders = "vscode-insiders"
	EditorGitHubDev      = "github.dev"
)

// Editors are the editors e can open a pull request in, the default first.
var Editors = []string{EditorVSCode, EditorVSCodeInsiders, EditorGitHubDev}

// settings are the app settings that were flags: each value, and for
// refresh, bots, and queues the value as written, kept while it cannot be
// read so a save of another setting does not lose it; editor is like them.
type settings struct {
	refresh    time.Duration
	refreshRaw json.RawMessage
	bots       []github.Bot
	botsRaw    json.RawMessage
	queues     []github.Queue
	queuesRaw  json.RawMessage
	editor     string
	editorRaw  json.RawMessage
	notify     bool
	mouse      bool
	title      bool
	// errs say why a value was not read, by key.
	errs map[string]error
}

func defaultSettings() settings {
	bots, _ := github.ParseBots(github.DefaultBots)
	queues, _ := github.ParseQueues(github.DefaultQueues)
	return settings{refresh: DefaultRefresh, bots: bots, queues: queues, editor: EditorVSCode, title: true, errs: map[string]error{}}
}

// decodeSettings reads the settings from the app object; a value that
// cannot be read leaves its default and an error.
func decodeSettings(app appJSON, path string) settings {
	s := defaultSettings()
	s.notify, s.mouse = app.Notify, app.Mouse
	if app.Title != nil {
		s.title = *app.Title
	}
	// Refresh: must be a JSON string, or nothing (not 300, not null, not an array)
	if len(app.Refresh) != 0 && string(app.Refresh) != "null" {
		s.refreshRaw = app.Refresh
		var refreshStr string
		if err := json.Unmarshal(app.Refresh, &refreshStr); err != nil {
			s.errs["refresh"] = fmt.Errorf("refresh in %q not read, using %s: %w", path, FormatRefresh(DefaultRefresh), err)
		} else if d, err := parseRefresh(refreshStr); err != nil {
			s.errs["refresh"] = fmt.Errorf("refresh %q in %q not read, using %s: %w", refreshStr, path, FormatRefresh(DefaultRefresh), err)
		} else {
			s.refresh = d
		}
	}
	// Bots: must be a JSON string, or nothing (not 5, not null, not an array)
	if len(app.Bots) != 0 && string(app.Bots) != "null" {
		s.botsRaw = app.Bots
		var botsStr string
		if err := json.Unmarshal(app.Bots, &botsStr); err != nil {
			s.errs["bots"] = fmt.Errorf("review bots in %q not read, using the defaults: %w", path, err)
		} else if bots, err := github.ParseBots(botsStr); err != nil {
			s.errs["bots"] = fmt.Errorf("review bots in %q not read, using the defaults: %w", path, err)
		} else {
			s.bots = bots
		}
	}
	// Queues: must be a JSON array, or nothing (not null, not a string)
	if len(app.Queues) != 0 && string(app.Queues) != "null" {
		s.queuesRaw = app.Queues
		var names []string
		if err := json.Unmarshal(app.Queues, &names); err != nil {
			s.errs["queues"] = fmt.Errorf("merge queues in %q not read, using both: %w", path, err)
		} else if queues, err := github.ParseQueues(strings.Join(names, ",")); err != nil {
			s.errs["queues"] = fmt.Errorf("merge queues in %q not read, using both: %w", path, err)
		} else {
			s.queues = queues
		}
	}
	// Editor: must be a JSON string naming one of Editors, or nothing
	if len(app.Editor) != 0 && string(app.Editor) != "null" {
		s.editorRaw = app.Editor
		var name string
		if err := json.Unmarshal(app.Editor, &name); err != nil {
			s.errs["editor"] = fmt.Errorf("editor in %q not read, using VS Code: %w", path, err)
		} else if !slices.Contains(Editors, name) {
			s.errs["editor"] = fmt.Errorf("editor %q in %q not read, using VS Code: unknown editor", name, path)
		} else {
			s.editor = name
		}
	}
	return s
}

// encode writes the settings that differ from their defaults, and the
// unreadable values as they were written.
func (s settings) encode(app *appJSON) {
	app.Refresh = s.refreshRaw
	app.Bots = s.botsRaw
	app.Queues = s.queuesRaw
	app.Editor = s.editorRaw
	app.Notify, app.Mouse = s.notify, s.mouse
	app.Title = nil
	if !s.title {
		off := false
		app.Title = &off
	}
}

// parseRefresh reads a saved refresh: "0", or a duration of at least
// MinRefresh.
func parseRefresh(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if d < 0 || (d > 0 && d < MinRefresh) {
		return 0, fmt.Errorf("must be 0 (off) or at least %s", FormatRefresh(MinRefresh))
	}
	return d, nil
}

// FormatRefresh writes a refresh interval: "0" for off, whole minutes as
// "5m", else Go's duration.
func FormatRefresh(d time.Duration) string {
	switch {
	case d == 0:
		return "0"
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	return d.String()
}

// saveSettings saves edited settings, keeping the store unchanged when the
// write fails.
func (s *Store) saveSettings(edit func(*settings)) error {
	next := s.settings
	next.errs = make(map[string]error, len(s.settings.errs))
	for k, v := range s.settings.errs {
		next.errs[k] = v
	}
	edit(&next)
	if err := s.saveApp(func(app *appJSON) { next.encode(app) }); err != nil {
		return err
	}
	s.settings = next
	return nil
}

// Refresh is how long after a fetch the lists refresh; zero is off.
func (s *Store) Refresh() time.Duration { return s.settings.refresh }

// SaveRefresh saves the refresh interval: 0, or at least MinRefresh.
func (s *Store) SaveRefresh(d time.Duration) error {
	if d < 0 || (d > 0 && d < MinRefresh) {
		return fmt.Errorf("refresh must be 0 (off) or at least %s", FormatRefresh(MinRefresh))
	}
	return s.saveSettings(func(c *settings) {
		c.refresh = d
		c.refreshRaw = nil
		if d != DefaultRefresh {
			// Encode as JSON string
			c.refreshRaw, _ = json.Marshal(FormatRefresh(d))
		}
		delete(c.errs, "refresh")
	})
}

// Bots are the review bots fetches judge.
func (s *Store) Bots() []github.Bot { return slices.Clone(s.settings.bots) }

// BotsText is the bots as ParseBots reads them.
func (s *Store) BotsText() string { return github.FormatBots(s.settings.bots) }

// SaveBots saves bots written as ParseBots reads them; "" is none.
func (s *Store) SaveBots(text string) error {
	bots, err := github.ParseBots(text)
	if err != nil {
		return err
	}
	return s.saveSettings(func(c *settings) {
		c.bots = bots
		c.botsRaw = nil
		formattedBots := github.FormatBots(bots)
		if formattedBots != github.FormatBots(defaultSettings().bots) {
			// Encode as JSON string
			c.botsRaw, _ = json.Marshal(formattedBots)
		}
		delete(c.errs, "bots")
	})
}

// Queues are the merge queues fetches read.
func (s *Store) Queues() []github.Queue { return slices.Clone(s.settings.queues) }

// SaveQueues saves the merge queues; none turns the Merge queue pane off.
func (s *Store) SaveQueues(queues []github.Queue) error {
	// Check for duplicates
	seen := make(map[github.Queue]bool)
	for _, q := range queues {
		if seen[q] {
			return fmt.Errorf("duplicate merge queue %q", q)
		}
		seen[q] = true
	}
	for _, q := range queues {
		if q != github.QueueTrunk && q != github.QueueGitHub {
			return fmt.Errorf("unknown merge queue %q", q)
		}
	}
	return s.saveSettings(func(c *settings) {
		c.queues, c.queuesRaw = slices.Clone(queues), nil
		both := len(queues) == 2 && slices.Contains(queues, github.QueueTrunk) && slices.Contains(queues, github.QueueGitHub)
		if !both {
			names := make([]string, len(queues))
			for i, q := range queues {
				names[i] = string(q)
			}
			c.queuesRaw, _ = json.Marshal(names)
		}
		delete(c.errs, "queues")
	})
}

// Editor is the editor e opens a pull request in, one of Editors.
func (s *Store) Editor() string { return s.settings.editor }

// SaveEditor saves the editor e opens a pull request in.
func (s *Store) SaveEditor(name string) error {
	if !slices.Contains(Editors, name) {
		return fmt.Errorf("unknown editor %q", name)
	}
	return s.saveSettings(func(c *settings) {
		c.editor, c.editorRaw = name, nil
		if name != EditorVSCode {
			c.editorRaw, _ = json.Marshal(name)
		}
		delete(c.errs, "editor")
	})
}

// Notify is whether desktop notifications are on.
func (s *Store) Notify() bool { return s.settings.notify }

// SaveNotify saves whether desktop notifications are on.
func (s *Store) SaveNotify(on bool) error {
	return s.saveSettings(func(c *settings) { c.notify = on })
}

// Mouse is whether mouse mode is on.
func (s *Store) Mouse() bool { return s.settings.mouse }

// SaveMouse saves whether mouse mode is on.
func (s *Store) SaveMouse(on bool) error {
	return s.saveSettings(func(c *settings) { c.mouse = on })
}

// Title is whether prpr sets the terminal title.
func (s *Store) Title() bool { return s.settings.title }

// SaveTitle saves whether prpr sets the terminal title.
func (s *Store) SaveTitle(on bool) error {
	return s.saveSettings(func(c *settings) { c.title = on })
}

// SettingErr says why the saved value of key ("refresh", "bots", "queues",
// or "editor") was not read; nil when it was.
func (s *Store) SettingErr(key string) error { return s.settings.errs[key] }

// SettingsErr joins every unreadable setting's error; nil when all were read.
func (s *Store) SettingsErr() error {
	var errs []error
	for _, key := range []string{"refresh", "bots", "queues", "editor"} {
		if err := s.settings.errs[key]; err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
