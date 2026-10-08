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
	_, cmd := updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine, ReviewRequests: reviews}})
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
	updateFetch(m, fetchFinishedMsg{err: errors.New("offline")})
	pr.Checks = "FAILURE"
	if got := fetch(m, []github.PullRequest{pr}, nil); len(got) != 1 {
		t.Fatalf("after a failed fetch notifications = %q, want the failing CI against the last good lists", got)
	}

	if err := m.preferences.Save("bob", ""); err != nil {
		t.Fatal(err)
	}
	_, cmd := updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "bob",
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
	_, cmd := updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: []github.PullRequest{changePR(9, "acme/b")}}})
	raw, msgs := run(cmd)
	if len(posted) != 1 || !strings.Contains(posted[0], "acme/b#9 review requested") {
		t.Fatalf("desktop notifications = %q, want the review request", posted)
	}
	if !slices.Equal(raw, []string{"\a"}) || len(msgs) != 0 {
		t.Fatalf("terminal output = %q, messages %v; want only a bell", raw, msgs)
	}

	fail = true
	_, cmd = updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: []github.PullRequest{changePR(9, "acme/b"), changePR(8, "acme/b")}}})
	_, msgs = run(cmd)
	for _, msg := range msgs {
		m.Update(msg)
	}
	if m.desktopNotify != nil || !strings.Contains(m.notice, "not allowed") {
		t.Fatalf("failed desktop notification kept the notifier or hid the error: notice %q", m.notice)
	}
	_, cmd = updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: []github.PullRequest{changePR(9, "acme/b"), changePR(8, "acme/b"), changePR(7, "acme/b")}}})
	if got := notifications(cmd); len(got) != 1 {
		t.Fatalf("after the failure notifications = %q, want OSC 9", got)
	}
}

func nudgedReview(u github.NudgeUrgency, at time.Time) github.PullRequest {
	pr := changePR(9, "acme/b")
	pr.ReviewStatus = github.ReviewApproved
	if u != 0 {
		pr.Nudge = &github.Nudge{Urgency: u, At: at}
	}
	return pr
}

func TestNudgesAlertByUrgency(t *testing.T) {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	m := notifyModel(t, "")
	fetch(m, nil, []github.PullRequest{nudgedReview(0, at)})
	if got := fetch(m, nil, []github.PullRequest{nudgedReview(github.NudgeLow, at)}); got != nil {
		t.Fatalf("a low nudge notified: %q", got)
	}
	got := fetch(m, nil, []github.PullRequest{nudgedReview(github.NudgeNormal, at)})
	if len(got) != 1 || !strings.Contains(got[0], "acme/b#9") {
		t.Fatalf("raising a nudge to normal = %q, want one notification", got)
	}
	if got := fetch(m, nil, []github.PullRequest{nudgedReview(github.NudgeNormal, at)}); got != nil {
		t.Fatalf("the same nudge notified twice: %q", got)
	}
	if got := fetch(m, nil, []github.PullRequest{nudgedReview(github.NudgeNormal, at.Add(time.Hour))}); len(got) != 1 {
		t.Fatalf("a later nudge = %q, want a notification", got)
	}
	if got := fetch(m, nil, []github.PullRequest{nudgedReview(github.NudgeUrgent, at.Add(time.Hour))}); len(got) != 1 {
		t.Fatalf("a raised urgency = %q, want a notification", got)
	}
}

func TestALowNudgeNeverAlertsEvenOnArrival(t *testing.T) {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	m := notifyModel(t, "")
	fetch(m, nil, nil)
	mention := changePR(10, "acme/b")
	mention.ReviewStatus, mention.Nudge = github.ReviewNudged, &github.Nudge{Urgency: github.NudgeLow, At: at}
	if got := fetch(m, nil, []github.PullRequest{mention}); got != nil {
		t.Fatalf("a mentions-only row with a low nudge notified: %q", got)
	}
	mention.Nudge = &github.Nudge{Urgency: github.NudgeNormal, At: at.Add(time.Hour)}
	if got := fetch(m, nil, []github.PullRequest{mention}); len(got) != 1 {
		t.Fatalf("raising it to normal = %q, want a notification", got)
	}
}

func TestNudgesNeverAlertOnFirstFetchOrDrafts(t *testing.T) {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	m := notifyModel(t, "")
	if got := fetch(m, nil, []github.PullRequest{nudgedReview(github.NudgeUrgent, at)}); got != nil {
		t.Fatalf("a first fetch notified: %q", got)
	}
	draft := nudgedReview(github.NudgeUrgent, at.Add(time.Hour))
	draft.Draft = true
	if got := fetch(m, nil, []github.PullRequest{draft}); got != nil {
		t.Fatalf("a draft notified: %q", got)
	}
}

func TestAnUrgentNudgeFlashesUntilFocus(t *testing.T) {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	m := titleModel(t)
	fetch(m, nil, []github.PullRequest{nudgedReview(0, at)})
	fetch(m, nil, []github.PullRequest{nudgedReview(github.NudgeUrgent, at)})
	if m.flashText == "" || !m.flashUntil.IsZero() {
		t.Fatalf("urgent flash: text %q until %v, want lasting", m.flashText, m.flashUntil)
	}
	// A lasting flash outlives flashDuration.
	clock := time.Now().Add(2 * flashDuration)
	m.now = func() time.Time { return clock }
	m.handleFlashTick(flashTickMsg{m.flashGeneration})
	if m.flashText == "" {
		t.Fatal("the urgent flash stopped after flashDuration")
	}
	m.handleFocus(focusIn)
	if m.flashText != "" {
		t.Fatal("focus did not stop the flash")
	}
}

func TestALaterFlashKeepsAnUrgentFlashLasting(t *testing.T) {
	m := titleModel(t)
	m.startFlash("urgent", true)
	m.startFlash("normal", false)
	if m.flashText != "normal" || !m.flashUntil.IsZero() {
		t.Fatalf("flash %q until %v, want a lasting replacement", m.flashText, m.flashUntil)
	}
}

func TestANormalNudgeFlashesForFlashDuration(t *testing.T) {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	m := titleModel(t)
	fetch(m, nil, []github.PullRequest{nudgedReview(0, at)})
	fetch(m, nil, []github.PullRequest{nudgedReview(github.NudgeNormal, at)})
	if m.flashText == "" || m.flashUntil.IsZero() {
		t.Fatalf("normal flash: text %q until %v, want flashDuration", m.flashText, m.flashUntil)
	}
}

func TestADismissedNudgeStaysSilent(t *testing.T) {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	m := notifyModel(t, "")
	mention := changePR(10, "acme/b")
	mention.ReviewStatus, mention.Nudge = github.ReviewNudged, &github.Nudge{Urgency: github.NudgeUrgent, At: at}
	fetch(m, nil, []github.PullRequest{mention})
	m.setFocus(paneReview)
	m.selectPR(paneReview, mention.Repository, mention.Number)
	press(m, tea.Key{Code: 'X', Text: "X"})
	for i := range 2 {
		if got := fetch(m, nil, []github.PullRequest{mention}); got != nil {
			t.Fatalf("fetch %d after dismissing notified: %q", i+1, got)
		}
	}
	if m.flashText != "" || m.nudge(&m.snapshot.ReviewRequests[0]) != nil {
		t.Fatalf("the dismissal did not hold: flash %q", m.flashText)
	}
}

func TestANudgeNoteNeverReachesTheNotificationTitleOrStatusLine(t *testing.T) {
	const note = "secret-note-text"
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	m := notifyModel(t, "")
	m.setTitle = true
	fetch(m, nil, []github.PullRequest{nudgedReview(0, at)})
	nudged := nudgedReview(github.NudgeUrgent, at)
	nudged.Nudge.Note = note
	got := fetch(m, nil, []github.PullRequest{nudged})
	if len(got) != 1 {
		t.Fatalf("notifications = %q, want one", got)
	}
	m.setFocus(paneReview)
	m.selectPR(paneReview, nudged.Repository, nudged.Number)
	view := m.View()
	for name, text := range map[string]string{"notification": got[0], "notice": m.notice, "flash": m.flashText,
		"title": view.WindowTitle, "list": view.Content} {
		if strings.Contains(text, note) {
			t.Fatalf("the note reached the %s: %q", name, text)
		}
	}
}
