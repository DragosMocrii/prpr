package github

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseVersionReadsTheRelease(t *testing.T) {
	for output, want := range map[string]Version{
		"gh version 2.97.0 (2026-07-31)\nhttps://github.com/cli/cli/releases/tag/v2.97.0\n": {2, 97, 0},
		"gh version 2.81.0\n":          {2, 81, 0},
		"gh version 10.2.3-rc.1 (x)\n": {10, 2, 3},
	} {
		if got, ok := parseVersion([]byte(output)); !ok || got != want {
			t.Errorf("parseVersion(%q) = %v, %t; want %v", output, got, ok, want)
		}
	}
	for _, output := range []string{"gh version DEV\n", "", "version 2.97.0"} {
		if got, ok := parseVersion([]byte(output)); ok {
			t.Errorf("parseVersion(%q) = %v, want unreadable", output, got)
		}
	}
}

func TestVersionOrder(t *testing.T) {
	older := []Version{{2, 80, 9}, {1, 99, 99}, {2, 81, -1}}
	for _, v := range older {
		if !v.Less(MinimumVersion) {
			t.Errorf("%v is not older than %v", v, MinimumVersion)
		}
	}
	for _, v := range []Version{MinimumVersion, {2, 81, 1}, {2, 100, 0}, {3, 0, 0}} {
		if v.Less(MinimumVersion) {
			t.Errorf("%v is older than %v", v, MinimumVersion)
		}
	}
}

// versionGH is a gh stand-in that prints output for --version, or fails.
func versionGH(t *testing.T, output string, fail bool) *Client {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	script := "#!/bin/sh\nprintf '%s' '" + output + "'\n"
	if fail {
		script += "exit 1\n"
	}
	path := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return &Client{path: path}
}

func TestCheckVersionRejectsAnOlderGH(t *testing.T) {
	err := versionGH(t, "gh version 2.45.0 (2024-03-04)\n", false).CheckVersion(context.Background())
	var old *VersionError
	if !errors.As(err, &old) || old.Found != (Version{2, 45, 0}) {
		t.Fatalf("err = %v, want a VersionError for 2.45.0", err)
	}
	if text := err.Error(); !strings.Contains(text, "2.45.0") || !strings.Contains(text, MinimumVersion.String()) {
		t.Fatalf("message %q names neither version", text)
	}
	for name, client := range map[string]*Client{
		"current":    versionGH(t, "gh version 2.97.0 (2026-07-31)\n", false),
		"minimum":    versionGH(t, "gh version 2.81.0\n", false),
		"unreadable": versionGH(t, "gh version DEV\n", false),
		"failing":    versionGH(t, "", true),
	} {
		if err := client.CheckVersion(context.Background()); err != nil {
			t.Errorf("%s gh: %v", name, err)
		}
	}
}
