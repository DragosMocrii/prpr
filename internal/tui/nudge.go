package tui

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/preferences"
)

// nudgeWidth is the width of the mark column while it draws a nudge icon.
const nudgeWidth = 2

// nudgeBlinkInterval paces the blinking of normal and urgent nudges.
const nudgeBlinkInterval = 500 * time.Millisecond

type nudgeTickMsg struct{ generation uint64 }

// nudgeIcon is the mark of a nudge of urgency u.
func nudgeIcon(ic *iconSet, u github.NudgeUrgency) string {
	switch u {
	case github.NudgeLow:
		return ic.nudgeLow
	case github.NudgeNormal:
		return ic.nudgeNormal
	case github.NudgeUrgent:
		return ic.nudgeUrgent
	}
	return ""
}

// nudge is a review row's nudge in effect: none in a preview, which does
// not know, nor once the viewer dismissed that very nudge.
func (m *model) nudge(pr *github.PullRequest) *github.Nudge {
	if pr.Nudge == nil || m.snapshot.Preview {
		return nil
	}
	if at, ok := m.dismissed[keyOf(pr)]; ok && at.Equal(pr.Nudge.At) {
		return nil
	}
	return pr.Nudge
}

// waiting reports whether a review row waits on someone other than the
// viewer: a nudge in effect makes it need the viewer, and a row listed only
// for its nudge waits once that nudge is dismissed.
func (m *model) waiting(pr *github.PullRequest) bool {
	return (pr.ReviewStatus.Waiting() || pr.ReviewStatus == github.ReviewNudged) && m.nudge(pr) == nil
}

// reviewRank orders Review requested: urgent nudges, pending requests,
// rows that need the viewer, then waiting rows.
func (m *model) reviewRank(pr *github.PullRequest) int {
	n := m.nudge(pr)
	switch {
	case n != nil && n.Urgency == github.NudgeUrgent:
		return 0
	case pr.ReviewStatus == github.ReviewRequested:
		return 1
	case !m.waiting(pr):
		return 2
	}
	return 3
}

// sortReview orders the review pane's rows by reviewRank, nudged rows
// first within a rank, otherwise in fetch order.
func (m *model) sortReview(visible []int, source []github.PullRequest) {
	slices.SortStableFunc(visible, func(a, b int) int {
		x, y := &source[a], &source[b]
		if r := cmp.Compare(m.reviewRank(x), m.reviewRank(y)); r != 0 {
			return r
		}
		nx, ny := m.nudge(x) != nil, m.nudge(y) != nil
		switch {
		case nx && !ny:
			return -1
		case ny && !nx:
			return 1
		}
		return 0
	})
}

// nudgeShown reports whether a visible row of Review requested shows a
// nudge; the mark column widens for it only then.
func (m *model) nudgeShown() bool {
	for _, index := range m.panes[paneReview].visible {
		if m.nudge(&m.snapshot.ReviewRequests[index]) != nil {
			return true
		}
	}
	return false
}

// blinking reports whether a shown nudge blinks: low ones stay put.
func (m *model) blinking() bool {
	for _, index := range m.panes[paneReview].visible {
		if n := m.nudge(&m.snapshot.ReviewRequests[index]); n != nil && n.Urgency >= github.NudgeNormal {
			return true
		}
	}
	return false
}

// nudgeMark draws a review row's mark cell: a low nudge's icon stays; a
// normal or urgent one alternates with the row's change mark.
func (m *model) nudgeMark(pr *github.PullRequest, gone bool, markCell string) string {
	n := m.nudge(pr)
	if gone || n == nil || (m.nudgeOff && n.Urgency >= github.NudgeNormal) {
		return markCell
	}
	return nudgeIcon(m.icons, n.Urgency)
}

// scheduleNudge starts the blink tick while a blinking nudge is shown. One
// chain runs at a time; each tick schedules the next.
func (m *model) scheduleNudge() tea.Cmd {
	if m.nudgeTicking || !m.blinking() {
		return nil
	}
	m.nudgeTicking = true
	m.nudgeGeneration++
	generation := m.nudgeGeneration
	return tea.Tick(nudgeBlinkInterval, func(time.Time) tea.Msg { return nudgeTickMsg{generation} })
}

// handleNudgeTick blinks the nudges, or ends the chain when none blinks.
func (m *model) handleNudgeTick(msg nudgeTickMsg) tea.Cmd {
	if msg.generation != m.nudgeGeneration {
		return nil
	}
	m.nudgeTicking = false
	if !m.blinking() {
		m.nudgeOff = false
		return nil
	}
	m.nudgeOff = !m.nudgeOff
	m.redrawRows(paneReview)
	return m.scheduleNudge()
}

// dismissNudge undoes the focused review row's nudge until the author
// nudges again, and saves that. The panes are rebuilt, since the review
// pane's order follows nudges.
func (m *model) dismissNudge() {
	if m.focus != paneReview {
		return
	}
	pr, gone, ok := m.paneRow(paneReview, m.focused().table.Cursor())
	if !ok || gone {
		return
	}
	n := m.nudge(pr)
	if n == nil {
		return
	}
	m.dismissed[keyOf(pr)] = n.At
	m.saveDismissals()
	m.keepSelection(m.rebuildVisiblePRs, false)
}

// selectedNudged reports whether the focused row shows a nudge.
func (m *model) selectedNudged() bool {
	if m.focus != paneReview {
		return false
	}
	pr, gone, ok := m.paneRow(paneReview, m.focused().table.Cursor())
	return ok && !gone && m.nudge(pr) != nil
}

// loadDismissals restores an account's dismissed nudges without writing.
func (m *model) loadDismissals(login string) {
	m.dismissed = make(map[prKey]time.Time)
	for _, d := range m.preferences.Dismissals(login) {
		m.dismissed[keyOf(&github.PullRequest{Repository: d.Repository, Number: d.Number})] = d.Nudged
	}
}

// pruneDismissals forgets the dismissals whose nudge a full fetch no longer
// finds on its review row, saving only when one is dropped.
func (m *model) pruneDismissals() {
	nudged := make(map[prKey]time.Time)
	for i := range m.snapshot.ReviewRequests {
		if pr := &m.snapshot.ReviewRequests[i]; pr.Nudge != nil {
			nudged[keyOf(pr)] = pr.Nudge.At
		}
	}
	changed := false
	for key, at := range m.dismissed {
		if current, ok := nudged[key]; !ok || !current.Equal(at) {
			delete(m.dismissed, key)
			changed = true
		}
	}
	if changed {
		m.saveDismissals()
	}
}

func (m *model) saveDismissals() {
	dismissals := make([]preferences.Dismissal, 0, len(m.dismissed))
	for key, at := range m.dismissed {
		dismissals = append(dismissals, preferences.Dismissal{Repository: key.repository, Number: key.number, Nudged: at})
	}
	slices.SortFunc(dismissals, func(a, b preferences.Dismissal) int {
		return cmp.Or(cmp.Compare(a.Repository, b.Repository), cmp.Compare(a.Number, b.Number))
	})
	if err := m.preferences.SaveDismissals(m.snapshot.Login, dismissals); err != nil {
		m.preferenceErr = fmt.Errorf("Dismissal not saved: %w", err)
	}
}
