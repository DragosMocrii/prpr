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
	NextPane        key.Binding
	PrevPane        key.Binding
	Refresh         key.Binding
	ClearMarks      key.Binding
	Details         key.Binding
	Back            key.Binding
	Search          key.Binding
	Drafts          key.Binding
	QuickFailing    key.Binding
	QuickReady      key.Binding
	ClearFilters    key.Binding
	Categories      key.Binding
	Open            key.Binding
	CopyURL         key.Binding
	Mouse           key.Binding
	Notify          key.Binding
	Icons           key.Binding
	Legend          key.Binding
	Rules           key.Binding
	RulesCancel     key.Binding
	Schedule        key.Binding
	Wake            key.Binding
	ScheduleCancel  key.Binding
	Snooze          key.Binding
	Undo            key.Binding
	Rerequest       key.Binding
	DismissPlead    key.Binding
	Account         key.Binding
	Retry           key.Binding
	Login           key.Binding
	Help            key.Binding
	Quit            key.Binding
	ForceQuit       key.Binding
	Table           table.KeyMap
	Pages           paginator.KeyMap
	Picker          pickerKeyMap
	SearchInput     searchKeyMap
	WatchlistName   searchKeyMap
	AccountPicker   accountKeyMap
}

type accountKeyMap struct {
	Up     key.Binding
	Down   key.Binding
	Apply  key.Binding
	Cancel key.Binding
	Retry  key.Binding
	Quit   key.Binding
}

type searchKeyMap struct {
	Apply  key.Binding
	Cancel key.Binding
	Clear  key.Binding
	Quit   key.Binding
}

type pickerKeyMap struct {
	Up     key.Binding
	Down   key.Binding
	Apply  key.Binding
	Cancel key.Binding
	Clear  key.Binding
	Retry  key.Binding
	Mark   key.Binding
	Edit   key.Binding
	Delete key.Binding
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
		PickRepository:  key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "repo")),
		AllRepositories: key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "all PRs")),
		NextPane:        key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "switch")),
		PrevPane:        key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "prev list")),
		Refresh:         key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		ClearMarks:      key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "clear marks")),
		Details:         key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "details")),
		Back:            key.NewBinding(key.WithKeys("esc", "enter"), key.WithHelp("esc", "back")),
		Search:          key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		Drafts:          key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "show drafts")),
		QuickFailing:    key.NewBinding(key.WithKeys("F"), key.WithHelp("F", "failing CI")),
		QuickReady:      key.NewBinding(key.WithKeys("M"), key.WithHelp("M", "ready to merge")),
		ClearFilters:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filters")),
		Categories:      key.NewBinding(key.WithKeys("1", "2", "3", "4", "5", "6", "7"), key.WithHelp("1–7", "filter")),
		Open:            key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open")),
		CopyURL:         key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy URL")),
		Mouse:           key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "mouse on")),
		Notify:          key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "notify on")),
		Icons:           key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "nerd icons")),
		Legend:          key.NewBinding(key.WithKeys("L"), key.WithHelp("L", "show legend")),
		Rules:           key.NewBinding(key.WithKeys(","), key.WithHelp(",", "ready rules")),
		RulesCancel:     key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel without saving")),
		Schedule:        key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "active hours")),
		Wake:            key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "wake 1h")),
		ScheduleCancel:  key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel without saving")),
		Snooze:          key.NewBinding(key.WithKeys("z"), key.WithHelp("z", "snooze")),
		Undo:            key.NewBinding(key.WithKeys("U"), key.WithHelp("U", "undo snooze")),
		Rerequest:       key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "request reviews again")),
		DismissPlead:    key.NewBinding(key.WithKeys("X"), key.WithHelp("X", "dismiss asked again")),
		Account:         key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "account")),
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
			Mark:   key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "mark")),
			Edit:   key.NewBinding(key.WithKeys("ctrl+e"), key.WithHelp("ctrl+e", "edit list")),
			Delete: key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "delete list")),
			Quit:   key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
		},
		AccountPicker: accountKeyMap{
			Up:     key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
			Down:   key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
			Apply:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "use account")),
			Cancel: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
			Retry:  key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "check again")),
			Quit:   key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
		},
		WatchlistName: searchKeyMap{
			Apply:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "save list")),
			Cancel: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
			Clear:  key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "clear")),
			Quit:   key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
		},
		SearchInput: searchKeyMap{
			Apply:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "keep search")),
			Cancel: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
			Clear:  key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "clear")),
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
	// A preview lets the scope be chosen before the details arrive.
	previewing := m.loading && !m.loginActive && m.snapshot.Preview && m.err == nil && m.picker == nil
	scopeChoice := (ready || previewing) && !m.scopeChosen && m.snapshot.Login != ""
	browsing := ready && m.scopeChosen && m.snapshot.Login != ""
	// Rows kept on screen during a refresh stay navigable.
	viewing := !m.loginActive && m.err == nil && m.picker == nil && m.scopeChosen && m.snapshot.Login != ""
	pane := m.focused()
	rows := viewing && rowCount(pane) > 0
	details := m.detailsShown()
	// The details screen keeps row movement, opening, refresh, and quit.
	listing := viewing && !details
	scheduleControls := !m.loginActive && m.picker == nil && m.accounts == nil && m.rulesEditor == nil && m.snoozeEditor == nil && m.rerequest == nil && !details
	k.Schedule.SetEnabled(scheduleControls && idle)
	k.Wake.SetEnabled(scheduleControls && (!m.manualWakeUntil.IsZero() ||
		(idle && (m.scheduleErr != nil || m.scheduleConfig.Enabled && !m.pollingAllowed()))))
	k.ScheduleCancel.SetEnabled(m.scheduleEditor != nil)
	if !m.manualWakeUntil.IsZero() {
		k.Wake.SetHelp("w", "use schedule")
	} else {
		k.Wake.SetHelp("w", "wake 1h")
	}

	k.ChoiceUp.SetEnabled(scopeChoice)
	k.ChoiceDown.SetEnabled(scopeChoice)
	k.Continue.SetEnabled(scopeChoice)
	k.PickRepository.SetEnabled(browsing && !details)
	k.AllRepositories.SetEnabled(browsing && !details)
	k.NextPane.SetEnabled(listing)
	k.PrevPane.SetEnabled(listing)
	k.Refresh.SetEnabled(idle && m.err == nil && m.picker == nil)
	if m.sleeping {
		k.Refresh.SetHelp("r", "refresh once")
	} else {
		k.Refresh.SetHelp("r", "refresh")
	}
	k.ClearMarks.SetEnabled(listing && m.hasMarks())
	k.Details.SetEnabled(rows && !details)
	for _, binding := range []*key.Binding{&k.Search, &k.Drafts, &k.QuickFailing, &k.QuickReady} {
		binding.SetEnabled(listing)
	}
	k.ClearFilters.SetEnabled(listing && m.filtersActive())
	if m.showDrafts {
		k.Drafts.SetHelp("D", "hide drafts")
	} else {
		k.Drafts.SetHelp("D", "show drafts")
	}
	k.Categories.SetEnabled(listing && m.summaryShown())
	k.Back.SetEnabled(details)
	k.Open.SetEnabled(rows)
	k.CopyURL.SetEnabled(rows)
	k.Mouse.SetEnabled(listing)
	if m.mouse {
		k.Mouse.SetHelp("m", "disable mouse")
	} else {
		k.Mouse.SetHelp("m", "enable mouse")
	}
	k.Notify.SetEnabled(listing)
	k.Icons.SetEnabled(listing)
	k.Legend.SetEnabled(listing)
	if m.legend {
		k.Legend.SetHelp("L", "hide legend")
	} else {
		k.Legend.SetHelp("L", "show legend")
	}
	k.Rules.SetEnabled(listing && m.rulesEditor == nil)
	k.Snooze.SetEnabled(listing && rows && m.snoozeEditor == nil)
	k.Undo.SetEnabled(listing && m.snoozeEditor == nil)
	k.Rerequest.SetEnabled(listing && rows && m.focus != paneReview && m.rerequest == nil)
	k.DismissPlead.SetEnabled(listing && rows && m.selectedPleading())
	if m.focus == paneSnoozed {
		k.Snooze.SetHelp("z", "wake")
	} else {
		k.Snooze.SetHelp("z", "snooze")
	}
	if m.icons.nerd {
		k.Icons.SetHelp("i", "use Unicode icons")
	} else {
		k.Icons.SetHelp("i", "use Nerd icons")
	}
	k.Account.SetEnabled(m.switchAccounts && idle && m.picker == nil && m.accounts == nil && !details)
	choosing := m.accounts != nil
	k.AccountPicker.Up.SetEnabled(choosing)
	k.AccountPicker.Down.SetEnabled(choosing)
	k.AccountPicker.Apply.SetEnabled(choosing && !m.accounts.busy)
	k.AccountPicker.Cancel.SetEnabled(choosing)
	k.AccountPicker.Retry.SetEnabled(choosing && !m.accounts.busy)
	k.AccountPicker.Quit.SetEnabled(choosing)
	if m.notify {
		k.Notify.SetHelp("n", "disable notifications")
	} else {
		k.Notify.SetHelp("n", "enable notifications")
	}
	k.Retry.SetEnabled(idle && m.err != nil)
	k.Login.SetEnabled(idle && m.err != nil)
	k.Help.SetEnabled(listing)
	if m.helpOpen {
		k.Help.SetHelp("?/esc", "close help")
	} else {
		k.Help.SetHelp("?", "help")
	}
	k.Quit.SetEnabled(m.picker == nil)
	for _, binding := range []*key.Binding{
		&k.Table.LineUp, &k.Table.LineDown, &k.Table.PageUp, &k.Table.PageDown,
		&k.Table.HalfPageUp, &k.Table.HalfPageDown, &k.Table.GotoTop, &k.Table.GotoBottom,
	} {
		binding.SetEnabled(rows)
	}
	paged := rows && !details && pane.pages.TotalPages > 1
	k.Pages.PrevPage.SetEnabled(paged)
	k.Pages.NextPage.SetEnabled(paged)
	for _, id := range paneIDs {
		m.panes[id].table.KeyMap = k.Table
		m.panes[id].pages.KeyMap = k.Pages
	}

	picking := m.picker != nil
	naming := picking && m.picker.naming != nil
	editable := picking && !m.picker.lookup && !naming
	marking := picking && len(m.picker.marked) > 0
	busy := picking && m.picker.busy
	var candidate repositoryCandidate
	if picking && len(m.picker.candidates) > 0 {
		candidate = m.picker.selectedCandidate()
	}
	k.Picker.Up.SetEnabled(editable)
	k.Picker.Down.SetEnabled(editable)
	// Marks can be named while the repository list still loads.
	k.Picker.Apply.SetEnabled(editable && (!busy || marking))
	if marking {
		k.Picker.Apply.SetHelp("enter", "name list")
	} else {
		k.Picker.Apply.SetHelp("enter", "apply")
	}
	k.Picker.Cancel.SetEnabled(picking && !naming)
	if marking {
		k.Picker.Cancel.SetHelp("esc", "clear marks")
	} else {
		k.Picker.Cancel.SetHelp("esc", "cancel")
	}
	k.Picker.Clear.SetEnabled(editable)
	k.Picker.Retry.SetEnabled(picking && !busy && !naming)
	k.Picker.Mark.SetEnabled(editable && (candidate.kind == knownRepositoryCandidate ||
		(candidate.kind == lookupRepositoryCandidate && !busy)))
	k.Picker.Edit.SetEnabled(editable && candidate.kind == watchlistCandidate)
	k.Picker.Delete.SetEnabled(editable && candidate.kind == watchlistCandidate)
	k.Picker.Quit.SetEnabled(picking)
	for _, binding := range []*key.Binding{&k.WatchlistName.Apply, &k.WatchlistName.Cancel, &k.WatchlistName.Clear, &k.WatchlistName.Quit} {
		binding.SetEnabled(naming)
	}
}

// helpKeys is a screen's help line: short keys in priority order, dropped
// from the end to fit, then pinned keys, which always stay.
type helpKeys struct {
	short  []key.Binding
	pinned []key.Binding
}

// helpGroup is a titled group of the help overlay.
type helpGroup struct {
	title string
	keys  []key.Binding
}

func (k keyMap) scopeChoiceHelp() helpKeys {
	return helpKeys{short: []key.Binding{k.ChoiceUp, k.ChoiceDown, k.Continue, k.Account, k.Schedule, k.Wake}, pinned: []key.Binding{k.Quit}}
}

func (k keyMap) errorHelp() helpKeys {
	return helpKeys{short: []key.Binding{k.Login, k.Retry, k.Account, k.Schedule, k.Wake}, pinned: []key.Binding{k.Quit}}
}

// listHelp leads with the keys that apply only now, then everyday keys.
// Every other list key is in the help overlay.
func (k keyMap) listHelp() helpKeys {
	return helpKeys{
		short: []key.Binding{k.ClearFilters, k.DismissPlead, k.ClearMarks,
			k.Details, k.Open, k.Search, k.NextPane, k.Categories, k.PickRepository, k.Refresh},
		pinned: []key.Binding{k.Help, k.Quit},
	}
}

// listGroups are the help overlay's groups: every list key, enabled or not.
func (k keyMap) listGroups() []helpGroup {
	t, p := k.Table, k.Pages
	return []helpGroup{
		{"Move", []key.Binding{t.LineUp, t.LineDown, t.GotoTop, t.GotoBottom, t.PageUp, t.PageDown,
			t.HalfPageUp, t.HalfPageDown, p.PrevPage, p.NextPage, k.NextPane, k.PrevPane}},
		{"This PR", []key.Binding{k.Details, k.Open, k.CopyURL, k.Snooze, k.Undo, k.Rerequest, k.DismissPlead}},
		{"Filter", []key.Binding{k.Search, k.Categories, k.QuickFailing, k.QuickReady, k.Drafts, k.ClearFilters}},
		{"View", []key.Binding{k.Legend, k.Icons, k.Mouse, k.Notify, k.ClearMarks}},
		{"Scope & settings", []key.Binding{k.PickRepository, k.AllRepositories, k.Account, k.Rules,
			k.Schedule, k.Wake, k.Refresh, k.Quit}},
	}
}

func (k keyMap) helpOverlayHelp() helpKeys {
	t := k.Table
	return helpKeys{short: []key.Binding{t.LineUp, t.LineDown}, pinned: []key.Binding{k.Help, k.ForceQuit}}
}

func (k keyMap) detailsHelp() helpKeys {
	t := k.Table
	return helpKeys{short: []key.Binding{t.LineUp, t.LineDown, k.Open, k.CopyURL, k.Refresh}, pinned: []key.Binding{k.Back, k.Quit}}
}

func (k keyMap) rulesHelp() helpKeys {
	return helpKeys{pinned: []key.Binding{k.RulesCancel, k.ForceQuit}}
}

func (k keyMap) snoozeHelp() helpKeys {
	return helpKeys{pinned: []key.Binding{k.RulesCancel, k.ForceQuit}}
}
func (k keyMap) scheduleHelp() helpKeys {
	return helpKeys{pinned: []key.Binding{k.ScheduleCancel, k.ForceQuit}}
}
func (k keyMap) sleepingHelp() helpKeys {
	return helpKeys{short: []key.Binding{k.Refresh, k.Schedule, k.Wake}, pinned: []key.Binding{k.Quit}}
}

func (k keyMap) searchHelp() helpKeys {
	s := k.SearchInput
	return helpKeys{short: []key.Binding{s.Apply, s.Clear}, pinned: []key.Binding{s.Cancel, s.Quit}}
}

func (k keyMap) pickerHelp() helpKeys {
	p := k.Picker
	return helpKeys{short: []key.Binding{p.Up, p.Down, p.Apply, p.Mark, p.Edit, p.Delete, p.Clear, p.Retry}, pinned: []key.Binding{p.Cancel, p.Quit}}
}

func (k keyMap) accountPickerHelp() helpKeys {
	a := k.AccountPicker
	return helpKeys{short: []key.Binding{a.Up, a.Down, a.Apply, a.Retry}, pinned: []key.Binding{a.Cancel, a.Quit}}
}

func (k keyMap) watchlistNameHelp() helpKeys {
	n := k.WatchlistName
	return helpKeys{short: []key.Binding{n.Apply, n.Clear}, pinned: []key.Binding{n.Cancel, n.Quit}}
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
