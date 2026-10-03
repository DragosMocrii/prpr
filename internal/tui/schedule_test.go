package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/preferences"
	"github.com/DragosMocrii/prpr/internal/schedule"
)

func scheduleModelAt(t *testing.T, now time.Time, config schedule.Config, login string) (*model, *time.Time) {
	t.Helper()
	store := testPreferences(t)
	if err := store.SaveSchedule(config); err != nil {
		t.Fatal(err)
	}
	if login != "" {
		if err := store.Save(login, ""); err != nil {
			t.Fatal(err)
		}
	}
	m := newModel(context.Background(), nil, store)
	clock := now
	m.now = func() time.Time { return clock }
	m.Update(windowSize(140, 32))
	return m, &clock
}

func mondayAt(hour int) time.Time {
	day := time.Date(2026, time.October, 5, hour, 0, 0, 0, time.Local)
	for day.Weekday() != time.Monday {
		day = day.AddDate(0, 0, 1)
	}
	return day
}

func mondayHours(start, end string) schedule.Config {
	return schedule.Config{Enabled: true, Days: []string{"mon"}, Start: start, End: end}
}

func TestSleepingStartupAndOneHourWakeToggle(t *testing.T) {
	m, clock := scheduleModelAt(t, mondayAt(20), mondayHours("09:00", "17:00"), "")
	if !m.sleeping || m.loading || !strings.Contains(ansiStrip(m.View().Content), "Next wake:") {
		t.Fatalf("asleep startup state: sleeping %t loading %t view %q", m.sleeping, m.loading, m.View().Content)
	}

	press(m, tea.Key{Code: 'w', Text: "w"})
	if m.sleeping || !m.loading || !m.now().Add(time.Hour).Equal(m.manualWakeUntil) || m.fetchQuiet {
		t.Fatalf("one-hour wake: sleeping %t loading %t until %v quiet %t", m.sleeping, m.loading, m.manualWakeUntil, m.fetchQuiet)
	}
	oldGeneration := m.refreshGeneration
	press(m, tea.Key{Code: 'w', Text: "w"})
	if !m.sleeping || m.loading || !m.manualWakeUntil.IsZero() || m.refreshGeneration == oldGeneration {
		t.Fatalf("return to schedule: sleeping %t loading %t override %v generation %d", m.sleeping, m.loading, m.manualWakeUntil, m.refreshGeneration)
	}
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "late"}, generation: oldGeneration, account: m.accountGeneration})
	if m.snapshot.Login != "" || !m.sleeping {
		t.Fatalf("cancelled fetch applied after sleep: login %q sleeping %t", m.snapshot.Login, m.sleeping)
	}

	// No wall-clock sleep: advance the injected clock to the schedule boundary.
	*clock = mondayAt(9).AddDate(0, 0, 1)
	m.Update(windowSize(140, 32))
	if !m.sleeping || m.loading {
		t.Fatalf("outside-hours clock adjustment: sleeping %t loading %t", m.sleeping, m.loading)
	}
}
func TestExpiredWakeRemainsActiveInsideScheduledWindow(t *testing.T) {
	m, clock := scheduleModelAt(t, mondayAt(8), mondayHours("09:00", "17:00"), "")
	if !m.sleeping {
		t.Fatal("startup outside active hours did not sleep")
	}
	press(m, tea.Key{Code: 'w', Text: "w"})
	until := m.manualWakeUntil
	*clock = mondayAt(9).Add(30 * time.Minute)
	m.Update(windowSize(140, 32))
	if m.sleeping || !m.manualWakeUntil.IsZero() || !m.loading || m.now().Before(until) {
		t.Fatalf("expired override inside active window: sleeping %t override %v loading %t", m.sleeping, m.manualWakeUntil, m.loading)
	}
}

func TestScheduledWakeWaitsForFirstScopeChoice(t *testing.T) {
	m, clock := scheduleModelAt(t, mondayAt(20), mondayHours("09:00", "17:00"), "")
	m.snapshot.Login, m.filterLogin = "alice", "alice"
	m.scopeChosen = false
	*clock = mondayAt(9).AddDate(0, 0, 7)
	m.Update(windowSize(140, 32))
	if m.sleeping || m.loading || !m.wakeFetchPending || !strings.Contains(ansiStrip(m.View().Content), "What would you like to watch?") {
		t.Fatalf("scheduled wake skipped the scope choice: sleeping %t loading %t pending %t view %q", m.sleeping, m.loading, m.wakeFetchPending, m.View().Content)
	}
	m.scopeChoiceCursor = 1
	press(m, tea.Key{Code: tea.KeyEnter})
	if !m.loading || m.wakeFetchPending {
		t.Fatalf("scope choice did not release the pending wake fetch: loading %t pending %t", m.loading, m.wakeFetchPending)
	}
}

func TestInvalidSavedScheduleFailsClosedButAllowsManualWake(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	data := []byte(`{"app":{"schedule":{"enabled":true,"days":["monday"],"start":"09:00","end":"17:00"}}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := preferences.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(context.Background(), nil, store)
	m.now = func() time.Time { return mondayAt(20) }
	m.Update(windowSize(140, 32))
	if m.scheduleErr == nil || !m.sleeping || m.pollingAllowed() {
		t.Fatalf("invalid saved schedule did not fail closed: error %v sleeping %t allowed %t", m.scheduleErr, m.sleeping, m.pollingAllowed())
	}
	press(m, tea.Key{Code: 'w', Text: "w"})
	if m.sleeping || m.manualWakeUntil.IsZero() || m.fetchQuiet {
		t.Fatalf("one-hour repair override unavailable: sleeping %t until %v quiet %t", m.sleeping, m.manualWakeUntil, m.fetchQuiet)
	}
}

func TestQuietRefreshKeepsSuccessfulSnapshotAndSuppressesAlerts(t *testing.T) {
	m, clock := scheduleModelAt(t, mondayAt(10), mondayHours("09:00", "17:00"), "alice")
	base := changePR(1, "acme/a")
	m.Init()
	updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: []github.PullRequest{base}}})
	m.notify = true
	*clock = mondayAt(20)
	m.Update(windowSize(140, 32))
	if !m.sleeping || len(m.snapshot.PullRequests) != 1 {
		t.Fatalf("sleep transition lost baseline: sleeping %t snapshot %+v", m.sleeping, m.snapshot)
	}
	quotaGeneration := m.quotaGeneration

	press(m, tea.Key{Code: 'r', Text: "r"})
	if !m.loading || !m.fetchQuiet || m.quotaRunning || m.quotaGeneration != quotaGeneration || !m.refreshDue.IsZero() {
		t.Fatalf("one-shot request state: loading %t quiet %t quota-running %t quota-generation %d refresh %v", m.loading, m.fetchQuiet, m.quotaRunning, m.quotaGeneration, m.refreshDue)
	}
	updated := base
	updated.Checks = "FAILURE"
	_, cmd := updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: []github.PullRequest{updated}, ReviewRequests: []github.PullRequest{changePR(9, "acme/b")}}})
	if got := notifications(cmd); len(got) != 0 {
		t.Fatalf("quiet refresh notified: %q", got)
	}
	if m.fetchQuiet || !m.sleeping || len(m.snapshot.ReviewRequests) != 1 {
		t.Fatalf("quiet success state: quiet %t sleeping %t reviews %d", m.fetchQuiet, m.sleeping, len(m.snapshot.ReviewRequests))
	}

	press(m, tea.Key{Code: 'r', Text: "r"})
	updateFetch(m, fetchFinishedMsg{err: errors.New("offline")})
	view := ansiStrip(m.View().Content)
	if !m.sleeping || m.err == nil || len(m.snapshot.PullRequests) != 1 || !strings.Contains(view, "Refresh failed") || !strings.Contains(view, updated.URL) {
		t.Fatalf("failed quiet refresh discarded or hid the last success: sleeping %t err %v snapshot %d view %q", m.sleeping, m.err, len(m.snapshot.PullRequests), view)
	}
}

func TestScheduledWakeCatchesUpWithGroupedAlert(t *testing.T) {
	m, clock := scheduleModelAt(t, mondayAt(10), mondayHours("09:00", "17:00"), "alice")
	m.Init()
	m.notify = true
	updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice"}})
	*clock = mondayAt(20)
	m.Update(windowSize(140, 32))
	if !m.sleeping {
		t.Fatal("did not enter sleep at the configured end")
	}

	// Advance directly to the next scheduled opening; tests never wait for a timer.
	*clock = mondayAt(9).AddDate(0, 0, 7)
	m.Update(windowSize(140, 32))
	if m.sleeping || !m.loading || m.fetchQuiet {
		t.Fatalf("scheduled wake did not start one full fetch: sleeping %t loading %t quiet %t", m.sleeping, m.loading, m.fetchQuiet)
	}
	one, two := changePR(9, "acme/b"), changePR(8, "acme/b")
	_, cmd := updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: []github.PullRequest{one, two}}})
	if got := notifications(cmd); len(got) != 1 || !strings.Contains(got[0], "2 updates") {
		t.Fatalf("scheduled catch-up alert = %q, notice %q, alerts %v, awake %t, quiet %t, active %t, notify %t, scope %t repo %q, activity deadline %v err %v now %v", got, m.notice, m.alerts(), !m.sleeping, m.fetchQuiet, m.pollingAllowed(), m.notify, m.scopeChosen, m.selectedRepository, m.activityDeadline, m.activityCtx.Err(), m.now())
	}
}

func TestScheduleEditorCancelsAndPersists(t *testing.T) {
	m, _ := scheduleModelAt(t, mondayAt(20), schedule.Default(), "")
	original := m.scheduleConfig
	pressKey(m, tea.Key{Code: 'S', Text: "S"})
	if m.scheduleEditor == nil || !strings.Contains(ansiStrip(m.scheduleEditor.form.View()), "Enable active hours") {
		t.Fatalf("S did not open the enable control: %q", m.View().Content)
	}
	pressKey(m, tea.Key{Code: tea.KeyEscape})
	if m.scheduleEditor != nil || original.Enabled != m.scheduleConfig.Enabled || original.Start != m.scheduleConfig.Start || original.End != m.scheduleConfig.End || !slices.Equal(original.Days, m.scheduleConfig.Days) {
		t.Fatal("Esc changed or kept the unsaved schedule")
	}
	active, _ := scheduleModelAt(t, mondayAt(20), mondayHours("09:00", "17:00"), "")
	pressKey(active, tea.Key{Code: 'S', Text: "S"})
	if active.scheduleEditor == nil || !strings.Contains(ansiStrip(active.scheduleEditor.form.View()), "Enable active hours") {
		t.Fatalf("enabled schedule hid the enable control: %q", active.View().Content)
	}
	active.loginActive = true // Keep the test form save from launching a GitHub request.
	pressKey(active, tea.Key{Code: 'n', Text: "n"})
	for range 6 {
		if active.scheduleEditor == nil {
			break
		}
		pressKey(active, tea.Key{Code: tea.KeyEnter})
	}
	if active.scheduleEditor != nil || active.scheduleConfig.Enabled || active.preferences.Schedule().Enabled || active.sleeping {
		t.Fatalf("form did not disable active hours immediately: editor %v config %+v sleeping %t", active.scheduleEditor != nil, active.scheduleConfig, active.sleeping)
	}

	pressKey(m, tea.Key{Code: 'S', Text: "S"})
	// Leave the default weekdays and clock values unchanged; toggle the saved
	// disabled default on through the actual confirm control.
	pressKey(m, tea.Key{Code: 'y', Text: "y"})
	for range 6 {
		if m.scheduleEditor == nil {
			break
		}
		pressKey(m, tea.Key{Code: tea.KeyEnter})
	}
	if m.scheduleEditor != nil {
		t.Fatalf("Enter did not complete the schedule form: state %v, form %q", m.scheduleEditor.form.State, ansiStrip(m.scheduleEditor.form.View()))
	}
	want := schedule.Default()
	want.Enabled = true
	if got := m.preferences.Schedule(); got.Enabled != want.Enabled || got.Start != want.Start || got.End != want.End || !slices.Equal(got.Days, want.Days) {
		t.Fatalf("persisted schedule = %+v, want %+v", got, want)
	}
	if !m.sleeping || m.scheduleErr != nil {
		t.Fatalf("saved schedule was not applied immediately: sleeping %t error %v", m.sleeping, m.scheduleErr)
	}
}

func ansiStrip(value string) string { return ansi.Strip(value) }

func TestSleepAfterFailedFetchShowsTheError(t *testing.T) {
	m, clock := scheduleModelAt(t, mondayAt(10), mondayHours("09:00", "17:00"), "alice")
	m.Init()
	updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: []github.PullRequest{changePR(1, "acme/a")}}})
	press(m, tea.Key{Code: 'r', Text: "r"})
	updateFetch(m, fetchFinishedMsg{err: errors.New("offline")})
	*clock = mondayAt(20)
	m.Update(windowSize(140, 32))
	if !m.sleeping || m.showingList() || m.detailsOpen() {
		t.Fatalf("cleared rows drawn as a list while asleep: sleeping %t list %t view %q", m.sleeping, m.showingList(), m.View().Content)
	}
}

func TestQuietFetchWakesSnoozesAlreadyPastSilently(t *testing.T) {
	m, clock := scheduleModelAt(t, mondayAt(20), mondayHours("09:00", "17:00"), "alice")
	pr := changePR(1, "acme/a")
	if err := m.preferences.SaveSnoozes("alice", []preferences.Snooze{{Repository: pr.Repository, Number: pr.Number, List: preferences.SnoozeMine, Until: mondayAt(12)}}); err != nil {
		t.Fatal(err)
	}
	m.Init()
	m.notify = true
	m.desktopNotify = nil
	press(m, tea.Key{Code: 'r', Text: "r"})
	if !m.fetchQuiet {
		t.Fatal("r while asleep did not start a quiet fetch")
	}
	updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: []github.PullRequest{pr}}})
	*clock = mondayAt(9).AddDate(0, 0, 7)
	m.Update(windowSize(140, 32))
	_, cmd := updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: []github.PullRequest{pr}}})
	if got := notifications(cmd); len(got) != 0 {
		t.Fatalf("a snooze past when it loaded alerted: %q", got)
	}
}
