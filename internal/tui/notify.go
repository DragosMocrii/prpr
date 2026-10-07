package tui

import (
	"context"
	"errors"
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

func checksFailing(state string) bool {
	return state == "FAILURE" || state == "ERROR"
}

// alerts compares a full fetch with the change baseline of each pane, before
// the baseline is replaced. Only authored pull requests listed in both
// fetches alert; a review row alerts when it arrives or stops waiting on
// others, but never when it starts waiting. A pull request a merge queue
// removed for failed tests alerts once; queued ones never alert, except that
// a submitted one still alerts on failing CI and changes requested, which
// keep it from entering the queue. readiness is each
// authored pull request's last known readiness by the rules: an unknown
// result keeps it, so a state that GitHub recomputes does not alert again. alerts updates
// it whether or not notifications are on.
func (m *model) alerts() []prAlert {
	var found []prAlert
	previous := make(map[prKey]*github.PullRequest)
	baseline := m.changes[listAuthored].baseline
	for i := range baseline {
		previous[keyOf(&baseline[i])] = &baseline[i]
	}
	readiness := make(map[prKey]bool)
	for i := range m.snapshot.PullRequests {
		pr := &m.snapshot.PullRequests[i]
		key := keyOf(pr)
		wasReady, known := m.readiness[key]
		if result := m.readyResult(pr); result.Known {
			readiness[key] = result.Ready
		} else if known {
			readiness[key] = wasReady
		}
		// A snoozed pull request alerts only when it wakes; one whose snooze
		// closed has no snooze left and alerts as it returns.
		if _, ok := m.snoozes[key]; ok {
			continue
		}
		old, listed := previous[key]
		if !listed || !m.inScope(pr) {
			continue
		}
		if pr.Queue != nil && pr.Queue.State == github.QueueRemovedFailed &&
			(old.Queue == nil || old.Queue.State != github.QueueRemovedFailed) {
			found = append(found, prAlert{pr, []string{"removed from the merge queue: tests failed"}})
			continue
		}
		// A queued pull request waits on the queue, not on its author, except
		// while submitted: the queue still waits on its checks and reviews.
		inQueue := queued(pr)
		if inQueue && pr.Queue.State != github.QueueSubmitted {
			continue
		}
		var kinds []string
		if !inQueue && known && !wasReady && readiness[key] {
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

	reviews := make(map[prKey]*github.PullRequest)
	reviewBaseline := m.changes[listReview].baseline
	for i := range reviewBaseline {
		reviews[keyOf(&reviewBaseline[i])] = &reviewBaseline[i]
	}
	for i := range m.snapshot.ReviewRequests {
		pr := &m.snapshot.ReviewRequests[i]
		if _, ok := m.snoozes[keyOf(pr)]; ok {
			continue
		}
		old, listed := reviews[keyOf(pr)]
		// A review row alerts when it starts needing the viewer: it arrives, or
		// it stops waiting on others. A request made again alerts, whatever
		// the row needed before.
		// A draft, shown or hidden, needs no one, and arrives when it leaves
		// draft.
		again := listed && requestedAgain(old, pr)
		if pr.ReviewStatus.Waiting() || pr.Draft || !m.inScope(pr) ||
			(listed && !old.ReviewStatus.Waiting() && !old.Draft && m.inScope(old) && !again) {
			continue
		}
		kind := "review requested"
		if again {
			kind = "review requested again"
		} else if text := reviewStatusText(pr.ReviewStatus); text != "" {
			kind = text
		}
		found = append(found, prAlert{pr, []string{kind}})
	}
	return found
}

// requestedAgain reports that the viewer's review was requested again: a
// reviewed row became a pending request, or a pending request's time, the
// latest direct request of the viewer, moved later. A reviewed row's time
// means something else, so it is not compared.
func requestedAgain(old, pr *github.PullRequest) bool {
	if pr.ReviewStatus != github.ReviewRequested {
		return false
	}
	if old.ReviewStatus != github.ReviewRequested {
		return true
	}
	return !old.WaitingSince.IsZero() && pr.WaitingSince.After(old.WaitingSince)
}

// resetReadiness records the authored pull requests' known merge readiness
// without alerting, for a new baseline.
func (m *model) resetReadiness() {
	m.readiness = make(map[prKey]bool)
	for i := range m.snapshot.PullRequests {
		if result := m.readyResult(&m.snapshot.PullRequests[i]); result.Known {
			m.readiness[keyOf(&m.snapshot.PullRequests[i])] = result.Ready
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

// desktopNotifiedMsg reports a failed desktop notification for its activity generation.
type desktopNotifiedMsg struct {
	err        error
	generation uint64
}

// notifyAlerts posts one grouped notification while this activity generation
// remains live.
func (m *model) notifyAlerts(alerts []prAlert) tea.Cmd {
	if !m.notify || len(alerts) == 0 || !m.notificationsAllowed() {
		return nil
	}
	text := alertText(alerts)
	m.setNotice(text)
	ctx, generation := m.activityContext(), m.activityGeneration
	allowed := liveUntil(ctx, m.activityDeadline, m.now)
	if m.desktopNotify == nil {
		raw := tea.Raw(ansi.Notify("prpr: "+text) + "\a")
		return func() tea.Msg {
			if !allowed() {
				return nil
			}
			return raw()
		}
	}
	desktop := m.desktopNotify
	bell := tea.Raw("\a")
	return tea.Batch(func() tea.Msg {
		if !allowed() {
			return nil
		}
		return bell()
	}, func() tea.Msg {
		if !allowed() {
			return nil
		}
		if err := desktop(ctx, "prpr", text); err != nil {
			return desktopNotifiedMsg{err: err, generation: generation}
		}
		return nil
	})
}

func (m *model) handleDesktopNotified(msg desktopNotifiedMsg) {
	if msg.generation != m.activityGeneration || errors.Is(msg.err, context.Canceled) || errors.Is(msg.err, context.DeadlineExceeded) {
		return
	}
	m.desktopNotify = nil
	m.setNotice(singleLine(msg.err.Error()) + "; using terminal notifications")
}

func (m *model) toggleNotify() {
	m.applyNotify(!m.notify)
	if m.notify {
		m.setNotice("Notifications on: alerts when a PR turns ready, fails CI, gets changes requested, requests your review, or needs you again after your review")
	} else {
		m.setNotice("Notifications off")
	}
}
