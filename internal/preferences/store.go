package preferences

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/readiness"
	"github.com/DragosMocrii/prpr/internal/schedule"
)

// Scope is an account's saved choice of what both lists show: one
// repository, a watchlist by name, or All repositories when both are empty.
type Scope struct {
	Repository string
	Watchlist  string
}

// Watchlist is a named set of repositories.
type Watchlist struct {
	Name         string
	Repositories []string
}

// MaxWatchlistName is the longest watchlist name, in characters.
const MaxWatchlistName = 40

// ValidWatchlistName reports whether name can name a watchlist: trimmed,
// non-empty, at most MaxWatchlistName characters, without control characters.
func ValidWatchlistName(name string) bool {
	if name == "" || name != strings.TrimSpace(name) || utf8.RuneCountInString(name) > MaxWatchlistName {
		return false
	}
	return !strings.ContainsFunc(name, unicode.IsControl)
}

// The lists a snooze can belong to.
const (
	SnoozeMine   = "mine"
	SnoozeReview = "review"
)

// Snooze is a pull request hidden from its list until a time or new activity.
type Snooze struct {
	Repository string
	Number     int
	List       string    // SnoozeMine or SnoozeReview
	Until      time.Time // always set; an activity snooze stores now+7d
	Activity   bool      // drawn "activity"
	Seen       []string  // wake signals held at the last full fetch
	Requested  time.Time // review rows: request time recorded while pending; zero otherwise
}

// snoozeJSON is a saved snooze.
type snoozeJSON struct {
	Repository string    `json:"repository"`
	Number     int       `json:"number"`
	List       string    `json:"list"`
	Until      time.Time `json:"until"`
	Activity   bool      `json:"activity,omitempty"`
	Seen       []string  `json:"seen,omitempty"`
	Requested  time.Time `json:"requested,omitzero"`
}

// validSnooze reports why a snooze cannot be saved, or nil.
func validSnooze(s Snooze) error {
	switch {
	case !github.ValidRepositoryName(s.Repository):
		return fmt.Errorf("snooze has invalid repository %q", s.Repository)
	case s.Number <= 0:
		return fmt.Errorf("snooze of %s has invalid number %d", s.Repository, s.Number)
	case s.List != SnoozeMine && s.List != SnoozeReview:
		return fmt.Errorf("snooze of %s#%d has unknown list %q", s.Repository, s.Number, s.List)
	case s.Until.IsZero():
		return fmt.Errorf("snooze of %s#%d has no end", s.Repository, s.Number)
	}
	for _, signal := range s.Seen {
		if signal == "" || strings.ContainsFunc(signal, unicode.IsControl) {
			return fmt.Errorf("snooze of %s#%d has invalid signal %q", s.Repository, s.Number, signal)
		}
	}
	return nil
}

func cloneSnoozes(snoozes []Snooze) []Snooze {
	cloned := slices.Clone(snoozes)
	for i := range cloned {
		cloned[i].Seen = slices.Clone(cloned[i].Seen)
	}
	return cloned
}

// account is what the store keeps for one GitHub account. chosen
// distinguishes a saved scope from none: a saved empty scope means the user
// chose All repositories.
type account struct {
	scope      Scope
	chosen     bool
	watchlists []Watchlist
	// snoozes are the account's snoozed pull requests. Saved snoozes that
	// cannot all be read are kept as snoozedRaw, written back unchanged
	// until snoozes are saved, with snoozeErr saying why.
	snoozes    []Snooze
	snoozedRaw json.RawMessage
	snoozeErr  error
}

type Store struct {
	path     string
	accounts map[string]account
	// pinned is the GitHub CLI account prpr uses, or "" for gh's active one.
	pinned string
	// icons is the saved icon set name, or "" for none saved.
	icons string
	// legend is whether the icon legend panel is open.
	legend bool
	// drafts is whether draft pull requests are shown.
	drafts bool
	// rules are the saved ready-to-merge rules, or the defaults. Saved
	// rules that cannot be read are kept as rulesRaw, written back
	// unchanged until rules are saved, with rulesErr saying why.
	rules    readiness.Rules
	rulesRaw json.RawMessage
	rulesErr error
	// Invalid saved schedules remain in scheduleRaw until the user repairs them.
	schedule    schedule.Config
	scheduleRaw json.RawMessage
	scheduleErr error
}

// appKey holds settings that belong to the app rather than to an account.
// Account keys always contain a slash, so it cannot name an account.
const appKey = "app"

type appJSON struct {
	Account  string          `json:"account,omitempty"`
	Icons    string          `json:"icons,omitempty"`
	Legend   bool            `json:"legend,omitempty"`
	Drafts   bool            `json:"drafts,omitempty"`
	Ready    json.RawMessage `json:"ready,omitempty"`
	Schedule json.RawMessage `json:"schedule,omitempty"`
}

func (a appJSON) empty() bool {
	return a.Account == "" && a.Icons == "" && !a.Legend && !a.Drafts && len(a.Ready) == 0 && len(a.Schedule) == 0
}

// app is the app settings as saved.
func (s *Store) app() appJSON {
	return appJSON{Account: s.pinned, Icons: s.icons, Legend: s.legend, Drafts: s.drafts, Ready: s.rulesRaw, Schedule: s.scheduleRaw}
}

// accountJSON is an account's value when it has watchlists, snoozes, or a
// watchlist scope; otherwise the value is the repository string alone, the
// format older versions read. An object without repository or watchlist has
// no saved scope.
type accountJSON struct {
	Repository *string             `json:"repository,omitempty"`
	Watchlist  string              `json:"watchlist,omitempty"`
	Watchlists map[string][]string `json:"watchlists,omitempty"`
	Snoozed    json.RawMessage     `json:"snoozed,omitempty"`
}

func Open(path string) (*Store, error) {
	store := &Store{path: path, accounts: make(map[string]account), rules: readiness.DefaultRules(), schedule: schedule.Default()}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read preferences %q: %w", path, err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		if err == nil {
			err = errors.New("expected a JSON object")
		}
		return nil, fmt.Errorf("decode preferences %q: %w", path, err)
	}
	for key, value := range raw {
		if key == appKey {
			var app appJSON
			if err := json.Unmarshal(value, &app); err != nil || !validLogin(app.Account) && app.Account != "" {
				return nil, fmt.Errorf("decode preferences %q: %q must be an object with a valid GitHub login as account", path, appKey)
			}
			if app.Icons != "" && !ValidIcons(app.Icons) {
				return nil, fmt.Errorf("decode preferences %q: %q has an unknown icon set %q", path, appKey, app.Icons)
			}
			store.pinned, store.icons, store.legend, store.drafts = app.Account, app.Icons, app.Legend, app.Drafts
			if len(app.Schedule) != 0 {
				store.scheduleRaw = app.Schedule
				config := schedule.Default()
				trimmed := strings.TrimSpace(string(app.Schedule))
				if !strings.HasPrefix(trimmed, "{") {
					store.scheduleErr = fmt.Errorf("active-hours schedule in %q must be an object", path)
				} else if err := json.Unmarshal(app.Schedule, &config); err != nil {
					store.scheduleErr = fmt.Errorf("active-hours schedule in %q not read: %w", path, err)
				} else if _, err := schedule.Compile(config); err != nil {
					store.scheduleErr = fmt.Errorf("active-hours schedule in %q not read: %w", path, err)
				} else {
					store.schedule = config
				}
				if store.scheduleErr != nil {
					store.schedule = schedule.Default()
				}
			}
			// Rules that cannot be read leave the defaults in use and the
			// rest of the preferences readable.
			if len(app.Ready) != 0 {
				store.rulesRaw = app.Ready
				if err := json.Unmarshal(app.Ready, &store.rules); err != nil {
					store.rules = readiness.DefaultRules()
					store.rulesErr = fmt.Errorf("ready-to-merge rules in %q not read, using the defaults: %w", path, err)
				}
			}
			continue
		}
		decoded, err := decodeAccount(value)
		if err != nil {
			return nil, fmt.Errorf("decode preferences %q: account %q %w", path, key, err)
		}
		store.accounts[key] = decoded
	}
	return store, nil
}

func decodeAccount(value json.RawMessage) (account, error) {
	var object *accountJSON
	switch trimmed := strings.TrimSpace(string(value)); {
	case strings.HasPrefix(trimmed, `"`):
		var repository string
		if err := json.Unmarshal(value, &repository); err != nil {
			return account{}, fmt.Errorf("must have a string repository value: %w", err)
		}
		if repository != "" && !github.ValidRepositoryName(repository) {
			return account{}, fmt.Errorf("has invalid repository %q", repository)
		}
		return account{scope: Scope{Repository: repository}, chosen: true}, nil
	case strings.HasPrefix(trimmed, "{"):
		if err := json.Unmarshal(value, &object); err != nil {
			return account{}, fmt.Errorf("must have a repository string or a watchlists object: %w", err)
		}
	default:
		return account{}, errors.New("must have a repository string or a watchlists object")
	}
	var decoded account
	for name, repositories := range object.Watchlists {
		if !ValidWatchlistName(name) {
			return account{}, fmt.Errorf("has invalid watchlist name %q", name)
		}
		if slices.ContainsFunc(decoded.watchlists, func(w Watchlist) bool { return strings.EqualFold(w.Name, name) }) {
			return account{}, fmt.Errorf("has two watchlists named %q", name)
		}
		if len(repositories) == 0 {
			return account{}, fmt.Errorf("has empty watchlist %q", name)
		}
		for _, repository := range repositories {
			if !github.ValidRepositoryName(repository) {
				return account{}, fmt.Errorf("watchlist %q has invalid repository %q", name, repository)
			}
		}
		decoded.watchlists = append(decoded.watchlists, Watchlist{Name: name, Repositories: uniqueRepositories(repositories)})
	}
	sortWatchlists(decoded.watchlists)
	if len(object.Snoozed) != 0 {
		decoded.snoozes, decoded.snoozeErr = decodeSnoozes(object.Snoozed)
		if decoded.snoozeErr != nil {
			decoded.snoozedRaw = object.Snoozed
		}
	}
	switch {
	case object.Repository != nil && object.Watchlist != "":
		return account{}, errors.New("has both a repository and a watchlist")
	case object.Repository != nil:
		if *object.Repository != "" && !github.ValidRepositoryName(*object.Repository) {
			return account{}, fmt.Errorf("has invalid repository %q", *object.Repository)
		}
		decoded.scope, decoded.chosen = Scope{Repository: *object.Repository}, true
	case object.Watchlist != "":
		// A scope naming a watchlist that is gone prompts for a new choice.
		if index := decoded.find(object.Watchlist); index >= 0 {
			decoded.scope, decoded.chosen = Scope{Watchlist: decoded.watchlists[index].Name}, true
		}
	}
	return decoded, nil
}

// decodeSnoozes reads saved snoozes, skipping unreadable ones; the error
// says why any were skipped.
func decodeSnoozes(raw json.RawMessage) ([]Snooze, error) {
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("snoozes not read: %w", err)
	}
	var snoozes []Snooze
	var problems []error
	for _, entry := range entries {
		var saved snoozeJSON
		if err := json.Unmarshal(entry, &saved); err != nil {
			problems = append(problems, err)
			continue
		}
		snooze := Snooze(saved)
		if err := validSnooze(snooze); err != nil {
			problems = append(problems, err)
			continue
		}
		snoozes = append(snoozes, snooze)
	}
	if len(problems) > 0 {
		return snoozes, fmt.Errorf("%d snoozes not read: %w", len(problems), errors.Join(problems...))
	}
	return snoozes, nil
}

func (a *account) find(name string) int {
	return slices.IndexFunc(a.watchlists, func(w Watchlist) bool { return strings.EqualFold(w.Name, name) })
}

func sortWatchlists(watchlists []Watchlist) {
	slices.SortFunc(watchlists, func(a, b Watchlist) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
}

// uniqueRepositories sorts repositories, dropping case-insensitive repeats.
func uniqueRepositories(repositories []string) []string {
	unique := make([]string, 0, len(repositories))
	for _, repository := range repositories {
		if !slices.ContainsFunc(unique, func(r string) bool { return strings.EqualFold(r, repository) }) {
			unique = append(unique, repository)
		}
	}
	slices.SortFunc(unique, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return unique
}

func (a account) encode() any {
	if len(a.watchlists) == 0 && len(a.snoozes) == 0 && a.snoozedRaw == nil && a.chosen && a.scope.Watchlist == "" {
		return a.scope.Repository
	}
	object := accountJSON{Watchlist: a.scope.Watchlist}
	if len(a.watchlists) > 0 {
		object.Watchlists = make(map[string][]string, len(a.watchlists))
	}
	if a.chosen && a.scope.Watchlist == "" {
		repository := a.scope.Repository
		object.Repository = &repository
	}
	for _, watchlist := range a.watchlists {
		object.Watchlists[watchlist.Name] = watchlist.Repositories
	}
	switch {
	case a.snoozedRaw != nil:
		object.Snoozed = a.snoozedRaw
	case len(a.snoozes) > 0:
		saved := make([]snoozeJSON, len(a.snoozes))
		for i, s := range a.snoozes {
			saved[i] = snoozeJSON(s)
		}
		object.Snoozed, _ = json.Marshal(saved)
	}
	return object
}

// validLogin reports whether login can name a gh account. It does not
// follow GitHub's login rules, which differ for managed users such as
// handle_shortcode: any single word without control characters will do.
func validLogin(login string) bool {
	return login != "" && !strings.ContainsFunc(login, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	})
}

// PinnedAccount returns the GitHub CLI account saved for prpr to use, or ""
// to follow gh's active account.
func (s *Store) PinnedAccount() string { return s.pinned }

// saveApp writes the app settings with edit applied. Callers update their
// field only once the write succeeds.
func (s *Store) saveApp(edit func(*appJSON)) error {
	app := s.app()
	edit(&app)
	return s.writeAll(s.accounts, app)
}

// SavePinnedAccount saves the account prpr uses; "" follows gh's active
// account.
func (s *Store) SavePinnedAccount(login string) error {
	if login != "" && !validLogin(login) {
		return fmt.Errorf("save preferences %q: invalid GitHub login %q", s.path, login)
	}
	if err := s.saveApp(func(app *appJSON) { app.Account = login }); err != nil {
		return err
	}
	s.pinned = login
	return nil
}

// ValidIcons reports whether name is an icon set prpr draws.
func ValidIcons(name string) bool {
	return name == "unicode" || name == "nerd"
}

// Icons returns the saved icon set name, or "" when none is saved.
func (s *Store) Icons() string { return s.icons }

// SaveIcons saves the icon set prpr draws.
func (s *Store) SaveIcons(name string) error {
	if !ValidIcons(name) {
		return fmt.Errorf("save preferences %q: unknown icon set %q", s.path, name)
	}
	if err := s.saveApp(func(app *appJSON) { app.Icons = name }); err != nil {
		return err
	}
	s.icons = name
	return nil
}

// Legend reports whether the icon legend panel was left open.
func (s *Store) Legend() bool { return s.legend }

// SaveLegend saves whether the icon legend panel is open.
func (s *Store) SaveLegend(open bool) error {
	if err := s.saveApp(func(app *appJSON) { app.Legend = open }); err != nil {
		return err
	}
	s.legend = open
	return nil
}

// ShowDrafts reports whether draft pull requests were left shown.
func (s *Store) ShowDrafts() bool { return s.drafts }

// SaveShowDrafts saves whether draft pull requests are shown.
func (s *Store) SaveShowDrafts(show bool) error {
	if err := s.saveApp(func(app *appJSON) { app.Drafts = show }); err != nil {
		return err
	}
	s.drafts = show
	return nil
}

// Rules returns the ready-to-merge rules: the saved ones, or the defaults.
func (s *Store) Rules() readiness.Rules { return s.rules.Clone() }

// RulesErr says why saved rules could not be read; nil when they were.
func (s *Store) RulesErr() error { return s.rulesErr }

// Schedule returns the saved active-hours configuration, or its defaults.
func (s *Store) Schedule() schedule.Config {
	config := s.schedule
	config.Days = slices.Clone(config.Days)
	return config
}

// ScheduleErr reports why a saved active-hours configuration could not be read.
func (s *Store) ScheduleErr() error { return s.scheduleErr }

// SaveSchedule validates and persists the active-hours configuration.
func (s *Store) SaveSchedule(config schedule.Config) error {
	if _, err := schedule.Compile(config); err != nil {
		return fmt.Errorf("save preferences %q: %w", s.path, err)
	}
	data, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("encode preferences %q: %w", s.path, err)
	}
	if err := s.saveApp(func(app *appJSON) { app.Schedule = data }); err != nil {
		return err
	}
	s.schedule, s.scheduleRaw, s.scheduleErr = config, data, nil
	s.schedule.Days = slices.Clone(config.Days)
	return nil
}

// SaveRules saves the ready-to-merge rules, replacing any that could not be
// read.
func (s *Store) SaveRules(rules readiness.Rules) error {
	data, err := json.Marshal(rules)
	if err != nil {
		return fmt.Errorf("encode preferences %q: %w", s.path, err)
	}
	if err := s.saveApp(func(app *appJSON) { app.Ready = data }); err != nil {
		return err
	}
	s.rules, s.rulesRaw, s.rulesErr = rules.Clone(), data, nil
	return nil
}

// Lookup returns an account's saved scope, and whether it has one.
func (s *Store) Lookup(login string) (Scope, bool) {
	a := s.accounts[accountKey(login)]
	return a.scope, a.chosen
}

// Watchlists returns an account's watchlists, sorted by name.
func (s *Store) Watchlists(login string) []Watchlist {
	watchlists := slices.Clone(s.accounts[accountKey(login)].watchlists)
	for i := range watchlists {
		watchlists[i].Repositories = slices.Clone(watchlists[i].Repositories)
	}
	return watchlists
}

// Snoozes returns an account's snoozed pull requests, in saved order.
func (s *Store) Snoozes(login string) []Snooze {
	return cloneSnoozes(s.accounts[accountKey(login)].snoozes)
}

// SnoozesErr says why some of an account's saved snoozes were not read.
func (s *Store) SnoozesErr(login string) error { return s.accounts[accountKey(login)].snoozeErr }

// SaveSnoozes replaces an account's snoozes, including any that could not
// be read.
func (s *Store) SaveSnoozes(login string, snoozes []Snooze) error {
	for _, snooze := range snoozes {
		if err := validSnooze(snooze); err != nil {
			return fmt.Errorf("save preferences %q: %w", s.path, err)
		}
	}
	return s.change(login, func(a *account) error {
		a.snoozes, a.snoozedRaw, a.snoozeErr = cloneSnoozes(snoozes), nil, nil
		return nil
	})
}

// Save saves the scope of one repository, or All repositories when
// repository is empty.
func (s *Store) Save(login, repository string) error {
	return s.SaveScope(login, Scope{Repository: repository})
}

// SaveScope saves an account's scope. A watchlist scope must name one of the
// account's watchlists.
func (s *Store) SaveScope(login string, scope Scope) error {
	return s.change(login, func(a *account) error {
		switch {
		case scope.Watchlist != "" && scope.Repository != "":
			return errors.New("a scope cannot have both a repository and a watchlist")
		case scope.Watchlist != "":
			index := a.find(scope.Watchlist)
			if index < 0 {
				return fmt.Errorf("no watchlist named %q", scope.Watchlist)
			}
			scope.Watchlist = a.watchlists[index].Name
		case scope.Repository != "" && !github.ValidRepositoryName(scope.Repository):
			return fmt.Errorf("invalid repository %q", scope.Repository)
		}
		a.scope, a.chosen = scope, true
		return nil
	})
}

// SaveWatchlist saves a watchlist, replacing any with its name, ignoring
// case. A non-empty replacing names the watchlist being edited, which is
// replaced too, so a new name renames it; a scope on it follows the rename.
func (s *Store) SaveWatchlist(login, replacing string, watchlist Watchlist) error {
	if !ValidWatchlistName(watchlist.Name) {
		return fmt.Errorf("invalid watchlist name %q", watchlist.Name)
	}
	if len(watchlist.Repositories) == 0 {
		return errors.New("a watchlist needs at least one repository")
	}
	for _, repository := range watchlist.Repositories {
		if !github.ValidRepositoryName(repository) {
			return fmt.Errorf("invalid repository %q", repository)
		}
	}
	watchlist.Repositories = uniqueRepositories(watchlist.Repositories)
	return s.change(login, func(a *account) error {
		active := a.scope.Watchlist != "" &&
			(strings.EqualFold(a.scope.Watchlist, replacing) || strings.EqualFold(a.scope.Watchlist, watchlist.Name))
		a.watchlists = slices.DeleteFunc(a.watchlists, func(w Watchlist) bool {
			return strings.EqualFold(w.Name, watchlist.Name) || (replacing != "" && strings.EqualFold(w.Name, replacing))
		})
		a.watchlists = append(a.watchlists, watchlist)
		sortWatchlists(a.watchlists)
		if active {
			a.scope.Watchlist = watchlist.Name
		}
		return nil
	})
}

// DeleteWatchlist deletes a watchlist. A scope on it becomes All
// repositories.
func (s *Store) DeleteWatchlist(login, name string) error {
	return s.change(login, func(a *account) error {
		index := a.find(name)
		if index < 0 {
			return fmt.Errorf("no watchlist named %q", name)
		}
		a.watchlists = slices.Delete(a.watchlists, index, index+1)
		if strings.EqualFold(a.scope.Watchlist, name) {
			a.scope = Scope{}
		}
		return nil
	})
}

// change applies edit to a copy of an account and writes every account. The
// store changes only when the write succeeds.
func (s *Store) change(login string, edit func(*account) error) error {
	if strings.TrimSpace(login) == "" {
		return fmt.Errorf("save preferences %q: GitHub login is empty", s.path)
	}
	key := accountKey(login)
	edited := s.accounts[key]
	edited.watchlists = slices.Clone(edited.watchlists)
	edited.snoozes = cloneSnoozes(edited.snoozes)
	if err := edit(&edited); err != nil {
		return fmt.Errorf("save preferences %q: %w", s.path, err)
	}
	accounts := make(map[string]account, len(s.accounts)+1)
	for k, v := range s.accounts {
		accounts[k] = v
	}
	accounts[key] = edited
	if err := s.writeAll(accounts, s.app()); err != nil {
		return err
	}
	s.accounts = accounts
	return nil
}

// writeAll writes every account and the app settings.
func (s *Store) writeAll(accounts map[string]account, app appJSON) error {
	encoded := make(map[string]any, len(accounts)+1)
	for k, v := range accounts {
		encoded[k] = v.encode()
	}
	if !app.empty() {
		encoded[appKey] = app
	}
	data, err := json.MarshalIndent(encoded, "", "  ")
	if err != nil {
		return fmt.Errorf("encode preferences %q: %w", s.path, err)
	}
	return s.write(data)
}

func (s *Store) write(data []byte) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("create preferences directory for %q: %w", s.path, err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(s.path), ".preferences-*")
	if err != nil {
		return fmt.Errorf("create temporary preferences for %q: %w", s.path, err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set permissions on temporary preferences for %q: %w", s.path, err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary preferences for %q: %w", s.path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary preferences for %q: %w", s.path, err)
	}
	if err := os.Rename(temporaryName, s.path); err != nil {
		return fmt.Errorf("replace preferences %q: %w", s.path, err)
	}
	return nil
}

func accountKey(login string) string { return "github.com/" + strings.ToLower(login) }
