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

// pleadIcon marks a review request that asks the viewer again. It is the
// same in both icon sets and, unlike the other icons, two cells wide.
const pleadIcon = "🙏"

// pleadWidth is the width of the mark column while it draws pleadIcon.
const pleadWidth = 2

// pleadBlinkInterval paces the marker's blinking.
const pleadBlinkInterval = 500 * time.Millisecond

type pleadTickMsg struct{ generation uint64 }

// pleading reports whether a review row shows the marker: a pending request
// that asks the viewer again, not dismissed since it was last requested.
// Preview rows never do, since a preview does not know.
func (m *model) pleading(pr *github.PullRequest) bool {
	if !pr.RequestedAgain || pr.ReviewStatus != github.ReviewRequested || m.snapshot.Preview {
		return false
	}
	at, dismissed := m.dismissed[keyOf(pr)]
	return !dismissed || pr.WaitingSince.After(at)
}

// pleadShown reports whether a visible row of Review requested shows the
// marker; the mark column widens for it only then.
func (m *model) pleadShown() bool {
	for _, index := range m.panes[paneReview].visible {
		if m.pleading(&m.snapshot.ReviewRequests[index]) {
			return true
		}
	}
	return false
}

// pleadMark draws a review row's mark cell: the marker alternates with the
// row's change mark as it blinks.
func (m *model) pleadMark(pr *github.PullRequest, gone bool, markCell string) string {
	if gone || m.pleadOff || !m.pleading(pr) {
		return markCell
	}
	return m.icons.plead
}

// schedulePlead starts the blink tick while a marker is shown. One chain
// runs at a time; each tick schedules the next.
func (m *model) schedulePlead() tea.Cmd {
	if m.pleadTicking || !m.pleadShown() {
		return nil
	}
	m.pleadTicking = true
	m.pleadGeneration++
	generation := m.pleadGeneration
	return tea.Tick(pleadBlinkInterval, func(time.Time) tea.Msg { return pleadTickMsg{generation} })
}

// handlePleadTick blinks the markers, or ends the chain when none is shown.
func (m *model) handlePleadTick(msg pleadTickMsg) tea.Cmd {
	if msg.generation != m.pleadGeneration {
		return nil
	}
	m.pleadTicking = false
	if !m.pleadShown() {
		m.pleadOff = false
		return nil
	}
	m.pleadOff = !m.pleadOff
	m.redrawRows(paneReview)
	return m.schedulePlead()
}

// dismissPlead hides the focused review row's marker until the viewer's
// review is requested again, and saves that.
func (m *model) dismissPlead() {
	if m.focus != paneReview {
		return
	}
	pr, gone, ok := m.paneRow(paneReview, m.focused().table.Cursor())
	if !ok || gone || !m.pleading(pr) {
		return
	}
	m.dismissed[keyOf(pr)] = pr.WaitingSince
	m.saveDismissals()
	m.redrawRows(paneReview)
}

// selectedPleading reports whether the focused row shows the marker.
func (m *model) selectedPleading() bool {
	if m.focus != paneReview {
		return false
	}
	pr, gone, ok := m.paneRow(paneReview, m.focused().table.Cursor())
	return ok && !gone && m.pleading(pr)
}

// loadDismissals restores an account's dismissed markers without writing.
func (m *model) loadDismissals(login string) {
	m.dismissed = make(map[prKey]time.Time)
	for _, d := range m.preferences.Dismissals(login) {
		m.dismissed[keyOf(&github.PullRequest{Repository: d.Repository, Number: d.Number})] = d.Requested
	}
}

// pruneDismissals forgets the dismissals of pull requests a full fetch no
// longer lists as pending requests, saving only when one is dropped.
func (m *model) pruneDismissals() {
	pending := make(map[prKey]bool)
	for i := range m.snapshot.ReviewRequests {
		if pr := &m.snapshot.ReviewRequests[i]; pr.ReviewStatus == github.ReviewRequested {
			pending[keyOf(pr)] = true
		}
	}
	changed := false
	for key := range m.dismissed {
		if !pending[key] {
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
		dismissals = append(dismissals, preferences.Dismissal{Repository: key.repository, Number: key.number, Requested: at})
	}
	slices.SortFunc(dismissals, func(a, b preferences.Dismissal) int {
		return cmp.Or(cmp.Compare(a.Repository, b.Repository), cmp.Compare(a.Number, b.Number))
	})
	if err := m.preferences.SaveDismissals(m.snapshot.Login, dismissals); err != nil {
		m.preferenceErr = fmt.Errorf("Dismissal not saved: %w", err)
	}
}
