package tui

import (
	"errors"
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/DragosMocrii/prpr/internal/github"
)

// settingSaved reports a setting's save: the warning when it failed, the
// value staying for this session.
func (m *model) settingSaved(err error) {
	if err != nil {
		m.preferenceErr = fmt.Errorf("Setting not saved: %w", err)
		m.settingsNotice = "Not saved."
		return
	}
	m.settingsNotice = "Saved."
}

// applyRefresh uses a new refresh interval from now; zero turns automatic
// refreshes off. A fetch running keeps going and schedules with the new
// interval when it finishes.
func (m *model) applyRefresh(d time.Duration) tea.Cmd {
	m.refreshInterval = d
	m.settingSaved(m.preferences.SaveRefresh(d))
	var authErr *github.AuthError
	if m.loading || errors.As(m.err, &authErr) {
		return nil
	}
	// A new generation drops the old timer.
	m.refreshGeneration++
	m.refreshDue = time.Time{}
	m.countdownGeneration++
	if m.snapshot.Login == "" && m.err == nil {
		return nil
	}
	return m.scheduleAutoRefresh()
}

// applyBots uses and saves bots written as ParseBots reads them; nothing
// changes when they cannot be read.
func (m *model) applyBots(text string) (tea.Cmd, error) {
	bots, err := github.ParseBots(text)
	if err != nil {
		return nil, err
	}
	if m.client != nil {
		m.client.SetBots(bots)
	}
	m.bots = len(bots) > 0
	m.sessionBots = bots
	m.settingSaved(m.preferences.SaveBots(text))
	m.rebuildPRTable(false)
	return m.settingsFetch(), nil
}

// applyQueues uses and saves the merge queues.
func (m *model) applyQueues(queues []github.Queue) tea.Cmd {
	if m.client != nil {
		m.client.SetQueues(queues)
	}
	m.sessionQueues = slices.Clone(queues)
	m.settingSaved(m.preferences.SaveQueues(queues))
	return m.settingsFetch()
}

// settingsFetch fetches with new bots or queues. A running fetch selected
// the old ones, so it is replaced; the new one's result becomes the change
// baseline without marking or alerting.
func (m *model) settingsFetch() tea.Cmd {
	// A failed fetch keeps the change baseline, so the next result must
	// replace it too; before any fetch, the first one resets anyway.
	m.settingsBaseline = true
	if !m.loading && m.snapshot.Login == "" && m.err == nil {
		return nil
	}
	return m.startFetch()
}

// applyEditor uses and saves the editor e opens a pull request in.
func (m *model) applyEditor(editor string) {
	m.editor = editor
	m.settingSaved(m.preferences.SaveEditor(editor))
}

// applyNotify turns desktop notifications on or off and saves it.
func (m *model) applyNotify(on bool) {
	m.notify = on
	m.settingSaved(m.preferences.SaveNotify(on))
}

// applyMouse turns mouse mode on or off and saves it.
func (m *model) applyMouse(on bool) {
	m.mouse = on
	m.pointer = pointer{}
	m.settingSaved(m.preferences.SaveMouse(on))
}

// applyTitle turns the terminal title on or off and saves it; off stops a
// flash.
func (m *model) applyTitle(on bool) {
	m.setTitle = on
	if !on {
		m.stopFlash()
		m.terminalFocus = focusUnknown
	}
	m.settingSaved(m.preferences.SaveTitle(on))
}

// applyFullRefresh uses and saves how often a fetch is complete, from the
// next fetch on.
func (m *model) applyFullRefresh(d time.Duration) {
	m.fullRefresh = d
	if m.client != nil {
		m.client.SetFullRefresh(d)
	}
	m.settingSaved(m.preferences.SaveFullRefresh(d))
}
