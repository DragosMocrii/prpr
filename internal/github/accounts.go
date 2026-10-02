package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Account is a GitHub CLI account authenticated to github.com.
type Account struct {
	Login string
	// Active is gh's active account, which gh uses when none is pinned.
	Active bool
	// OK reports whether gh could use the account's token; Error says why
	// not.
	OK    bool
	Error string
}

// secret holds a token. It formats as [redacted] in every form, so a token
// cannot reach a log, error, or screen through formatting.
type secret string

const redacted = "[redacted]"

func (secret) String() string                      { return redacted }
func (secret) GoString() string                    { return redacted }
func (secret) Format(f fmt.State, _ rune)          { _, _ = io.WriteString(f, redacted) }
func (secret) MarshalJSON() ([]byte, error)        { return json.Marshal(redacted) }
func (secret) MarshalText() ([]byte, error)        { return []byte(redacted), nil }
func (secret) LogValue() slog.Value                { return slog.StringValue(redacted) }
func (s secret) scrub(text string) string          { return strings.ReplaceAll(text, string(s), redacted) }
func (s secret) environment(env []string) []string { return append(env, "GH_TOKEN="+string(s)) }

// EnvironmentToken reports whether the environment gives gh a token, which
// gh uses instead of any stored account, so prpr cannot pin one.
func EnvironmentToken() bool {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}

// UseAccount pins the account that every later gh request uses, or follows
// gh's active account when login is empty. It only records the choice: the
// next request reads the account's token from gh.
func (c *Client) UseAccount(login string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.login, c.token = login, ""
}

// PinnedAccount returns the pinned account, or "" when following gh's
// active account.
func (c *Client) PinnedAccount() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.login
}

// accountToken returns the pinned account's token, reading it from gh when
// it is not cached or reread is set. It is empty when no account is pinned.
func (c *Client) accountToken(ctx context.Context, reread bool) (secret, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.login == "" || (c.token != "" && !reread) {
		return c.token, nil
	}
	// Reading the token needs no token: gh reads its own store.
	cmd := exec.CommandContext(ctx, c.path, "auth", "token", "--hostname", "github.com", "--user", c.login)
	cmd.Env = withoutTokens(os.Environ())
	out, err := cmd.Output()
	token := secret(strings.TrimSpace(string(out)))
	if err != nil || token == "" {
		if err == nil {
			err = errors.New("gh returned no token")
		}
		c.token = ""
		return "", &AuthError{Err: commandError(c.login+" is not logged in to gh on github.com", err)}
	}
	c.token = token
	return token, nil
}

// PinnedTokenChanged reports whether gh now has a token for the pinned
// account that differs from the one prpr last used, for example after a new
// login. It reads gh's local store only and makes no GitHub request.
func (c *Client) PinnedTokenChanged(ctx context.Context) bool {
	c.mu.Lock()
	login, last := c.login, c.token
	c.mu.Unlock()
	if login == "" {
		return false
	}
	cmd := exec.CommandContext(ctx, c.path, "auth", "token", "--hostname", "github.com", "--user", login)
	cmd.Env = withoutTokens(os.Environ())
	out, err := cmd.Output()
	token := secret(strings.TrimSpace(string(out)))
	return err == nil && token != "" && token != last
}

// command builds a gh command that runs as the pinned account, if any. The
// token is passed only in the child's environment, never as an argument.
func (c *Client) command(ctx context.Context, args ...string) (*exec.Cmd, secret, error) {
	token, err := c.accountToken(ctx, false)
	if err != nil {
		return nil, "", err
	}
	cmd := exec.CommandContext(ctx, c.path, args...)
	if token != "" {
		cmd.Env = token.environment(withoutTokens(os.Environ()))
	}
	return cmd, token, nil
}

// output runs a gh command as the pinned account. Its error names message
// and gh's message, with any copy of the token removed.
func (c *Client) output(ctx context.Context, message string, args ...string) ([]byte, error) {
	cmd, token, err := c.command(ctx, args...)
	if err != nil {
		return nil, err
	}
	data, err := cmd.Output()
	if err != nil {
		err = commandError(message, err)
		if token != "" {
			err = errors.New(token.scrub(err.Error()))
		}
		return nil, err
	}
	return data, nil
}

// withoutTokens drops the variables that give gh a token or make it log
// requests, so a child sees only the token prpr passes.
func withoutTokens(env []string) []string {
	kept := make([]string, 0, len(env))
	for _, variable := range env {
		name, _, _ := strings.Cut(variable, "=")
		switch strings.ToUpper(name) {
		case "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_DEBUG", "DEBUG":
			continue
		}
		kept = append(kept, variable)
	}
	return kept
}

// Accounts lists the accounts gh has stored for github.com, read from the
// JSON form of gh auth status, which never includes tokens.
func (c *Client) Accounts(ctx context.Context) ([]Account, error) {
	cmd := exec.CommandContext(ctx, c.path, "auth", "status", "--hostname", "github.com", "--json", "hosts")
	cmd.Env = withoutTokens(os.Environ())
	data, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && strings.Contains(string(exitErr.Stderr), "unknown flag") {
			return nil, errors.New("this GitHub CLI cannot list accounts; update gh to choose one")
		}
		return nil, commandError("GitHub CLI accounts could not be listed", err)
	}
	return decodeAccounts(data)
}

func decodeAccounts(data []byte) ([]Account, error) {
	var status struct {
		Hosts map[string][]struct {
			Login       *string `json:"login"`
			Active      bool    `json:"active"`
			State       string  `json:"state"`
			Error       string  `json:"error"`
			TokenSource string  `json:"tokenSource"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(data, &status); err != nil || status.Hosts == nil {
		if err == nil {
			err = errors.New("no hosts")
		}
		return nil, fmt.Errorf("decode GitHub CLI accounts: %w", err)
	}
	var accounts []Account
	for _, entry := range status.Hosts["github.com"] {
		// Environment tokens are not stored accounts.
		if entry.Login == nil || *entry.Login == "" || entry.TokenSource == "GH_TOKEN" || entry.TokenSource == "GITHUB_TOKEN" {
			continue
		}
		accounts = append(accounts, Account{
			Login:  *entry.Login,
			Active: entry.Active,
			OK:     entry.State == "success",
			Error:  entry.Error,
		})
	}
	return accounts, nil
}

// openURL starts the browser without waiting for it, so a browser that
// keeps running is neither waited on nor stopped when prpr quits.
func (c *Client) openURL(ctx context.Context, url string) error {
	cmd := c.openURLCommand(ctx, url)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("Could not open the browser: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// openURLCommand opens a URL in the browser without gh, so the browser
// process never inherits the pinned account's token. It follows gh's own
// order: GH_BROWSER, gh's browser setting, BROWSER, then the system default.
func (c *Client) openURLCommand(ctx context.Context, url string) *exec.Cmd {
	browser := os.Getenv("GH_BROWSER")
	if browser == "" {
		setting := exec.CommandContext(ctx, c.path, "config", "get", "browser")
		setting.Env = withoutTokens(os.Environ())
		if out, err := setting.Output(); err == nil {
			browser = strings.TrimSpace(string(out))
		}
	}
	if browser == "" {
		browser = os.Getenv("BROWSER")
	}
	var cmd *exec.Cmd
	if fields := strings.Fields(browser); len(fields) > 0 {
		cmd = exec.Command(fields[0], append(fields[1:], url)...)
	} else {
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", url)
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		default:
			cmd = exec.Command("xdg-open", url)
		}
	}
	cmd.Env = withoutTokens(os.Environ())
	return cmd
}
