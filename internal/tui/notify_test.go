package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/DragosMocrii/prpr/internal/github"
)

// run runs cmd and its batched commands, returning what they write to the
// terminal and the messages they send.
func run(cmd tea.Cmd) (raw []string, msgs []tea.Msg) {
	if cmd == nil {
		return nil, nil
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, cmd := range msg {
			r, m := run(cmd)
			raw, msgs = append(raw, r...), append(msgs, m...)
		}
	case tea.RawMsg:
		if text, ok := msg.Msg.(string); ok {
			raw = append(raw, text)
		}
	case nil:
	default:
		msgs = append(msgs, msg)
	}
	return raw, msgs
}

// notifications runs cmd and returns the OSC 9 notifications it writes.
func notifications(cmd tea.Cmd) []string {
	raw, _ := run(cmd)
	var found []string
	for _, text := range raw {
		if strings.HasPrefix(text, "\x1b]9;") {
			found = append(found, text)
		}
	}
	return found
}

func fetch(m *model, mine, reviews []github.PullRequest) []string {
	_, cmd := m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine, ReviewRequests: reviews}})
	return notifications(cmd)
}

func notifyModel(t *testing.T, repository string) *model {
	t.Helper()
	store := testPreferences(t)
	if err := store.Save("alice", repository); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 140, 30)
	m.notify = true
	return m
}

func TestNotifiesEachTransitionOnceInOneNotification(t *testing.T) {
	m := notifyModel(t, "")
	ready, failing, changes := changePR(1, "acme/a"), changePR(2, "acme/a"), changePR(3, "acme/b")
	review := changePR(9, "acme/b")
	if got := fetch(m, []github.PullRequest{ready, failing, changes}, nil); got != nil {
		t.Fatalf("first fetch notified: %q", got)
	}

	ready.MergeState = "CLEAN"
	failing.Checks = "FAILURE"
	changes.ReviewDecision = "CHANGES_REQUESTED"
	got := fetch(m, []github.PullRequest{ready, failing, changes}, []github.PullRequest{review})
	if len(got) != 1 {
		t.Fatalf("notifications = %q, want one for the refresh", got)
	}
	for _, want := range []string{"4 updates", "acme/a#1 ready to merge", "acme/a#2 failing CI", "+2 more"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("notification %q does not contain %q", got[0], want)
		}
	}
	if !strings.Contains(m.notice, "4 updates") {
		t.Errorf("status notice = %q, want the alert text", m.notice)
	}

	if got := fetch(m, []github.PullRequest{ready, failing, changes}, []github.PullRequest{review}); got != nil {
		t.Fatalf("unchanged refresh notified again: %q", got)
	}
}

func TestSingleAlertNamesThePullRequest(t *testing.T) {
	m := notifyModel(t, "")
	fetch(m, nil, nil)
	review := changePR(9, "acme/b")
	review.Title = "Fix\x1b]9;spoof\x07 it"
	got := fetch(m, nil, []github.PullRequest{review})
	if len(got) != 1 || !strings.Contains(got[0], "acme/b#9 review requested") {
		t.Fatalf("notifications = %q, want the review request", got)
	}
	if !strings.HasSuffix(got[0], "\x07\a") {
		t.Fatalf("notification %q does not end with the terminator and a bell", got[0])
	}
	if body := strings.TrimSuffix(strings.TrimPrefix(got[0], "\x1b]9;"), "\x07\a"); strings.ContainsAny(body, "\x1b\x07") {
		t.Fatalf("title control characters reached the notification: %q", got[0])
	}
}

func TestNoAlertsForNewAuthoredPRsUnknownStatesScopeOrWhenOff(t *testing.T) {
	m := notifyModel(t, "acme/a")
	blocked, outside := changePR(1, "acme/a"), changePR(2, "acme/b")
	fetch(m, []github.PullRequest{blocked, outside}, nil)

	// GitHub forgets the merge state, then reports it again unchanged.
	blocked.MergeState = "UNKNOWN"
	fetch(m, []github.PullRequest{blocked, outside}, nil)
	blocked.MergeState = "BLOCKED"
	if got := fetch(m, []github.PullRequest{blocked, outside}, nil); got != nil {
		t.Fatalf("recomputed state notified: %q", got)
	}

	// Ready after an unknown state still alerts: the last known state was
	// blocked.
	blocked.MergeState = "UNKNOWN"
	fetch(m, []github.PullRequest{blocked, outside}, nil)
	blocked.MergeState = "CLEAN"
	if got := fetch(m, []github.PullRequest{blocked, outside}, nil); len(got) != 1 {
		t.Fatalf("blocked → unknown → clean notifications = %q, want one", got)
	}
	// Ready, then unknown, then ready again is the same state.
	blocked.MergeState = "UNKNOWN"
	fetch(m, []github.PullRequest{blocked, outside}, nil)
	blocked.MergeState = "CLEAN"
	if got := fetch(m, []github.PullRequest{blocked, outside}, nil); got != nil {
		t.Fatalf("clean → unknown → clean notified: %q", got)
	}

	outside.Checks = "FAILURE"
	fresh := changePR(3, "acme/a")
	fresh.Checks, fresh.MergeState = "FAILURE", "CLEAN"
	if got := fetch(m, []github.PullRequest{blocked, outside, fresh}, []github.PullRequest{changePR(9, "acme/b")}); got != nil {
		t.Fatalf("out-of-scope or new authored PRs notified: %q", got)
	}

	m.notify = false
	blocked.Checks = "FAILURE"
	if got := fetch(m, []github.PullRequest{blocked, outside, fresh}, nil); got != nil {
		t.Fatalf("notified while off: %q", got)
	}
}

func TestAccountSwitchAndFailedFetchDoNotAlert(t *testing.T) {
	m := notifyModel(t, "")
	pr := changePR(1, "acme/a")
	fetch(m, []github.PullRequest{pr}, nil)
	m.Update(fetchFinishedMsg{err: errors.New("offline")})
	pr.Checks = "FAILURE"
	if got := fetch(m, []github.PullRequest{pr}, nil); len(got) != 1 {
		t.Fatalf("after a failed fetch notifications = %q, want the failing CI against the last good lists", got)
	}

	if err := m.preferences.Save("bob", ""); err != nil {
		t.Fatal(err)
	}
	_, cmd := m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "bob",
		PullRequests: []github.PullRequest{changePR(5, "acme/a")}, ReviewRequests: []github.PullRequest{changePR(6, "acme/a")}}})
	if got := notifications(cmd); got != nil {
		t.Fatalf("account switch notified: %q", got)
	}
}

func TestNotifyKeyTogglesAndTitleShowsIt(t *testing.T) {
	m := changesModel(t, changePR(1, "acme/a"))
	press(m, tea.Key{Code: 'n', Text: "n"})
	if !m.notify || !strings.Contains(m.listLines()[0], "notify") {
		t.Fatalf("n did not turn notifications on: %t %q", m.notify, m.listLines()[0])
	}
	press(m, tea.Key{Code: 'n', Text: "n"})
	if m.notify || strings.Contains(m.listLines()[0], "notify") {
		t.Fatalf("n did not turn notifications off: %t %q", m.notify, m.listLines()[0])
	}
}

func TestDesktopNotifierReplacesOSC9AndFallsBackWhenItFails(t *testing.T) {
	m := notifyModel(t, "")
	var posted []string
	fail := false
	m.desktopNotify = func(_ context.Context, title, body string) error {
		posted = append(posted, title+": "+body)
		if fail {
			return errors.New("not allowed")
		}
		return nil
	}
	fetch(m, nil, nil)
	_, cmd := m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: []github.PullRequest{changePR(9, "acme/b")}}})
	raw, msgs := run(cmd)
	if len(posted) != 1 || !strings.Contains(posted[0], "acme/b#9 review requested") {
		t.Fatalf("desktop notifications = %q, want the review request", posted)
	}
	if !slices.Equal(raw, []string{"\a"}) || len(msgs) != 0 {
		t.Fatalf("terminal output = %q, messages %v; want only a bell", raw, msgs)
	}

	fail = true
	_, cmd = m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: []github.PullRequest{changePR(9, "acme/b"), changePR(8, "acme/b")}}})
	_, msgs = run(cmd)
	for _, msg := range msgs {
		m.Update(msg)
	}
	if m.desktopNotify != nil || !strings.Contains(m.notice, "not allowed") {
		t.Fatalf("failed desktop notification kept the notifier or hid the error: notice %q", m.notice)
	}
	_, cmd = m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: []github.PullRequest{changePR(9, "acme/b"), changePR(8, "acme/b"), changePR(7, "acme/b")}}})
	if got := notifications(cmd); len(got) != 1 {
		t.Fatalf("after the failure notifications = %q, want OSC 9", got)
	}
}

func TestARenewedReviewRequestAlertsAgain(t *testing.T) {
	m := notifyModel(t, "")
	review := changePR(9, "acme/b")
	review.WaitingSince = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	fetch(m, nil, []github.PullRequest{review})
	if got := fetch(m, nil, []github.PullRequest{review}); got != nil {
		t.Fatalf("an unchanged request notified: %q", got)
	}
	review.WaitingSince = review.WaitingSince.Add(time.Hour)
	got := fetch(m, nil, []github.PullRequest{review})
	if len(got) != 1 || !strings.Contains(got[0], "acme/b#9 review requested again") {
		t.Fatalf("notifications = %q, want the request made again", got)
	}
	if got := fetch(m, nil, []github.PullRequest{review}); got != nil {
		t.Fatalf("the renewed request notified twice: %q", got)
	}
}

func TestAReviewRequestedAgainAfterReviewingAlerts(t *testing.T) {
	m := notifyModel(t, "")
	review := changePR(9, "acme/b")
	review.ReviewStatus = github.ReviewNewCommits
	fetch(m, nil, []github.PullRequest{review})
	review.ReviewStatus = github.ReviewRequested
	got := fetch(m, nil, []github.PullRequest{review})
	if len(got) != 1 || !strings.Contains(got[0], "acme/b#9 review requested again") {
		t.Fatalf("notifications = %q, want the request made again", got)
	}
}
