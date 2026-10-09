package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/DragosMocrii/prpr/internal/github"
)

var watchDate = time.Date(2026, 10, 9, 4, 0, 0, 0, time.UTC)

// watchModel is a model whose first fetch succeeded, with Live updates on
// and the chain's baseline read answered.
func watchModel(t *testing.T, interval time.Duration) *model {
	t.Helper()
	m := autoRefreshModel(t, interval)
	m.live = true
	m.startFetch()
	finishFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice",
		PullRequests:   []github.PullRequest{{Number: 1, Repository: "acme/a", Mergeable: "MERGEABLE", MergeState: "CLEAN"}},
		ReviewRequests: []github.PullRequest{{Number: 7, Repository: "acme/b", Author: "bob"}},
	}})
	if m.watch.cancel == nil {
		t.Fatal("successful fetch did not start the watcher")
	}
	updateWatch(m, github.Notifications{Status: github.NotificationsChanged, Date: watchDate, LastModified: "lm0"})
	if !m.watch.since.Equal(watchDate) || m.loading {
		t.Fatalf("baseline: since %v loading %v", m.watch.since, m.loading)
	}
	return m
}

func updateWatch(m *model, n github.Notifications) tea.Cmd {
	_, cmd := m.Update(notificationsMsg{result: n, generation: m.watch.generation, account: m.accountGeneration, sentRefresh: m.refreshGeneration})
	return cmd
}

func changed(threads ...github.NotificationThread) github.Notifications {
	return github.Notifications{Status: github.NotificationsChanged, Date: watchDate.Add(time.Minute), LastModified: "lm1", Threads: threads}
}

func thread(reason, kind, repository string, number int) github.NotificationThread {
	return github.NotificationThread{Reason: reason, SubjectType: kind, Repository: repository, Number: number, UpdatedAt: watchDate.Add(30 * time.Second)}
}

func TestWatchFetchesForRelevantThreads(t *testing.T) {
	for _, tc := range []struct {
		name   string
		thread github.NotificationThread
		fetch  bool
	}{
		{"listed authored", thread("author", "PullRequest", "acme/a", 1), true},
		{"listed review, other case", thread("comment", "PullRequest", "Acme/B", 7), true},
		{"new review request", thread("review_requested", "PullRequest", "acme/c", 3), true},
		{"new mention", thread("mention", "PullRequest", "acme/c", 4), true},
		{"ci in an authored repository", thread("ci_activity", "CheckSuite", "ACME/a", 0), true},
		{"ci elsewhere", thread("ci_activity", "CheckSuite", "acme/c", 0), false},
		{"unlisted subscribed pull request", thread("subscribed", "PullRequest", "acme/c", 5), false},
		{"issue", thread("mention", "Issue", "acme/a", 9), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := watchModel(t, time.Minute)
			cmd := updateWatch(m, changed(tc.thread))
			if m.loading != tc.fetch || cmd == nil {
				t.Fatalf("fetch = %v, want %v (cmd %v)", m.loading, tc.fetch, cmd != nil)
			}
			if !m.watch.since.Equal(watchDate.Add(time.Minute)) || m.watch.lastModified != "lm1" || !m.watch.live {
				t.Fatalf("watch = %+v", m.watch)
			}
		})
	}
}

func TestWatchIgnoresThreadsOutOfScopeOrNotNewer(t *testing.T) {
	m := watchModel(t, time.Minute)
	m.applyRepository("acme/b")
	old := thread("review_requested", "PullRequest", "acme/b", 8)
	old.UpdatedAt = watchDate
	updateWatch(m, changed(thread("review_requested", "PullRequest", "acme/c", 3), thread("ci_activity", "CheckSuite", "acme/a", 0), old))
	if m.loading {
		t.Fatal("fetched for an out-of-scope or old thread")
	}
}

func TestWatchFetchesForAFullPage(t *testing.T) {
	m := watchModel(t, time.Minute)
	n := changed()
	n.Full = true
	updateWatch(m, n)
	if !m.loading {
		t.Fatal("full page did not fetch")
	}
}

func TestWatchNotModifiedKeepsSinceAndSchedulesTheNextRead(t *testing.T) {
	m := watchModel(t, time.Minute)
	cmd := updateWatch(m, github.Notifications{Status: github.NotificationsNotModified, Date: watchDate.Add(time.Hour), PollInterval: 90 * time.Second})
	if cmd == nil || m.loading || !m.watch.since.Equal(watchDate) || !m.watch.live {
		t.Fatalf("304: cmd %v loading %v watch %+v", cmd != nil, m.loading, m.watch)
	}
	if _, cmd := m.Update(watchTickMsg{generation: m.watch.generation, account: m.accountGeneration}); cmd == nil {
		t.Fatal("tick did not read")
	}
}

func TestWatchFetchesOnceAfterAFetchThatBeganBeforeTheRead(t *testing.T) {
	m := watchModel(t, time.Minute)
	m.startFetch()
	// Two relevant reads, both sent while this fetch ran.
	updateWatch(m, changed(thread("author", "PullRequest", "acme/a", 1)))
	updateWatch(m, changed(thread("author", "PullRequest", "acme/a", 1)))
	if !m.watch.pending {
		t.Fatal("relevant read during a fetch was not kept")
	}
	before := m.refreshGeneration
	finishFetch(m, aliceSnapshot())
	if !m.loading || m.refreshGeneration != before+1 || m.watch.pending {
		t.Fatalf("pending fetch: loading %v generation %d->%d pending %v", m.loading, before, m.refreshGeneration, m.watch.pending)
	}
	finishFetch(m, aliceSnapshot())
	if m.loading {
		t.Fatal("pending fetch ran twice")
	}
}

func TestWatchSkipsTheFollowUpWhenTheFetchBeganAfterTheRead(t *testing.T) {
	m := watchModel(t, time.Minute)
	sent := m.refreshGeneration
	m.startFetch()
	m.Update(notificationsMsg{result: changed(thread("author", "PullRequest", "acme/a", 1)),
		generation: m.watch.generation, account: m.accountGeneration, sentRefresh: sent})
	if m.watch.pending {
		t.Fatal("a fetch begun after the read was followed by another")
	}
}

func TestWatchDropsReadsFromAnOlderAccountOrChain(t *testing.T) {
	m := watchModel(t, time.Minute)
	generation, account := m.watch.generation, m.accountGeneration
	m.accountGeneration++
	m.Update(notificationsMsg{result: changed(thread("author", "PullRequest", "acme/a", 1)), generation: generation, account: account, sentRefresh: m.refreshGeneration})
	if m.loading {
		t.Fatal("older account's read fetched")
	}
	m.accountGeneration = account
	m.Update(notificationsMsg{result: changed(thread("author", "PullRequest", "acme/a", 1)), generation: generation - 1, account: account, sentRefresh: m.refreshGeneration})
	if m.loading {
		t.Fatal("older chain's read fetched")
	}
}

func TestWatchAccountChoiceResetsTheChain(t *testing.T) {
	m := watchModel(t, time.Minute)
	// newModel leaves useAccount unset; New points it at the client.
	m.useAccount = func(string) {}
	m.chooseAccount("carol")
	if m.watch.cancel != nil || !m.watch.since.IsZero() || m.watch.lastModified != "" || m.watch.live {
		t.Fatalf("watch after account choice = %+v", m.watch)
	}
}

func TestWatchBacksOffAndCapsAtTheRefreshInterval(t *testing.T) {
	for _, tc := range []struct {
		interval, cap time.Duration
	}{{5 * time.Minute, 5 * time.Minute}, {0, watchMaxBackoff}} {
		m := watchModel(t, tc.interval)
		var last time.Duration
		for range 10 {
			updateWatch(m, github.Notifications{Status: github.NotificationsTransient, Err: errors.New("offline")})
			if m.watch.backoff < last || m.watch.backoff > tc.cap {
				t.Fatalf("interval %v: backoff %v after %v", tc.interval, m.watch.backoff, last)
			}
			last = m.watch.backoff
		}
		if last != tc.cap || m.watch.live {
			t.Fatalf("interval %v: backoff %v live %v", tc.interval, last, m.watch.live)
		}
		updateWatch(m, github.Notifications{Status: github.NotificationsNotModified})
		if m.watch.backoff != 0 || !m.watch.live {
			t.Fatal("answered read did not end the backoff")
		}
		updateWatch(m, changed(thread("author", "PullRequest", "acme/a", 1)))
		if !m.loading {
			t.Fatalf("interval %v: relevant read did not fetch", tc.interval)
		}
	}
}

func TestWatchStopsWhenUnsupportedUntilReset(t *testing.T) {
	m := watchModel(t, time.Minute)
	if cmd := updateWatch(m, github.Notifications{Status: github.NotificationsUnsupported}); cmd != nil {
		t.Fatal("unsupported read scheduled another")
	}
	if !m.watch.unsupported || m.watch.cancel != nil || m.watch.live {
		t.Fatalf("watch = %+v", m.watch)
	}
	if cmd := finishFetch(m, aliceSnapshot()); m.watch.cancel != nil {
		t.Fatalf("fetch restarted an unsupported chain (cmd %v)", cmd != nil)
	}
	m.resetWatch()
	m.pollWatch()
	if m.watch.cancel == nil {
		t.Fatal("reset chain did not start")
	}
}

func TestWatchPausesWhileBlockedAndResumesAfterAFetch(t *testing.T) {
	m := watchModel(t, time.Minute)
	m.loginActive = true
	if cmd := updateWatch(m, changed(thread("author", "PullRequest", "acme/a", 1))); cmd != nil || !m.watch.paused || m.watch.cancel != nil {
		t.Fatal("watcher read during login")
	}
	m.Update(loginFinishedMsg{})
	finishFetch(m, aliceSnapshot())
	if m.watch.paused || m.watch.cancel == nil {
		t.Fatal("successful fetch did not resume the watcher")
	}
}

func TestCountdownMarksLiveOnlyWhileHealthy(t *testing.T) {
	m := watchModel(t, time.Minute)
	m.scheduleAutoRefresh()
	if !strings.HasSuffix(m.countdownText(), " · live") {
		t.Fatalf("countdown = %q", m.countdownText())
	}
	updateWatch(m, github.Notifications{Status: github.NotificationsTransient, Err: errors.New("offline")})
	if strings.Contains(m.countdownText(), "live") {
		t.Fatalf("countdown while backing off = %q", m.countdownText())
	}
}
