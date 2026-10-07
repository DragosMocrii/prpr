package tui

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

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
	// errSnoozesNotSaved marks the warning of a failed snooze save.
	errSnoozesNotSaved = errors.New("Snoozes not saved")
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
// (listAuthored or listReview): known lists every signal whose data is known,
// held the ones that are true.
func (m *model) snoozeSignals(list listID, pr *github.PullRequest) (held, known []string) {
	add := func(signal string, isKnown, isHeld bool) {
		if isKnown {
			known = append(known, signal)
			if isHeld {
				held = append(held, signal)
			}
		}
	}
	if list == listReview {
		add(signalNeedsYou, true, needsYou(pr.ReviewStatus))
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
func requestedAt(list listID, pr *github.PullRequest) time.Time {
	if list == listReview && pr.ReviewStatus == github.ReviewRequested {
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
	list := listAuthored
	if s.List == preferences.SnoozeReview {
		list = listReview
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

// snoozeList is the list a snoozed pull request was snoozed from: listAuthored
// or listReview.
func (m *model) snoozeList(pr *github.PullRequest) listID {
	key := keyOf(pr)
	if s, ok := m.snoozes[key]; ok {
		if s.List == preferences.SnoozeReview {
			return listReview
		}
		return listAuthored
	}
	if m.snoozeClosed[key] == listReview {
		return listReview
	}
	return listAuthored
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
func listName(list listID) string {
	if list == listReview {
		return preferences.SnoozeReview
	}
	return preferences.SnoozeMine
}

// Snooze form choices.
const (
	snoozeActivity = "activity"
	snoozeHour     = "hour"
	snoozeTomorrow = "tomorrow"
	snoozeMonday   = "monday"
	snoozeCustom   = "custom"
)

// snoozeEditor is the snooze form for one pull request of list (paneMine or
// paneReview). Its fields write to choice and typed.
type snoozeEditor struct {
	form          *huh.Form
	key           prKey
	list          listID
	title         string
	choice, typed string
}

// openSnooze opens the snooze form on the focused row.
func (m *model) openSnooze() tea.Cmd {
	pr, gone, ok := m.paneRow(m.focus, m.focused().table.Cursor())
	switch {
	case !ok:
		return nil
	case m.snapshot.Preview:
		m.setNotice("Snoozing waits for the details to load")
		return nil
	case gone:
		m.setNotice("Gone pull requests cannot be snoozed")
		return nil
	}
	list := listAuthored
	if m.focus == paneReview {
		list = listReview
	}
	e := &snoozeEditor{key: keyOf(pr), list: list, choice: snoozeActivity,
		title: "Snooze " + alertName(pr)}
	m.snoozeEditor = e
	e.form = huh.NewForm(
		huh.NewGroup(huh.NewSelect[string]().Title(e.title).Options(
			huh.NewOption("Until activity (at most 7 days)", snoozeActivity),
			huh.NewOption("1 hour", snoozeHour),
			huh.NewOption("Tomorrow 9:00", snoozeTomorrow),
			huh.NewOption("Next Monday 9:00", snoozeMonday),
			huh.NewOption("Custom…", snoozeCustom),
		).Value(&e.choice)),
		huh.NewGroup(huh.NewInput().Title("Snooze until").
			Description("45m, 3h, 2d, 1w, tomorrow, fri, 14:30, or 2026-10-10 14:00").
			Value(&e.typed).Validate(func(text string) error {
			_, err := parseSnoozeTime(text, m.now())
			return err
		})).WithHideFunc(func() bool { return e.choice != snoozeCustom }),
	)
	dark := m.darkBackground
	e.form.WithAccessible(false).WithShowHelp(true).
		WithTheme(huh.ThemeFunc(func(bool) *huh.Styles { return rulesTheme(dark) }))
	e.form.SubmitCmd, e.form.CancelCmd = nil, nil
	m.sizeSnoozeForm()
	return e.form.Init()
}

// sizeSnoozeForm fits the form to the terminal; every resize sets it again.
func (m *model) sizeSnoozeForm() {
	m.snoozeEditor.form.WithWidth(max(min(m.width, rulesWidth), 1)).WithHeight(max(m.height-rulesChrome, 1))
}

// updateSnooze passes a message to the form. Esc cancels; a finished form
// snoozes.
func (m *model) updateSnooze(msg tea.Msg) tea.Cmd {
	e := m.snoozeEditor
	if press, ok := msg.(tea.KeyPressMsg); ok && press.String() == "esc" {
		m.snoozeEditor = nil
		return nil
	}
	form, cmd := e.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		e.form = f
	}
	switch e.form.State {
	case huh.StateAborted:
		m.snoozeEditor = nil
		return nil
	case huh.StateCompleted:
		m.snoozeEditor = nil
		now := m.now()
		var until time.Time
		activity := false
		switch e.choice {
		case snoozeHour:
			until = now.Add(time.Hour)
		case snoozeTomorrow:
			until = tomorrowMorning(now)
		case snoozeMonday:
			until = nextWeekday(now, time.Monday)
		case snoozeCustom:
			var err error
			if until, err = parseSnoozeTime(e.typed, now); err != nil {
				m.setNotice(err.Error())
				return nil
			}
		default:
			until, activity = now.Add(activityLimit), true
		}
		return m.snooze(e.key, e.list, until, activity)
	}
	return cmd
}

// snoozeLines draws the snooze form.
func (m *model) snoozeLines() []string {
	lines := []string{m.titleLine("prpr — "+m.accountLabel()+" — snooze", ""), ""}
	lines = append(lines, strings.Split(m.snoozeEditor.form.View(), "\n")...)
	return append(lines, "", m.shortHelp(m.keys.snoozeHelp()))
}

// snooze hides a pull request of list until a time, or until activity.
func (m *model) snooze(key prKey, list listID, until time.Time, activity bool) tea.Cmd {
	var pr *github.PullRequest
	source := m.source(list)
	for i := range source {
		if keyOf(&source[i]) == key {
			pr = &source[i]
			break
		}
	}
	if pr == nil {
		m.setNotice(fmt.Sprintf("%s#%d is no longer listed", key.repository, key.number))
		return nil
	}
	held, known := m.snoozeSignals(list, pr)
	// An unknown merge state keeps the readiness last known, so a fetch that
	// reads the state again does not wake it as newly ready.
	if list == listAuthored && !slices.Contains(known, signalReady) && m.readiness[key] {
		held = append(held, signalReady)
		slices.Sort(held)
	}
	entry := preferences.Snooze{Repository: pr.Repository, Number: pr.Number, List: listName(list),
		Until: until, Activity: activity, Seen: held, Requested: requestedAt(list, pr)}
	focus, row := m.focus, m.focused().table.Cursor()
	m.keepSelection(func() {
		if m.snoozes == nil {
			m.snoozes = make(map[prKey]preferences.Snooze)
		}
		m.snoozes[key] = entry
		delete(m.woke, key)
		m.rebuildVisiblePRs()
	}, false)
	if m.focus == focus {
		m.reselectRow(focus, row)
	}
	m.lastSnooze = &key
	m.saveSnoozes()
	when := "until activity"
	if !activity {
		when = "until " + wakeText(entry, m.now())
	}
	m.setNotice("Snoozed " + alertName(pr) + " " + when + " · U undo")
	return m.scheduleSnoozeTick()
}

// reselectRow puts a pane's cursor on row, or the last row when fewer remain.
func (m *model) reselectRow(id paneID, row int) {
	pane := &m.panes[id]
	moveCursor(&pane.table, min(row, rowCount(pane)-1))
	m.syncPages(id)
}

// wakeNow ends the snooze of the Snoozed pane's selected pull request.
func (m *model) wakeNow() tea.Cmd {
	row := m.focused().table.Cursor()
	pr, gone, ok := m.paneRow(paneSnoozed, row)
	switch {
	case !ok:
		return nil
	case gone:
		m.setNotice("Gone pull requests cannot be woken")
		return nil
	}
	key := keyOf(pr)
	name := alertName(pr)
	m.keepSelection(func() {
		delete(m.snoozes, key)
		if m.woke == nil {
			m.woke = make(map[prKey]string)
		}
		m.woke[key] = "woken"
		m.rebuildVisiblePRs()
	}, false)
	if m.focus == paneSnoozed {
		m.reselectRow(paneSnoozed, row)
	}
	m.saveSnoozes()
	m.setNotice("Woke " + name)
	return m.scheduleSnoozeTick()
}

// undoSnooze ends the last snooze and shows the pull request where it was.
func (m *model) undoSnooze() tea.Cmd {
	if m.lastSnooze == nil {
		m.setNotice("Nothing to undo")
		return nil
	}
	key := *m.lastSnooze
	if _, ok := m.snoozes[key]; !ok {
		m.lastSnooze = nil
		m.setNotice("Nothing to undo")
		return nil
	}
	m.lastSnooze = nil
	m.keepSelection(func() {
		delete(m.snoozes, key)
		m.rebuildVisiblePRs()
	}, false)
	for _, list := range [][]github.PullRequest{m.snapshot.PullRequests, m.snapshot.ReviewRequests} {
		for i := range list {
			if keyOf(&list[i]) != key {
				continue
			}
			pr := &list[i]
			for _, id := range m.focusPanes() {
				if m.selectPR(id, pr.Repository, pr.Number) {
					m.setFocus(id)
					m.selectPR(id, pr.Repository, pr.Number)
					break
				}
			}
		}
	}
	m.saveSnoozes()
	m.setNotice("Snooze undone")
	return m.scheduleSnoozeTick()
}

// saveSnoozes saves the snoozes, by repository then number. A failed save
// keeps them for the session and shows the warning. They save under the
// account they were loaded for, which a failed fetch or an account choice
// still awaiting its fetch leaves in place.
func (m *model) saveSnoozes() {
	entries := make([]preferences.Snooze, 0, len(m.snoozes))
	for _, entry := range m.snoozes {
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b preferences.Snooze) int {
		if c := strings.Compare(a.Repository, b.Repository); c != 0 {
			return c
		}
		return a.Number - b.Number
	})
	if err := m.preferences.SaveSnoozes(m.filterLogin, entries); err != nil {
		m.preferenceErr = fmt.Errorf("%w: %w", errSnoozesNotSaved, err)
		return
	}
	// Saves also run on their own, so a success clears only its own warning.
	if errors.Is(m.preferenceErr, errSnoozesNotSaved) {
		m.preferenceErr = nil
	}
}

// snoozeCheckInterval is the furthest ahead the snooze timer runs, since a
// suspended machine's timers run late.
const snoozeCheckInterval = time.Minute

// snoozeTickMsg is a snooze timer tick; ticks from an older generation are
// dropped.
type snoozeTickMsg struct{ generation uint64 }

// snoozeListID is the list a saved snooze belongs to: listAuthored or
// paneReview.
func snoozeListID(s preferences.Snooze) listID {
	if s.List == preferences.SnoozeReview {
		return listReview
	}
	return listAuthored
}

// markWoke records why a pull request woke, for its name tag.
func (m *model) markWoke(key prKey, reason string) {
	if m.woke == nil {
		m.woke = make(map[prKey]string)
	}
	m.woke[key] = reason
}

// wakeAlert adds the alert of a pull request that woke, when alerting and
// the pull request is listed in the scope.
func (m *model) wakeAlert(alerts []prAlert, alert bool, pr *github.PullRequest, kind string) []prAlert {
	if alert && pr != nil && m.inScope(pr) {
		alerts = append(alerts, prAlert{pr, []string{kind}})
	}
	return alerts
}

// loadSnoozes replaces the snoozes with an account's saved ones, on an
// account change. Those already past wake silently; restoring the rest
// never writes.
func (m *model) loadSnoozes(login string) {
	m.snoozes = make(map[prKey]preferences.Snooze)
	for _, s := range m.preferences.Snoozes(login) {
		m.snoozes[prKey{strings.ToLower(s.Repository), s.Number}] = s
	}
	m.snoozeClosed, m.woke, m.lastSnooze = nil, nil, nil
	// Read before a wake saves, which drops the unreadable entries.
	unread := m.preferences.SnoozesErr(login)
	// Snoozes already past when they load wake silently, even during a
	// quiet fetch, so the next normal fetch cannot alert for them.
	m.wakeDue(false)
	if unread != nil && m.preferenceErr == nil {
		m.preferenceErr = fmt.Errorf("Some snoozes not read: %s", singleLine(unread.Error()))
	}
}

// snoozedRow is a snoozed pull request listed in its own list.
type snoozedRow struct {
	key  prKey
	list listID
	pr   *github.PullRequest
}

// listedSnoozes are the snoozed pull requests listed in their own lists, in
// fetch order (the authored list, then the review list), so wake alerts
// follow the same order as other alerts.
func (m *model) listedSnoozes() []snoozedRow {
	var rows []snoozedRow
	for _, id := range openLists {
		source := m.source(id)
		for i := range source {
			key := keyOf(&source[i])
			if s, ok := m.snoozes[key]; ok && snoozeListID(s) == id {
				rows = append(rows, snoozedRow{key, id, &source[i]})
			}
		}
	}
	return rows
}

// expire wakes every snooze whose end is not after now, with alerts for
// the listed ones when alert. It reports whether any woke; it does not save.
func (m *model) expire(alert bool) ([]prAlert, bool) {
	now := m.now()
	var alerts []prAlert
	woke := false
	for _, row := range m.listedSnoozes() {
		if !m.snoozes[row.key].Until.After(now) {
			alerts = m.wakeAlert(alerts, alert, row.pr, "snooze ended")
		}
	}
	for key, s := range m.snoozes {
		if !s.Until.After(now) {
			delete(m.snoozes, key)
			m.markWoke(key, "snooze ended")
			woke = true
		}
	}
	return alerts, woke
}

// wakeDue wakes the snoozes that ended and saves when any woke. The caller
// rebuilds the panes.
func (m *model) wakeDue(alert bool) []prAlert {
	alerts, woke := m.expire(alert)
	if woke {
		m.saveSnoozes()
	}
	return alerts
}

// reviewSnoozes compares a full fetch with the snoozes: a closed pull
// request's snooze is deleted and its row is gone in Snoozed, a new signal
// or a passed end wakes, and otherwise the signals seen are recorded. It
// saves once when anything changed.
func (m *model) reviewSnoozes(alert bool) []prAlert {
	var alerts []prAlert
	changed := false
	listed := make(map[prKey]bool)
	for _, row := range m.listedSnoozes() {
		listed[row.key] = true
		s := m.snoozes[row.key]
		held, known := m.snoozeSignals(row.list, row.pr)
		reason, seen, requested := snoozeWake(s, held, known, row.pr)
		if reason != "" {
			delete(m.snoozes, row.key)
			m.markWoke(row.key, reason)
			alerts = m.wakeAlert(alerts, alert, row.pr, "woke: "+reason)
			changed = true
			continue
		}
		if !slices.Equal(seen, s.Seen) || !requested.Equal(s.Requested) {
			s.Seen, s.Requested = seen, requested
			m.snoozes[row.key] = s
			changed = true
		}
	}
	// A snooze whose pull request left its list closed: its row is gone in
	// Snoozed.
	for key, s := range m.snoozes {
		if listed[key] {
			continue
		}
		delete(m.snoozes, key)
		if m.snoozeClosed == nil {
			m.snoozeClosed = make(map[prKey]listID)
		}
		m.snoozeClosed[key] = snoozeListID(s)
		changed = true
	}
	expired, woke := m.expire(alert)
	alerts = append(alerts, expired...)
	// A pull request listed again is no longer gone.
	for _, id := range openLists {
		source := m.source(id)
		for i := range source {
			delete(m.snoozeClosed, keyOf(&source[i]))
		}
	}
	if changed || woke {
		m.saveSnoozes()
	}
	return alerts
}

// scheduleSnoozeTick starts a timer for the earliest snooze end, at most
// snoozeCheckInterval ahead. Every call starts a new generation.
func (m *model) scheduleSnoozeTick() tea.Cmd {
	return m.scheduleSnoozeTickWhen(false)
}

// snoozeTickDelay is how long to wait before checking a snooze ending at
// until: at most snoozeCheckInterval, and zero for one already due, unless
// idle (no rows to wake against), when a due snooze would only spin.
func snoozeTickDelay(until, now time.Time, idle bool) time.Duration {
	if idle {
		return snoozeCheckInterval
	}
	return min(max(until.Sub(now), 0), snoozeCheckInterval)
}

// scheduleSnoozeTickWhen is scheduleSnoozeTick, waiting a full
// snoozeCheckInterval when idle.
func (m *model) scheduleSnoozeTickWhen(idle bool) tea.Cmd {
	m.snoozeGeneration++
	if m.sleeping || m.fetchQuiet || len(m.snoozes) == 0 {
		return nil
	}
	var earliest time.Time
	for _, s := range m.snoozes {
		if earliest.IsZero() || s.Until.Before(earliest) {
			earliest = s.Until
		}
	}
	generation := m.snoozeGeneration
	delay := snoozeTickDelay(earliest, m.now(), idle)
	return tea.Tick(delay, func(time.Time) tea.Msg { return snoozeTickMsg{generation: generation} })
}

// handleSnoozeTick wakes the snoozes that ended, with one notification, and
// schedules the next tick.
func (m *model) handleSnoozeTick(msg snoozeTickMsg) tea.Cmd {
	if msg.generation != m.snoozeGeneration {
		return nil
	}
	if m.sleeping || m.fetchQuiet {
		return nil
	}
	now := m.now()
	// With no rows to alert for, a due snooze waits for the next full fetch,
	// whose time check wakes it with its alert.
	if m.snapshot.Login == "" || m.err != nil {
		return m.scheduleSnoozeTickWhen(true)
	}
	anyDue := false
	for _, s := range m.snoozes {
		if !s.Until.After(now) {
			anyDue = true
			break
		}
	}
	var alerts []prAlert
	if anyDue {
		m.keepingSelection(func() {
			alerts = m.wakeDue(true)
			m.rebuildVisiblePRs()
		})
	}
	cmds := []tea.Cmd{m.notifyAlerts(alerts)}
	if len(alerts) > 0 {
		cmds = append(cmds, m.startFlash(alertText(alerts)))
	}
	return tea.Batch(append(cmds, m.scheduleSnoozeTick())...)
}
