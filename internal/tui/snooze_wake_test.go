package tui

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/preferences"
)

func snoozedModel(t *testing.T, until time.Time, list string, number int) (*model, []github.PullRequest, []github.PullRequest) {
	t.Helper()
	m, mine, review, _, _ := snoozedModelFile(t, until, list, number)
	return m, mine, review
}

// snoozedModelFile is snoozedModel that also returns the preferences file's
// path and its bytes just before the first fetch.
func snoozedModelFile(t *testing.T, until time.Time, list string, number int) (*model, []github.PullRequest, []github.PullRequest, string, []byte) {
	t.Helper()
	mine, review := snoozePRs()
	path := filepath.Join(t.TempDir(), "preferences.json")
	store, err := preferences.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("alice", ""); err != nil {
		t.Fatal(err)
	}
	repository := "acme/api"
	if list == preferences.SnoozeReview {
		repository = "acme/web"
	}
	if err := store.SaveSnoozes("alice", []preferences.Snooze{{Repository: repository, Number: number, List: list, Until: until}}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 140, 40)
	m.now = func() time.Time { return snoozeNow }
	m.notify = true
	m.desktopNotify = nil
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine, ReviewRequests: review}})
	return m, mine, review, path, before
}

func TestSavedSnoozesLoadWithoutWriting(t *testing.T) {
	m, _, _, path, before := snoozedModelFile(t, snoozeNow.Add(time.Hour), preferences.SnoozeMine, 2)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("restoring wrote preferences:\n%s\nthen\n%s", before, after)
	}
	if got := paneNumbers(m, paneSnoozed); len(got) != 1 || got[0] != 2 {
		t.Fatalf("Snoozed = %v", got)
	}
	if saved := m.preferences.Snoozes("alice"); len(saved) != 1 {
		t.Fatalf("saved = %+v", saved)
	}
}

func TestFirstFetchWakesSilently(t *testing.T) {
	m, _, _ := snoozedModel(t, snoozeNow.Add(-time.Minute), preferences.SnoozeMine, 2)
	if len(m.snoozes) != 0 {
		t.Fatal("an expired snooze stayed")
	}
	if m.woke[prKey{"acme/api", 2}] != "snooze ended" {
		t.Fatalf("woke = %v", m.woke)
	}
	if m.notice != "" {
		t.Fatalf("first fetch notified: %q", m.notice)
	}
}

func TestFetchWakeAlertsOnce(t *testing.T) {
	m, mine, review := snoozedModel(t, snoozeNow.Add(24*time.Hour), preferences.SnoozeMine, 2)
	changed := append([]github.PullRequest(nil), mine...)
	changed[1].ReviewDecision = "CHANGES_REQUESTED"
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: changed, ReviewRequests: review}})
	if len(m.snoozes) != 0 || m.woke[prKey{"acme/api", 2}] != "changes requested" {
		t.Fatalf("snoozes %v, woke %v", m.snoozes, m.woke)
	}
	if !strings.Contains(m.notice, "woke: changes requested") || strings.Count(m.notice, "acme/api#2") != 1 {
		t.Fatalf("notice = %q, want one wake alert", m.notice)
	}
}

func TestSnoozedRowsDoNotAlert(t *testing.T) {
	m, mine, review := snoozedModel(t, snoozeNow.Add(24*time.Hour), preferences.SnoozeMine, 2)
	m.notice = ""
	failing := append([]github.PullRequest(nil), mine...)
	failing[1].Checks = "FAILURE"
	// What the snooze saw already held failing CI, so this is no wake.
	s := m.snoozes[prKey{"acme/api", 2}]
	s.Seen = []string{signalFailing}
	m.snoozes[prKey{"acme/api", 2}] = s
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: failing, ReviewRequests: review}})
	if m.notice != "" || len(m.snoozes) != 1 {
		t.Fatalf("a snoozed pull request alerted or woke: notice %q, snoozes %d", m.notice, len(m.snoozes))
	}
}

func TestClosedSnoozeIsGoneInTheSnoozedPane(t *testing.T) {
	m, mine, review := snoozedModel(t, snoozeNow.Add(24*time.Hour), preferences.SnoozeMine, 2)
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine[:1], ReviewRequests: review}})
	pane := &m.panes[paneSnoozed]
	if len(pane.visible) != 0 || len(pane.gone) != 1 {
		t.Fatalf("Snoozed visible %v gone %v, want one gone row", pane.visible, pane.gone)
	}
	if len(m.panes[paneMine].gone) != 0 {
		t.Fatal("the closed snooze is a gone row in My PRs")
	}
	if len(m.preferences.Snoozes("alice")) != 0 {
		t.Fatal("the closed snooze is still saved")
	}
}

func TestPreviewNeverWakesOrRecords(t *testing.T) {
	m, mine, review := snoozedModel(t, snoozeNow.Add(24*time.Hour), preferences.SnoozeMine, 2)
	before := m.preferences.Snoozes("alice")
	changed := append([]github.PullRequest(nil), mine...)
	changed[1].ReviewDecision = "CHANGES_REQUESTED"
	m.applySnapshot(github.Snapshot{Login: "alice", Preview: true, PullRequests: changed, ReviewRequests: review})
	after := m.preferences.Snoozes("alice")
	if len(m.snoozes) != 1 || len(after) != 1 || !slices.Equal(after[0].Seen, before[0].Seen) {
		t.Fatal("a preview woke or changed a snooze")
	}
}

func TestTimerWakesAndDropsStaleTicks(t *testing.T) {
	m, _, _ := snoozedModel(t, snoozeNow.Add(time.Hour), preferences.SnoozeMine, 2)
	stale := m.snoozeGeneration
	m.scheduleSnoozeTick()
	m.now = func() time.Time { return snoozeNow.Add(2 * time.Hour) }
	m.Update(snoozeTickMsg{generation: stale})
	if len(m.snoozes) != 1 {
		t.Fatal("a stale tick woke a snooze")
	}
	m.Update(snoozeTickMsg{generation: m.snoozeGeneration})
	if len(m.snoozes) != 0 || m.woke[prKey{"acme/api", 2}] != "snooze ended" {
		t.Fatalf("timer did not wake: %v", m.woke)
	}
	if !strings.Contains(m.notice, "snooze ended") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestTimerWakeAfterAFailedFetchWaitsForTheNextFetch(t *testing.T) {
	m, mine, review := snoozedModel(t, snoozeNow.Add(time.Hour), preferences.SnoozeMine, 2)
	m.Update(fetchFinishedMsg{err: errors.New("network down")})
	m.now = func() time.Time { return snoozeNow.Add(2 * time.Hour) }
	m.Update(snoozeTickMsg{generation: m.snoozeGeneration})
	if len(m.snoozes) != 1 {
		t.Fatal("timer woke with no row to alert for")
	}
	if saved := m.preferences.Snoozes("alice"); len(saved) != 1 {
		t.Fatalf("alice has %+v saved, want the snooze kept", saved)
	}
	m.notice = ""
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine, ReviewRequests: review}})
	if len(m.snoozes) != 0 || m.woke[prKey{"acme/api", 2}] != "snooze ended" {
		t.Fatalf("snoozes %v, woke %v", m.snoozes, m.woke)
	}
	if !strings.Contains(m.notice, "snooze ended") {
		t.Fatalf("notice = %q, want the wake alert", m.notice)
	}
	if saved := m.preferences.Snoozes("alice"); len(saved) != 0 {
		t.Fatalf("alice still has %+v saved", saved)
	}
}

func TestSnoozeTickDelay(t *testing.T) {
	due := snoozeNow.Add(-time.Minute)
	if got := snoozeTickDelay(due, snoozeNow, false); got != 0 {
		t.Fatalf("due snooze delay = %v, want 0", got)
	}
	if got := snoozeTickDelay(snoozeNow.Add(time.Hour), snoozeNow, false); got != snoozeCheckInterval {
		t.Fatalf("far snooze delay = %v, want %v", got, snoozeCheckInterval)
	}
	if got := snoozeTickDelay(due, snoozeNow, true); got != snoozeCheckInterval {
		t.Fatalf("waiting delay = %v, want %v", got, snoozeCheckInterval)
	}
}

func TestTickWhileAFetchFailedSchedulesTheNextTickAndDropsOlderOnes(t *testing.T) {
	m, _, _ := snoozedModel(t, snoozeNow.Add(time.Hour), preferences.SnoozeMine, 2)
	m.Update(fetchFinishedMsg{err: errors.New("network down")})
	m.now = func() time.Time { return snoozeNow.Add(2 * time.Hour) }
	before := m.snoozeGeneration
	if cmd := m.handleSnoozeTick(snoozeTickMsg{generation: before}); cmd == nil {
		t.Fatal("no next tick scheduled")
	}
	if m.snoozeGeneration != before+1 {
		t.Fatalf("generation = %d, want %d", m.snoozeGeneration, before+1)
	}
}

func TestReturningSnoozedReviewRequestAlertsAgain(t *testing.T) {
	m, mine, review := snoozedModel(t, snoozeNow.Add(24*time.Hour), preferences.SnoozeMine, 2)
	snoozeFor(m, preferences.Snooze{Repository: "acme/web", Number: 7, List: preferences.SnoozeReview,
		Until: snoozeNow.Add(24 * time.Hour), Seen: []string{signalRequested}})
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine, ReviewRequests: review}})
	if len(m.snoozes) != 2 {
		t.Fatalf("snoozes = %v, want both kept", m.snoozes)
	}
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine}})
	if _, ok := m.snoozeClosed[prKey{"acme/web", 7}]; !ok {
		t.Fatal("the snooze did not close")
	}
	m.notice = ""
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine, ReviewRequests: review}})
	if !strings.Contains(m.notice, "review requested") {
		t.Fatalf("notice = %q, want the arrival alert", m.notice)
	}
}

func TestSnoozeKeepsReadyWhileMergeStateIsUnknown(t *testing.T) {
	m, mine, review := snoozedModel(t, snoozeNow.Add(24*time.Hour), preferences.SnoozeMine, 2)
	key := prKey{"acme/api", 2}
	delete(m.snoozes, key)
	unknown := append([]github.PullRequest(nil), mine...)
	unknown[1].MergeState = ""
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: unknown, ReviewRequests: review}})
	m.readiness[key] = true
	m.snooze(key, listAuthored, snoozeNow.Add(24*time.Hour), false)
	if !slices.Contains(m.snoozes[key].Seen, signalReady) {
		t.Fatalf("Seen = %v, want ready kept", m.snoozes[key].Seen)
	}
	clean := append([]github.PullRequest(nil), mine...)
	clean[1].MergeState = "CLEAN"
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: clean, ReviewRequests: review}})
	if len(m.snoozes) != 1 {
		t.Fatalf("woke on a state it had already seen: %v", m.woke)
	}
}

func TestTimerWakeCarriesTheSelectionOutOfSnoozed(t *testing.T) {
	m, _, _ := snoozedModel(t, snoozeNow.Add(time.Hour), preferences.SnoozeMine, 2)
	m.setFocus(paneSnoozed)
	m.now = func() time.Time { return snoozeNow.Add(2 * time.Hour) }
	m.Update(snoozeTickMsg{generation: m.snoozeGeneration})
	pr, ok := m.paneSelectedPR(m.focus)
	if m.focus != paneMine || !ok || pr.Number != 2 {
		t.Fatalf("focus %v on %+v, want My PRs on #2", m.focus, pr)
	}
}

func TestReopenedSnoozeLeavesTheSnoozedPane(t *testing.T) {
	m, mine, review := snoozedModel(t, snoozeNow.Add(24*time.Hour), preferences.SnoozeMine, 2)
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine[:1], ReviewRequests: review}})
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine, ReviewRequests: review}})
	if len(m.snoozeClosed) != 0 {
		t.Fatalf("closed = %v", m.snoozeClosed)
	}
	if got := paneNumbers(m, paneMine); !slices.Contains(got, 2) {
		t.Fatalf("My PRs = %v, want the reopened #2", got)
	}
}

func TestSnoozeSavesKeepOtherWarnings(t *testing.T) {
	m, mine, review := snoozedModel(t, snoozeNow.Add(24*time.Hour), preferences.SnoozeMine, 2)
	other := errors.New("Rules not read")
	m.preferenceErr = other
	failing := append([]github.PullRequest(nil), mine...)
	failing[1].ReviewDecision = "APPROVED"
	s := m.snoozes[prKey{"acme/api", 2}]
	s.Seen = []string{signalApproved, signalFailing}
	m.snoozes[prKey{"acme/api", 2}] = s
	// Checks are now known passing, so Seen drops failing and saves.
	failing[1].Checks = "SUCCESS"
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: failing, ReviewRequests: review}})
	if saved := m.preferences.Snoozes("alice"); len(saved) != 1 || !slices.Equal(saved[0].Seen, []string{signalApproved}) {
		t.Fatalf("saved = %+v, want Seen [approved]", saved)
	}
	if m.preferenceErr != other {
		t.Fatalf("preferenceErr = %v, want the earlier warning kept", m.preferenceErr)
	}
}

func TestUnreadableSnoozesWarnEvenWhenLoadWakes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	body := `{"github.com/alice":{"repository":"","snoozed":[` +
		`{"repository":"acme/api","number":2,"list":"mine","until":"2026-10-01T09:00:00Z"},` +
		`{"repository":"acme/api","number":4,"list":"elsewhere","until":"2026-10-06T09:00:00Z"}]}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := preferences.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mine, review := snoozePRs()
	m := testModel(store, 140, 40)
	m.now = func() time.Time { return snoozeNow }
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine, ReviewRequests: review}})
	if m.woke[prKey{"acme/api", 2}] != "snooze ended" {
		t.Fatalf("woke = %v", m.woke)
	}
	if m.preferenceErr == nil || strings.Contains(m.preferenceErr.Error(), "\n") {
		t.Fatalf("preferenceErr = %q, want a single-line warning", m.preferenceErr)
	}
}

func TestWakeAlertsFollowFetchOrder(t *testing.T) {
	for range 20 {
		m, _, _ := snoozedModel(t, snoozeNow.Add(time.Hour), preferences.SnoozeMine, 2)
		snoozeFor(m,
			preferences.Snooze{Repository: "acme/web", Number: 7, List: preferences.SnoozeReview,
				Until: snoozeNow.Add(time.Hour), Seen: []string{signalRequested}},
			preferences.Snooze{Repository: "acme/api", Number: 1, List: preferences.SnoozeMine, Until: snoozeNow.Add(time.Hour)})
		m.now = func() time.Time { return snoozeNow.Add(2 * time.Hour) }
		m.Update(snoozeTickMsg{generation: m.snoozeGeneration})
		first, second, web := strings.Index(m.notice, "acme/api#1"), strings.Index(m.notice, "acme/api#2"), strings.Index(m.notice, "+1 more")
		if first < 0 || second < first || web < second {
			t.Fatalf("notice = %q, want #1, #2, then the review row", m.notice)
		}
	}
}

func TestAccountsKeepTheirOwnSnoozes(t *testing.T) {
	m, mine, review := snoozedModel(t, snoozeNow.Add(time.Hour), preferences.SnoozeMine, 2)
	if err := m.preferences.Save("bob", ""); err != nil {
		t.Fatal(err)
	}
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "bob", PullRequests: mine, ReviewRequests: review}})
	if len(m.snoozes) != 0 || len(m.panes[paneSnoozed].visible) != 0 {
		t.Fatal("bob sees alice's snoozes")
	}
}

func TestAnUrgentNudgeWakeFlashIsLasting(t *testing.T) {
	m, mine, review := snoozedModel(t, snoozeNow.Add(24*time.Hour), preferences.SnoozeReview, 7)
	m.setTitle = true
	// The pending request is already seen, so only the nudge can wake it.
	key := prKey{"acme/web", 7}
	held, _ := m.snoozeSignals(listReview, &review[0])
	m.snoozes[key] = preferences.Snooze{Repository: "acme/web", Number: 7, List: preferences.SnoozeReview,
		Until: snoozeNow.Add(24 * time.Hour), Seen: held}
	delete(m.woke, key)
	nudged := append([]github.PullRequest(nil), review...)
	nudged[0].Nudge = &github.Nudge{Urgency: github.NudgeUrgent, At: snoozeNow}
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine, ReviewRequests: nudged}})
	if m.woke[prKey{"acme/web", 7}] != "nudged" {
		t.Fatalf("woke = %v", m.woke)
	}
	if m.flashText == "" || !m.flashUntil.IsZero() {
		t.Fatalf("urgent wake flash: text %q until %v, want lasting", m.flashText, m.flashUntil)
	}
}
