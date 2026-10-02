package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/preferences"
)

// accountsModel shows bob's lists with account switching on; used records
// each account the client is told to use.
func accountsModel(t *testing.T) (*model, *[]string) {
	t.Helper()
	m := changesModel(t, changePR(1, "acme/a"))
	m.switchAccounts = true
	var used []string
	m.useAccount = func(login string) { used = append(used, login) }
	m.listAccounts = func(context.Context) ([]github.Account, error) {
		return []github.Account{{Login: "alice", Active: true, OK: true}, {Login: "work", OK: true}}, nil
	}
	return m, &used
}

func pressA(m *model) tea.Cmd {
	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'a', Text: "a"}))
	return cmd
}

// listed runs the picker's listing and delivers its result.
func listed(t *testing.T, m *model, cmd tea.Cmd) {
	t.Helper()
	_, msgs := run(cmd)
	for _, msg := range msgs {
		if msg, ok := msg.(accountsListedMsg); ok {
			m.Update(msg)
			return
		}
	}
	t.Fatal("the picker did not list accounts")
}

func TestPinningAnAccountSavesItAndDropsTheOldAccountsResults(t *testing.T) {
	m, used := accountsModel(t)
	listed(t, m, pressA(m))
	if m.accounts == nil || len(m.accounts.accounts) != 2 {
		t.Fatalf("picker = %+v, want two accounts", m.accounts)
	}
	press(m, tea.Key{Code: tea.KeyDown})
	press(m, tea.Key{Code: tea.KeyDown})
	enter(m)
	if m.accounts != nil || m.pinnedAccount != "work" || len(*used) != 1 || (*used)[0] != "work" {
		t.Fatalf("choosing work: picker %v, pinned %q, used %q", m.accounts, m.pinnedAccount, *used)
	}
	if got := m.preferences.PinnedAccount(); got != "work" {
		t.Fatalf("saved account = %q", got)
	}
	if !m.loading || m.snapshot.Login != "" {
		t.Fatalf("pinning did not start a fresh fetch: loading %t, rows of %q", m.loading, m.snapshot.Login)
	}

	// The fetch started as alice finishes late and is dropped.
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: []github.PullRequest{changePR(9, "acme/z")}}})
	if m.snapshot.Login != "" || !m.loading {
		t.Fatalf("a result fetched as the previous account was applied: %q", m.snapshot.Login)
	}
	m.Update(fetchFinishedMsg{account: m.accountGeneration, snapshot: github.Snapshot{Login: "work"}})
	if m.snapshot.Login != "work" || !strings.Contains(m.View().Content, "@work (pinned)") {
		t.Fatalf("the pinned account's result: login %q, view %q", m.snapshot.Login, m.View().Content)
	}

	// The first row follows gh's active account again.
	listed(t, m, pressA(m))
	if m.accounts.cursor != 2 {
		t.Fatalf("picker cursor = %d, want the pinned account", m.accounts.cursor)
	}
	press(m, tea.Key{Code: tea.KeyUp})
	press(m, tea.Key{Code: tea.KeyUp})
	enter(m)
	if m.pinnedAccount != "" || (*used)[len(*used)-1] != "" || m.preferences.PinnedAccount() != "" {
		t.Fatalf("following the active account: pinned %q, used %q, saved %q", m.pinnedAccount, *used, m.preferences.PinnedAccount())
	}
}

func TestStaleAccountListsAndQuotaReadsAreIgnored(t *testing.T) {
	m, _ := accountsModel(t)
	cmd := pressA(m)
	esc(m)
	pressA(m)
	listed(t, m, cmd)
	if !m.accounts.busy {
		t.Fatal("the listing of a closed picker was applied")
	}
	esc(m)

	m.accountGeneration++
	cmd = m.handleQuota(rateLimitMsg{limit: github.RateLimit{Limit: 5000, Remaining: 1}})
	if m.quotaKnown && m.quota.Remaining == 1 {
		t.Fatal("a quota read as the previous account was shown")
	}
	if cmd == nil {
		t.Fatal("a stale quota read ended the poll chain")
	}
}

func TestAccountChoiceIsOffWithAnEnvironmentToken(t *testing.T) {
	m, _ := accountsModel(t)
	m.switchAccounts = false
	pressA(m)
	if m.accounts != nil {
		t.Fatal("a opened the account picker with an environment token")
	}
}

func TestLoggedOutPinnedAccountOffersAnotherAccount(t *testing.T) {
	m, _ := accountsModel(t)
	m.Update(fetchFinishedMsg{err: &github.AuthError{Err: errors.New("work is not logged in to gh on github.com")}})
	view := m.View().Content
	if !strings.Contains(view, "work is not logged in") || !strings.Contains(view, "Press a to choose another GitHub CLI account") {
		t.Fatalf("error screen = %q", view)
	}
	listed(t, m, pressA(m))
	if m.accounts == nil {
		t.Fatal("a did not open the picker from the error screen")
	}
	if view := m.View().Content; !strings.Contains(view, "@alice · active in gh") || !strings.Contains(view, "Follow gh's active account (in use)") {
		t.Fatalf("picker = %q", view)
	}
}

func TestFailedAccountSaveStaysShownForTheNewAccount(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to a read-only directory")
	}
	dir := t.TempDir()
	store, err := preferences.Open(filepath.Join(dir, "preferences.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, login := range []string{"alice", "work"} {
		if err := store.Save(login, ""); err != nil {
			t.Fatal(err)
		}
	}
	m, _ := accountsModel(t)
	m.preferences = store
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	m.chooseAccount("work")
	m.Update(fetchFinishedMsg{account: m.accountGeneration, snapshot: github.Snapshot{Login: "work"}})
	if store.PinnedAccount() != "" {
		t.Fatal("the store saved the account")
	}
	if view := m.View().Content; !strings.Contains(view, "Account choice not saved") {
		t.Fatalf("the failed save is not shown: %q", view)
	}
}

func TestPinnedAccountRecoversOnceGHHasANewToken(t *testing.T) {
	m, _ := accountsModel(t)
	changed := false
	m.tokenChanged = func(context.Context) bool { return changed }
	authFailure := fetchFinishedMsg{err: &github.AuthError{Err: errors.New("bad credentials")}}

	// Following the active account waits for a key, as before.
	if _, cmd := m.Update(authFailure); cmd != nil {
		t.Fatal("an unpinned authentication failure scheduled a retry")
	}

	m.pinnedAccount = "work"
	if _, cmd := m.Update(authFailure); cmd == nil {
		t.Fatal("a pinned authentication failure did not wait for a new token")
	}
	if view := m.View().Content; !strings.Contains(view, "Waiting for work to log in again") {
		t.Fatalf("error screen = %q", view)
	}
	recheck := tokenRecheckMsg{generation: m.refreshGeneration, account: m.accountGeneration}
	check := func() tea.Cmd {
		_, cmd := m.Update(recheck)
		_, msgs := run(cmd)
		if len(msgs) != 1 {
			t.Fatalf("recheck messages = %v", msgs)
		}
		_, next := m.Update(msgs[0])
		return next
	}
	if next := check(); next == nil || m.loading {
		t.Fatalf("an unchanged token: next %v, loading %t; want another wait and no fetch", next, m.loading)
	}
	changed = true
	check()
	if !m.loading {
		t.Fatal("a new token did not start a fetch")
	}
	// The fetch replaced the wait, so its old ticks end.
	if _, cmd := m.Update(recheck); cmd != nil {
		t.Fatal("a recheck from before the fetch ran")
	}
}
