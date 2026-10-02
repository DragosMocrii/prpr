package notifier

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func found(name string) (string, error) { return "/usr/bin/" + name, nil }

func missing(string) (string, error) { return "", errors.New("not found") }

func TestDesktopIsUnavailableOverSSHWithoutADisplayOrCommand(t *testing.T) {
	bus := func(path string) error {
		if path == "/run/user/1000/bus" {
			return nil
		}
		return errors.New("no such file")
	}
	for _, tc := range []struct {
		name   string
		goos   string
		env    map[string]string
		lookup func(string) (string, error)
		want   bool
	}{
		{"mac", "darwin", nil, found, true},
		{"mac over SSH", "darwin", map[string]string{"SSH_CONNECTION": "1 2 3 4"}, found, false},
		{"linux desktop", "linux", map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus"}, found, true},
		{"bus at the default path", "linux", map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"}, found, true},
		{"container with a display but no bus", "linux", map[string]string{"DISPLAY": ":0", "WAYLAND_DISPLAY": "vscode.sock", "XDG_RUNTIME_DIR": "/tmp/user/1000"}, found, false},
		{"linux without notify-send", "linux", map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus"}, missing, false},
		{"windows", "windows", nil, found, false},
	} {
		if got := desktop(tc.goos, env(tc.env), bus, tc.lookup) != nil; got != tc.want {
			t.Errorf("%s: available = %t, want %t", tc.name, got, tc.want)
		}
	}
}

func TestCommandsPassTextAsArguments(t *testing.T) {
	body := `a" & do shell script "x" <b>`
	mac := osascriptCommand(context.Background(), "/usr/bin/osascript", "prpr", body)
	if args := mac.Args; args[len(args)-2] != "prpr" || args[len(args)-1] != body {
		t.Fatalf("osascript args = %q, want title and body last, unchanged", args)
	}
	for _, arg := range mac.Args[1 : len(mac.Args)-2] {
		if strings.Contains(arg, "shell script") {
			t.Fatalf("body reached the script source: %q", mac.Args)
		}
	}
	linux := notifySendCommand(context.Background(), "/usr/bin/notify-send", "prpr", body)
	if got := linux.Args[len(linux.Args)-1]; got != `a" &amp; do shell script "x" &lt;b&gt;` {
		t.Fatalf("notify-send body = %q, want markup escaped", got)
	}
	if linux.Args[len(linux.Args)-3] != "--" {
		t.Fatalf("notify-send args = %q, want -- before the text", linux.Args)
	}
}
