package tui

import (
	"slices"
	"testing"
	"time"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/preferences"
)

// snoozeNow is a Saturday afternoon in a fixed zone, so no daylight saving
// change moves the expected times.
var snoozeZone = time.FixedZone("T", -4*3600)
var snoozeNow = time.Date(2026, 10, 3, 15, 30, 0, 0, snoozeZone)

func TestParseSnoozeTime(t *testing.T) {
	at := func(month time.Month, day, hour, minute int) time.Time {
		return time.Date(2026, month, day, hour, minute, 0, 0, snoozeZone)
	}
	for _, c := range []struct {
		text string
		want time.Time
	}{
		{"45m", snoozeNow.Add(45 * time.Minute)},
		{"3h", snoozeNow.Add(3 * time.Hour)},
		{"2d", at(10, 5, 15, 30)},
		{"1w", at(10, 10, 15, 30)},
		{" Tomorrow ", at(10, 4, 9, 0)},
		{"sat", at(10, 10, 9, 0)}, // never today
		{"monday", at(10, 5, 9, 0)},
		{"fri", at(10, 9, 9, 0)},
		{"16:00", at(10, 3, 16, 0)},
		{"9:15", at(10, 4, 9, 15)}, // already past today
		{"2026-10-10", at(10, 10, 9, 0)},
		{"2026-10-10 14:00", at(10, 10, 14, 0)},
	} {
		got, err := parseSnoozeTime(c.text, snoozeNow)
		if err != nil || !got.Equal(c.want) {
			t.Errorf("parseSnoozeTime(%q) = %v, %v; want %v", c.text, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "soon", "0h", "-3h", "+3h", "3x", "2026-10-02", "2026-10-03 15:00", "2027-12-01", "400d", "99999999999999999999m", "25:00"} {
		if got, err := parseSnoozeTime(bad, snoozeNow); err == nil {
			t.Errorf("parseSnoozeTime(%q) = %v, want an error", bad, got)
		}
	}
}

func TestPresetTimes(t *testing.T) {
	if got, want := tomorrowMorning(snoozeNow), time.Date(2026, 10, 4, 9, 0, 0, 0, snoozeZone); !got.Equal(want) {
		t.Errorf("tomorrow = %v, want %v", got, want)
	}
	monday := time.Date(2026, 10, 5, 8, 0, 0, 0, snoozeZone)
	if got, want := nextWeekday(monday, time.Monday), time.Date(2026, 10, 12, 9, 0, 0, 0, snoozeZone); !got.Equal(want) {
		t.Errorf("next Monday from a Monday = %v, want %v", got, want)
	}
}

func TestWakeText(t *testing.T) {
	for _, c := range []struct {
		snooze preferences.Snooze
		want   string
	}{
		{preferences.Snooze{Until: snoozeNow.Add(time.Hour), Activity: true}, "activity"},
		{preferences.Snooze{Until: time.Date(2026, 10, 3, 18, 5, 0, 0, snoozeZone)}, "18:05"},
		{preferences.Snooze{Until: time.Date(2026, 10, 6, 9, 0, 0, 0, snoozeZone)}, "Tue 09:00"},
		{preferences.Snooze{Until: time.Date(2026, 10, 12, 9, 0, 0, 0, snoozeZone)}, "Oct 12"},
	} {
		if got := wakeText(c.snooze, snoozeNow); got != c.want {
			t.Errorf("wakeText(%v) = %q, want %q", c.snooze.Until, got, c.want)
		}
	}
}

func authoredPR(mutate func(*github.PullRequest)) *github.PullRequest {
	pr := &github.PullRequest{Repository: "acme/api", Number: 12, Mergeable: "MERGEABLE", MergeState: "BLOCKED",
		Checks: "PENDING", ReviewDecision: "REVIEW_REQUIRED"}
	if mutate != nil {
		mutate(pr)
	}
	return pr
}

func TestEachAuthoredSignalWakesAlone(t *testing.T) {
	m := testModel(testPreferences(t), 120, 30)
	base := authoredPR(nil)
	held, known := m.snoozeSignals(listAuthored, base)
	snooze := preferences.Snooze{List: preferences.SnoozeMine, Seen: held}
	for _, c := range []struct {
		name   string
		mutate func(*github.PullRequest)
		reason string
	}{
		{"ready", func(pr *github.PullRequest) { pr.MergeState = "CLEAN" }, "ready to merge"},
		{"failing", func(pr *github.PullRequest) { pr.Checks = "FAILURE" }, "CI failing"},
		{"changes", func(pr *github.PullRequest) { pr.ReviewDecision = "CHANGES_REQUESTED" }, "changes requested"},
		{"approved", func(pr *github.PullRequest) { pr.ReviewDecision = "APPROVED" }, "approved"},
		{"conflict", func(pr *github.PullRequest) { pr.Mergeable, pr.MergeState = "CONFLICTING", "DIRTY" }, "conflicts"},
		{"queue failed", func(pr *github.PullRequest) { pr.Queue = &github.QueueEntry{State: github.QueueRemovedFailed} }, "queue failed"},
		{"queue canceled", func(pr *github.PullRequest) { pr.Queue = &github.QueueEntry{State: github.QueueRemovedCanceled} }, "queue canceled"},
	} {
		pr := authoredPR(c.mutate)
		h, k := m.snoozeSignals(listAuthored, pr)
		if reason, _, _ := snoozeWake(snooze, h, k, pr); reason != c.reason {
			t.Errorf("%s: reason = %q, want %q", c.name, reason, c.reason)
		}
	}
	if reason, seen, _ := snoozeWake(snooze, held, known, base); reason != "" || !slices.Equal(seen, held) {
		t.Errorf("unchanged: reason %q, seen %v", reason, seen)
	}
}

func TestNoiseDoesNotWake(t *testing.T) {
	m := testModel(testPreferences(t), 120, 30)
	base := authoredPR(nil)
	held, _ := m.snoozeSignals(listAuthored, base)
	snooze := preferences.Snooze{List: preferences.SnoozeMine, Seen: held}
	pr := authoredPR(func(pr *github.PullRequest) {
		pr.Comments, pr.UpdatedAt, pr.Additions = 9, snoozeNow, 40
		pr.Bots = []github.BotReview{{Name: "bot", State: github.BotConcerns}}
	})
	h, k := m.snoozeSignals(listAuthored, pr)
	if reason, _, _ := snoozeWake(snooze, h, k, pr); reason != "" {
		t.Fatalf("comments, bots, and updatedAt woke it: %q", reason)
	}
}

func TestUnknownSignalsKeepWhatWasSeen(t *testing.T) {
	m := testModel(testPreferences(t), 120, 30)
	failing := authoredPR(func(pr *github.PullRequest) { pr.Checks, pr.ReviewDecision = "FAILURE", "APPROVED" })
	held, _ := m.snoozeSignals(listAuthored, failing)
	snooze := preferences.Snooze{List: preferences.SnoozeMine, Seen: held}
	unknown := authoredPR(func(pr *github.PullRequest) {
		pr.Checks, pr.ReviewDecision, pr.Mergeable, pr.MergeState = "", "", "UNKNOWN", "UNKNOWN"
	})
	h, k := m.snoozeSignals(listAuthored, unknown)
	reason, seen, _ := snoozeWake(snooze, h, k, unknown)
	if reason != "" || !slices.Contains(seen, signalFailing) || !slices.Contains(seen, signalApproved) {
		t.Fatalf("unknown data: reason %q, seen %v; want no wake and failing, approved kept", reason, seen)
	}
	// Failing again after it passed wakes, as the failing-CI alert does.
	passing := authoredPR(func(pr *github.PullRequest) { pr.Checks = "SUCCESS" })
	h, k = m.snoozeSignals(listAuthored, passing)
	_, seen, _ = snoozeWake(snooze, h, k, passing)
	snooze.Seen = seen
	h, k = m.snoozeSignals(listAuthored, failing)
	if reason, _, _ := snoozeWake(snooze, h, k, failing); reason != "CI failing" {
		t.Fatalf("failing again: reason %q", reason)
	}
}

func reviewPR(status github.ReviewStatus, since time.Time) *github.PullRequest {
	return &github.PullRequest{Repository: "acme/web", Number: 7, ReviewStatus: status, WaitingSince: since,
		Mergeable: "MERGEABLE", MergeState: "BLOCKED"}
}

func TestReviewSignals(t *testing.T) {
	m := testModel(testPreferences(t), 120, 30)
	first := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	pending := reviewPR(github.ReviewRequested, first)
	held, _ := m.snoozeSignals(listReview, pending)
	snooze := preferences.Snooze{List: preferences.SnoozeReview, Seen: held, Nudged: requestedAt(listReview, pending)}
	again := reviewPR(github.ReviewRequested, first.Add(time.Hour))
	h, k := m.snoozeSignals(listReview, again)
	if reason, _, requested := snoozeWake(snooze, h, k, again); reason != "review requested again" || !requested.Equal(first.Add(time.Hour)) {
		t.Errorf("re-request: %q, %v", reason, requested)
	}
	for _, status := range []github.ReviewStatus{github.ReviewNewCommits, github.ReviewAuthorReplied, github.ReviewDismissed} {
		pr := reviewPR(status, first.Add(time.Hour))
		h, k := m.snoozeSignals(listReview, pr)
		if reason, _, _ := snoozeWake(snooze, h, k, pr); reason != "needs your review" {
			t.Errorf("%v: reason %q", status, reason)
		}
	}
	activity := reviewPR(github.ReviewNewActivity, first.Add(time.Hour))
	h, k = m.snoozeSignals(listReview, activity)
	if reason, _, _ := snoozeWake(snooze, h, k, activity); reason != "" {
		t.Errorf("new activity woke it: %q", reason)
	}
}

func TestReviewingASnoozedRowDoesNotWakeIt(t *testing.T) {
	m := testModel(testPreferences(t), 120, 30)
	first := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	pending := reviewPR(github.ReviewRequested, first)
	held, _ := m.snoozeSignals(listReview, pending)
	snooze := preferences.Snooze{List: preferences.SnoozeReview, Seen: held, Nudged: requestedAt(listReview, pending)}
	for _, status := range []github.ReviewStatus{github.ReviewWaitingOnAuthor, github.ReviewApproved} {
		reviewed := reviewPR(status, first.Add(2*time.Hour))
		h, k := m.snoozeSignals(listReview, reviewed)
		if reason, _, _ := snoozeWake(snooze, h, k, reviewed); reason != "" {
			t.Errorf("reviewing (%v) woke it: %q", status, reason)
		}
	}
	// Requested again after the review: pending again wakes.
	waiting := reviewPR(github.ReviewWaitingOnAuthor, first.Add(2*time.Hour))
	h, k := m.snoozeSignals(listReview, waiting)
	_, seen, requested := snoozeWake(snooze, h, k, waiting)
	snooze.Seen, snooze.Nudged = seen, requested
	again := reviewPR(github.ReviewRequested, first.Add(3*time.Hour))
	h, k = m.snoozeSignals(listReview, again)
	if reason, _, _ := snoozeWake(snooze, h, k, again); reason != "review requested again" {
		t.Errorf("requested after a review: %q", reason)
	}
}
