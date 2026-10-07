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
	// columns are the pane's table columns in drawn order.
	columns []columnSpec
	// Name tags: the review status (statusTag), a removed queue entry
	// (queueTag), and why a snooze woke (wokeTag).
	statusTag, queueTag, wokeTag bool
	// rowStyle restyles every cell after the mark; "" leaves them.
	rowStyle func(r *rowContext) (on, off string)
}

func fixedText(text string) func(*model) string {
	return func(*model) string { return text }
}

// goneStyle strikes through gone rows.
func goneStyle(r *rowContext) (string, string) {
	if r.gone {
		return goneOn, goneOff
	}
	return "", ""
}

var (
	mergeColumn = columnSpec{title: iconTitle("Merge"), width: iconWidth(5, 1), changed: cellMerge,
		text: func(r *rowContext) string {
			// A preview has no merge state, but conflicts are already known.
			if r.preview && r.pr.Mergeable != "CONFLICTING" {
				return pendingText
			}
			return r.m.mergeCell(r.pr)
		}}
	authorColumn = columnSpec{title: plainTitle("Author"), changed: cellAuthor,
		width: func(c *columnContext) int { return min(16, max(8, c.author)) },
		text:  func(r *rowContext) string { return singleLine(r.pr.Author) }}
	queueColumn = columnSpec{title: iconTitle("Queue"), width: iconWidth(9, 1), changed: cellQueue,
		text: func(r *rowContext) string { return queueText(r.ic, r.pr.Queue.State) }}
	detailColumn = columnSpec{title: plainTitle("Detail"), rank: 1, changed: cellQueue,
		width: func(c *columnContext) int { return min(24, max(10, c.width/6)) },
		text:  func(r *rowContext) string { return singleLine(r.pr.Queue.Detail) }}
	wakesColumn = columnSpec{title: iconTitle("Wakes"), width: fixedWidth(9),
		text: func(r *rowContext) string {
			// A snooze deleted when its pull request closed has no wake time.
			if s, ok := r.m.snoozes[keyOf(r.pr)]; ok {
				return wakeText(s, r.now)
			}
			return "—"
		}}
	fromColumn = columnSpec{title: plainTitle("From"), width: fixedWidth(12), rank: 1,
		text: func(r *rowContext) string {
			switch {
			case queued(r.pr):
				return "queue"
			case r.m.snoozeList(r.pr) == listReview:
				if from := reviewStatusText(r.pr.ReviewStatus); from != "" {
					return from
				}
				return "review"
			}
			return "mine"
		}}
	mergedAtColumn = columnSpec{title: iconTitle("Merged"), width: fixedWidth(6),
		text: func(r *rowContext) string { return ageText(r.pr.MergedAt, r.now) }}
	mergedByColumn = columnSpec{title: plainTitle("Merged by"), rank: 1,
		width: func(c *columnContext) int { return min(16, max(len("Merged by"), c.mergedBy)) },
		text:  func(r *rowContext) string { return singleLine(r.pr.MergedBy) }}
)

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
		queueTag: true, wokeTag: true,
		columns: []columnSpec{markColumn, mergeColumn, repositoryColumn, numberColumn,
			nameColumn(cellName | cellState | cellQueue),
			ageColumn, botsColumn, ciColumn, reviewColumn, commentsColumn, sizeColumn},
		rowStyle: goneStyle,
	},
	paneQueue: {
		name: "Merge queue", empty: fixedText("Nothing in a merge queue"),
		lists: []listID{listAuthored}, tracker: listAuthored,
		onlyWithRows: true, short: true, quickFilters: true,
		wokeTag:  true,
		columns:  []columnSpec{markColumn, queueColumn, repositoryColumn, numberColumn, nameColumn(cellName), detailColumn},
		rowStyle: goneStyle,
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
		statusTag: true, wokeTag: true,
		// Review requested gives up Review first: a pending request mostly
		// shows it as required.
		columns: []columnSpec{markColumn, repositoryColumn, numberColumn,
			nameColumn(cellName | cellState), authorColumn,
			ranked(ageColumn, 1), ranked(botsColumn, 2), ranked(ciColumn, 3),
			ranked(reviewColumn, 6), ranked(commentsColumn, 4), ranked(sizeColumn, 5)},
		rowStyle: func(r *rowContext) (string, string) {
			if r.gone {
				return goneOn, goneOff
			}
			if r.pr.ReviewStatus.Waiting() {
				return waitingOn, waitingOff
			}
			return "", ""
		},
	},
	paneSnoozed: {
		name: "Snoozed", empty: fixedText("Nothing snoozed"),
		lists: []listID{listAuthored, listReview}, perRowTracker: true,
		onlyWithRows: true, short: true, quickFilters: true,
		columns: []columnSpec{markColumn, wakesColumn, repositoryColumn, numberColumn, nameColumn(cellName), fromColumn},
		rowStyle: func(r *rowContext) (string, string) {
			if r.gone {
				return goneOn, goneOff
			}
			return dimOn, dimOff
		},
	},
	paneMerged: {
		name: "Merged", empty: fixedText("Nothing merged recently"),
		lists: []listID{listMerged}, tracker: listMerged,
		onlyWithRows: true, leftover: true,
		collapsible: true, saveCollapsed: (*preferences.Store).SaveMergedCollapsed,
		// Merged rows are never gone: falling out of the last N is not news.
		columns: []columnSpec{markColumn, mergedAtColumn, repositoryColumn, numberColumn, nameColumn(cellName), mergedByColumn},
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
