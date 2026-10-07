package tui

import "github.com/DragosMocrii/prpr/internal/preferences"

// paneSpec is what sets a pane apart: its name, the lists it draws from,
// and how the frame lays it out. Behavior that depends on the pane in
// focus (keys, details, snoozing) still asks which pane it is.
type paneSpec struct {
	name string
	// empty is the empty pane's text before the scope is named.
	empty func(m *model) string
	// lists are the lists the pane draws from, in index order: the Snoozed
	// pane's indexes run over the authored list, then the review list.
	lists []listID
	// tracker is the list whose change tracker marks the pane's rows;
	// perRowTracker panes (Snoozed) use each row's snoozeList instead.
	tracker       listID
	perRowTracker bool
	// onlyWithRows panes are drawn only while they have rows.
	onlyWithRows bool
	// short panes, beside other filled panes, are no taller than their rows.
	short bool
	// leftover panes take only the lines the other panes leave.
	leftover bool
	// collapsible panes can be drawn as their title alone; saveCollapsed
	// saves that choice.
	collapsible   bool
	saveCollapsed func(store *preferences.Store, collapsed bool) error
	// attention panes are counted and filtered by attention categories.
	attention bool
	// quickFilters panes can match quick filters; while a quick or category
	// filter is active, a pane without them shows no rows.
	quickFilters bool
}

func fixedText(text string) func(*model) string {
	return func(*model) string { return text }
}

var paneSpecs = [len(paneIDs)]paneSpec{
	paneMine: {
		name: "My PRs",
		empty: func(m *model) string {
			switch {
			case m.snoozedShows(listAuthored):
				return "No other open pull requests"
			case len(m.panes[paneQueue].visible) > 0:
				return "No open pull requests outside the merge queue"
			}
			return "No open pull requests"
		},
		lists: []listID{listAuthored}, tracker: listAuthored,
		attention: true, quickFilters: true,
	},
	paneQueue: {
		name: "Merge queue", empty: fixedText("Nothing in a merge queue"),
		lists: []listID{listAuthored}, tracker: listAuthored,
		onlyWithRows: true, short: true, quickFilters: true,
	},
	paneReview: {
		name: "Review requested",
		empty: func(m *model) string {
			if m.snoozedShows(listReview) {
				return "No other review requests"
			}
			return "No review requests"
		},
		lists: []listID{listReview}, tracker: listReview,
		attention: true, quickFilters: true,
	},
	paneSnoozed: {
		name: "Snoozed", empty: fixedText("Nothing snoozed"),
		lists: []listID{listAuthored, listReview}, perRowTracker: true,
		onlyWithRows: true, short: true, quickFilters: true,
	},
	paneMerged: {
		name: "Merged", empty: fixedText("Nothing merged recently"),
		lists: []listID{listMerged}, tracker: listMerged,
		onlyWithRows: true, leftover: true,
		collapsible: true, saveCollapsed: (*preferences.Store).SaveMergedCollapsed,
	},
}

// toggleCollapsed draws a collapsible pane as its title alone, or with its
// table again, and saves it. Focus leaves a pane it collapses.
func (m *model) toggleCollapsed(id paneID) {
	spec := &paneSpecs[id]
	if !spec.collapsible {
		return
	}
	m.collapsed[id] = !m.collapsed[id]
	m.settingSaved(spec.saveCollapsed(m.preferences, m.collapsed[id]))
	m.rebuildPRTable(false)
}
