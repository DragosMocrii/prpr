package github

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

// MinimumVersion is the oldest gh prpr works with: the release that added
// `gh auth status --json`, which account discovery reads. Raise it when
// prpr starts using a newer gh flag.
var MinimumVersion = Version{2, 81, 0}

// Version is a gh release number.
type Version struct{ Major, Minor, Patch int }

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Less reports whether v is an older release than w.
func (v Version) Less(w Version) bool {
	if v.Major != w.Major {
		return v.Major < w.Major
	}
	if v.Minor != w.Minor {
		return v.Minor < w.Minor
	}
	return v.Patch < w.Patch
}

// VersionError reports a gh older than MinimumVersion.
type VersionError struct{ Found Version }

func (e *VersionError) Error() string {
	return fmt.Sprintf("prpr needs GitHub CLI (gh) %s or later; found %s. Upgrade it with brew upgrade gh, scoop update gh, "+
		"winget upgrade GitHub.cli, or your package manager; see https://cli.github.com/", MinimumVersion, e.Found)
}

var versionLine = regexp.MustCompile(`(?m)^gh version (\d+)\.(\d+)\.(\d+)`)

// parseVersion reads the release from `gh --version`; false for output it
// does not recognize, such as a build from source.
func parseVersion(output []byte) (Version, bool) {
	match := versionLine.FindSubmatch(output)
	if match == nil {
		return Version{}, false
	}
	var parts [3]int
	for i := range parts {
		n, err := strconv.Atoi(string(match[i+1]))
		if err != nil {
			return Version{}, false
		}
		parts[i] = n
	}
	return Version{parts[0], parts[1], parts[2]}, true
}

// versionTimeout bounds `gh --version`, which reads nothing remote.
const versionTimeout = 10 * time.Second

// CheckVersion returns a *VersionError when gh is older than
// MinimumVersion. A gh whose version cannot be read passes: the commands
// that need a newer one report their own errors.
func (c *Client) CheckVersion(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.path, "--version")
	cmd.Env = withoutTokens(os.Environ())
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	if found, ok := parseVersion(output); ok && found.Less(MinimumVersion) {
		return &VersionError{Found: found}
	}
	return nil
}
