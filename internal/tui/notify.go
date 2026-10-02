package tui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

// notifyShownAlerts is how many pull requests one notification names; the
// rest are counted.
const notifyShownAlerts = 2

// notifyTitleWidth caps the title a single-alert notification quotes.
const notifyTitleWidth = 80

// prAlert is one pull request whose status turned into something worth a
// notification since the previous fetch.
type prAlert struct {
	pr    *github.PullRequest
	kinds []string
}

// mergeKnown reports whether GitHub has computed the merge state, so its
// readiness can be compared.
func mergeKnown(pr *github.PullRequest) bool {
	return !unknownMergeState(pr)
}

func checksFailing(state string) bool {
	return state == "FAILURE" || state == "ERROR"
}

// alerts compares a full fetch with the change baseline of each pane, before
// the baseline is replaced. Only pull requests listed in both fetches alert,
// except review requests, which alert when they arrive. readiness is each
// authored pull request's last known merge readiness: an unknown state keeps
// it, so a state that GitHub recomputes does not alert again. alerts updates
// it whether or not notifications are on.
func (m *model) alerts() []prAlert {
	var found []prAlert
	previous := make(map[prKey]*github.PullRequest)
	baseline := m.changes[paneMine].baseline
	for i := range baseline {
		previous[keyOf(&baseline[i])] = &baseline[i]
	}
	readiness := make(map[prKey]bool)
	for i := range m.snapshot.PullRequests {
		pr := &m.snapshot.PullRequests[i]
		key := keyOf(pr)
		wasReady, known := m.readiness[key]
		if mergeKnown(pr) {
			readiness[key] = mergeReady(pr.Draft, pr.Mergeable, pr.MergeState)
		} else if known {
			readiness[key] = wasReady
		}
		old, listed := previous[key]
		if !listed || !m.inScope(pr) {
			continue
		}
		var kinds []string
		if known && !wasReady && readiness[key] {
			kinds = append(kinds, "ready to merge")
		}
		if checksFailing(pr.Checks) && !checksFailing(old.Checks) {
			kinds = append(kinds, "failing CI")
		}
		if pr.ReviewDecision == "CHANGES_REQUESTED" && old.ReviewDecision != "CHANGES_REQUESTED" {
			kinds = append(kinds, "changes requested")
		}
		if len(kinds) > 0 {
			found = append(found, prAlert{pr, kinds})
		}
	}
	m.readiness = readiness

	requested := make(map[prKey]bool)
	for i := range m.changes[paneReview].baseline {
		requested[keyOf(&m.changes[paneReview].baseline[i])] = true
	}
	for i := range m.snapshot.ReviewRequests {
		pr := &m.snapshot.ReviewRequests[i]
		if !requested[keyOf(pr)] && m.inScope(pr) {
			found = append(found, prAlert{pr, []string{"review requested"}})
		}
	}
	return found
}

// resetReadiness records the authored pull requests' known merge readiness
// without alerting, for a new baseline.
func (m *model) resetReadiness() {
	m.readiness = make(map[prKey]bool)
	for i := range m.snapshot.PullRequests {
		if pr := &m.snapshot.PullRequests[i]; mergeKnown(pr) {
			m.readiness[keyOf(pr)] = mergeReady(pr.Draft, pr.Mergeable, pr.MergeState)
		}
	}
}

func alertName(pr *github.PullRequest) string {
	return singleLine(pr.Repository) + "#" + strconv.Itoa(pr.Number)
}

// alertText describes a refresh's alerts in one line: a single pull request
// with its title, or the first few and a count of the rest.
func alertText(alerts []prAlert) string {
	if len(alerts) == 1 {
		alert := alerts[0]
		return alertName(alert.pr) + " " + strings.Join(alert.kinds, ", ") + " — " +
			ansi.Truncate(strings.Join(strings.Fields(singleLine(alert.pr.Title)), " "), notifyTitleWidth, "…")
	}
	parts := make([]string, 0, notifyShownAlerts+1)
	for _, alert := range alerts[:min(len(alerts), notifyShownAlerts)] {
		parts = append(parts, alertName(alert.pr)+" "+strings.Join(alert.kinds, ", "))
	}
	if rest := len(alerts) - notifyShownAlerts; rest > 0 {
		parts = append(parts, "+"+strconv.Itoa(rest)+" more")
	}
	return strconv.Itoa(len(alerts)) + " updates — " + strings.Join(parts, "; ")
}

// desktopNotifiedMsg reports a failed desktop notification.
type desktopNotifiedMsg struct{ err error }

// notifyAlerts posts one notification for a refresh's alerts: through the
// desktop notifier when prpr runs where one reaches the desktop, else as an
// OSC 9 terminal sequence. Both ring the bell, which terminals without
// either, such as VS Code's, show on their tab, and the text is repeated in
// the status line.
func (m *model) notifyAlerts(alerts []prAlert) tea.Cmd {
	if !m.notify || len(alerts) == 0 {
		return nil
	}
	text := alertText(alerts)
	m.setNotice(text)
	if m.desktopNotify == nil {
		return tea.Raw(ansi.Notify("prpr: "+text) + "\a")
	}
	ctx, desktop := m.ctx, m.desktopNotify
	return tea.Batch(tea.Raw("\a"), func() tea.Msg {
		if err := desktop(ctx, "prpr", text); err != nil {
			return desktopNotifiedMsg{err}
		}
		return nil
	})
}

// handleDesktopNotified falls back to terminal notifications after the
// desktop notifier fails.
func (m *model) handleDesktopNotified(msg desktopNotifiedMsg) {
	m.desktopNotify = nil
	m.setNotice(singleLine(msg.err.Error()) + "; using terminal notifications")
}

func (m *model) toggleNotify() {
	m.notify = !m.notify
	if m.notify {
		m.setNotice("Notifications on: alerts when a PR turns ready, fails CI, gets changes requested, or requests your review")
	} else {
		m.setNotice("Notifications off")
	}
}
