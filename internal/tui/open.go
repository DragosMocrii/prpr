package tui

import (
	"fmt"
	"net/url"

	tea "charm.land/bubbletea/v2"

	"github.com/DragosMocrii/prpr/internal/preferences"
)

// browserOpenedMsg reports how opening a pull request in the browser or an
// editor went.
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

// editorName is how Settings and notices name an editor.
func editorName(editor string) string {
	switch editor {
	case preferences.EditorVSCodeInsiders:
		return "VS Code Insiders"
	case preferences.EditorGitHubDev:
		return "github.dev"
	}
	return "VS Code"
}

// editorLink is the link that opens a pull request in editor. VS Code's
// GitHub Pull Requests extension opens its pull request view for the
// open-pull-request-webview path, given the pull request's URL as uri.
func editorLink(editor, prURL string) string {
	switch editor {
	case preferences.EditorGitHubDev:
		// prURL passed safePullRequestURL, so it parses.
		link, _ := url.Parse(prURL)
		link.Scheme, link.Host = "https", "github.dev"
		return link.String()
	case preferences.EditorVSCodeInsiders:
		return "vscode-insiders://github.vscode-pull-request-github/open-pull-request-webview?uri=" + url.QueryEscape(prURL)
	}
	return "vscode://github.vscode-pull-request-github/open-pull-request-webview?uri=" + url.QueryEscape(prURL)
}

// openSelectedInEditor opens the focused pane's selected pull request in
// the chosen editor.
func (m *model) openSelectedInEditor() tea.Cmd {
	pr, ok := m.selectedPR()
	if !ok {
		return nil
	}
	if !safePullRequestURL(pr.URL) {
		m.setNotice(fmt.Sprintf("#%d has no GitHub link to open", pr.Number))
		return nil
	}
	m.markRead()
	m.setNotice(fmt.Sprintf("Opening #%d in %s…", pr.Number, editorName(m.editor)))
	ctx, open, link, id := m.ctx, m.openLink, editorLink(m.editor, pr.URL), m.noticeID
	return func() tea.Msg {
		return browserOpenedMsg{noticeID: id, err: open(ctx, link)}
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
