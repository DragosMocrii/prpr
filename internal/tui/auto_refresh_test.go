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
	_, cmd := m.Update(msg)
	return cmd
}

func aliceSnapshot() fetchFinishedMsg {
	return fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: []github.PullRequest{{Number: 1, Repository: "acme/a"}}}}
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
