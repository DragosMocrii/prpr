package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"charm.land/bubbletea/v2"
	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/preferences"
	"github.com/DragosMocrii/prpr/internal/tui"
)

func main() {
	if err := run(); err != nil {
		var reported reportedError
		if !errors.As(err, &reported) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

// reportedError marks an error the flag package has already printed.
type reportedError struct{ err error }

func (e reportedError) Error() string { return e.err.Error() }
func (e reportedError) Unwrap() error { return e.err }

// minRefreshInterval keeps automatic refreshes from polling GitHub too often.
const minRefreshInterval = 30 * time.Second

// parseFlags returns the auto-refresh interval; zero turns it off.
func parseFlags(args []string, output io.Writer) (time.Duration, error) {
	flags := flag.NewFlagSet("prpr", flag.ContinueOnError)
	flags.SetOutput(output)
	refresh := flags.Duration("refresh", 5*time.Minute, "refresh both lists this long after each fetch, e.g. 90s or 10m; 0 turns it off")
	if err := flags.Parse(args); err != nil {
		return 0, reportedError{err}
	}
	if flags.NArg() != 0 {
		return 0, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	if *refresh < 0 || (*refresh > 0 && *refresh < minRefreshInterval) {
		return 0, fmt.Errorf("--refresh must be 0 (off) or at least %s", minRefreshInterval)
	}
	return *refresh, nil
}

func run() error {
	refreshInterval, err := parseFlags(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
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

	_, err = tea.NewProgram(tui.New(ctx, client, store, refreshInterval)).Run()
	return err
}
