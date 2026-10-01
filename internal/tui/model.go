package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/preferences"
)

const (
	minimumWidth  = 40
	minimumHeight = 8
)

type model struct {
	ctx                 context.Context
	client              *github.Client
	preferences         *preferences.Store
	cancel              context.CancelFunc
	snapshot            github.Snapshot
	selectedRepository  string
	filterLogin         string
	panes               [2]prPane
	focus               paneID
	keys                keyMap
	help                help.Model
	spinner             spinner.Model
	darkBackground      bool
	picker              *repositoryPicker
	repositoryRequestID uint64
	width               int
	height              int
	loading             bool
	loginActive         bool
	scopeChosen         bool
	scopeChoiceCursor   int
	preferenceErr       error
	err                 error
}

type fetchFinishedMsg struct {
	snapshot github.Snapshot
	err      error
}

type loginFinishedMsg struct{ err error }

func New(ctx context.Context, client *github.Client, preferences *preferences.Store) tea.Model {
	appCtx, cancel := context.WithCancel(ctx)
	m := newModel(appCtx, client, preferences)
	m.cancel = cancel
	return m
}

func newModel(ctx context.Context, client *github.Client, preferences *preferences.Store) *model {
	m := &model{
		ctx:            ctx,
		client:         client,
		preferences:    preferences,
		cancel:         func() {},
		keys:           defaultKeyMap(),
		help:           help.New(),
		spinner:        spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		darkBackground: true,
	}
	m.panes = [2]prPane{newPRPane(), newPRPane()}
	m.rebuildPRTable(true)
	return m
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.startFetch())
}

func (m *model) startFetch() tea.Cmd {
	m.closeRepositoryPicker()
	m.loading = true
	m.loginActive = false
	m.err = nil
	client, ctx := m.client, m.ctx
	return tea.Batch(func() tea.Msg {
		snapshot, err := client.Fetch(ctx)
		return fetchFinishedMsg{snapshot: snapshot, err: err}
	}, m.spinner.Tick)
}

// refreshing reports whether a fetch is replacing rows that are still shown.
func (m *model) refreshing() bool {
	return m.loading && !m.loginActive && m.snapshot.Login != ""
}

// spinning reports whether a visible request is in flight. Spinner ticks
// that arrive otherwise are dropped, which ends the tick loop.
func (m *model) spinning() bool {
	return (m.loading && !m.loginActive) || (m.picker != nil && m.picker.busy)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.SetWidth(msg.Width)
		m.rebuildPRTable(false)
		if m.picker != nil {
			m.picker.setWidth(m.width)
			m.picker.clamp(m.pickerViewportHeight())
		}
	case tea.BackgroundColorMsg:
		m.darkBackground = msg.IsDark()
		m.help.Styles = help.DefaultStyles(m.darkBackground)
		if m.picker != nil {
			m.picker.setDark(m.darkBackground)
		}
	case spinner.TickMsg:
		if m.spinning() {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	case repositoryListFinishedMsg:
		m.handleRepositoryListFinished(msg)
	case repositoryLookupFinishedMsg:
		return m, m.handleRepositoryLookupFinished(msg)
	case fetchFinishedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			m.snapshot = github.Snapshot{}
			for _, id := range paneIDs {
				m.panes[id].visible = nil
			}
			m.rebuildPRTable(true)
		} else {
			accountChanged := !strings.EqualFold(m.filterLogin, msg.snapshot.Login)
			if accountChanged {
				if m.filterLogin != "" {
					m.preferenceErr = nil
				}
				m.filterLogin = msg.snapshot.Login
				if repository, found := m.preferences.Lookup(msg.snapshot.Login); found {
					m.selectedRepository = repository
					m.scopeChosen = true
				} else {
					m.selectedRepository = ""
					m.scopeChosen = false
					m.scopeChoiceCursor = 0
				}
			}
			type selection struct {
				repository string
				number     int
				ok         bool
			}
			var previous [2]selection
			for _, id := range paneIDs {
				if pr, ok := m.paneSelectedPR(id); ok {
					previous[id] = selection{pr.Repository, pr.Number, true}
				}
			}
			m.snapshot = msg.snapshot
			m.err = nil
			m.rebuildVisiblePRs()
			for _, id := range paneIDs {
				if previous[id].ok {
					m.selectPR(id, previous[id].repository, previous[id].number)
				}
			}
			if accountChanged {
				focus := paneMine
				if len(m.panes[paneMine].visible) == 0 && len(m.panes[paneReview].visible) > 0 {
					focus = paneReview
				}
				m.setFocus(focus)
			}
		}
	case loginFinishedMsg:
		m.loginActive = false
		m.loading = false
		if msg.err != nil {
			m.err = fmt.Errorf("GitHub login did not complete: %w", msg.err)
			return m, nil
		}
		return m, m.startFetch()
	case tea.PasteMsg:
		if m.picker != nil {
			return m, m.picker.paste(msg.Content, m.pickerViewportHeight())
		}
	case tea.KeyPressMsg:
		return m, m.handleKey(msg)
	}
	return m, nil
}

func (m *model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	m.syncKeys()
	k := m.keys
	if key.Matches(msg, k.ForceQuit) {
		m.closeRepositoryPicker()
		m.cancel()
		return tea.Quit
	}
	if m.picker != nil {
		return m.updateRepositoryPicker(msg)
	}
	switch {
	case key.Matches(msg, k.Quit):
		m.cancel()
		return tea.Quit
	case key.Matches(msg, k.Refresh, k.Retry):
		return m.startFetch()
	case key.Matches(msg, k.Login):
		m.loading = true
		m.loginActive = true
		return tea.ExecProcess(m.client.LoginCommand(m.ctx), func(err error) tea.Msg {
			return loginFinishedMsg{err: err}
		})
	case key.Matches(msg, k.PickRepository):
		return m.openRepositoryPicker()
	case key.Matches(msg, k.AllRepositories):
		m.chooseRepository("")
	case key.Matches(msg, k.NextPane, k.PrevPane):
		// Two panes: next and previous are the same move.
		m.setFocus(1 - m.focus)
	case key.Matches(msg, k.Help):
		m.help.ShowAll = !m.help.ShowAll
		m.rebuildPRTable(false)
	case key.Matches(msg, k.ChoiceUp):
		m.scopeChoiceCursor = 0
	case key.Matches(msg, k.ChoiceDown):
		m.scopeChoiceCursor = 1
	case key.Matches(msg, k.Continue):
		if m.scopeChoiceCursor == 1 {
			m.chooseRepository("")
			return nil
		}
		return m.openRepositoryPicker()
	case key.Matches(msg, k.Pages.PrevPage, k.Pages.NextPage):
		pane := m.focused()
		page := pane.pages.Page
		pane.pages, _ = pane.pages.Update(msg)
		if pane.pages.Page != page {
			pane.table.SetCursor(pane.pages.Page * pane.pages.PerPage)
		}
		m.syncPages(m.focus)
	default:
		pane := m.focused()
		pane.table, _ = pane.table.Update(msg)
		m.syncPages(m.focus)
	}
	return nil
}

func (m *model) updateRepositoryPicker(msg tea.KeyPressMsg) tea.Cmd {
	p := m.picker
	k := m.keys.Picker
	switch {
	case key.Matches(msg, k.Cancel):
		m.closeRepositoryPicker()
		return nil
	case key.Matches(msg, k.Retry):
		return m.startRepositoryList()
	case key.Matches(msg, k.Apply):
		candidate := p.selectedCandidate()
		switch candidate.kind {
		case allRepositoriesCandidate:
			m.closeRepositoryPicker()
			m.chooseRepository("")
		case knownRepositoryCandidate:
			m.closeRepositoryPicker()
			m.chooseRepository(candidate.repository)
		case lookupRepositoryCandidate:
			return m.startRepositoryLookup(candidate.repository)
		}
		return nil
	case key.Matches(msg, k.Up):
		p.move(-1, m.pickerViewportHeight())
		return nil
	case key.Matches(msg, k.Down):
		p.move(1, m.pickerViewportHeight())
		return nil
	case key.Matches(msg, k.Clear):
		p.setQuery("")
		p.clamp(m.pickerViewportHeight())
		return nil
	}
	// Picker keys that are disabled right now must not fall through to text input.
	if bound(msg, k.Apply, k.Retry, k.Up, k.Down, k.Clear) {
		return nil
	}
	return p.updateInput(msg, m.pickerViewportHeight())
}

func (m *model) rebuildVisiblePRs() {
	for _, id := range paneIDs {
		pane := &m.panes[id]
		pane.visible = pane.visible[:0]
		for index, pr := range m.source(id) {
			if m.selectedRepository == "" || strings.EqualFold(pr.Repository, m.selectedRepository) {
				pane.visible = append(pane.visible, index)
			}
		}
	}
	m.rebuildPRTable(true)
}

func (m *model) applyRepository(repository string) {
	m.selectedRepository = repository
	m.rebuildVisiblePRs()
}

func (m *model) chooseRepository(repository string) {
	m.applyRepository(repository)
	m.scopeChosen = true
	if err := m.preferences.Save(m.snapshot.Login, repository); err != nil {
		m.preferenceErr = fmt.Errorf("Selection not saved: %w", err)
		return
	}
	m.preferenceErr = nil
}

func (m *model) View() tea.View {
	var lines []string
	switch {
	case m.width < minimumWidth || m.height < minimumHeight:
		lines = wrapWords("Terminal too small; resize or press Ctrl+C to quit.", m.width)
	case m.loading && !m.loginActive && !m.refreshing():
		lines = []string{m.spinner.View() + " Loading open pull requests..."}
	case m.err != nil:
		lines = m.errorLines()
	case m.picker != nil:
		lines = m.repositoryPickerLines()
	case !m.scopeChosen:
		lines = m.scopeChoiceLines()
	case !m.panesFit():
		lines = wrapWords("Terminal too small; resize or press Ctrl+C to quit.", m.width)
	default:
		lines = m.listLines()
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], m.width, "…")
	}
	view := tea.NewView(strings.Join(lines, "\n"))
	// Screens fill the terminal height; inline rendering lets the terminal
	// scroll the top line away.
	view.AltScreen = true
	return view
}

// helpLines renders the help for a screen. Full help is used only when it is
// toggled on and the screen has a full view.
func (m *model) helpLines(screen func(keyMap) helpKeys) []string {
	m.syncKeys()
	keys := screen(m.keys)
	m.help.SetWidth(m.width)
	if m.help.ShowAll && keys.full != nil && m.fullHelpFits(keys) {
		// help.Model can overflow its width when no ellipsis fits, so pass
		// only the leading columns that fit.
		full := m.help.FullHelpView(keys.full[:1])
		for n := 2; n <= len(keys.full); n++ {
			view := m.help.FullHelpView(keys.full[:n])
			if lipgloss.Width(view) > m.width {
				break
			}
			full = view
		}
		return strings.Split(full, "\n")
	}
	return []string{m.help.ShortHelpView(keys.short)}
}

func (m *model) scopeChoiceLines() []string {
	pick, all := "  Pick a repository", "  Show all my PRs"
	if m.scopeChoiceCursor == 0 {
		pick = "> Pick a repository"
	} else {
		all = "> Show all my PRs"
	}
	return append([]string{
		m.titleLine(fmt.Sprintf("prpr — @%s", m.snapshot.Login)),
		"What would you like to watch?",
		pick,
		all,
	}, m.helpLines(keyMap.scopeChoiceHelp)...)
}

func (m *model) errorLines() []string {
	message := "GitHub request failed: " + m.err.Error()
	var authErr *github.AuthError
	if errors.As(m.err, &authErr) {
		message = m.err.Error()
	}
	return append(append([]string{message}, m.helpLines(keyMap.errorHelp)...),
		"Login command: gh auth login --hostname github.com --web")
}

// panesFit reports whether every drawn pane with rows has room for its columns.
func (m *model) panesFit() bool {
	layout := m.layoutPanes()
	for _, id := range paneIDs {
		if layout.single && id != m.focus {
			continue
		}
		if pane := &m.panes[id]; len(pane.visible) > 0 && !pane.fits {
			return false
		}
	}
	return true
}

func (m *model) listLines() []string {
	scope := "All repositories"
	if m.selectedRepository != "" {
		scope = singleLine(m.selectedRepository)
	}
	lines := []string{m.titleLine(fmt.Sprintf("prpr — @%s — %s", m.snapshot.Login, scope))}
	layout := m.layoutPanes()
	for _, id := range paneIDs {
		if layout.single && id != m.focus {
			continue
		}
		lines = append(lines, m.paneTitle(id, layout.single))
		if len(m.panes[id].visible) == 0 {
			lines = append(lines, m.emptyPaneLine(id))
			continue
		}
		lines = append(lines, strings.Split(m.panes[id].table.View(), "\n")...)
	}
	selectedURL := ""
	if pr, ok := m.selectedPR(); ok {
		selectedURL = singleLine(pr.URL)
	}
	lines = append(lines, selectedURL)
	status := "✓ clean  ✗ conflicts  ? unknown"
	if m.preferenceErr != nil {
		status = m.preferenceErr.Error()
	}
	if pane := m.focused(); len(pane.visible) > 0 && pane.pages.TotalPages > 1 {
		status = m.pageIndicator() + "  " + status
	}
	lines = append(lines, status)
	return append(lines, m.helpLines(keyMap.listHelp)...)
}

// titleLine puts a refresh indicator in the top-right corner while a fetch
// replaces the rows on screen. The title is truncated to keep it visible.
func (m *model) titleLine(title string) string {
	if !m.refreshing() {
		return title
	}
	indicator := m.spinner.View() + " Refreshing"
	space := m.width - lipgloss.Width(indicator) - 1
	if space < 1 {
		return indicator
	}
	title = ansi.Truncate(title, space, "…")
	return title + strings.Repeat(" ", m.width-lipgloss.Width(title)-lipgloss.Width(indicator)) + indicator
}

func singleLine(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
}

func wrapWords(value string, width int) []string {
	if width < 1 {
		return nil
	}
	var lines []string
	line := ""
	for _, word := range strings.Fields(value) {
		for len(word) > width {
			if line != "" {
				lines = append(lines, line)
				line = ""
			}
			lines = append(lines, word[:width])
			word = word[width:]
		}
		if line == "" {
			line = word
		} else if len(line)+1+len(word) <= width {
			line += " " + word
		} else {
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
