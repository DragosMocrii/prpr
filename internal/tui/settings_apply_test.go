package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/DragosMocrii/prpr/internal/github"
)

func botPR(number int, bots ...github.BotReview) github.PullRequest {
	return github.PullRequest{Number: number, Repository: "acme/api", URL: "https://example.test/acme/api/pull/1", Bots: bots}
}

func TestBotsChangeFetchesAndTheResultMarksNothing(t *testing.T) {
	m := newPaneModel(t, 140, 40, []github.PullRequest{botPR(1)}, nil)
	before := m.refreshGeneration
	if _, err := m.applyBots(""); err != nil {
		t.Fatal(err)
	}
	if m.bots {
		t.Fatal("bots off still draws the Bots column")
	}
	if !m.loading || m.refreshGeneration == before || !m.settingsBaseline {
		t.Fatalf("no baseline fetch: loading %v generation %d→%d baseline %v", m.loading, before, m.refreshGeneration, m.settingsBaseline)
	}
	if m.preferences.BotsText() != "" {
		t.Fatalf("bots not saved: %q", m.preferences.BotsText())
	}
	changed := botPR(1, github.BotReview{Name: "Copilot"})
	changed.Title = "renamed"
	updateSnapshot(m, "alice", changed)
	if len(m.changes[paneMine].marks) != 0 || m.settingsBaseline {
		t.Fatalf("the baseline fetch marked %v (baseline still %v)", m.changes[paneMine].marks, m.settingsBaseline)
	}
	// The next fetch tracks changes again.
	changed.Title = "renamed again"
	updateSnapshot(m, "alice", changed)
	if len(m.changes[paneMine].marks) != 1 {
		t.Fatalf("the next fetch marked %d rows, want 1", len(m.changes[paneMine].marks))
	}
}

func TestBotsChangeDuringAFetchIsSilent(t *testing.T) {
	m := newPaneModel(t, 140, 40, []github.PullRequest{botPR(1)}, nil)
	m.startFetch()
	running := m.refreshGeneration
	if _, err := m.applyBots(""); err != nil {
		t.Fatal(err)
	}
	changed := botPR(1)
	changed.Title = "renamed"
	// The fetch that began with the old bots finishes: it is dropped.
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: []github.PullRequest{changed}},
		generation: running, account: m.accountGeneration})
	if !m.settingsBaseline {
		t.Fatal("the old fetch's result consumed the baseline")
	}
	updateSnapshot(m, "alice", changed)
	if len(m.changes[paneMine].marks) != 0 {
		t.Fatalf("the baseline fetch marked %v", m.changes[paneMine].marks)
	}
}

func TestBotsChangeBeforeTheFirstFetchStartsNothing(t *testing.T) {
	m := testModel(testPreferences(t), 120, 30)
	if _, err := m.applyBots(""); err != nil {
		t.Fatal(err)
	}
	if m.loading {
		t.Fatal("a bots change before any fetch started one")
	}
}

func TestInvalidBotsChangeNothing(t *testing.T) {
	m := newPaneModel(t, 140, 40, []github.PullRequest{botPR(1)}, nil)
	m.bots = true
	if _, err := m.applyBots("no-equals"); err == nil {
		t.Fatal("invalid bots accepted")
	}
	if !m.bots || m.loading || m.preferences.BotsText() != github.DefaultBots {
		t.Fatal("invalid bots changed something")
	}
}

func TestQueuesChangeIsSaved(t *testing.T) {
	m := newPaneModel(t, 140, 40, []github.PullRequest{botPR(1)}, nil)
	m.applyQueues(nil)
	if got := m.preferences.Queues(); len(got) != 0 || !m.settingsBaseline {
		t.Fatalf("queues %v baseline %v", got, m.settingsBaseline)
	}
}

func TestRefreshChangeReschedules(t *testing.T) {
	m := newPaneModel(t, 140, 40, []github.PullRequest{botPR(1)}, nil)
	before := m.refreshGeneration
	if cmd := m.applyRefresh(10 * time.Minute); cmd == nil || m.refreshGeneration == before {
		t.Fatalf("no new timer: cmd %v generation %d→%d", cmd != nil, before, m.refreshGeneration)
	}
	if m.refreshInterval != 10*time.Minute || m.preferences.Refresh() != 10*time.Minute {
		t.Fatal("refresh not applied or saved")
	}
	m.applyRefresh(0)
	if !m.refreshDue.IsZero() || strings.Contains(m.View().Content, "refresh in") {
		t.Fatal("refresh off still counts down")
	}
}

func TestTitleOffLeavesTheTerminalTitleAlone(t *testing.T) {
	m := newPaneModel(t, 140, 40, []github.PullRequest{botPR(1)}, nil)
	m.applyTitle(true)
	if view := m.View(); view.WindowTitle == "" || !view.ReportFocus {
		t.Fatal("title on draws no title")
	}
	m.flashText = "alert"
	m.applyTitle(false)
	if view := m.View(); view.WindowTitle != "" || view.ReportFocus || m.flashText != "" || m.preferences.Title() {
		t.Fatalf("title off: %q focus %v flash %q saved %v", view.WindowTitle, view.ReportFocus, m.flashText, m.preferences.Title())
	}
}

func TestShortcutsSaveMouseAndNotifications(t *testing.T) {
	m := newPaneModel(t, 140, 40, []github.PullRequest{botPR(1)}, nil)
	press(m, mouseKey)
	pressMsg(m, letter("n"))
	if !m.preferences.Mouse() || !m.preferences.Notify() {
		t.Fatalf("saved mouse %v notify %v", m.preferences.Mouse(), m.preferences.Notify())
	}
}
