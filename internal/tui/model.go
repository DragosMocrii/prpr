package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
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
	changes             [2]paneChanges
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
	now                 func() time.Time
	refreshInterval     time.Duration
	// refreshGeneration increments with every fetch so that auto-refresh
	// ticks scheduled before it are ignored.
	refreshGeneration uint64
	// refreshDue is when the scheduled auto-refresh fires; zero when none is
	// pending. countdownGeneration increments with every schedule so only the
	// newest countdown tick chain keeps running.
	refreshDue          time.Time
	countdownGeneration uint64
	quota               github.RateLimit
	quotaKnown          bool
	quotaStale          bool
	quotaPaused         bool
	// bots shows the Bots column and the selected PR's bot breakdown.
	bots bool
}

type fetchFinishedMsg struct {
	snapshot github.Snapshot
	err      error
}

type loginFinishedMsg struct{ err error }

type autoRefreshMsg struct{ generation uint64 }

type countdownTickMsg struct{ generation uint64 }

// New returns the app model. A positive refreshInterval refetches both lists
// that long after each fetch finishes.
func New(ctx context.Context, client *github.Client, preferences *preferences.Store, refreshInterval time.Duration) tea.Model {
	appCtx, cancel := context.WithCancel(ctx)
	m := newModel(appCtx, client, preferences)
	m.cancel = cancel
	m.refreshInterval = refreshInterval
	m.bots = len(client.Bots()) > 0
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
		now:            time.Now,
	}
	m.panes = [2]prPane{newPRPane(), newPRPane()}
	m.rebuildPRTable(true)
	return m
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.startFetch(), m.pollQuota())
}

func (m *model) startFetch() tea.Cmd {
	m.closeRepositoryPicker()
	m.loading = true
	m.loginActive = false
	m.refreshGeneration++
	m.refreshDue = time.Time{}
	m.err = nil
	client, ctx := m.client, m.ctx
	return tea.Batch(func() tea.Msg {
		snapshot, err := client.Fetch(ctx)
		return fetchFinishedMsg{snapshot: snapshot, err: err}
	}, m.spinner.Tick)
}

// scheduleAutoRefresh starts the auto-refresh timer for the current fetch
// generation.
func (m *model) scheduleAutoRefresh() tea.Cmd {
	if m.refreshInterval <= 0 {
		return nil
	}
	generation := m.refreshGeneration
	m.refreshDue = m.now().Add(m.refreshInterval)
	m.countdownGeneration++
	return tea.Batch(tea.Tick(m.refreshInterval, func(time.Time) tea.Msg {
		return autoRefreshMsg{generation: generation}
	}), m.countdownTick())
}

// countdownTick redraws the countdown when its shown second changes. The
// chain ends when the refresh is due, starts, or is rescheduled.
func (m *model) countdownTick() tea.Cmd {
	remaining := m.refreshDue.Sub(m.now())
	if m.refreshDue.IsZero() || remaining <= 0 {
		return nil
	}
	delay := remaining % time.Second
	if delay == 0 {
		delay = time.Second
	}
	generation := m.countdownGeneration
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return countdownTickMsg{generation: generation}
	})
}

// countdownText is the time left before the auto-refresh, rounded up to the
// second, or empty when none is pending.
func (m *model) countdownText() string {
	if m.refreshDue.IsZero() {
		return ""
	}
	seconds := int((m.refreshDue.Sub(m.now()) + time.Second - 1) / time.Second)
	seconds = max(seconds, 0)
	text := fmt.Sprintf("%d:%02d", seconds/60%60, seconds%60)
	if seconds >= 3600 {
		text = fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
	}
	return "refresh in " + text
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
		m.applyFocusStyles()
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
	case rateLimitMsg, quotaTickMsg:
		return m, m.handleQuota(msg)
	case countdownTickMsg:
		if msg.generation != m.countdownGeneration {
			return m, nil
		}
		return m, m.countdownTick()
	case autoRefreshMsg:
		if msg.generation != m.refreshGeneration {
			return m, nil
		}
		// Fetching would close the picker or skip the scope choice, and login
		// fetches when it finishes.
		if m.picker != nil || m.loginActive || (!m.scopeChosen && m.snapshot.Login != "") {
			return m, m.scheduleAutoRefresh()
		}
		return m, m.startFetch()
	case fetchFinishedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			m.snapshot = github.Snapshot{}
			// Change tracking keeps its baseline, so the next success is
			// compared with the last good lists.
			for _, id := range paneIDs {
				m.panes[id].visible = nil
				m.panes[id].gone = nil
			}
			m.rebuildPRTable(true)
			// Authentication failures need a login, not a retry.
			var authErr *github.AuthError
			if errors.As(msg.err, &authErr) {
				return m, nil
			}
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
			m.keepingSelection(func() {
				m.snapshot = msg.snapshot
				m.err = nil
				for _, id := range paneIDs {
					if accountChanged {
						m.changes[id].reset(m.source(id))
					} else {
						m.changes[id].update(m.source(id))
					}
				}
				m.rebuildVisiblePRs()
			})
			if accountChanged {
				focus := paneMine
				if len(m.panes[paneMine].visible) == 0 && len(m.panes[paneReview].visible) > 0 {
					focus = paneReview
				}
				m.setFocus(focus)
			}
		}
		if m.err == nil {
			return m, tea.Batch(m.scheduleAutoRefresh(), m.resumeQuota())
		}
		return m, m.scheduleAutoRefresh()
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
	case key.Matches(msg, k.ClearMarks):
		m.keepingSelection(func() {
			for _, id := range paneIDs {
				m.changes[id].clear()
			}
			m.rebuildVisiblePRs()
		})
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
		left := pane.table.Cursor()
		page := pane.pages.Page
		pane.pages, _ = pane.pages.Update(msg)
		if pane.pages.Page != page {
			moveCursor(&pane.table, pane.pages.Page*pane.pages.PerPage)
		}
		m.syncPages(m.focus)
		m.leaveRow(m.focus, left)
	default:
		pane := m.focused()
		left := pane.table.Cursor()
		pane.table, _ = pane.table.Update(msg)
		m.syncPages(m.focus)
		m.leaveRow(m.focus, left)
	}
	return nil
}

// leaveRow clears the mark of the row the cursor just left, or drops it when
// it is gone. A gone row above the cursor shifts the cursor up with the rows.
func (m *model) leaveRow(id paneID, row int) {
	pane := &m.panes[id]
	if pane.table.Cursor() == row {
		return
	}
	pr, gone, ok := m.paneRow(id, row)
	if !ok {
		return
	}
	if !gone {
		if m.changes[id].see(pr) {
			m.redrawRows(id)
		}
		return
	}
	m.changes[id].dismiss(pr)
	m.rebuildGone(id)
	target := pane.table.Cursor()
	if row < target {
		target--
	}
	m.redrawRows(id)
	moveCursor(&pane.table, target)
	m.syncPages(id)
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
			if m.inScope(&pr) {
				pane.visible = append(pane.visible, index)
			}
		}
		m.rebuildGone(id)
	}
	m.rebuildPRTable(true)
}

// keepingSelection runs rebuild, then reselects each pane's pull request if
// it is still in the pane.
func (m *model) keepingSelection(rebuild func()) {
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
	rebuild()
	for _, id := range paneIDs {
		if previous[id].ok {
			m.selectPR(id, previous[id].repository, previous[id].number)
		}
	}
}

func (m *model) inScope(pr *github.PullRequest) bool {
	return m.selectedRepository == "" || strings.EqualFold(pr.Repository, m.selectedRepository)
}

// rebuildGone lists a pane's gone pull requests in the current scope.
func (m *model) rebuildGone(id paneID) {
	pane := &m.panes[id]
	pane.gone = pane.gone[:0]
	for index := range m.changes[id].gone {
		if m.inScope(&m.changes[id].gone[index]) {
			pane.gone = append(pane.gone, index)
		}
	}
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
		m.titleLine(fmt.Sprintf("prpr — @%s", m.snapshot.Login), ""),
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
	lines := []string{message}
	if quota := m.quotaText(0); quota != "" {
		lines = append(lines, quota)
	}
	return append(append(lines, m.helpLines(keyMap.errorHelp)...),
		"Login command: gh auth login --hostname github.com --web")
}

// panesFit reports whether every drawn pane with rows has room for its columns.
func (m *model) panesFit() bool {
	layout := m.layoutPanes()
	for _, id := range paneIDs {
		if layout.single && id != m.focus {
			continue
		}
		if pane := &m.panes[id]; rowCount(pane) > 0 && !pane.fits {
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
	title := fmt.Sprintf("prpr — @%s — %s", m.snapshot.Login, scope)
	if m.refreshInterval > 0 {
		title += " · auto " + intervalText(m.refreshInterval)
	}
	lines := []string{m.titleLine(title, m.countdownText())}
	layout := m.layoutPanes()
	for _, id := range paneIDs {
		if layout.single && id != m.focus {
			continue
		}
		lines = append(lines, m.paneTitle(id, layout.single))
		if rowCount(&m.panes[id]) == 0 {
			lines = append(lines, m.emptyPaneLine(id))
			continue
		}
		lines = append(lines, m.tableLines(id)...)
	}
	selected := ""
	if pr, ok := m.selectedPR(); ok {
		selected = singleLine(pr.URL)
		// The breakdown is dropped rather than truncated so the URL stays whole.
		if breakdown := botBreakdown(pr.Bots); m.bots && breakdown != "" &&
			lipgloss.Width(selected)+2+lipgloss.Width(breakdown) <= m.width {
			selected += "  " + breakdown
		}
	}
	lines = append(lines, selected)
	fixed, legend := "", ""
	if pane := m.focused(); rowCount(pane) > 0 && pane.pages.TotalPages > 1 {
		fixed = m.pageIndicator()
	}
	if m.preferenceErr != nil {
		fixed = strings.TrimLeft(fixed+"  "+m.preferenceErr.Error(), " ")
	} else if !(layout.single && m.focus != paneMine) && rowCount(&m.panes[paneMine]) > 0 {
		legend = "✓ ready  ● blocked  ↓ behind  ✗ conflicts  ? unknown"
	}
	lines = append(lines, m.statusLine(fixed, legend))
	return append(lines, m.helpLines(keyMap.listHelp)...)
}

// titleLine puts a refresh indicator in the top-right corner while a fetch
// replaces the rows on screen, and otherwise the countdown when one is given.
// The title is truncated to keep the corner visible.
func (m *model) titleLine(title, countdown string) string {
	indicator := countdown
	if m.refreshing() {
		indicator = m.spinner.View() + " Refreshing"
	}
	if indicator == "" {
		return title
	}
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

// intervalText drops the zero units time.Duration.String prints, so 5m0s
// reads as 5m.
func intervalText(d time.Duration) string {
	text := d.String()
	if strings.HasSuffix(text, "m0s") {
		text = strings.TrimSuffix(text, "0s")
	}
	if strings.HasSuffix(text, "h0m") {
		text = strings.TrimSuffix(text, "0m")
	}
	return text
}
