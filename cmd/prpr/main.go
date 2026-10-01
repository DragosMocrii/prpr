package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"charm.land/bubbletea/v2"
	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/preferences"
	"github.com/DragosMocrii/prpr/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	client, err := github.NewClient()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return errors.New("GitHub CLI (gh) is required. Install it from https://cli.github.com/")
		}
		return err
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("resolve user configuration directory: %w", err)
	}
	preferencePath := filepath.Join(configDir, "prpr", "preferences.json")
	store, err := preferences.Open(preferencePath)
	if err != nil {
		return fmt.Errorf("cannot load preferences at %q; repair or remove this file before retrying: %w", preferencePath, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err = tea.NewProgram(tui.New(ctx, client, store)).Run()
	return err
}
