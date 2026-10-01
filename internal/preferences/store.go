package preferences

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"prpr/internal/github"
)

type Store struct {
	path    string
	choices map[string]string
}

func Open(path string) (*Store, error) {
	store := &Store{path: path, choices: make(map[string]string)}
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
	for account, value := range raw {
		var decoded any
		if err := json.Unmarshal(value, &decoded); err != nil {
			return nil, fmt.Errorf("decode preferences %q: account %q must have a string repository value: %w", path, account, err)
		}
		repository, ok := decoded.(string)
		if !ok {
			return nil, fmt.Errorf("decode preferences %q: account %q must have a string repository value", path, account)
		}
		if repository != "" && !github.ValidRepositoryName(repository) {
			return nil, fmt.Errorf("decode preferences %q: account %q has invalid repository %q", path, account, repository)
		}
		store.choices[account] = repository
	}
	return store, nil
}

func (s *Store) Lookup(login string) (string, bool) {
	repository, found := s.choices[accountKey(login)]
	return repository, found
}

func (s *Store) Save(login, repository string) error {
	if strings.TrimSpace(login) == "" {
		return fmt.Errorf("save preferences %q: GitHub login is empty", s.path)
	}
	if repository != "" && !github.ValidRepositoryName(repository) {
		return fmt.Errorf("save preferences %q: invalid repository %q", s.path, repository)
	}
	choices := make(map[string]string, len(s.choices)+1)
	for account, value := range s.choices {
		choices[account] = value
	}
	choices[accountKey(login)] = repository
	data, err := json.MarshalIndent(choices, "", "  ")
	if err != nil {
		return fmt.Errorf("encode preferences %q: %w", s.path, err)
	}
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
	s.choices = choices
	return nil
}

func accountKey(login string) string { return "github.com/" + strings.ToLower(login) }
