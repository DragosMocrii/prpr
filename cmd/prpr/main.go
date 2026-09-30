package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"charm.land/bubbletea/v2"
	"prpr/internal/github"
	"prpr/internal/tui"
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err = tea.NewProgram(tui.New(ctx, client)).Run()
	return err
}
