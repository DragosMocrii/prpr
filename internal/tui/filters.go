package tui

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

// quickFilter narrows both lists to one kind of pull request. At most one is
// active.
type quickFilter int

const (
	quickNone quickFilter = iota
	quickFailing
	quickReady
)

func (q quickFilter) label() string {
	switch q {
	case quickFailing:
		return "failing CI"
	case quickReady:
		return "ready to merge"
	default:
		return ""
	}
}

// filtersActive reports whether a search or quick filter hides rows.
func (m *model) filtersActive() bool {
	return strings.TrimSpace(m.search) != "" || m.quick != quickNone || m.category != 0
}

// filterText names the active search and quick filter for the title line.
func (m *model) filterText() string {
	var parts []string
	if search := strings.TrimSpace(m.search); search != "" {
		parts = append(parts, "search "+strconv.Quote(singleLine(search)))
	}
	if m.quick != quickNone {
		parts = append(parts, m.quick.label())
	}
	if m.category != 0 {
		parts = append(parts, attentionCategories[m.category-1].label)
	}
	if m.showDrafts {
		parts = append(parts, "drafts shown")
	}
	return strings.Join(parts, " · ")
}

// shown reports whether a pull request in pane id passes the repository
// scope, the search, the quick filter, and the attention category. A preview
// row's CI and merge state are unknown, so it never matches the failing or
// ready filters.
func (m *model) shown(id paneID, pr *github.PullRequest, preview bool) bool {
	if !m.inScope(pr) || !m.inPane(id, pr) {
		return false
	}
	if (m.quick != quickNone || m.category != 0) && !paneSpecs[id].quickFilters {
		return false
	}
	if m.category != 0 && !m.categoryMatches(m.category, id, pr, preview) {
		return false
	}
	switch m.quick {
	case quickFailing:
		if preview || !checksFailing(pr.Checks) {
			return false
		}
	case quickReady:
		if preview || !m.readyIn(id, pr) {
			return false
		}
	}
	return searchMatches(pr, m.search)
}

// searchMatches reports whether every word of the search appears, ignoring
// case, in the title, repository, author, or #number.
func searchMatches(pr *github.PullRequest, search string) bool {
	words := strings.Fields(strings.ToLower(search))
	if len(words) == 0 {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		pr.Title, pr.Repository, pr.Author, "#" + strconv.Itoa(pr.Number),
	}, "\n"))
	for _, word := range words {
		if !strings.Contains(haystack, word) {
			return false
		}
	}
	return true
}

// applyFilters rebuilds both lists, keeping each pane's selection when its
// pull request is still shown.
func (m *model) applyFilters() {
	m.keepingSelection(m.rebuildVisiblePRs)
}

func (m *model) toggleQuick(q quickFilter) {
	if m.quick == q {
		m.quick = quickNone
	} else {
		m.quick = q
		m.category = 0
	}
	m.applyFilters()
}

// toggleDrafts shows or hides draft pull requests and saves the choice. A
// failed save keeps the new choice for the session and shows the warning.
func (m *model) toggleDrafts() {
	m.showDrafts = !m.showDrafts
	m.settingSaved(m.preferences.SaveShowDrafts(m.showDrafts))
	m.applyFilters()
}

// hiddenDrafts counts the drafts in a pane's repository scope that are
// hidden.
func (m *model) hiddenDrafts(id paneID) int {
	if m.showDrafts {
		return 0
	}
	count := 0
	m.eachSourcePR(id, func(pr *github.PullRequest) {
		if pr.Draft && m.inRepositoryScope(pr) && m.inPane(id, pr) {
			count++
		}
	})
	return count
}

func (m *model) clearFilters() {
	m.search = ""
	m.quick = quickNone
	m.category = 0
	m.applyFilters()
}

// openSearch starts editing the search; the input replaces the status line.
func (m *model) openSearch() tea.Cmd {
	input := textinput.New()
	input.Prompt = "/ "
	input.Placeholder = "title, repository, author, or #number"
	// Terminal paste arrives as tea.PasteMsg; skip the clipboard helper.
	input.KeyMap.Paste.SetEnabled(false)
	input.SetValue(m.search)
	input.CursorEnd()
	m.searchBefore = m.search
	m.searching = &input
	m.setSearchStyle()
	return m.searching.Focus()
}

func (m *model) setSearchStyle() {
	if m.searching == nil {
		return
	}
	styles := textinput.DefaultStyles(m.darkBackground)
	styles.Cursor.Blink = false
	m.searching.SetStyles(styles)
	// Leave a column for the cursor cell after the prompt and text.
	m.searching.SetWidth(max(1, m.width-ansi.StringWidth(m.searching.Prompt)-1))
}

// updateSearch edits the search, filtering both lists as it changes. Enter
// keeps it; Esc restores the search from before editing.
func (m *model) updateSearch(msg tea.Msg) tea.Cmd {
	k := m.keys.SearchInput
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(msg, k.Apply):
			m.searching = nil
			m.search = strings.TrimSpace(m.search)
			return nil
		case key.Matches(msg, k.Cancel):
			m.searching = nil
			m.search = m.searchBefore
			m.applyFilters()
			return nil
		case key.Matches(msg, k.Clear):
			m.searching.SetValue("")
			m.search = ""
			m.applyFilters()
			return nil
		}
	}
	if paste, ok := msg.(tea.PasteMsg); ok {
		msg = tea.PasteMsg{Content: singleLine(paste.Content)}
	}
	var cmd tea.Cmd
	*m.searching, cmd = m.searching.Update(msg)
	if value := m.searching.Value(); value != m.search {
		m.search = value
		m.applyFilters()
	}
	return cmd
}
