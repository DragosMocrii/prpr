package tui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/preferences"
)

func (p *repositoryPicker) isMarked(repository string) bool {
	_, ok := p.marked[strings.ToLower(repository)]
	return ok
}

func (p *repositoryPicker) setMark(repository string, marked bool) {
	if !marked {
		delete(p.marked, strings.ToLower(repository))
		return
	}
	if p.marked == nil {
		p.marked = make(map[string]string)
	}
	p.marked[strings.ToLower(repository)] = repository
}

// clearMarks drops the marks and any watchlist being edited.
func (p *repositoryPicker) clearMarks() {
	p.marked = nil
	p.editing = ""
	p.diagnostic = ""
}

func (p *repositoryPicker) markedRepositories() []string {
	repositories := make([]string, 0, len(p.marked))
	for _, repository := range p.marked {
		repositories = append(repositories, repository)
	}
	return sortedRepositoryNames(repositories)
}

func (p *repositoryPicker) findWatchlist(name string) (preferences.Watchlist, bool) {
	index := slices.IndexFunc(p.watchlists, func(w preferences.Watchlist) bool { return strings.EqualFold(w.Name, name) })
	if index < 0 {
		return preferences.Watchlist{}, false
	}
	return p.watchlists[index], true
}

// markCandidate marks or unmarks the repository under the cursor. A
// repository typed as owner/repo is checked first, then marked.
func (m *model) markCandidate() tea.Cmd {
	p := m.picker
	candidate := p.selectedCandidate()
	switch candidate.kind {
	case knownRepositoryCandidate:
		p.setMark(candidate.repository, !p.isMarked(candidate.repository))
		p.diagnostic = ""
	case lookupRepositoryCandidate:
		return m.startRepositoryLookup(candidate.repository, true)
	}
	return nil
}

// editWatchlist marks the repositories of the watchlist under the cursor so
// they can be changed and saved again.
func (m *model) editWatchlist() {
	p := m.picker
	watchlist, ok := p.findWatchlist(p.selectedCandidate().watchlist)
	if !ok {
		return
	}
	p.marked = nil
	for _, repository := range watchlist.Repositories {
		p.setMark(repository, true)
	}
	p.editing = watchlist.Name
	p.diagnostic = ""
	p.repositories = sortedRepositoryNames(append(p.repositories, watchlist.Repositories...))
	p.setQuery("")
	p.clamp(m.pickerViewportHeight())
}

// deleteWatchlist deletes the watchlist under the cursor on the second
// press. Deleting the watchlist on screen shows All repositories, which the
// store saves.
func (m *model) deleteWatchlist() {
	p := m.picker
	name := p.selectedCandidate().watchlist
	if !strings.EqualFold(p.confirmDelete, name) {
		p.confirmDelete = name
		p.diagnostic = "Press ctrl+d again to delete " + name + "."
		return
	}
	p.confirmDelete = ""
	if err := m.preferences.DeleteWatchlist(m.snapshot.Login, name); err != nil {
		p.diagnostic = "Watchlist not deleted: " + err.Error()
		return
	}
	p.watchlists = m.preferences.Watchlists(m.snapshot.Login)
	if strings.EqualFold(p.editing, name) {
		p.editing = ""
	}
	if strings.EqualFold(m.watchlist.Name, name) {
		m.applyScope(preferences.Scope{})
		m.preferenceErr = nil
	}
	p.diagnostic = "Deleted " + name + "."
	p.rebuildCandidates()
	p.clamp(m.pickerViewportHeight())
}

// openWatchlistName asks for the name of the marked repositories'
// watchlist, starting from the name of the one being edited.
func (m *model) openWatchlistName() tea.Cmd {
	p := m.picker
	input := textinput.New()
	input.Prompt = "Name: "
	input.Placeholder = "e.g. My services"
	input.CharLimit = preferences.MaxWatchlistName
	// Terminal paste arrives as tea.PasteMsg; skip the clipboard helper.
	input.KeyMap.Paste.SetEnabled(false)
	styles := textinput.DefaultStyles(m.darkBackground)
	styles.Cursor.Blink = false
	input.SetStyles(styles)
	input.SetWidth(max(1, m.width-ansi.StringWidth(input.Prompt)-1))
	input.SetValue(p.editing)
	input.CursorEnd()
	p.input.Blur()
	p.naming = &input
	p.confirmReplace = ""
	p.diagnostic = ""
	return p.naming.Focus()
}

// updateWatchlistName edits the name. Enter saves the watchlist and shows
// it; Esc goes back to the marks.
func (m *model) updateWatchlistName(msg tea.KeyPressMsg) tea.Cmd {
	p := m.picker
	k := m.keys.WatchlistName
	switch {
	case key.Matches(msg, k.Cancel):
		p.naming = nil
		p.confirmReplace, p.diagnostic = "", ""
		return p.input.Focus()
	case key.Matches(msg, k.Clear):
		p.naming.SetValue("")
		p.confirmReplace = ""
		return nil
	case key.Matches(msg, k.Apply):
		m.saveWatchlist()
		return nil
	}
	before := p.naming.Value()
	var cmd tea.Cmd
	*p.naming, cmd = p.naming.Update(msg)
	if p.naming.Value() != before {
		p.confirmReplace, p.diagnostic = "", ""
	}
	return cmd
}

// saveWatchlist saves the marked repositories under the typed name. Taking
// the name of another watchlist needs a second Enter. A failed save keeps
// the name input open with the error.
func (m *model) saveWatchlist() {
	p := m.picker
	name := strings.TrimSpace(p.naming.Value())
	if !preferences.ValidWatchlistName(name) {
		p.diagnostic = "Type a name of up to 40 characters."
		return
	}
	if other, exists := p.findWatchlist(name); exists && !strings.EqualFold(other.Name, p.editing) && !strings.EqualFold(p.confirmReplace, name) {
		p.confirmReplace = name
		p.diagnostic = "A watchlist named " + other.Name + " exists; enter replaces it."
		return
	}
	watchlist := preferences.Watchlist{Name: name, Repositories: p.markedRepositories()}
	if err := m.preferences.SaveWatchlist(m.snapshot.Login, p.editing, watchlist); err != nil {
		p.diagnostic = "Watchlist not saved: " + err.Error()
		return
	}
	m.closeRepositoryPicker()
	m.chooseScope(preferences.Scope{Watchlist: name})
}
