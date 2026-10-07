package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/preferences"
)

// refreshChoices are the intervals Refresh every cycles through.
var refreshChoices = []time.Duration{0, time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute}

// fullRefreshChoices are the intervals Full refresh every cycles through;
// zero is never.
var fullRefreshChoices = []time.Duration{5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour, 0}

// nextFullRefresh is the choice after d, the last wrapping to the first; a
// value that is not a choice goes to the default.
func nextFullRefresh(d time.Duration) time.Duration {
	i := slices.Index(fullRefreshChoices, d)
	if i < 0 {
		return preferences.DefaultFullRefresh
	}
	return fullRefreshChoices[(i+1)%len(fullRefreshChoices)]
}

func formatFullRefresh(d time.Duration) string {
	switch {
	case d == 0:
		return "never"
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	}
	return preferences.FormatRefresh(d)
}

// settingKind is how a Settings row changes.
type settingKind uint8

const (
	settingHeading settingKind = iota
	settingToggle
	settingChoice
	settingText
	settingChecklist
	settingEditor
)

// settingRow is a row of Settings: a heading, or a setting with its value,
// the list key that also changes it, and what Space or Enter does.
type settingRow struct {
	kind     settingKind
	label    string
	shortcut string
	// errKey names the setting's unreadable saved value, if any.
	errKey string
	value  func(m *model) string
	change func(m *model) tea.Cmd
}

// settingsEditor is the open Settings screen.
type settingsEditor struct {
	rows           []settingRow
	cursor, offset int
	// editing is the bots field while it is open; queues is the checklist's
	// choices while it is open, with queueCursor on one of them.
	editing     *textinput.Model
	queues      []github.Queue
	queueCursor int
	// problem says why the bots field kept the old value.
	problem string
}

var queueChoices = []github.Queue{github.QueueTrunk, github.QueueGitHub}

func queueName(q github.Queue) string {
	if q == github.QueueTrunk {
		return "Trunk"
	}
	return "GitHub"
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func settingRows() []settingRow {
	return []settingRow{
		{kind: settingHeading, label: "Display"},
		{kind: settingChoice, label: "Icons", shortcut: "i",
			value: func(m *model) string {
				if m.icons.nerd {
					return "Nerd"
				}
				return "Unicode"
			},
			change: func(m *model) tea.Cmd { m.toggleIcons(); return nil }},
		{kind: settingToggle, label: "Show drafts", shortcut: "D",
			value:  func(m *model) string { return onOff(m.showDrafts) },
			change: func(m *model) tea.Cmd { m.toggleDrafts(); return nil }},
		{kind: settingToggle, label: "Legend", shortcut: "L",
			value: func(m *model) string {
				if m.legend {
					return "shown"
				}
				return "hidden"
			},
			change: func(m *model) tea.Cmd { m.toggleLegend(); return nil }},
		{kind: settingToggle, label: "Mouse", shortcut: "m",
			value:  func(m *model) string { return onOff(m.mouse) },
			change: func(m *model) tea.Cmd { m.applyMouse(!m.mouse); return nil }},
		{kind: settingChoice, label: "Editor", errKey: "editor",
			value:  func(m *model) string { return editorName(m.editor) },
			change: func(m *model) tea.Cmd { m.applyEditor(nextEditor(m.editor)); return nil }},
		{kind: settingToggle, label: "Terminal title",
			value:  func(m *model) string { return onOff(m.setTitle) },
			change: func(m *model) tea.Cmd { m.applyTitle(!m.setTitle); return nil }},
		{kind: settingHeading, label: "Updates"},
		{kind: settingChoice, label: "Refresh every", errKey: "refresh",
			value: func(m *model) string {
				if m.refreshInterval == 0 {
					return "off"
				}
				return preferences.FormatRefresh(m.refreshInterval)
			},
			change: func(m *model) tea.Cmd { return m.applyRefresh(nextRefresh(m.refreshInterval)) }},
		{kind: settingChoice, label: "Full refresh every", errKey: "fullRefresh",
			value:  func(m *model) string { return formatFullRefresh(m.fullRefresh) },
			change: func(m *model) tea.Cmd { m.applyFullRefresh(nextFullRefresh(m.fullRefresh)); return nil }},
		{kind: settingToggle, label: "Desktop notifications", shortcut: "n",
			value:  func(m *model) string { return onOff(m.notify) },
			change: func(m *model) tea.Cmd { m.applyNotify(!m.notify); return nil }},
		{kind: settingHeading, label: "Data"},
		{kind: settingText, label: "Review bots", errKey: "bots",
			value: func(m *model) string {
				var names []string
				for _, bot := range m.sessionBots {
					names = append(names, bot.Name)
				}
				if len(names) == 0 {
					return "none"
				}
				return strings.Join(names, ", ")
			},
			change: func(m *model) tea.Cmd { return m.settings.openBotsField(github.FormatBots(m.sessionBots), m.width) }},
		{kind: settingChecklist, label: "Merge queues", errKey: "queues",
			value: func(m *model) string {
				var names []string
				for _, q := range m.sessionQueues {
					names = append(names, queueName(q))
				}
				if len(names) == 0 {
					return "none"
				}
				return strings.Join(names, ", ")
			},
			change: func(m *model) tea.Cmd {
				// A non-nil list marks the checklist open, even with no queues.
				m.settings.queues, m.settings.queueCursor = append([]github.Queue{}, m.sessionQueues...), 0
				return nil
			}},
		{kind: settingChoice, label: "Recently merged", errKey: "merged",
			value:  func(m *model) string { return formatMerged(m.merged) },
			change: func(m *model) tea.Cmd { return m.applyMerged(nextMerged(m.merged)) }},
		{kind: settingToggle, label: "Merged list", shortcut: "H",
			value: func(m *model) string {
				if m.mergedCollapsed {
					return "collapsed"
				}
				return "shown"
			},
			change: func(m *model) tea.Cmd { m.toggleMergedCollapsed(); return nil }},
		{kind: settingHeading, label: "Editors"},
		{kind: settingEditor, label: "Ready-to-merge rules",
			value: func(m *model) string {
				if len(m.rules.Owners) == 0 {
					return "default (merge button)"
				}
				return plural(len(m.rules.Owners), "owner rule")
			},
			change: func(m *model) tea.Cmd { return m.openRules() }},
		{kind: settingEditor, label: "Active hours",
			value: func(m *model) string {
				if !m.scheduleConfig.Enabled {
					return "always"
				}
				return m.scheduleConfig.Start + "–" + m.scheduleConfig.End
			},
			change: func(m *model) tea.Cmd { return m.openSchedule() }},
	}
}

// nextRefresh is the first choice after d; a value between choices goes to
// the next larger one, and the last wraps to off.
func nextRefresh(d time.Duration) time.Duration {
	for _, choice := range refreshChoices {
		if choice > d {
			return choice
		}
	}
	return refreshChoices[0]
}

// nextEditor is the editor after editor in preferences.Editors, the last
// wrapping to the first.
func nextEditor(editor string) string {
	i := slices.Index(preferences.Editors, editor)
	return preferences.Editors[(i+1)%len(preferences.Editors)]
}

// openSettings opens Settings on its first setting.
func (m *model) openSettings() tea.Cmd {
	m.settings = &settingsEditor{rows: settingRows(), cursor: 1}
	m.settingsNotice = ""
	return nil
}

func (e *settingsEditor) openBotsField(value string, width int) tea.Cmd {
	input := textinput.New()
	input.Prompt = ""
	input.SetWidth(max(width-2-e.labelWidth()-3-1, 10))
	input.SetValue(value)
	input.CursorEnd()
	e.editing, e.problem = &input, ""
	return input.Focus()
}

// labelWidth is the width of the widest setting label.
func (e *settingsEditor) labelWidth() int {
	width := 0
	for _, row := range e.rows {
		width = max(width, ansi.StringWidth(row.label))
	}
	return width
}

// step moves the cursor to the next setting in dir, skipping headings.
func (e *settingsEditor) step(dir int) {
	for i := e.cursor + dir; i >= 0 && i < len(e.rows); i += dir {
		if e.rows[i].kind != settingHeading {
			e.cursor = i
			return
		}
	}
}

// updateSettings handles a message while Settings is open and no editor
// it opened is.
func (m *model) updateSettings(msg tea.Msg) tea.Cmd {
	e := m.settings
	k := m.keys.SettingsKeys
	press, ok := msg.(tea.KeyPressMsg)
	switch {
	case e.editing != nil:
		if !ok {
			var cmd tea.Cmd
			*e.editing, cmd = e.editing.Update(msg)
			return cmd
		}
		switch {
		case key.Matches(press, k.Revert):
			e.editing, e.problem = nil, ""
			return nil
		case key.Matches(press, k.Keep):
			cmd, err := m.applyBots(strings.TrimSpace(e.editing.Value()))
			if err != nil {
				e.problem = err.Error()
				return nil
			}
			e.editing, e.problem = nil, ""
			return cmd
		}
		var cmd tea.Cmd
		*e.editing, cmd = e.editing.Update(msg)
		return cmd
	case !ok:
		return nil
	case e.queues != nil:
		switch {
		case key.Matches(press, k.Revert):
			e.queues = nil
		case key.Matches(press, k.Up):
			e.queueCursor = max(e.queueCursor-1, 0)
		case key.Matches(press, k.Down):
			e.queueCursor = min(e.queueCursor+1, len(queueChoices)-1)
		case key.Matches(press, k.Toggle):
			q := queueChoices[e.queueCursor]
			if i := slices.Index(e.queues, q); i >= 0 {
				e.queues = slices.Delete(e.queues, i, i+1)
			} else {
				e.queues = append(e.queues, q)
			}
		case key.Matches(press, k.Keep):
			queues := slices.DeleteFunc(slices.Clone(queueChoices), func(q github.Queue) bool { return !slices.Contains(e.queues, q) })
			e.queues = nil
			return m.applyQueues(queues)
		}
		return nil
	}
	switch {
	case key.Matches(press, k.Close):
		m.settings = nil
	case key.Matches(press, k.Up):
		e.step(-1)
	case key.Matches(press, k.Down):
		e.step(1)
	case key.Matches(press, k.Change):
		return e.rows[e.cursor].change(m)
	}
	return nil
}

var (
	settingsHeading = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	settingsFaint   = lipgloss.NewStyle().Faint(true)
	settingsBad     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

// settingsLines draws Settings, scrolled to keep the cursor in view.
func (m *model) settingsLines() []string {
	e := m.settings
	header := []string{m.titleLine("prpr — "+m.accountLabel()+" — settings", ""), ""}
	help := m.shortHelp(m.keys.settingsHelp())
	switch {
	case e.editing != nil:
		help = m.shortHelp(m.keys.settingsFieldHelp())
	case e.queues != nil:
		help = m.shortHelp(m.keys.settingsQueuesHelp())
	}
	status := settingsFaint.Render(m.settingsNotice)
	if m.settingsNotice == "Not saved." && m.preferenceErr != nil {
		status = settingsBad.Render(singleLine(m.preferenceErr.Error()))
	}
	footer := []string{"", status, help}
	labelWidth := e.labelWidth()
	var body []string
	cursorLine := 0
	for i, row := range e.rows {
		if i == e.cursor {
			cursorLine = len(body)
		}
		if row.kind == settingHeading {
			body = append(body, settingsHeading.Render(row.label))
			continue
		}
		marker := "  "
		if i == e.cursor {
			marker = "> "
		}
		value := row.value(m)
		prefix := 2 + labelWidth + 3
		if i == e.cursor && e.editing != nil {
			// The field scrolls within the room the row leaves.
			e.editing.SetWidth(max(m.width-prefix-1, 10))
			value = e.editing.View()
		}
		if row.kind == settingEditor {
			value += "  →"
		}
		line := marker + row.label + strings.Repeat(" ", labelWidth-ansi.StringWidth(row.label)+3) + value
		if row.shortcut != "" {
			line += "  " + settingsFaint.Render(row.shortcut)
		}
		if row.errKey != "" {
			if err := m.preferences.SettingErr(row.errKey); err != nil {
				line += "  " + settingsBad.Render("saved value not read")
			}
		}
		body = append(body, line)
		if i == e.cursor && e.editing != nil && e.problem != "" {
			// Under the field, which pads itself to its width.
			body = append(body, strings.Repeat(" ", prefix)+settingsBad.Render(singleLine(e.problem)))
		}
		if i == e.cursor && e.queues != nil {
			for j, q := range queueChoices {
				box := "[ ]"
				if slices.Contains(e.queues, q) {
					box = "[x]"
				}
				pointer := "    "
				if j == e.queueCursor {
					pointer = "  > "
				}
				body = append(body, pointer+box+" "+queueName(q))
			}
		}
	}
	height := max(m.height-len(header)-len(footer), 1)
	// The heading above the cursor's row stays in view with it.
	top := max(cursorLine-1, 0)
	e.offset = min(e.offset, top)
	e.offset = max(e.offset, cursorLine-height+1, 0)
	if e.queues != nil || e.problem != "" {
		e.offset = max(e.offset, cursorLine+len(queueChoices)-height+1)
	}
	end := min(e.offset+height, len(body))
	lines := append(header, body[e.offset:end]...)
	for len(lines) < len(header)+height {
		lines = append(lines, "")
	}
	lines = append(lines, footer...)
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], m.width, "…")
	}
	return lines
}
