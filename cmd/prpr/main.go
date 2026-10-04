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
	"strings"

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

type options struct {
	version bool
}

// movedFlags names the setting that replaced each removed flag.
var movedFlags = map[string]string{
	"refresh": "change Refresh every",
	"bots":    "edit Review bots",
	"queues":  "choose Merge queues",
	"notify":  "turn on Desktop notifications",
	"icons":   "choose Icons",
	"title":   "turn off Terminal title",
}

// movedFlag reports the first removed flag in args, in any spelling:
// -name, --name, -name=value, or --name value.
func movedFlag(args []string) (string, bool) {
	for _, arg := range args {
		if arg == "--" {
			return "", false
		}
		name, ok := strings.CutPrefix(arg, "-")
		if !ok {
			continue
		}
		name = strings.TrimPrefix(name, "-")
		name, _, _ = strings.Cut(name, "=")
		if _, moved := movedFlags[name]; moved {
			return name, true
		}
	}
	return "", false
}

// parseFlags reads the flags: only --version. A removed flag names the
// setting that replaced it.
func parseFlags(args []string, output io.Writer) (options, error) {
	if name, ok := movedFlag(args); ok {
		return options{}, fmt.Errorf("--%s moved to settings: press , in prpr and %s", name, movedFlags[name])
	}
	flags := flag.NewFlagSet("prpr", flag.ContinueOnError)
	flags.SetOutput(output)
	showVersion := flags.Bool("version", false, "print the version and exit")
	if err := flags.Parse(args); err != nil {
		return options{}, reportedError{err}
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	return options{version: *showVersion}, nil
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
	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("resolve user configuration directory: %w", err)
	}
	preferencePath := filepath.Join(configDir, "prpr", "preferences.json")
	store, err := preferences.Open(preferencePath)
	if err != nil {
		return fmt.Errorf("cannot load preferences at %q; repair or remove this file before retrying: %w", preferencePath, err)
	}
	client, err := github.NewClient(store.Bots())
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return errors.New("GitHub CLI (gh) is required. Install it from https://cli.github.com/")
		}
		return err
	}
	client.SetQueues(store.Queues())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err = tea.NewProgram(tui.New(ctx, client, store)).Run()
	return err
}
