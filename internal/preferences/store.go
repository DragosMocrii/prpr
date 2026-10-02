package preferences

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/DragosMocrii/prpr/internal/github"
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

// account is what the store keeps for one GitHub account. chosen
// distinguishes a saved scope from none: a saved empty scope means the user
// chose All repositories.
type account struct {
	scope      Scope
	chosen     bool
	watchlists []Watchlist
}

type Store struct {
	path     string
	accounts map[string]account
}

// accountJSON is an account's value when it has watchlists or a watchlist
// scope; otherwise the value is the repository string alone, the format
// older versions read. An object without repository or watchlist has no
// saved scope.
type accountJSON struct {
	Repository *string             `json:"repository,omitempty"`
	Watchlist  string              `json:"watchlist,omitempty"`
	Watchlists map[string][]string `json:"watchlists,omitempty"`
}

func Open(path string) (*Store, error) {
	store := &Store{path: path, accounts: make(map[string]account)}
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
	if len(a.watchlists) == 0 && a.chosen && a.scope.Watchlist == "" {
		return a.scope.Repository
	}
	object := accountJSON{Watchlist: a.scope.Watchlist, Watchlists: make(map[string][]string, len(a.watchlists))}
	if a.chosen && a.scope.Watchlist == "" {
		repository := a.scope.Repository
		object.Repository = &repository
	}
	for _, watchlist := range a.watchlists {
		object.Watchlists[watchlist.Name] = watchlist.Repositories
	}
	return object
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
	if err := edit(&edited); err != nil {
		return fmt.Errorf("save preferences %q: %w", s.path, err)
	}
	accounts := make(map[string]account, len(s.accounts)+1)
	for k, v := range s.accounts {
		accounts[k] = v
	}
	accounts[key] = edited
	encoded := make(map[string]any, len(accounts))
	for k, v := range accounts {
		encoded[k] = v.encode()
	}
	data, err := json.MarshalIndent(encoded, "", "  ")
	if err != nil {
		return fmt.Errorf("encode preferences %q: %w", s.path, err)
	}
	if err := s.write(data); err != nil {
		return err
	}
	s.accounts = accounts
	return nil
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
