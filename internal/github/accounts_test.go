package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const fakeToken = "gho_FAKE_TOKEN_FOR_TESTS"

// fakeGH writes a gh stand-in that logs each call's arguments and GH_TOKEN,
// prints fakeToken for alice's auth token, and answers the queries prpr
// sends. failAPI makes API calls fail with the token on stderr.
func fakeGH(t *testing.T, failAPI bool) (*Client, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	fail := ":"
	if failAPI {
		fail = `echo "boom $GH_TOKEN" >&2; exit 1`
	}
	script := fmt.Sprintf(`#!/bin/sh
printf 'token=%%s args=%%s\n' "${GH_TOKEN:-none}" "$1 $2 $3" >> %q
case "$*" in
  "auth token --hostname github.com --user alice") echo %s ;;
  "auth token"*) echo "no oauth token found for github.com account $6" >&2; exit 1 ;;
  "auth status --hostname github.com --json hosts") echo '{"hosts":{"github.com":[{"state":"success","active":true,"host":"github.com","login":"bob","tokenSource":"keyring"},{"state":"error","error":"HTTP 401","active":false,"host":"github.com","login":"alice","tokenSource":"keyring"}]}}' ;;
  "auth status"*) ;;
  "api graphql"*rateLimit*) %s; echo '{"data":{"rateLimit":{"limit":5000,"remaining":4999,"resetAt":"2026-10-02T10:00:00Z"}}}' ;;
  "api graphql"*mentions:@me*) %s; echo '{"data":{"search":{"nodes":[]}}}' ;;
  "api graphql"*search*) %s; echo '[{"data":{"search":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}]' ;;
  "api graphql"*) %s; echo '[{"data":{"viewer":{"login":"alice","pullRequests":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}}]' ;;
  "api"*) %s; echo '[[]]' ;;
esac
`, log, fakeToken, fail, fail, fail, fail, fail)
	path := filepath.Join(dir, "gh")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return &Client{path: path}, log
}

func calls(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestPinnedAccountTokenReachesOnlyGHRequests(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "inherited")
	client, log := fakeGH(t, false)
	client.UseAccount("alice")
	ctx := context.Background()
	if _, err := client.Fetch(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Preview(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RateLimit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListRepositories(ctx); err != nil {
		t.Fatal(err)
	}
	for _, call := range calls(t, log) {
		reading := strings.Contains(call, "args=auth token")
		if reading && !strings.HasPrefix(call, "token=none ") {
			t.Errorf("reading the token got a token: %s", call)
		}
		if !reading && !strings.HasPrefix(call, "token="+fakeToken+" ") {
			t.Errorf("request did not run as the pinned account: %s", call)
		}
	}
}

func TestFollowingTheActiveAccountPassesNoToken(t *testing.T) {
	client, log := fakeGH(t, false)
	if _, err := client.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, call := range calls(t, log) {
		if !strings.HasPrefix(call, "token=none ") || strings.Contains(call, "auth token") {
			t.Errorf("unpinned call = %s, want no token and no token read", call)
		}
	}
}

func TestFetchRereadsTheTokenAndReportsALoggedOutAccount(t *testing.T) {
	client, log := fakeGH(t, false)
	client.UseAccount("alice")
	ctx := context.Background()
	for range 2 {
		if _, err := client.Fetch(ctx); err != nil {
			t.Fatal(err)
		}
	}
	reads := 0
	for _, call := range calls(t, log) {
		if strings.Contains(call, "auth token") {
			reads++
		}
	}
	if reads != 2 {
		t.Fatalf("token reads = %d, want one per fetch", reads)
	}

	client.UseAccount("carol")
	_, err := client.Fetch(ctx)
	var authErr *AuthError
	if !errors.As(err, &authErr) || !strings.Contains(err.Error(), "carol is not logged in") {
		t.Fatalf("logged-out account error = %v, want an AuthError naming it", err)
	}
}

func TestErrorsNeverContainTheToken(t *testing.T) {
	client, _ := fakeGH(t, true)
	client.UseAccount("alice")
	_, err := client.Fetch(context.Background())
	if err == nil || strings.Contains(err.Error(), fakeToken) || !strings.Contains(err.Error(), "boom [redacted]") {
		t.Fatalf("error = %v, want gh's message with the token redacted", err)
	}
	token := secret(fakeToken)
	for _, text := range []string{
		fmt.Sprint(token), fmt.Sprintf("%v %+v %#v %s %q %x", token, token, token, token, token, token),
		fmt.Sprint(struct{ T secret }{token}),
	} {
		if strings.Contains(text, fakeToken) {
			t.Fatalf("formatting printed the token: %s", text)
		}
	}
	data, _ := json.Marshal(struct{ T secret }{token})
	var buf strings.Builder
	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "token", token)
	if strings.Contains(string(data), fakeToken) || strings.Contains(buf.String(), fakeToken) {
		t.Fatalf("JSON or log printed the token: %s %s", data, buf.String())
	}
}

func TestAccountsListsStoredAccounts(t *testing.T) {
	client, _ := fakeGH(t, false)
	accounts, err := client.Accounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Account{{Login: "bob", Active: true, OK: true}, {Login: "alice", Error: "HTTP 401"}}
	if fmt.Sprint(accounts) != fmt.Sprint(want) {
		t.Fatalf("accounts = %+v, want %+v", accounts, want)
	}
}

func TestDecodeAccountsSkipsEnvironmentTokensAndOtherHosts(t *testing.T) {
	accounts, err := decodeAccounts([]byte(`{"hosts":{
		"github.com":[
			{"state":"success","active":true,"login":"env","tokenSource":"GH_TOKEN"},
			{"state":"error","active":true,"login":"","tokenSource":"GH_TOKEN"},
			{"state":"success","active":false,"login":"alice","tokenSource":"/home/a/.config/gh/hosts.yml"}],
		"ghe.example.com":[{"state":"success","active":true,"login":"corp","tokenSource":"keyring"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0] != (Account{Login: "alice", OK: true}) {
		t.Fatalf("accounts = %+v, want alice only", accounts)
	}
	for _, data := range []string{`[]`, `{}`, `{"hosts":`, `null`} {
		if _, err := decodeAccounts([]byte(data)); err == nil {
			t.Errorf("decodeAccounts(%s) succeeded", data)
		}
	}
}

func TestWithoutTokensDropsTokenAndDebugVariables(t *testing.T) {
	got := withoutTokens([]string{"PATH=/bin", "GH_TOKEN=a", "GITHUB_TOKEN=b", "gh_debug=api", "GH_ENTERPRISE_TOKEN=c", "HOME=/h"})
	if strings.Join(got, " ") != "PATH=/bin HOME=/h" {
		t.Fatalf("withoutTokens = %q", got)
	}
}

func TestOpeningAsAPinnedAccountKeepsTheTokenFromTheBrowser(t *testing.T) {
	client, log := fakeGH(t, false)
	browser := filepath.Join(t.TempDir(), "browser")
	if err := os.WriteFile(browser, []byte(fmt.Sprintf("#!/bin/sh\nprintf 'browser token=%%s args=%%s\\n' \"${GH_TOKEN:-none}\" \"$*\" >> %q\n", log)), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GH_BROWSER", browser+" --new-window")
	t.Setenv("GH_TOKEN", "")
	client.UseAccount("alice")
	ctx := context.Background()
	if _, err := client.Fetch(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.OpenInBrowser(ctx, "https://github.com/acme/a/pull/1"); err != nil {
		t.Fatal(err)
	}
	// The browser runs on without prpr waiting for it.
	want := "browser token=none args=--new-window https://github.com/acme/a/pull/1"
	var got []string
	for range 100 {
		if got = calls(t, log); got[len(got)-1] == want {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if last := got[len(got)-1]; last != want {
		t.Fatalf("browser call = %q, want the URL without a token", last)
	}
	for _, call := range got {
		if strings.Contains(call, "args=pr view") {
			t.Fatalf("opened through gh, which would pass the token on: %s", call)
		}
	}
}

func TestPinnedTokenChangedComparesWithTheLastToken(t *testing.T) {
	client, log := fakeGH(t, false)
	ctx := context.Background()
	if client.PinnedTokenChanged(ctx) {
		t.Fatal("an unpinned client reported a new token")
	}
	client.UseAccount("alice")
	if !client.PinnedTokenChanged(ctx) {
		t.Fatal("a token after none was not new")
	}
	if _, err := client.Fetch(ctx); err != nil {
		t.Fatal(err)
	}
	if client.PinnedTokenChanged(ctx) {
		t.Fatal("the token in use was reported as new")
	}
	client.UseAccount("carol")
	if client.PinnedTokenChanged(ctx) {
		t.Fatal("a logged-out account reported a new token")
	}
	for _, call := range calls(t, log) {
		if strings.Contains(call, "auth token") && !strings.HasPrefix(call, "token=none ") {
			t.Fatalf("checking the token passed one: %s", call)
		}
	}
}
