package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// browserOpenedMsg reports how opening a pull request in the browser went.
// noticeID is the notice that announced it.
type browserOpenedMsg struct {
	noticeID uint64
	err      error
}

func (m *model) setNotice(notice string) {
	m.notice = notice
	m.noticeID++
}

// openSelected opens the focused pane's selected pull request in the browser.
func (m *model) openSelected() tea.Cmd {
	pr, ok := m.selectedPR()
	if !ok {
		return nil
	}
	if !safePullRequestURL(pr.URL) {
		m.setNotice(fmt.Sprintf("#%d has no GitHub link to open", pr.Number))
		return nil
	}
	m.markRead()
	m.setNotice(fmt.Sprintf("Opening #%d in the browser…", pr.Number))
	ctx, open, url, id := m.ctx, m.openBrowser, pr.URL, m.noticeID
	return func() tea.Msg {
		return browserOpenedMsg{noticeID: id, err: open(ctx, url)}
	}
}

// handleBrowserOpened reports a failure, and otherwise clears the notice it
// announced unless a newer one replaced it.
func (m *model) handleBrowserOpened(msg browserOpenedMsg) {
	if msg.err != nil {
		m.setNotice(singleLine(msg.err.Error()))
	} else if msg.noticeID == m.noticeID {
		m.notice = ""
	}
}

// copySelected copies the focused pane's selected pull request URL with
// OSC 52. The terminal does not confirm it, so terminals without OSC 52
// support ignore it silently.
func (m *model) copySelected() tea.Cmd {
	pr, ok := m.selectedPR()
	if !ok {
		return nil
	}
	if !safePullRequestURL(pr.URL) {
		m.setNotice(fmt.Sprintf("#%d has no GitHub link to copy", pr.Number))
		return nil
	}
	m.markRead()
	m.setNotice(fmt.Sprintf("Copied the URL of #%d", pr.Number))
	return tea.SetClipboard(pr.URL)
}
