package tui

import (
	"slices"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/paginator"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
)

type keyMap struct {
	ChoiceUp        key.Binding
	ChoiceDown      key.Binding
	Continue        key.Binding
	PickRepository  key.Binding
	AllRepositories key.Binding
	Refresh         key.Binding
	Retry           key.Binding
	Login           key.Binding
	Help            key.Binding
	Quit            key.Binding
	ForceQuit       key.Binding
	Table           table.KeyMap
	Pages           paginator.KeyMap
	Picker          pickerKeyMap
}

type pickerKeyMap struct {
	Up     key.Binding
	Down   key.Binding
	Apply  key.Binding
	Cancel key.Binding
	Clear  key.Binding
	Retry  key.Binding
	Quit   key.Binding
}

func defaultKeyMap() keyMap {
	tableKeys := table.DefaultKeyMap()
	tableKeys.PageUp.SetHelp("b/pgup", "page up")
	tableKeys.PageDown.SetHelp("f/pgdn", "page down")
	return keyMap{
		ChoiceUp:        key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		ChoiceDown:      key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Continue:        key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "continue")),
		PickRepository:  key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "change repo")),
		AllRepositories: key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "all PRs")),
		Refresh:         key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Retry:           key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "retry")),
		Login:           key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "log in to GitHub")),
		Help:            key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "more")),
		Quit:            key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
		ForceQuit:       key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
		Table:           tableKeys,
		Pages: paginator.KeyMap{
			PrevPage: key.NewBinding(key.WithKeys("left"), key.WithHelp("←", "prev page")),
			NextPage: key.NewBinding(key.WithKeys("right"), key.WithHelp("→", "next page")),
		},
		Picker: pickerKeyMap{
			Up:     key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", "up")),
			Down:   key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", "down")),
			Apply:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "apply")),
			Cancel: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
			Clear:  key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "clear")),
			Retry:  key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "retry")),
			Quit:   key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
		},
	}
}

// syncKeys derives every binding's enabled state from the current model
// state. Disabled bindings neither match key presses nor appear in help.
func (m *model) syncKeys() {
	k := &m.keys
	idle := !m.loading && !m.loginActive
	ready := idle && m.err == nil && m.picker == nil
	scopeChoice := ready && !m.scopeChosen && m.snapshot.Login != ""
	browsing := ready && m.scopeChosen && m.snapshot.Login != ""
	// Rows kept on screen during a refresh stay navigable.
	viewing := !m.loginActive && m.err == nil && m.picker == nil && m.scopeChosen && m.snapshot.Login != ""
	rows := viewing && len(m.visiblePRs) > 0

	k.ChoiceUp.SetEnabled(scopeChoice)
	k.ChoiceDown.SetEnabled(scopeChoice)
	k.Continue.SetEnabled(scopeChoice)
	k.PickRepository.SetEnabled(browsing)
	k.AllRepositories.SetEnabled(browsing)
	k.Refresh.SetEnabled(idle && m.err == nil && m.picker == nil)
	k.Retry.SetEnabled(idle && m.err != nil)
	k.Login.SetEnabled(idle && m.err != nil)
	k.Help.SetEnabled(viewing)
	if m.help.ShowAll {
		k.Help.SetHelp("?", "less")
	} else {
		k.Help.SetHelp("?", "more")
	}
	k.Quit.SetEnabled(m.picker == nil)
	for _, binding := range []*key.Binding{
		&k.Table.LineUp, &k.Table.LineDown, &k.Table.PageUp, &k.Table.PageDown,
		&k.Table.HalfPageUp, &k.Table.HalfPageDown, &k.Table.GotoTop, &k.Table.GotoBottom,
	} {
		binding.SetEnabled(rows)
	}
	m.prTable.KeyMap = k.Table
	paged := rows && m.prPages.TotalPages > 1
	k.Pages.PrevPage.SetEnabled(paged)
	k.Pages.NextPage.SetEnabled(paged)
	m.prPages.KeyMap = k.Pages

	picking := m.picker != nil
	editable := picking && !m.picker.lookup
	k.Picker.Up.SetEnabled(editable)
	k.Picker.Down.SetEnabled(editable)
	k.Picker.Apply.SetEnabled(picking && !m.picker.busy)
	k.Picker.Cancel.SetEnabled(picking)
	k.Picker.Clear.SetEnabled(editable)
	k.Picker.Retry.SetEnabled(picking && !m.picker.busy)
	k.Picker.Quit.SetEnabled(picking)
}

// helpKeys adapts a screen's bindings to help.KeyMap.
type helpKeys struct {
	short []key.Binding
	full  [][]key.Binding
}

func (h helpKeys) ShortHelp() []key.Binding  { return h.short }
func (h helpKeys) FullHelp() [][]key.Binding { return h.full }

func (k keyMap) scopeChoiceHelp() helpKeys {
	return helpKeys{short: []key.Binding{k.ChoiceUp, k.ChoiceDown, k.Continue, k.Quit}}
}

func (k keyMap) errorHelp() helpKeys {
	return helpKeys{short: []key.Binding{k.Login, k.Retry, k.Quit}}
}

func (k keyMap) listHelp() helpKeys {
	t := k.Table
	return helpKeys{
		short: []key.Binding{t.LineUp, t.LineDown, k.PickRepository, k.AllRepositories, k.Refresh, k.Help, k.Quit},
		full: [][]key.Binding{
			{t.LineUp, t.LineDown, t.GotoTop, t.GotoBottom},
			{t.PageUp, t.PageDown, t.HalfPageUp, t.HalfPageDown},
			{k.Pages.PrevPage, k.Pages.NextPage},
			{k.PickRepository, k.AllRepositories, k.Refresh},
			{k.Help, k.Quit},
		},
	}
}

func (k keyMap) pickerHelp() helpKeys {
	p := k.Picker
	return helpKeys{short: []key.Binding{p.Up, p.Down, p.Apply, p.Cancel, p.Clear, p.Retry, p.Quit}}
}

// bound reports whether msg is one of the bindings' keys, enabled or not.
func bound(msg tea.KeyPressMsg, bindings ...key.Binding) bool {
	for _, binding := range bindings {
		if slices.Contains(binding.Keys(), msg.String()) {
			return true
		}
	}
	return false
}

// fullHelpFits reports whether the full help leaves room for the list header,
// a minimal table, and the status line.
func (m *model) fullHelpFits(keys helpKeys) bool {
	rows := 0
	for _, column := range keys.full {
		rows = max(rows, len(column))
	}
	return m.height-7 >= rows
}
