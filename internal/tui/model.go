package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"prpr/internal/github"
)

const (
	minimumWidth  = 20
	minimumHeight = 8
)

type model struct {
	ctx                 context.Context
	client              *github.Client
	cancel              context.CancelFunc
	snapshot            github.Snapshot
	selectedRepository  string
	filterLogin         string
	visiblePRs          []int
	picker              *repositoryPicker
	repositoryRequestID uint64
	cursor              int
	offset              int
	width               int
	height              int
	loading             bool
	loginActive         bool
	err                 error
}

type fetchFinishedMsg struct {
	snapshot github.Snapshot
	err      error
}

type loginFinishedMsg struct{ err error }

func New(ctx context.Context, client *github.Client) tea.Model {
	appCtx, cancel := context.WithCancel(ctx)
	return &model{ctx: appCtx, client: client, cancel: cancel}
}

func (m *model) Init() tea.Cmd {
	return m.startFetch()
}

func (m *model) startFetch() tea.Cmd {
	m.closeRepositoryPicker()
	m.loading = true
	m.loginActive = false
	m.err = nil
	m.snapshot = github.Snapshot{}
	m.visiblePRs = nil
	m.cursor = 0
	m.offset = 0
	return func() tea.Msg {
		snapshot, err := m.client.Fetch(m.ctx)
		return fetchFinishedMsg{snapshot: snapshot, err: err}
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampSelection()
		if m.picker != nil {
			m.picker.clamp(m.pickerViewportHeight())
		}
	case repositoryListFinishedMsg:
		m.handleRepositoryListFinished(msg)
	case repositoryLookupFinishedMsg:
		m.handleRepositoryLookupFinished(msg)
	case fetchFinishedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			m.snapshot = github.Snapshot{}
			m.visiblePRs = nil
			m.cursor, m.offset = 0, 0
		} else {
			if m.filterLogin != "" && m.filterLogin != msg.snapshot.Login {
				m.selectedRepository = ""
			}
			m.filterLogin = msg.snapshot.Login
			m.snapshot = msg.snapshot
			m.err = nil
			m.rebuildVisiblePRs()
			m.cursor, m.offset = 0, 0
			m.clampSelection()
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
		if m.picker != nil && !m.picker.lookup {
			m.picker.appendInput(msg.Content)
			m.picker.clamp(m.pickerViewportHeight())
		}
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			m.closeRepositoryPicker()
			m.cancel()
			return m, tea.Quit
		}
		if m.picker != nil {
			return m, m.updateRepositoryPicker(msg)
		}
		switch msg.String() {
		case "q":
			m.cancel()
			return m, tea.Quit
		case "r":
			if !m.loading && !m.loginActive {
				return m, m.startFetch()
			}
		case "l":
			if m.err != nil && !m.loading && !m.loginActive {
				m.loading = true
				m.loginActive = true
				return m, tea.ExecProcess(m.client.LoginCommand(m.ctx), func(err error) tea.Msg {
					return loginFinishedMsg{err: err}
				})
			}
		case "up", "k":
			if !m.loading && !m.loginActive && m.cursor > 0 {
				m.cursor--
				m.clampSelection()
			}
		case "down", "j":
			if !m.loading && !m.loginActive && m.cursor+1 < len(m.visiblePRs) {
				m.cursor++
				m.clampSelection()
			}
		case "p":
			if !m.loading && !m.loginActive && m.err == nil && m.snapshot.Login != "" {
				return m, m.openRepositoryPicker()
			}
		}
	}
	return m, nil
}

func (m *model) updateRepositoryPicker(msg tea.KeyPressMsg) tea.Cmd {
	p := m.picker
	switch msg.String() {
	case "esc":
		m.closeRepositoryPicker()
		return nil
	case "ctrl+r":
		if !p.busy {
			return m.startRepositoryList()
		}
		return nil
	case "enter":
		if p.busy {
			return nil
		}
		candidate := p.selectedCandidate()
		switch candidate.kind {
		case allRepositoriesCandidate:
			m.closeRepositoryPicker()
			m.applyRepository("")
		case knownRepositoryCandidate:
			m.closeRepositoryPicker()
			m.applyRepository(candidate.repository)
		case lookupRepositoryCandidate:
			return m.startRepositoryLookup(candidate.repository)
		}
		return nil
	case "up":
		if !p.lookup {
			p.move(-1, m.pickerViewportHeight())
		}
		return nil
	case "down":
		if !p.lookup {
			p.move(1, m.pickerViewportHeight())
		}
		return nil
	}
	if p.lookup {
		return nil
	}
	p.handleTextKey(msg)
	p.clamp(m.pickerViewportHeight())
	return nil
}

func (m *model) rebuildVisiblePRs() {
	m.visiblePRs = m.visiblePRs[:0]
	for index, pr := range m.snapshot.PullRequests {
		if m.selectedRepository == "" || strings.EqualFold(pr.Repository, m.selectedRepository) {
			m.visiblePRs = append(m.visiblePRs, index)
		}
	}
	m.cursor, m.offset = 0, 0
	m.clampSelection()
}

func (m *model) applyRepository(repository string) {
	m.selectedRepository = repository
	m.rebuildVisiblePRs()
}

func (m *model) clampSelection() {
	count := len(m.visiblePRs)
	if count == 0 {
		m.cursor, m.offset = 0, 0
		return
	}
	if m.cursor >= count {
		m.cursor = count - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	visible := m.viewportHeight()
	if visible < 1 {
		m.offset = m.cursor
		return
	}
	if m.offset > m.cursor {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+visible {
		m.offset = m.cursor - visible + 1
	}
	maxOffset := count - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *model) viewportHeight() int {
	available := m.height - 4 // account header, repository scope, selected URL, and controls
	if available < 0 {
		return 0
	}
	return available
}

func (m *model) View() tea.View {
	var lines []string
	switch {
	case m.width < minimumWidth || m.height < minimumHeight:
		lines = wrapWords("Terminal too small; resize or press q to quit.", m.width)
	case m.picker != nil:
		lines = m.repositoryPickerLines()
	case m.loading && !m.loginActive:
		lines = []string{"Loading open pull requests..."}
	case m.err != nil:
		lines = m.errorLines()
	default:
		lines = m.listLines()
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], m.width, "…")
	}
	view := tea.NewView(strings.Join(lines, "\n"))
	return view
}

func (m *model) errorLines() []string {
	message := "GitHub request failed: " + m.err.Error()
	var authErr *github.AuthError
	if errors.As(m.err, &authErr) {
		message = m.err.Error()
	}
	return []string{
		message,
		"l: Log in to GitHub    r: Retry    q: Quit",
		"Login command: gh auth login --hostname github.com --web",
	}
}

func (m *model) listLines() []string {
	lines := []string{fmt.Sprintf("prpr — @%s — %d open PRs", m.snapshot.Login, len(m.visiblePRs))}
	if m.selectedRepository == "" {
		lines = append(lines, "Repository: All repositories")
	} else {
		lines = append(lines, "Repository: "+singleLine(m.selectedRepository))
	}
	if len(m.visiblePRs) == 0 {
		if m.selectedRepository == "" {
			lines = append(lines, "No open pull requests.")
		} else {
			lines = append(lines, "No open pull requests in "+singleLine(m.selectedRepository)+".")
		}
	} else {
		end := m.offset + m.viewportHeight()
		if end > len(m.visiblePRs) {
			end = len(m.visiblePRs)
		}
		for i := m.offset; i < end; i++ {
			pr := m.snapshot.PullRequests[m.visiblePRs[i]]
			marker := "  "
			if i == m.cursor {
				marker = "> "
			}
			row := fmt.Sprintf("%s%s #%d", marker, singleLine(pr.Repository), pr.Number)
			if pr.Draft {
				row += " [draft]"
			}
			row += " " + singleLine(pr.Title)
			lines = append(lines, row)
		}
		selected := m.snapshot.PullRequests[m.visiblePRs[m.cursor]]
		lines = append(lines, singleLine(selected.URL))
	}
	lines = append(lines, "↑/k: Up    ↓/j: Down    p: Repository    r: Refresh    q: Quit")
	return lines
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
