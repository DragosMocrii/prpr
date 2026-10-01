package tui

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

type repositoryCandidateKind uint8

const (
	allRepositoriesCandidate repositoryCandidateKind = iota
	knownRepositoryCandidate
	lookupRepositoryCandidate
)

type repositoryCandidate struct {
	kind       repositoryCandidateKind
	label      string
	repository string
}

type repositoryPicker struct {
	input        textinput.Model
	repositories []string
	candidates   []repositoryCandidate
	cursor       int
	offset       int
	busy         bool
	lookup       bool
	diagnostic   string
	cancel       context.CancelFunc
}

type repositoryListFinishedMsg struct {
	requestID    uint64
	repositories []string
	err          error
}

type repositoryLookupFinishedMsg struct {
	requestID  uint64
	repository string
	err        error
}

func (m *model) openRepositoryPicker() tea.Cmd {
	m.repositoryRequestID++
	picker := &repositoryPicker{input: textinput.New()}
	picker.input.Prompt = "Find: "
	picker.input.Placeholder = "owner/repo"
	// Terminal paste arrives as tea.PasteMsg; skip the clipboard helper.
	picker.input.KeyMap.Paste.SetEnabled(false)
	picker.setDark(m.darkBackground)
	picker.setWidth(m.width)
	for _, pr := range m.snapshot.PullRequests {
		picker.repositories = append(picker.repositories, pr.Repository)
	}
	picker.repositories = sortedRepositoryNames(picker.repositories)
	m.picker = picker
	picker.rebuildCandidates()
	return tea.Batch(picker.input.Focus(), m.startRepositoryList())
}

func (p *repositoryPicker) setDark(dark bool) {
	styles := textinput.DefaultStyles(dark)
	styles.Cursor.Blink = false
	p.input.SetStyles(styles)
}

func (p *repositoryPicker) setWidth(width int) {
	// Leave a column for the cursor cell after the prompt and text.
	p.input.SetWidth(max(1, width-ansi.StringWidth(p.input.Prompt)-1))
}

func (m *model) startRepositoryList() tea.Cmd {
	if m.picker == nil || m.picker.busy {
		return nil
	}
	m.picker.cancelCurrent()
	ctx, cancel := context.WithCancel(m.ctx)
	m.picker.cancel = cancel
	m.picker.busy = true
	m.picker.lookup = false
	m.picker.diagnostic = ""
	m.repositoryRequestID++
	requestID := m.repositoryRequestID
	client := m.client
	return tea.Batch(func() tea.Msg {
		repositories, err := client.ListRepositories(ctx)
		return repositoryListFinishedMsg{requestID: requestID, repositories: repositories, err: err}
	}, m.spinner.Tick)
}

func (m *model) startRepositoryLookup(repository string) tea.Cmd {
	if m.picker == nil || m.picker.busy {
		return nil
	}
	m.picker.cancelCurrent()
	ctx, cancel := context.WithCancel(m.ctx)
	m.picker.cancel = cancel
	m.picker.busy = true
	m.picker.lookup = true
	m.picker.input.Blur()
	m.picker.diagnostic = ""
	m.repositoryRequestID++
	requestID := m.repositoryRequestID
	client := m.client
	return tea.Batch(func() tea.Msg {
		canonical, err := client.ResolveRepository(ctx, repository)
		return repositoryLookupFinishedMsg{requestID: requestID, repository: canonical, err: err}
	}, m.spinner.Tick)
}

func (p *repositoryPicker) cancelCurrent() {
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
}

func (m *model) closeRepositoryPicker() {
	if m.picker != nil {
		m.picker.cancelCurrent()
		m.picker = nil
		m.repositoryRequestID++
	}
}

func (m *model) handleRepositoryListFinished(msg repositoryListFinishedMsg) {
	if m.picker == nil || msg.requestID != m.repositoryRequestID {
		return
	}
	m.picker.cancelCurrent()
	m.picker.busy = false
	m.picker.lookup = false
	if msg.err != nil {
		m.picker.diagnostic = msg.err.Error()
	} else {
		m.picker.repositories = sortedRepositoryNames(append(m.picker.repositories, msg.repositories...))
		m.picker.diagnostic = ""
	}
	m.picker.rebuildCandidates()
	m.picker.clamp(m.pickerViewportHeight())
}

func (m *model) handleRepositoryLookupFinished(msg repositoryLookupFinishedMsg) tea.Cmd {
	if m.picker == nil || msg.requestID != m.repositoryRequestID {
		return nil
	}
	m.picker.cancelCurrent()
	m.picker.busy = false
	m.picker.lookup = false
	if msg.err != nil {
		m.picker.diagnostic = msg.err.Error()
		return m.picker.input.Focus()
	}
	m.closeRepositoryPicker()
	m.chooseRepository(msg.repository)
	return nil
}

func sortedRepositoryNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	unique := make([]string, 0, len(names))
	for _, name := range names {
		if !github.ValidRepositoryName(name) {
			continue
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, name)
	}
	sort.Slice(unique, func(i, j int) bool {
		return strings.ToLower(unique[i]) < strings.ToLower(unique[j])
	})
	return unique
}

func (p *repositoryPicker) rebuildCandidates() {
	p.candidates = []repositoryCandidate{{kind: allRepositoriesCandidate, label: "All repositories"}}
	trimmed := strings.TrimSpace(p.query())
	matching := 0
	exact := false
	for _, name := range p.repositories {
		if strings.Contains(strings.ToLower(name), strings.ToLower(trimmed)) {
			p.candidates = append(p.candidates, repositoryCandidate{kind: knownRepositoryCandidate, label: name, repository: name})
			matching++
		}
		if strings.EqualFold(name, trimmed) {
			exact = true
		}
	}
	if trimmed != "" && github.ValidRepositoryName(trimmed) && !exact {
		p.candidates = append(p.candidates, repositoryCandidate{
			kind:       lookupRepositoryCandidate,
			label:      "Use " + trimmed + " (check access)",
			repository: trimmed,
		})
	}
	p.cursor = 0
	if trimmed != "" && (matching > 0 || (!exact && github.ValidRepositoryName(trimmed))) {
		for i, candidate := range p.candidates {
			if candidate.kind != allRepositoriesCandidate {
				p.cursor = i
				break
			}
		}
	}
	p.offset = 0
}

func (p *repositoryPicker) query() string {
	return p.input.Value()
}

func (p *repositoryPicker) setQuery(value string) {
	p.input.SetValue(value)
	p.rebuildCandidates()
}

// updateInput forwards a key to the query input and rebuilds the candidates
// only when the query text changed, so cursor movement keeps the selection.
func (p *repositoryPicker) updateInput(msg tea.Msg, visible int) tea.Cmd {
	before := p.query()
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	if p.query() != before {
		p.rebuildCandidates()
	}
	p.clamp(visible)
	return cmd
}

// paste discards control characters instead of letting the input turn
// newlines into spaces inside a repository name.
func (p *repositoryPicker) paste(content string, visible int) tea.Cmd {
	content = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, content)
	return p.updateInput(tea.PasteMsg{Content: content}, visible)
}

func (p *repositoryPicker) move(delta, visible int) {
	if len(p.candidates) == 0 {
		p.cursor, p.offset = 0, 0
		return
	}
	p.cursor += delta
	p.clamp(visible)
}

func (p *repositoryPicker) clamp(visible int) {
	if len(p.candidates) == 0 {
		p.cursor, p.offset = 0, 0
		return
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
	if p.cursor >= len(p.candidates) {
		p.cursor = len(p.candidates) - 1
	}
	if visible < 1 {
		p.offset = p.cursor
		return
	}
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+visible {
		p.offset = p.cursor - visible + 1
	}
	if max := len(p.candidates) - visible; p.offset > max && max >= 0 {
		p.offset = max
	}
	if p.offset < 0 {
		p.offset = 0
	}
}

func (m *model) pickerViewportHeight() int {
	if visible := m.height - 4; visible > 0 {
		return visible
	}
	return 0
}

func (m *model) repositoryPickerLines() []string {
	p := m.picker
	visible := m.pickerViewportHeight()
	p.clamp(visible)
	lines := []string{"Select repository", p.input.View()}
	end := p.offset + visible
	if end > len(p.candidates) {
		end = len(p.candidates)
	}
	for i := p.offset; i < end; i++ {
		marker := "  "
		if i == p.cursor {
			marker = "> "
		}
		lines = append(lines, marker+singleLine(p.candidates[i].label))
	}
	status := "Repositories you can access; enter owner/repo for others."
	switch {
	case p.busy && p.lookup:
		status = m.spinner.View() + " Checking repository..."
	case p.busy:
		status = m.spinner.View() + " Loading repositories..."
	case p.diagnostic != "":
		status = singleLine(p.diagnostic)
	case strings.TrimSpace(p.query()) != "" && len(p.candidates) == 1:
		status = "No matching repositories. Enter owner/repo to check another repository."
	}
	lines = append(lines, status)
	return append(lines, m.helpLines(keyMap.pickerHelp)...)
}

func (p *repositoryPicker) selectedCandidate() repositoryCandidate {
	return p.candidates[p.cursor]
}
