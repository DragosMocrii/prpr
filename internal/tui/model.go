package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
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
	visiblePRs          []int
	prTable             table.Model
	prTableFits         bool
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
	m := &model{ctx: appCtx, client: client, preferences: preferences, cancel: cancel}
	m.rebuildPRTable(true)
	return m
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
	m.rebuildPRTable(true)
	return func() tea.Msg {
		snapshot, err := m.client.Fetch(m.ctx)
		return fetchFinishedMsg{snapshot: snapshot, err: err}
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.rebuildPRTable(false)
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
			m.rebuildPRTable(true)
		} else {
			if !strings.EqualFold(m.filterLogin, msg.snapshot.Login) {
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
			m.snapshot = msg.snapshot
			m.err = nil
			m.rebuildVisiblePRs()
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
		case "p":
			if m.scopeChosen && !m.loading && !m.loginActive && m.err == nil && m.snapshot.Login != "" {
				return m, m.openRepositoryPicker()
			}
		case "c":
			if m.scopeChosen && !m.loading && !m.loginActive && m.err == nil && m.snapshot.Login != "" {
				m.chooseRepository("")
			}
		case "up", "k", "down", "j":
			if !m.loading && !m.loginActive && m.err == nil {
				if m.scopeChosen {
					if len(m.visiblePRs) > 0 {
						m.prTable, _ = m.prTable.Update(msg)
					}
				} else if msg.String() == "up" || msg.String() == "k" {
					m.scopeChoiceCursor = 0
				} else {
					m.scopeChoiceCursor = 1
				}
			}
		case "enter":
			if !m.scopeChosen && !m.loading && !m.loginActive && m.err == nil && m.snapshot.Login != "" {
				if m.scopeChoiceCursor == 1 {
					m.chooseRepository("")
				} else {
					return m, m.openRepositoryPicker()
				}
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
			m.chooseRepository("")
		case knownRepositoryCandidate:
			m.closeRepositoryPicker()
			m.chooseRepository(candidate.repository)
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
	case m.loading && !m.loginActive:
		lines = []string{"Loading open pull requests..."}
	case m.err != nil:
		lines = m.errorLines()
	case m.picker != nil:
		lines = m.repositoryPickerLines()
	case !m.scopeChosen:
		lines = m.scopeChoiceLines()
	case len(m.visiblePRs) > 0 && !m.prTableFits:
		lines = wrapWords("Terminal too small; resize or press Ctrl+C to quit.", m.width)
	default:
		lines = m.listLines()
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], m.width, "…")
	}
	view := tea.NewView(strings.Join(lines, "\n"))
	return view
}

func (m *model) scopeChoiceLines() []string {
	pick, all := "  Pick a repository", "  Show all my PRs"
	if m.scopeChoiceCursor == 0 {
		pick = "> Pick a repository"
	} else {
		all = "> Show all my PRs"
	}
	return []string{
		fmt.Sprintf("prpr — @%s", m.snapshot.Login),
		"What would you like to watch?",
		pick,
		all,
		"↑/↓ or j/k: Select    Enter: Continue    q: Quit",
	}
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
		lines = append(lines, "")
	} else {
		selectedURL := ""
		if pr, ok := m.selectedPR(); ok {
			selectedURL = singleLine(pr.URL)
		}
		lines = append(lines, selectedURL)
		lines = append(lines, strings.Split(m.prTable.View(), "\n")...)
	}
	status := "✓ clean  ✗ conflicts  ? unknown"
	if m.preferenceErr != nil {
		status = m.preferenceErr.Error()
	}
	lines = append(lines, status, "↑/k: Up    ↓/j: Down    p: Change repo    c: All PRs    r: Refresh    q: Quit")
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
