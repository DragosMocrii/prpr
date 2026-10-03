package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

func autoRefreshModel(t *testing.T, interval time.Duration) *model {
	t.Helper()
	store := testPreferences(t)
	if err := store.Save("alice", ""); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 100, 24)
	m.refreshInterval = interval
	return m
}

func finishFetch(m *model, msg fetchFinishedMsg) tea.Cmd {
	msg.generation = m.refreshGeneration
	msg.account = m.accountGeneration
	_, cmd := m.Update(msg)
	return cmd
}

func aliceSnapshot() fetchFinishedMsg {
	return fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: []github.PullRequest{{Number: 1, Repository: "acme/a", Mergeable: "MERGEABLE", MergeState: "CLEAN"}}}}
}

func TestAutoRefreshTickStartsFetchAndManualRefreshResetsTimer(t *testing.T) {
	m := autoRefreshModel(t, time.Minute)
	m.startFetch()
	if cmd := finishFetch(m, aliceSnapshot()); cmd == nil {
		t.Fatal("finished fetch did not schedule an auto-refresh")
	}
	due := m.refreshGeneration
	m.Update(autoRefreshMsg{generation: due})
	if !m.refreshing() {
		t.Fatal("due auto-refresh tick did not start a refresh")
	}
	finishFetch(m, aliceSnapshot())
	stale := m.refreshGeneration
	press(m, tea.Key{Code: 'r', Text: "r"})
	finishFetch(m, aliceSnapshot())
	m.Update(autoRefreshMsg{generation: stale})
	if m.loading {
		t.Fatal("tick scheduled before a manual refresh still fired")
	}
	m.Update(autoRefreshMsg{generation: m.refreshGeneration})
	if !m.loading {
		t.Fatal("tick scheduled after the manual refresh did not fire")
	}
}

func TestAutoRefreshWaitsWhileThePickerIsOpen(t *testing.T) {
	m := autoRefreshModel(t, time.Minute)
	m.startFetch()
	finishFetch(m, aliceSnapshot())
	press(m, tea.Key{Code: 'p', Text: "p"})
	if m.picker == nil {
		t.Fatal("picker did not open")
	}
	_, cmd := m.Update(autoRefreshMsg{generation: m.refreshGeneration})
	if m.picker == nil || m.loading || cmd == nil {
		t.Fatalf("tick during picker: picker %v loading %t rescheduled %t", m.picker, m.loading, cmd != nil)
	}
}

func TestAutoRefreshWaitsWhileTheScopePromptIsShown(t *testing.T) {
	m := testModel(testPreferences(t), 100, 24)
	m.refreshInterval = time.Minute
	m.startFetch()
	finishFetch(m, aliceSnapshot())
	if m.scopeChosen {
		t.Fatal("fresh account skipped the scope prompt")
	}
	_, cmd := m.Update(autoRefreshMsg{generation: m.refreshGeneration})
	if m.loading || cmd == nil {
		t.Fatalf("tick during scope prompt: loading %t rescheduled %t", m.loading, cmd != nil)
	}
}

func TestAutoRefreshRetriesErrorsButNotAuthErrors(t *testing.T) {
	m := autoRefreshModel(t, time.Minute)
	m.startFetch()
	if cmd := finishFetch(m, fetchFinishedMsg{err: errors.New("offline")}); cmd == nil {
		t.Fatal("failed fetch did not schedule a retry")
	}
	m.Update(autoRefreshMsg{generation: m.refreshGeneration})
	if !m.loading {
		t.Fatal("retry tick did not start a fetch")
	}
	if cmd := finishFetch(m, fetchFinishedMsg{err: &github.AuthError{Err: errors.New("logged out")}}); cmd != nil {
		t.Fatal("auth failure scheduled an automatic retry")
	}
}

func TestAutoRefreshOffSchedulesNothing(t *testing.T) {
	m := autoRefreshModel(t, 0)
	m.startFetch()
	if cmd := finishFetch(m, aliceSnapshot()); cmd != nil {
		t.Fatal("disabled auto-refresh scheduled a tick")
	}
	if title := ansi.Strip(m.listLines()[0]); strings.Contains(title, "auto") {
		t.Fatalf("disabled auto-refresh shown in title: %q", title)
	}
}

func TestAutoRefreshIntervalShownInTitle(t *testing.T) {
	m := autoRefreshModel(t, 90*time.Second)
	m.startFetch()
	finishFetch(m, aliceSnapshot())
	if title := ansi.Strip(m.listLines()[0]); !strings.Contains(title, "auto 1m30s") {
		t.Fatalf("title = %q", title)
	}
}

func TestAutoRefreshTitleFitsNarrowTerminalsWhileRefreshing(t *testing.T) {
	store := testPreferences(t)
	repository := "some-organization/a-rather-long-repository-name"
	if err := store.Save("alice", repository); err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{40, 60} {
		m := testModel(store, width, 16)
		m.refreshInterval = 5 * time.Minute
		finishFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: []github.PullRequest{{Number: 1, Repository: repository}}}})
		assertBounded(t, m, width, 16)
		m.startFetch()
		if !m.refreshing() {
			t.Fatal("refresh indicator not shown")
		}
		assertBounded(t, m, width, 16)
	}
}

func titleOf(m *model) string {
	return ansi.Strip(m.listLines()[0])
}

func TestAutoRefreshCountdownShownAndResetByManualRefresh(t *testing.T) {
	m := autoRefreshModel(t, 5*time.Minute)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	m.startFetch()
	if title := titleOf(m); strings.Contains(title, "refresh in") {
		t.Fatalf("countdown shown while loading: %q", title)
	}
	finishFetch(m, aliceSnapshot())
	if title := titleOf(m); !strings.HasSuffix(title, "refresh in 5:00") || !strings.Contains(title, "auto 5m") {
		t.Fatalf("title after fetch = %q", title)
	}
	now = now.Add(28*time.Second + 500*time.Millisecond)
	if title := titleOf(m); !strings.HasSuffix(title, "refresh in 4:32") {
		t.Fatalf("title after 28.5s = %q", title)
	}
	press(m, tea.Key{Code: 'r', Text: "r"})
	if title := titleOf(m); strings.Contains(title, "refresh in") || !strings.Contains(title, "Refreshing") {
		t.Fatalf("title during manual refresh = %q", title)
	}
	finishFetch(m, aliceSnapshot())
	if title := titleOf(m); !strings.HasSuffix(title, "refresh in 5:00") {
		t.Fatalf("manual refresh did not reset the countdown: %q", title)
	}
}

func TestAutoRefreshCountdownHiddenWithoutPendingRefresh(t *testing.T) {
	m := autoRefreshModel(t, 0)
	m.startFetch()
	finishFetch(m, aliceSnapshot())
	if title := titleOf(m); strings.Contains(title, "refresh in") {
		t.Fatalf("countdown shown with auto-refresh off: %q", title)
	}

	m = autoRefreshModel(t, time.Minute)
	m.startFetch()
	finishFetch(m, aliceSnapshot())
	press(m, tea.Key{Code: 'r', Text: "r"})
	finishFetch(m, fetchFinishedMsg{err: &github.AuthError{Err: errors.New("logged out")}})
	if m.countdownText() != "" {
		t.Fatalf("countdown pending after an auth failure: %q", m.countdownText())
	}
}

func TestCountdownTicksStopWhenRescheduledOrDue(t *testing.T) {
	m := autoRefreshModel(t, time.Minute)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	m.startFetch()
	finishFetch(m, aliceSnapshot())
	old := m.countdownGeneration
	if _, cmd := m.Update(countdownTickMsg{generation: old}); cmd == nil {
		t.Fatal("current countdown tick did not schedule the next one")
	}
	press(m, tea.Key{Code: 'p', Text: "p"})
	m.Update(autoRefreshMsg{generation: m.refreshGeneration})
	if _, cmd := m.Update(countdownTickMsg{generation: old}); cmd != nil {
		t.Fatal("countdown tick from before a reschedule kept its chain alive")
	}
	now = now.Add(time.Minute)
	if _, cmd := m.Update(countdownTickMsg{generation: m.countdownGeneration}); cmd != nil {
		t.Fatal("countdown kept ticking once the refresh was due")
	}
}

func TestCountdownFitsNarrowTerminals(t *testing.T) {
	for _, width := range []int{40, 60} {
		m := autoRefreshModel(t, time.Hour+time.Minute)
		m.Update(tea.WindowSizeMsg{Width: width, Height: 16})
		m.startFetch()
		finishFetch(m, aliceSnapshot())
		lines := assertBounded(t, m, width, 16)
		if title := ansi.Strip(lines[0]); !strings.HasSuffix(title, "refresh in 1:01:00") {
			t.Fatalf("width %d title = %q", width, title)
		}
	}
}

func TestUnknownMergeStatesRefreshSoonerUntilKnown(t *testing.T) {
	m := autoRefreshModel(t, 5*time.Minute)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	unknown := aliceSnapshot()
	unknown.snapshot.PullRequests = []github.PullRequest{{Number: 1, Repository: "acme/a", Mergeable: "UNKNOWN", MergeState: "UNKNOWN"}}
	for _, want := range []time.Duration{15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute} {
		m.startFetch()
		finishFetch(m, unknown)
		if got := m.refreshDue.Sub(now); got != want {
			t.Fatalf("refresh in %v, want %v", got, want)
		}
	}
	// Known merge states go back to the interval, and an unknown one out of
	// scope does not count.
	m.startFetch()
	finishFetch(m, aliceSnapshot())
	if got := m.refreshDue.Sub(now); got != 5*time.Minute {
		t.Fatalf("refresh in %v once known", got)
	}
	m.chooseRepository("acme/b")
	m.startFetch()
	finishFetch(m, unknown)
	if got := m.refreshDue.Sub(now); got != 5*time.Minute {
		t.Fatalf("refresh in %v for an unknown state out of scope", got)
	}
}
