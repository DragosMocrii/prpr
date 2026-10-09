package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/DragosMocrii/prpr/internal/github"
)

// watchMinInterval is the shortest delay between notifications reads,
// GitHub's X-Poll-Interval unless it asks for longer.
const watchMinInterval = time.Minute

// watchMaxBackoff caps the delay after failed reads while auto-refresh is
// off; otherwise the refresh interval caps it.
const watchMaxBackoff = 15 * time.Minute

// watchState is the Live updates chain, which reads GitHub notifications
// as a signal to fetch early.
type watchState struct {
	cancel     context.CancelFunc
	generation uint64
	// paused: off or blocked (sleep, login, authentication); a successful
	// fetch resumes it.
	paused bool
	// live: the last read was answered (200 or 304); the countdown says so.
	live bool
	// unsupported: the token cannot read notifications; no reads until the
	// account changes or Live updates is turned off and on.
	unsupported bool
	// since is GitHub's Date of the last baseline or 200; zero before the
	// baseline read.
	since        time.Time
	lastModified string
	backoff      time.Duration
	// pending: a relevant change came while a fetch that began before the
	// read was running; one more fetch follows that one.
	pending bool
}

type notificationsMsg struct {
	result     github.Notifications
	account    uint64
	generation uint64
	// sentRefresh is refreshGeneration when the read was sent.
	sentRefresh uint64
}

type watchTickMsg struct {
	account    uint64
	generation uint64
}

// pollWatch starts the notifications chain unless one runs, Live updates
// is off or unsupported, or polling is blocked.
func (m *model) pollWatch() tea.Cmd {
	if !m.live || m.watch.unsupported || m.quotaBlocked() {
		m.watch.paused = true
		return nil
	}
	if m.watch.cancel != nil {
		return nil
	}
	m.watch.generation++
	m.watch.paused = false
	return m.readWatch(m.watch.generation, m.accountGeneration)
}

// readWatch cancels the chain's previous read and starts the next one.
func (m *model) readWatch(generation, account uint64) tea.Cmd {
	if m.watch.cancel != nil {
		m.watch.cancel()
	}
	ctx, cancel := context.WithCancel(m.activityContext())
	m.watch.cancel = cancel
	client, live := m.client, liveUntil(ctx, m.activityDeadline, m.now)
	since, lastModified, sent := m.watch.since, m.watch.lastModified, m.refreshGeneration
	return func() tea.Msg {
		msg := notificationsMsg{account: account, generation: generation, sentRefresh: sent}
		if !live() {
			msg.result = github.Notifications{Status: github.NotificationsTransient, Err: context.Canceled}
			return msg
		}
		msg.result = client.Notifications(ctx, since, lastModified)
		return msg
	}
}

// invalidateWatch stops the chain; its results and ticks are dropped.
func (m *model) invalidateWatch() {
	if m.watch.cancel != nil {
		m.watch.cancel()
		m.watch.cancel = nil
	}
	m.watch.generation++
	m.watch.paused, m.watch.live, m.watch.pending = true, false, false
}

// resetWatch stops the chain and forgets what it read, so the next chain
// starts with a baseline read.
func (m *model) resetWatch() {
	m.invalidateWatch()
	m.watch.unsupported = false
	m.watch.since, m.watch.lastModified, m.watch.backoff = time.Time{}, "", 0
}

// handleWatch advances the chain and drops every obsolete result.
func (m *model) handleWatch(msg tea.Msg) tea.Cmd {
	if !m.live || m.quotaBlocked() {
		m.invalidateWatch()
		return nil
	}
	switch msg := msg.(type) {
	case watchTickMsg:
		if msg.generation != m.watch.generation || msg.account != m.accountGeneration {
			return nil
		}
		return m.readWatch(msg.generation, msg.account)
	case notificationsMsg:
		if msg.generation != m.watch.generation || msg.account != m.accountGeneration {
			return nil
		}
		return m.handleNotifications(msg)
	}
	return nil
}

func (m *model) handleNotifications(msg notificationsMsg) tea.Cmd {
	n := msg.result
	delay := max(n.PollInterval, watchMinInterval)
	var fetch tea.Cmd
	switch n.Status {
	case github.NotificationsChanged, github.NotificationsNotModified:
		m.watch.live, m.watch.backoff = true, 0
		if n.LastModified != "" {
			m.watch.lastModified = n.LastModified
		}
		baseline := m.watch.since.IsZero()
		if n.Status == github.NotificationsChanged && !baseline && m.watchRelevant(n) {
			fetch = m.fetchForWatch(msg.sentRefresh)
		}
		if !n.Date.IsZero() && (baseline || n.Status == github.NotificationsChanged) {
			m.watch.since = n.Date
		}
	case github.NotificationsRateLimited:
		m.watch.live = false
		delay = max(n.RetryAfter, watchMinInterval)
	case github.NotificationsUnsupported:
		m.invalidateWatch()
		m.watch.unsupported = true
		return nil
	default:
		if errors.Is(n.Err, context.Canceled) || errors.Is(n.Err, context.DeadlineExceeded) {
			break
		}
		m.watch.live = false
		m.watch.backoff = min(max(m.watch.backoff*2, delay), m.watchBackoffCap())
		delay = m.watch.backoff
	}
	generation, account := m.watch.generation, m.accountGeneration
	return tea.Batch(fetch, tea.Tick(delay, func(time.Time) tea.Msg {
		return watchTickMsg{generation: generation, account: account}
	}))
}

// watchBackoffCap is the longest delay after failed reads: the refresh
// interval, or watchMaxBackoff while auto-refresh is off.
func (m *model) watchBackoffCap() time.Duration {
	if m.refreshInterval > 0 {
		return max(m.refreshInterval, watchMinInterval)
	}
	return watchMaxBackoff
}

// fetchForWatch fetches for a relevant change read by a request sent at
// refresh generation sent. A fetch running since before that request may
// have missed the change, so one more follows it; one begun after sees it.
func (m *model) fetchForWatch(sent uint64) tea.Cmd {
	if m.loading && !m.fetchQuiet {
		if m.refreshGeneration == sent {
			m.watch.pending = true
		}
		return nil
	}
	return m.startAutomaticFetch()
}

// watchRelevant reports whether a read holds a thread updated after since
// that is worth a fetch: a listed pull request, a new review request or
// mention, or CI in a repository of a listed authored pull request, all in
// scope. A full page counts, since threads past it were not read.
func (m *model) watchRelevant(n github.Notifications) bool {
	if n.Full {
		return true
	}
	for _, thread := range n.Threads {
		if !thread.UpdatedAt.After(m.watch.since) {
			continue
		}
		if thread.SubjectType == "PullRequest" && thread.Number > 0 {
			if m.listsPullRequest(thread.Repository, thread.Number) {
				return true
			}
			if (thread.Reason == "review_requested" || thread.Reason == "mention") &&
				m.inRepositoryScope(&github.PullRequest{Repository: thread.Repository}) {
				return true
			}
		}
		if thread.Reason == "ci_activity" && m.authorsIn(thread.Repository) {
			return true
		}
	}
	return false
}

// listsPullRequest reports whether the authored or review list holds the
// pull request within the repository scope; snoozed rows are in them too.
func (m *model) listsPullRequest(repository string, number int) bool {
	for _, id := range []listID{listAuthored, listReview} {
		list := m.source(id)
		for i := range list {
			if pr := &list[i]; pr.Number == number && strings.EqualFold(pr.Repository, repository) && m.inRepositoryScope(pr) {
				return true
			}
		}
	}
	return false
}

// authorsIn reports whether an in-scope authored pull request is listed in
// repository.
func (m *model) authorsIn(repository string) bool {
	list := m.source(listAuthored)
	for i := range list {
		if pr := &list[i]; strings.EqualFold(pr.Repository, repository) && m.inScope(pr) {
			return true
		}
	}
	return false
}
