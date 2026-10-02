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
	"runtime/debug"
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

// version is set by release builds with -ldflags "-X main.version=v1.2.3".
var version string

// versionString falls back to the module version that go install records.
func versionString() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// minRefreshInterval keeps automatic refreshes from polling GitHub too often.
const minRefreshInterval = 30 * time.Second

type options struct {
	// refresh is the auto-refresh interval; zero turns it off.
	refresh time.Duration
	bots    []github.Bot
	notify  bool
	version bool
}

func parseFlags(args []string, output io.Writer) (options, error) {
	flags := flag.NewFlagSet("prpr", flag.ContinueOnError)
	flags.SetOutput(output)
	refresh := flags.Duration("refresh", 5*time.Minute, "refresh both lists this long after each fetch, e.g. 90s or 10m; 0 turns it off")
	showVersion := flags.Bool("version", false, "print the version and exit")
	bots := flags.String("bots", github.DefaultBots, "review bots as comma-separated Name=login or Name=login:check entries; empty hides the Bots column")
	notify := flags.Bool("notify", false, "send a desktop notification when a PR turns ready to merge, fails CI, gets changes requested, or requests your review; n toggles it")
	if err := flags.Parse(args); err != nil {
		return options{}, reportedError{err}
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	if *refresh < 0 || (*refresh > 0 && *refresh < minRefreshInterval) {
		return options{}, fmt.Errorf("--refresh must be 0 (off) or at least %s", minRefreshInterval)
	}
	parsed, err := github.ParseBots(*bots)
	if err != nil {
		return options{}, fmt.Errorf("--bots: %w", err)
	}
	return options{refresh: *refresh, bots: parsed, notify: *notify, version: *showVersion}, nil
}

func run() error {
	opts, err := parseFlags(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if opts.version {
		fmt.Println("prpr", versionString())
		return nil
	}
	client, err := github.NewClient(opts.bots)
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

	_, err = tea.NewProgram(tui.New(ctx, client, store, opts.refresh, opts.notify)).Run()
	return err
}
