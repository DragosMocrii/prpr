// Package notifier posts desktop notifications through the operating
// system's own notifier command, when prpr runs where one can reach the
// user's desktop.
package notifier

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Func posts a notification with a title and body.
type Func func(ctx context.Context, title, body string) error

// Desktop returns the notifier for this machine, or nil when none can reach
// the desktop: over SSH, inside a container without a session bus, or on systems
// without a supported command. The terminal's own notification is used then.
func Desktop() Func {
	return desktop(runtime.GOOS, os.Getenv, func(path string) error {
		_, err := os.Stat(path)
		return err
	}, exec.LookPath)
}

// sessionBus reports whether a D-Bus session bus is reachable: named by
// DBUS_SESSION_BUS_ADDRESS, or at its default path under XDG_RUNTIME_DIR.
func sessionBus(getenv func(string) string, stat func(string) error) bool {
	if getenv("DBUS_SESSION_BUS_ADDRESS") != "" {
		return true
	}
	runtimeDir := getenv("XDG_RUNTIME_DIR")
	return runtimeDir != "" && stat(filepath.Join(runtimeDir, "bus")) == nil
}

func desktop(goos string, getenv func(string) string, stat func(string) error, lookPath func(string) (string, error)) Func {
	// Over SSH the command would notify the remote machine.
	if getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" {
		return nil
	}
	var build func(ctx context.Context, path, title, body string) *exec.Cmd
	var name string
	switch {
	case goos == "darwin":
		name, build = "osascript", osascriptCommand
	// notify-send talks to the session bus. Dev containers often forward a
	// display but not the bus, so the display alone is not enough.
	case goos == "linux" && sessionBus(getenv, stat):
		name, build = "notify-send", notifySendCommand
	default:
		return nil
	}
	path, err := lookPath(name)
	if err != nil {
		return nil
	}
	return func(ctx context.Context, title, body string) error {
		if output, err := build(ctx, path, title, body).CombinedOutput(); err != nil {
			if text := strings.TrimSpace(string(output)); text != "" {
				return fmt.Errorf("desktop notification failed: %s", text)
			}
			return fmt.Errorf("desktop notification failed: %w", err)
		}
		return nil
	}
}

// osascriptCommand passes the text as arguments, never as script source, so
// it cannot inject AppleScript.
func osascriptCommand(ctx context.Context, path, title, body string) *exec.Cmd {
	return exec.CommandContext(ctx, path,
		"-e", "on run argv",
		"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
		"-e", "end run",
		title, body)
}

// notifySendCommand escapes the body, which notification servers may read as
// markup.
func notifySendCommand(ctx context.Context, path, title, body string) *exec.Cmd {
	escape := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return exec.CommandContext(ctx, path, "--app-name=prpr", "--", title, escape.Replace(body))
}
