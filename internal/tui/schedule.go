package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/schedule"
)

type scheduleTickMsg struct{ generation uint64 }

type scheduleEditor struct {
	form    *huh.Form
	enabled bool
	days    []string
	start   string
	end     string
}

const scheduleWidth = rulesWidth
const scheduleChrome = rulesChrome

func (m *model) pollingAllowed() bool { return m.scheduledActivity(m.now()) }

// manualWake reports whether a w wake holds at now.
func (m *model) manualWake(now time.Time) bool { return now.Before(m.manualWakeUntil) }

// windowEnabled reports whether a readable schedule limits activity.
func (m *model) windowEnabled() bool { return m.scheduleErr == nil && m.scheduleConfig.Enabled }

// fetchDeferred reports whether an automatic fetch must wait: login owns the
// terminal, a form or picker is open, or the first scope choice is pending.
func (m *model) fetchDeferred() bool {
	return m.loginActive || m.overlayOpen() || (m.snapshot.Login != "" && !m.scopeChosen)
}

// activityContext is the context automatic requests run under: it ends at
// the activity deadline.
func (m *model) activityContext() context.Context {
	if m.activityCtx == nil {
		return m.ctx
	}
	return m.activityCtx
}

// liveUntil reports, from inside a command, whether ctx is still live and
// the deadline, checked against the model's clock, has not passed.
func liveUntil(ctx context.Context, deadline time.Time, now func() time.Time) func() bool {
	return func() bool { return ctx.Err() == nil && (deadline.IsZero() || now().Before(deadline)) }
}

func (m *model) cancelFetch() {
	if m.fetchCancel != nil {
		m.fetchCancel()
		m.fetchCancel = nil
	}
}

// resetScheduleTimer drops the pending schedule tick so the next reconcile
// schedules a new one.
func (m *model) resetScheduleTimer() {
	m.scheduleGeneration++
	m.scheduleTimerAt = time.Time{}
}

func (m *model) notificationsAllowed() bool {
	return m.pollingAllowed() && !m.fetchQuiet
}

func (m *model) startAutomaticFetch() tea.Cmd {
	if !m.pollingAllowed() {
		m.wakeFetchPending = false
		return nil
	}
	if m.fetchDeferred() {
		m.wakeFetchPending = true
		return nil
	}
	if m.err != nil && m.awaitingToken() {
		m.wakeFetchPending = false
		return nil
	}
	if m.loading && !m.fetchQuiet {
		m.wakeFetchPending = false
		return nil
	}
	m.wakeFetchPending = false
	m.refetchForRules = false
	return m.startFetch()
}

func (m *model) scheduledActivity(now time.Time) bool {
	if m.manualWake(now) {
		return true
	}
	if m.scheduleErr != nil {
		return false
	}
	return m.scheduleWindow.Active(now.In(time.Local))
}

// nextWindowBoundary is when the schedule next opens or closes; zero when
// no schedule limits activity.
func (m *model) nextWindowBoundary(now time.Time) time.Time {
	if !m.windowEnabled() {
		return time.Time{}
	}
	return m.scheduleWindow.NextChange(now.In(time.Local))
}

func (m *model) effectiveDeadline(now time.Time) time.Time {
	if m.manualWake(now) {
		if !m.windowEnabled() {
			return m.manualWakeUntil
		}
		expires := m.manualWakeUntil.In(time.Local)
		if m.scheduleWindow.Active(expires) {
			return m.scheduleWindow.NextChange(expires)
		}
		return m.manualWakeUntil
	}
	return m.nextWindowBoundary(now)
}

func (m *model) replaceActivity(deadline time.Time) {
	if m.activityCancel != nil {
		m.activityCancel()
	}
	m.activityGeneration++
	m.activityDeadline = deadline
	var ctx context.Context
	var cancel context.CancelFunc
	if deadline.IsZero() {
		ctx, cancel = context.WithCancel(m.ctx)
	} else {
		delay := deadline.Sub(m.now())
		if delay < 0 {
			delay = 0
		}
		ctx, cancel = context.WithTimeout(m.ctx, delay)
	}
	m.activityCtx, m.activityCancel = ctx, cancel
}

// keptWhileAsleep reports whether the list still holds the last full rows
// although a fetch failed: only a quiet fetch while asleep keeps them, and a
// failure that cleared them shows the error screen instead.
func (m *model) keptWhileAsleep() bool {
	return m.sleeping && m.snapshot.Login != "" && !m.snapshot.Preview
}

func (m *model) invalidateQuota() {
	if m.quotaCancel != nil {
		m.quotaCancel()
		m.quotaCancel = nil
	}
	m.quotaGeneration++
	m.quotaPaused = true
}

func (m *model) enterSleep() {
	m.sleeping = true
	m.wakeFetchPending = false
	m.refreshGeneration++
	m.countdownGeneration++
	m.refreshDue = time.Time{}
	m.cancelFetch()
	if !m.loginActive {
		m.loading = false
	}
	m.fetchQuiet = false
	m.refetchForRules = false
	m.invalidateQuota()
	m.invalidateWatch()
	m.snoozeGeneration++
	m.stopFlash()
	if m.activityCancel != nil {
		m.activityCancel()
		m.activityCancel = nil
	}
	m.activityCtx = nil
	m.activityGeneration++
	m.activityDeadline = time.Time{}
	if m.snapshot.Preview {
		m.snapshot = github.Snapshot{}
		m.rebuildVisiblePRs()
	}
}

func (m *model) resumeActivity() tea.Cmd {
	m.sleeping = false
	deadline := m.effectiveDeadline(m.now())
	m.replaceActivity(deadline)
	if m.awaitingToken() {
		return m.scheduleTokenRecheck()
	}
	return tea.Batch(m.startAutomaticFetch(), m.pollQuota())
}

// reconcileSchedule applies activity transitions and maintains one local
// reevaluation timer. The timer only notices schedule/clock changes.
func (m *model) reconcileSchedule() tea.Cmd {
	now := m.now()
	if !m.manualWakeUntil.IsZero() && !m.manualWake(now) {
		m.manualWakeUntil = time.Time{}
		m.resetScheduleTimer()
	}
	active := m.scheduledActivity(now)
	first := !m.scheduleInitialized
	m.scheduleInitialized = true
	var cmds []tea.Cmd
	if first {
		if active {
			m.sleeping = false
			m.replaceActivity(m.effectiveDeadline(now))
		} else {
			m.enterSleep()
		}
	} else if !active && !m.sleeping {
		m.enterSleep()
	} else if active && m.sleeping {
		cmds = append(cmds, m.resumeActivity())
	} else if active {
		deadline := m.effectiveDeadline(now)
		if !deadline.Equal(m.activityDeadline) {
			m.replaceActivity(deadline)
			if m.fetchCancel != nil {
				m.cancelFetch()
				m.loading = false
			}
			m.invalidateQuota()
			m.invalidateWatch()
			cmds = append(cmds, m.startAutomaticFetch(), m.pollQuota())
		}
	}
	if m.wakeFetchPending && !m.sleeping && !m.loading && !m.fetchDeferred() {
		cmds = append(cmds, m.startAutomaticFetch())
	}
	if now.Before(m.scheduleTimerAt) {
		return tea.Batch(cmds...)
	}
	m.scheduleTimerAt = time.Time{}
	next := m.nextWindowBoundary(now)
	if m.manualWake(now) && (next.IsZero() || m.manualWakeUntil.Before(next)) {
		next = m.manualWakeUntil
	}
	if !next.IsZero() {
		delay := min(max(next.Sub(now), time.Millisecond), time.Minute)
		m.scheduleGeneration++
		generation := m.scheduleGeneration
		m.scheduleTimerAt = now.Add(delay)
		cmds = append(cmds, tea.Tick(delay, func(time.Time) tea.Msg { return scheduleTickMsg{generation} }))
	}
	return tea.Batch(cmds...)
}

func (m *model) wakeForHour() tea.Cmd {
	if !m.manualWakeUntil.IsZero() {
		m.manualWakeUntil = time.Time{}
	} else if !m.pollingAllowed() {
		m.manualWakeUntil = m.now().Add(time.Hour)
	} else {
		return nil
	}
	m.resetScheduleTimer()
	return m.reconcileSchedule()
}

func (m *model) openSchedule() tea.Cmd {
	config := m.scheduleConfig
	if m.scheduleErr != nil {
		config = schedule.Default()
	}
	m.scheduleEditor = &scheduleEditor{
		enabled: config.Enabled,
		days:    slices.Clone(config.Days),
		start:   config.Start,
		end:     config.End,
	}
	return m.showScheduleForm()
}

func (m *model) showScheduleForm() tea.Cmd {
	e := m.scheduleEditor
	zone, _ := m.now().In(time.Local).Zone()
	dayNames := []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
	dayLabels := []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}
	options := make([]huh.Option[string], len(dayNames))
	for i := range dayNames {
		options[i] = huh.NewOption(dayLabels[i], dayNames[i])
	}
	// Days and hours matter only to an enabled schedule; a disabled one keeps
	// the values it had.
	disabled := func() bool { return !e.enabled }
	form := huh.NewForm(
		huh.NewGroup(huh.NewConfirm().Title("Enable active hours").Description("Pause automatic polling outside this local-time schedule.").Value(&e.enabled)),
		huh.NewGroup(huh.NewMultiSelect[string]().Title("Active weekdays").Description("Choose the days this window starts (local time, "+zone+").").Options(options...).Value(&e.days)).WithHideFunc(disabled),
		huh.NewGroup(
			huh.NewInput().Title("Start (HH:mm)").Description("Inclusive local start time.").Value(&e.start).Validate(func(value string) error {
				config := schedule.Config{Enabled: true, Days: e.days, Start: value, End: e.end}
				_, err := schedule.Compile(config)
				return err
			}),
			huh.NewInput().Title("End (HH:mm)").Description("Exclusive local end; a time earlier than Start continues into the next day.").Value(&e.end).Validate(func(value string) error {
				config := schedule.Config{Enabled: true, Days: e.days, Start: e.start, End: value}
				_, err := schedule.Compile(config)
				return err
			}),
		).WithHideFunc(disabled),
	)
	dark := m.darkBackground
	form.WithAccessible(false).WithShowHelp(true).WithTheme(huh.ThemeFunc(func(bool) *huh.Styles { return rulesTheme(dark) }))
	form.SubmitCmd, form.CancelCmd = nil, nil
	e.form = form
	m.sizeScheduleForm()
	return form.Init()
}

func (m *model) sizeScheduleForm() {
	m.scheduleEditor.form.WithWidth(max(min(m.width, scheduleWidth), 1)).WithHeight(max(m.height-scheduleChrome, 1))
}

func (m *model) closeSchedule() { m.scheduleEditor = nil }

func (m *model) updateSchedule(msg tea.Msg) tea.Cmd {
	e := m.scheduleEditor
	if press, ok := msg.(tea.KeyPressMsg); ok && (press.Code == tea.KeyEscape || press.Code == tea.KeyEsc) {
		m.closeSchedule()
		return nil
	}
	form, cmd := e.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		e.form = f
	}
	switch e.form.State {
	case huh.StateAborted:
		m.closeSchedule()
		return nil
	case huh.StateCompleted:
		return m.saveSchedule()
	}
	return cmd
}

func (m *model) saveSchedule() tea.Cmd {
	e := m.scheduleEditor
	config := schedule.Config{Enabled: e.enabled, Days: slices.Clone(e.days), Start: strings.TrimSpace(e.start), End: strings.TrimSpace(e.end)}
	window, err := schedule.Compile(config)
	if err != nil {
		return nil
	}
	m.closeSchedule()
	m.manualWakeUntil = time.Time{}
	m.scheduleConfig, m.scheduleWindow, m.scheduleErr = config, window, nil
	m.resetScheduleTimer()
	if err := m.preferences.SaveSchedule(config); err != nil {
		m.preferenceErr = fmt.Errorf("Schedule not saved: %w", err)
	} else {
		m.preferenceErr = nil
		m.setNotice("Active hours saved")
	}
	return m.reconcileSchedule()
}

func (m *model) scheduleLines() []string {
	lines := []string{m.titleLine("prpr — "+m.accountLabel()+" — active hours", ""), ""}
	if m.scheduleErr != nil {
		lines = append(lines, "Saved schedule could not be read; automatic polling remains paused until repaired.", "")
	}
	lines = append(lines, strings.Split(m.scheduleEditor.form.View(), "\n")...)
	return append(lines, "", m.shortHelp(m.keys.scheduleHelp()))
}

func (m *model) scheduleStatus() string {
	if m.manualWake(m.now()) {
		return "Awake until " + m.manualWakeUntil.In(time.Local).Format("Mon 15:04")
	}
	if !m.sleeping {
		return ""
	}
	var parts []string
	if m.snapshot.Login != "" && !m.snapshot.Preview {
		parts = append(parts, "old snapshot")
	}
	if m.loading && m.fetchQuiet {
		parts = append(parts, "one-shot refresh")
	}
	if !m.lastSuccessAt.IsZero() {
		parts = append(parts, "last updated "+m.lastSuccessAt.In(time.Local).Format("Mon 15:04"))
	}
	if m.scheduleErr != nil {
		parts = append(parts, "saved schedule needs repair")
	} else if wake := m.nextWindowBoundary(m.now()); !wake.IsZero() {
		parts = append(parts, "next wake "+wake.In(time.Local).Format("Mon 15:04"))
	}
	return "Sleeping · " + strings.Join(parts, " · ")
}

func (m *model) sleepingLines() []string {
	lines := []string{m.titleLine("prpr — Sleeping", ""), "Automatic GitHub polling and alerts are paused."}
	if m.scheduleErr != nil {
		lines = append(lines, "The saved active-hours schedule could not be read; repair it with S or wake once with w.")
	} else if wake := m.nextWindowBoundary(m.now()); !wake.IsZero() {
		lines = append(lines, "Next wake: "+wake.In(time.Local).Format("Monday 15:04"))
	}
	return append(append(lines, ""), m.helpLines(keyMap.sleepingHelp)...)
}
