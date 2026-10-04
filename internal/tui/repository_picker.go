package tui

import (
	"context"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/preferences"
)

type repositoryCandidateKind uint8

const (
	allRepositoriesCandidate repositoryCandidateKind = iota
	watchlistCandidate
	ownerCandidate
	knownRepositoryCandidate
	lookupRepositoryCandidate
)

type repositoryCandidate struct {
	kind       repositoryCandidateKind
	label      string
	repository string
	// watchlist names a watchlist candidate's watchlist.
	watchlist string
	// owner names an owner candidate's owner.
	owner string
}

type repositoryPicker struct {
	input textinput.Model
	// star marks watchlists.
	star         string
	repositories []string
	candidates   []repositoryCandidate
	cursor       int
	offset       int
	busy         bool
	lookup       bool
	diagnostic   string
	cancel       context.CancelFunc
	// watchlists are the account's saved watchlists.
	watchlists []preferences.Watchlist
	// marked holds the repositories marked for a watchlist, by lowercase
	// name. editing names the watchlist they were loaded from, if any.
	marked  map[string]string
	editing string
	// markLookup marks the looked-up repository instead of choosing it.
	markLookup bool
	// naming is the watchlist name input while it is open. confirmReplace
	// and confirmDelete name the watchlist that the next Enter replaces or
	// the next Ctrl+D deletes.
	naming         *textinput.Model
	confirmReplace string
	confirmDelete  string
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
	picker := &repositoryPicker{input: textinput.New(), star: m.icons.star}
	picker.input.Prompt = "Find: "
	picker.input.Placeholder = "owner/repo"
	// Terminal paste arrives as tea.PasteMsg; skip the clipboard helper.
	picker.input.KeyMap.Paste.SetEnabled(false)
	picker.setDark(m.darkBackground)
	picker.setWidth(m.width)
	for _, pr := range append(slices.Clone(m.snapshot.PullRequests), m.snapshot.ReviewRequests...) {
		picker.repositories = append(picker.repositories, pr.Repository)
	}
	picker.repositories = sortedRepositoryNames(picker.repositories)
	picker.watchlists = m.preferences.Watchlists(m.snapshot.Login)
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

// startRepositoryLookup checks access to a repository, then chooses it, or
// marks it for a watchlist when mark is set.
func (m *model) startRepositoryLookup(repository string, mark bool) tea.Cmd {
	if m.picker == nil || m.picker.busy {
		return nil
	}
	m.picker.markLookup = mark
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
	if m.picker.markLookup {
		m.picker.repositories = sortedRepositoryNames(append(m.picker.repositories, msg.repository))
		m.picker.setMark(msg.repository, true)
		m.picker.setQuery("")
		m.picker.clamp(m.pickerViewportHeight())
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
	for _, watchlist := range p.watchlists {
		if strings.Contains(strings.ToLower(watchlist.Name), strings.ToLower(trimmed)) {
			p.candidates = append(p.candidates, repositoryCandidate{
				kind:      watchlistCandidate,
				label:     p.star + " " + watchlist.Name + " · " + plural(len(watchlist.Repositories), "repo"),
				watchlist: watchlist.Name,
			})
			matching++
		}
	}
	for _, owner := range repositoryOwners(p.repositories) {
		label := owner.name + "/*"
		if strings.Contains(strings.ToLower(label), strings.ToLower(trimmed)) {
			p.candidates = append(p.candidates, repositoryCandidate{
				kind:  ownerCandidate,
				label: label + " · " + plural(owner.repositories, "repo"),
				owner: owner.name,
			})
			matching++
		}
	}
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

type repositoryOwner struct {
	name         string
	repositories int
}

// repositoryOwners counts the repositories of each owner, sorted by name,
// keeping the first spelling of each owner.
func repositoryOwners(names []string) []repositoryOwner {
	var owners []repositoryOwner
	for _, name := range names {
		owner, _, _ := strings.Cut(name, "/")
		if i := slices.IndexFunc(owners, func(o repositoryOwner) bool { return strings.EqualFold(o.name, owner) }); i >= 0 {
			owners[i].repositories++
			continue
		}
		owners = append(owners, repositoryOwner{name: owner, repositories: 1})
	}
	slices.SortFunc(owners, func(a, b repositoryOwner) int {
		return strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name))
	})
	return owners
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
	if p.naming != nil {
		var cmd tea.Cmd
		*p.naming, cmd = p.naming.Update(tea.PasteMsg{Content: content})
		p.confirmReplace = ""
		return cmd
	}
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
	input := p.input.View()
	if p.naming != nil {
		input = p.naming.View()
	}
	lines := []string{"Select repository, organization, or watchlist", input}
	end := p.offset + visible
	if end > len(p.candidates) {
		end = len(p.candidates)
	}
	for i := p.offset; i < end; i++ {
		marker := "  "
		if i == p.cursor {
			marker = "> "
		}
		candidate := p.candidates[i]
		if len(p.marked) > 0 && candidate.kind == knownRepositoryCandidate {
			if p.isMarked(candidate.repository) {
				marker += "[x] "
			} else {
				marker += "[ ] "
			}
		}
		lines = append(lines, marker+singleLine(candidate.label))
	}
	status := "Repositories you can access; enter owner/repo for others."
	switch {
	case p.busy && p.lookup:
		status = m.spinner.View() + " Checking repository..."
	case p.busy:
		status = m.spinner.View() + " Loading repositories..."
	case p.diagnostic != "":
		status = singleLine(p.diagnostic)
	case p.naming != nil:
		status = "Name the watchlist of " + plural(len(p.marked), "repo") + "."
	case p.editing != "":
		status = "Editing " + p.editing + ": " + strconv.Itoa(len(p.marked)) + " marked; enter saves, esc discards changes."
	case len(p.marked) > 0:
		status = strconv.Itoa(len(p.marked)) + " marked; enter names the watchlist, esc clears marks."
	case len(p.watchlists) == 0:
		status = "Repositories you can access; enter owner/repo for others, space marks repositories for a watchlist."
	case strings.TrimSpace(p.query()) != "" && len(p.candidates) == 1:
		status = "No matching repositories. Enter owner/repo to check another repository."
	}
	lines = append(lines, status)
	if p.naming != nil {
		return append(lines, m.helpLines(keyMap.watchlistNameHelp)...)
	}
	return append(lines, m.helpLines(keyMap.pickerHelp)...)
}

func (p *repositoryPicker) selectedCandidate() repositoryCandidate {
	return p.candidates[p.cursor]
}
