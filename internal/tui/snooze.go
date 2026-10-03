package tui

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/preferences"
)

// activityLimit caps an "until activity" snooze, so a review obligation
// cannot stay hidden forever.
const activityLimit = 7 * 24 * time.Hour

// wakeHour is when snoozes to a day end, local time.
const wakeHour = 9

// maxSnooze is the furthest a typed snooze may end.
const maxSnooze = 366 * 24 * time.Hour

var (
	errSnoozeTime   = errors.New("try 45m, 3h, 2d, 1w, tomorrow, fri, 14:30, or 2026-10-10 14:00")
	errSnoozePast   = errors.New("that time has passed")
	errSnoozeTooFar = errors.New("snoozes end within a year")
)

func atWakeHour(day time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), wakeHour, 0, 0, 0, day.Location())
}

// tomorrowMorning is the next calendar day at wakeHour.
func tomorrowMorning(now time.Time) time.Time { return atWakeHour(now.AddDate(0, 0, 1)) }

// nextWeekday is the next day after today that falls on weekday, at
// wakeHour.
func nextWeekday(now time.Time, weekday time.Weekday) time.Time {
	days := (int(weekday) - int(now.Weekday()) + 7) % 7
	if days == 0 {
		days = 7
	}
	return atWakeHour(now.AddDate(0, 0, days))
}

var snoozeWeekdays = map[string]time.Weekday{
	"sun": time.Sunday, "sunday": time.Sunday, "mon": time.Monday, "monday": time.Monday,
	"tue": time.Tuesday, "tues": time.Tuesday, "tuesday": time.Tuesday,
	"wed": time.Wednesday, "wednesday": time.Wednesday,
	"thu": time.Thursday, "thur": time.Thursday, "thurs": time.Thursday, "thursday": time.Thursday,
	"fri": time.Friday, "friday": time.Friday, "sat": time.Saturday, "saturday": time.Saturday,
}

// parseSnoozeTime reads a typed snooze end in now's location: a duration
// (45m, 3h, 2d, 1w), tomorrow, a weekday, a time of day, or a date with an
// optional time. The result is after now and within maxSnooze.
func parseSnoozeTime(text string, now time.Time) (time.Time, error) {
	text = strings.ToLower(strings.Join(strings.Fields(text), " "))
	loc := now.Location()
	within := func(at time.Time) (time.Time, error) {
		switch {
		case !at.After(now):
			return time.Time{}, errSnoozePast
		case at.Sub(now) > maxSnooze:
			return time.Time{}, errSnoozeTooFar
		}
		return at, nil
	}
	if n := len(text); n >= 2 && strings.Trim(text[:n-1], "0123456789") == "" {
		count, err := strconv.Atoi(text[:n-1])
		if err != nil {
			return time.Time{}, errSnoozeTooFar
		}
		if count == 0 {
			return time.Time{}, errSnoozeTime
		}
		if count > int(maxSnooze/time.Minute) {
			return time.Time{}, errSnoozeTooFar
		}
		switch text[n-1] {
		case 'm':
			return within(now.Add(time.Duration(count) * time.Minute))
		case 'h':
			return within(now.Add(time.Duration(count) * time.Hour))
		case 'd':
			return within(now.AddDate(0, 0, count))
		case 'w':
			return within(now.AddDate(0, 0, 7*count))
		}
		return time.Time{}, errSnoozeTime
	}
	if text == "tomorrow" {
		return tomorrowMorning(now), nil
	}
	if weekday, ok := snoozeWeekdays[text]; ok {
		return nextWeekday(now, weekday), nil
	}
	if clock, err := time.ParseInLocation("15:04", text, loc); err == nil {
		at := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour(), clock.Minute(), 0, 0, loc)
		if !at.After(now) {
			at = at.AddDate(0, 0, 1)
		}
		return at, nil
	}
	if at, err := time.ParseInLocation("2006-01-02 15:04", text, loc); err == nil {
		return within(at)
	}
	if day, err := time.ParseInLocation("2006-01-02", text, loc); err == nil {
		return within(atWakeHour(day))
	}
	return time.Time{}, errSnoozeTime
}

// wakeText is when a snooze ends, short: activity, a time today, a weekday
// and time within a week, else a date.
func wakeText(s preferences.Snooze, now time.Time) string {
	until := s.Until.In(now.Location())
	switch {
	case s.Activity:
		return "activity"
	case until.Year() == now.Year() && until.YearDay() == now.YearDay():
		return until.Format("15:04")
	case until.Sub(now) < 6*24*time.Hour:
		return until.Format("Mon 15:04")
	}
	return until.Format("Jan 2")
}

// Wake signals, saved in a snooze's Seen. Each is a fixed predicate over
// fields the full fetch reads.
const (
	signalReady         = "ready"
	signalFailing       = "failing"
	signalChanges       = "changes"
	signalApproved      = "approved"
	signalConflict      = "conflict"
	signalQueueFailed   = "queue-failed"
	signalQueueCanceled = "queue-canceled"
	signalNeedsYou      = "needs-you"
	signalRequested     = "requested"
)

// wakeReasons is the order reasons are chosen in when several signals turn
// true at once, with each one's words.
var wakeReasons = []struct{ signal, text string }{
	{signalChanges, "changes requested"},
	{signalFailing, "CI failing"},
	{signalConflict, "conflicts"},
	{signalQueueFailed, "queue failed"},
	{signalQueueCanceled, "queue canceled"},
	{signalReady, "ready to merge"},
	{signalApproved, "approved"},
	{signalRequested, "review requested again"},
	{signalNeedsYou, "needs your review"},
}

// snoozeSignals evaluates the wake signals of a pull request of list
// (paneMine or paneReview): known lists every signal whose data is known,
// held the ones that are true.
func (m *model) snoozeSignals(list paneID, pr *github.PullRequest) (held, known []string) {
	add := func(signal string, isKnown, isHeld bool) {
		if isKnown {
			known = append(known, signal)
			if isHeld {
				held = append(held, signal)
			}
		}
	}
	if list == paneReview {
		add(signalNeedsYou, true, pr.ReviewStatus == github.ReviewNewCommits ||
			pr.ReviewStatus == github.ReviewAuthorReplied || pr.ReviewStatus == github.ReviewDismissed)
		add(signalRequested, true, pr.ReviewStatus == github.ReviewRequested)
		slices.Sort(held)
		slices.Sort(known)
		return held, known
	}
	result := m.readyResult(pr)
	add(signalReady, result.Known, result.Ready)
	add(signalFailing, pr.Checks != "", checksFailing(pr.Checks))
	add(signalChanges, pr.ReviewDecision != "", pr.ReviewDecision == "CHANGES_REQUESTED")
	add(signalApproved, pr.ReviewDecision != "", pr.ReviewDecision == "APPROVED")
	conflict := pr.Mergeable == "CONFLICTING" || pr.MergeState == "DIRTY"
	add(signalConflict, conflict || !unknownMergeState(pr), conflict)
	state := queueState(pr)
	add(signalQueueFailed, true, state == github.QueueRemovedFailed)
	add(signalQueueCanceled, true, state == github.QueueRemovedCanceled)
	slices.Sort(held)
	slices.Sort(known)
	return held, known
}

// requestedAt is the request time a review row records while a direct
// request is pending; zero otherwise.
func requestedAt(list paneID, pr *github.PullRequest) time.Time {
	if list == paneReview && pr.ReviewStatus == github.ReviewRequested {
		return pr.WaitingSince
	}
	return time.Time{}
}

// snoozeWake compares a snoozed pull request's signals with what its snooze
// saw. It returns why it wakes, or "", and what to record: current values
// where known, the recorded ones elsewhere, and the request time.
func snoozeWake(s preferences.Snooze, held, known []string, pr *github.PullRequest) (string, []string, time.Time) {
	var seen []string
	for _, signal := range s.Seen {
		if !slices.Contains(known, signal) {
			seen = append(seen, signal)
		}
	}
	seen = append(seen, held...)
	slices.Sort(seen)
	seen = slices.Compact(seen)
	requested := s.Requested
	list := paneMine
	if s.List == preferences.SnoozeReview {
		list = paneReview
	}
	if at := requestedAt(list, pr); !at.IsZero() {
		requested = at
	}
	// A pending request asked again moves its request time later.
	if !s.Requested.IsZero() && requested.After(s.Requested) {
		return "review requested again", seen, requested
	}
	for _, reason := range wakeReasons {
		if slices.Contains(held, reason.signal) && !slices.Contains(s.Seen, reason.signal) {
			return reason.text, seen, requested
		}
	}
	return "", seen, requested
}

// snoozed reports whether a pull request is snoozed, or was snoozed when
// it closed: either way it belongs to the Snoozed pane.
func (m *model) snoozed(pr *github.PullRequest) bool {
	key := keyOf(pr)
	if _, ok := m.snoozes[key]; ok {
		return true
	}
	_, ok := m.snoozeClosed[key]
	return ok
}

// snoozeList is the list a snoozed pull request was snoozed from: paneMine
// or paneReview.
func (m *model) snoozeList(pr *github.PullRequest) paneID {
	key := keyOf(pr)
	if s, ok := m.snoozes[key]; ok {
		if s.List == preferences.SnoozeReview {
			return paneReview
		}
		return paneMine
	}
	if m.snoozeClosed[key] == paneReview {
		return paneReview
	}
	return paneMine
}

// snoozeAt is the snooze of the pull request at an index of the Snoozed
// pane's visible rows, which run over the authored list, then the review
// list.
func (m *model) snoozeAt(index int) preferences.Snooze {
	list, index := splitIndex(index, len(m.snapshot.PullRequests))
	source := m.source(list)
	if index < 0 || index >= len(source) {
		return preferences.Snooze{}
	}
	return m.snoozes[keyOf(&source[index])]
}

// listName is a list's name in a saved snooze.
func listName(list paneID) string {
	if list == paneReview {
		return preferences.SnoozeReview
	}
	return preferences.SnoozeMine
}
