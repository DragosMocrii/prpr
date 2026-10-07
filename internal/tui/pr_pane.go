package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/paginator"
	"charm.land/bubbles/v2/table"
	"charm.land/lipgloss/v2"

	"github.com/DragosMocrii/prpr/internal/github"
)

type paneID int

const (
	paneMine paneID = iota
	paneReview
	// paneQueue shows authored pull requests a merge queue holds. It shares
	// the authored list and its change tracker with paneMine.
	paneQueue
	// paneSnoozed shows snoozed pull requests of both lists. Each row keeps
	// its own list's change tracker.
	paneSnoozed
	// paneMerged shows the viewer's most recently merged pull requests, from
	// their own list and change tracker.
	paneMerged
)

// paneIDs is the drawing order.
var paneIDs = [...]paneID{paneMine, paneQueue, paneReview, paneSnoozed, paneMerged}

// listID names a fetched list, each with its change tracker. Panes draw
// from lists; the Snoozed pane draws from both open lists.
type listID int

const (
	listAuthored listID = iota
	listReview
	listMerged
	listCount
)

// openLists name the two fetched lists of open pull requests.
var openLists = [...]listID{listAuthored, listReview}

// allLists are every list with a change tracker: the open lists and the
// merged list.
var allLists = [...]listID{listAuthored, listReview, listMerged}

// queued reports whether a merge queue holds an authored pull request.
func queued(pr *github.PullRequest) bool {
	return pr.Queue != nil && pr.Queue.State.InQueue()
}

// inPane reports whether a pull request of the pane's list belongs to it:
// a snoozed pull request belongs only to the Snoozed pane, and the rest of
// the authored list splits between My PRs and the queue pane.
func (m *model) inPane(id paneID, pr *github.PullRequest) bool {
	if id == paneMerged {
		return true
	}
	if m.snoozed(pr) {
		return id == paneSnoozed
	}
	switch id {
	case paneMine:
		return !queued(pr)
	case paneQueue:
		return queued(pr)
	case paneSnoozed:
		return false
	}
	return true
}

// tracker is the change tracker of a pane's list. The Snoozed pane has no
// list of its own: use trackerFor.
func (m *model) tracker(id paneID) *paneChanges {
	return &m.changes[paneSpecs[id].tracker]
}

// trackerFor is the change tracker of a pull request in pane id: for the
// Snoozed pane, the tracker of the list it was snoozed from.
func (m *model) trackerFor(id paneID, pr *github.PullRequest) *paneChanges {
	if paneSpecs[id].perRowTracker {
		return &m.changes[m.snoozeList(pr)]
	}
	return m.tracker(id)
}

// rowMark is a row's change mark: its tracker's, and changed in the name
// while it is woken. Woken marks live outside the trackers, so a tracker
// reset on an account's first fetch does not erase them.
func (m *model) rowMark(id paneID, pr *github.PullRequest) rowMark {
	mark := m.trackerFor(id, pr).mark(pr)
	if _, ok := m.woke[keyOf(pr)]; ok {
		mark.kind = max(mark.kind, markChanged)
		mark.cells |= cellName
	}
	return mark
}

// splitIndex maps an index that runs over the authored list and then the
// review list to its list and the index there.
func splitIndex(index, authored int) (listID, int) {
	if index < authored {
		return listAuthored, index
	}
	return listReview, index - authored
}

// eachSourcePR calls fn with every pull request of the lists pane id
// draws from: both for the Snoozed pane.
func (m *model) eachSourcePR(id paneID, fn func(*github.PullRequest)) {
	for _, list := range paneSpecs[id].lists {
		source := m.source(list)
		for i := range source {
			fn(&source[i])
		}
	}
}

// drawnPanes lists the panes on screen in order: the queue and Snoozed
// panes only while they have rows, and the Merged pane only while it has
// rows and room (see mergedRoom).
func (m *model) drawnPanes() []paneID {
	ids := m.openPanes()
	if rowCount(&m.panes[paneMerged]) == 0 {
		return ids
	}
	// Collapsed, the Merged pane is a title line, which the other panes
	// give up while they keep their smallest tables.
	if m.collapsed[paneMerged] && m.height-m.listChromeHeight()-1 >= m.openPanesNeed(ids) ||
		!m.collapsed[paneMerged] && m.mergedRoom() >= minDualTableHeight {
		ids = append(ids, paneMerged)
	}
	return ids
}

// openPanesNeed is the fewest lines the panes of open pull requests take
// side by side: a title and the smallest table each, or a title and an
// empty line.
func (m *model) openPanesNeed(ids []paneID) int {
	total := 0
	for _, id := range ids {
		if rowCount(&m.panes[id]) > 0 {
			total += 1 + minDualTableHeight
		} else {
			total += 2
		}
	}
	return total
}

// focusPanes lists the drawn panes that can take the focus: all but a
// collapsed one.
func (m *model) focusPanes() []paneID {
	return slices.DeleteFunc(m.drawnPanes(), func(id paneID) bool { return m.collapsed[id] })
}

// openPanes lists the drawn panes of open pull requests: every drawn pane
// but Merged.
func (m *model) openPanes() []paneID {
	ids := make([]paneID, 0, len(paneIDs))
	for _, id := range paneIDs {
		spec := &paneSpecs[id]
		if spec.leftover || spec.onlyWithRows && rowCount(&m.panes[id]) == 0 {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// mergedRoom is how many lines the Merged pane's table may take: what the
// list leaves once the panes of open pull requests have a title and every
// row each, and the Merged pane its title. Merged pull requests need
// nothing of the viewer, so they never squeeze the other lists or switch
// them to one pane at a time.
func (m *model) mergedRoom() int {
	room := m.height - m.listChromeHeight() - 1
	for _, id := range m.openPanes() {
		room-- // title
		if rows := rowCount(&m.panes[id]); rows > 0 {
			room -= max(minDualTableHeight, tableHeaderLen+rows)
		} else {
			room-- // empty line
		}
	}
	return room
}

// nextPane is the drawn pane step places after the focused one, wrapping.
func (m *model) nextPane(step int) paneID {
	drawn := m.focusPanes()
	at := slices.Index(drawn, m.focus)
	if at < 0 {
		return drawn[0]
	}
	return drawn[(at+step+len(drawn))%len(drawn)]
}

// prPane is one pull-request list. visible holds indices into the pane's
// source slice, so a row is identified by its visible index, never by number.
// Rows after the visible ones are gone pull requests: gone holds indices into
// the pane's changes.gone.
type prPane struct {
	visible []int
	gone    []int
	table   table.Model
	pages   paginator.Model
	fits    bool
}

func newPRPane() prPane {
	pages := paginator.New()
	pages.Type = paginator.Dots
	return prPane{pages: pages}
}

// rowCount counts a pane's rows: visible pull requests, then gone ones.
func rowCount(pane *prPane) int {
	return len(pane.visible) + len(pane.gone)
}

// minTableHeight is a table header (title and border) plus one row. It is the
// smallest table in single-pane mode.
const minTableHeight = 3

// minDualTableHeight is a table header plus two rows, the smallest table when
// several panes are drawn.
const minDualTableHeight = 4

// paneLayout gives each pane's table height, including its header. A zero
// height means the pane is empty or not drawn.
type paneLayout struct {
	single bool // only the focused pane is drawn
	tables [len(paneIDs)]int
}

// listChromeHeight counts list lines outside the panes: the title, the
// summary and footer rule when shown, the legend when open, the selected
// URL, the status line, and the help.
func (m *model) listChromeHeight() int {
	return m.listChromeBase() + len(m.legendLines())
}

// listChromeBase is listChromeHeight without the legend, which takes only
// what is left.
func (m *model) listChromeBase() int {
	height := 4
	if m.summaryShown() {
		height++
	}
	if m.footerRuleShown() {
		height++
	}
	return height
}

// footerRuleShown reports whether a rule sets the footer apart; like the
// summary, it is dropped to leave rows for the lists on short terminals.
func (m *model) footerRuleShown() bool {
	return m.height >= minSummaryHeight
}

func (m *model) layoutPanes() paneLayout {
	avail := m.height - m.listChromeHeight()
	switch {
	case !slices.Contains(m.drawnPanes(), paneMerged):
		return m.layoutOpenPanes(avail)
	case m.collapsed[paneMerged]:
		return m.layoutOpenPanes(avail - 1)
	}
	// Merged takes its rows, up to what the other panes leave; they share
	// the rest.
	merged := m.layoutMerged()
	layout := m.layoutOpenPanes(avail - 1 - merged)
	layout.tables[paneMerged] = merged
	return layout
}

// layoutMerged is the Merged pane's table height when it is drawn.
func (m *model) layoutMerged() int {
	return min(tableHeaderLen+rowCount(&m.panes[paneMerged]), m.mergedRoom())
}

// layoutOpenPanes lays out the panes of open pull requests in avail lines.
func (m *model) layoutOpenPanes(avail int) paneLayout {
	filled := func(id paneID) bool { return rowCount(&m.panes[id]) > 0 }
	drawn := m.openPanes()
	total := m.openPanesNeed(drawn)
	var full []paneID
	for _, id := range drawn {
		if filled(id) {
			full = append(full, id)
		}
	}
	var layout paneLayout
	if avail < total {
		layout.single = true
		if filled(m.focus) {
			layout.tables[m.focus] = max(minTableHeight, avail-1)
		}
		return layout
	}
	// Pane titles, then an empty line for each pane without rows; filled
	// panes share the rest, earlier panes taking the remainder.
	rows := avail - len(drawn) - (len(drawn) - len(full))
	// The queue and Snoozed panes are usually short: beside other filled
	// panes, each table takes no more than its rows, and they share what it
	// leaves.
	for _, short := range paneIDs {
		if !paneSpecs[short].short {
			continue
		}
		if len(full) > 1 && filled(short) {
			fit := max(minDualTableHeight, tableHeaderLen+rowCount(&m.panes[short]))
			layout.tables[short] = min(fit, rows/len(full))
			rows -= layout.tables[short]
			full = slices.DeleteFunc(full, func(id paneID) bool { return id == short })
		}
	}
	for i, id := range full {
		layout.tables[id] = rows / len(full)
		if i < rows%len(full) {
			layout.tables[id]++
		}
	}
	return layout
}

// source is the pull requests of a fetched list.
func (m *model) source(list listID) []github.PullRequest {
	switch list {
	case listReview:
		return m.snapshot.ReviewRequests
	case listMerged:
		return m.snapshot.Merged
	}
	return m.snapshot.PullRequests
}

// paneSource is the list a pane draws from; nil for a pane drawing from
// several (see paneRow).
func (m *model) paneSource(id paneID) []github.PullRequest {
	if lists := paneSpecs[id].lists; len(lists) == 1 {
		return m.source(lists[0])
	}
	return nil
}

func (m *model) focused() *prPane {
	return &m.panes[m.focus]
}

// paneRow maps a pane row to its pull request and reports whether that pull
// request is gone.
func (m *model) paneRow(id paneID, row int) (*github.PullRequest, bool, bool) {
	pane := &m.panes[id]
	if row < 0 {
		return nil, false, false
	}
	if row < len(pane.visible) {
		index, source := pane.visible[row], m.paneSource(id)
		if len(paneSpecs[id].lists) > 1 {
			var list listID
			list, index = splitIndex(index, len(m.snapshot.PullRequests))
			source = m.source(list)
		}
		if index >= 0 && index < len(source) {
			return &source[index], false, true
		}
		return nil, false, false
	}
	row -= len(pane.visible)
	if row >= len(pane.gone) {
		return nil, false, false
	}
	index, gone := pane.gone[row], m.changes[listAuthored].gone
	if id == paneSnoozed {
		var list listID
		list, index = splitIndex(index, len(gone))
		gone = m.changes[list].gone
	} else {
		gone = m.tracker(id).gone
	}
	if index >= 0 && index < len(gone) {
		return &gone[index], true, true
	}
	return nil, false, false
}

func (m *model) paneSelectedPR(id paneID) (*github.PullRequest, bool) {
	pr, _, ok := m.paneRow(id, m.panes[id].table.Cursor())
	return pr, ok
}

func (m *model) selectedPR() (*github.PullRequest, bool) {
	return m.paneSelectedPR(m.focus)
}

// selectPR moves a pane's cursor to the row for repository and number, if
// that pull request is still in the pane, as a visible or gone row, and
// reports whether it is.
func (m *model) selectPR(id paneID, repository string, number int) bool {
	pane := &m.panes[id]
	for row := range rowCount(pane) {
		pr, _, ok := m.paneRow(id, row)
		if ok && pr.Number == number && strings.EqualFold(pr.Repository, repository) {
			moveCursor(&pane.table, row)
			m.syncPages(id)
			return true
		}
	}
	return false
}

// syncPages derives a pane's page indicator from its table's visible rows and
// cursor. The table still owns scrolling; pages only report position.
func (m *model) syncPages(id paneID) {
	pane := &m.panes[id]
	pane.pages.PerPage = max(1, pane.table.Height())
	pane.pages.TotalPages = 1
	pane.pages.SetTotalPages(rowCount(pane))
	pane.pages.Page = min(pane.table.Cursor()/pane.pages.PerPage, pane.pages.TotalPages-1)
}

func (m *model) pageIndicator() string {
	pages := m.focused().pages
	if pages.TotalPages > 12 {
		pages.Type = paginator.Arabic
		pages.ArabicFormat = "page %d/%d"
	}
	return pages.View()
}

// applyFocusStyles highlights the selected row only in the focused pane and
// lets only that table take key input. Styles are swapped in place so each
// table keeps its scroll position.
func (m *model) applyFocusStyles() {
	for _, id := range paneIDs {
		pane := &m.panes[id]
		pane.table.SetStyles(tableStyles(id == m.focus, m.darkBackground))
		if id == m.focus {
			pane.table.Focus()
		} else {
			pane.table.Blur()
		}
	}
}

// Row backgrounds in 256-color indices: stripes are fainter than the hovered
// row, and the hovered row fainter than the selected one, on both dark and
// light terminals.
const (
	darkStripe    = 235
	darkHover     = 237
	darkSelected  = 238
	lightStripe   = 254
	lightHover    = 253
	lightSelected = 252
)

// tableHeaderLen counts the table's header lines: titles and border.
const tableHeaderLen = 2

func tableStyles(focused, dark bool) table.Styles {
	selected := lipgloss.NewStyle()
	if focused {
		color := lightSelected
		if dark {
			color = darkSelected
		}
		selected = selected.Bold(true).Background(lipgloss.ANSIColor(color))
	}
	return table.Styles{
		Header:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).Padding(0, 1).Border(lipgloss.NormalBorder(), false, false, true, false),
		Cell:     lipgloss.NewStyle().Padding(0, 1),
		Selected: selected,
	}
}

// tableLines renders a pane's table with every other row striped and the
// hovered row highlighted. Cells end
// their colors with full resets, so each row's background, the selected
// row's included, is turned back on after every reset to span the row.
func (m *model) tableLines(id paneID) []string {
	pane := &m.panes[id]
	lines := strings.Split(pane.table.View(), "\n")
	stripe, hover := lightStripe, lightHover
	if m.darkBackground {
		stripe, hover = darkStripe, darkHover
	}
	hovered := -1
	if row := m.hoveredRow(id); row >= 0 {
		hovered = tableHeaderLen + row - firstVisibleRow(pane.table)
	}
	for i := tableHeaderLen; i < len(lines) && i-tableHeaderLen < len(pane.table.Rows()); i++ {
		if background := leadingBackground(lines[i]); background != "" {
			lines[i] = keepBackground(lines[i], background)
		} else if i == hovered {
			lines[i] = keepBackground(lines[i], fmt.Sprintf("\x1b[48;5;%dm", hover))
		} else if (i-tableHeaderLen)%2 == 1 {
			lines[i] = keepBackground(lines[i], fmt.Sprintf("\x1b[48;5;%dm", stripe))
		}
	}
	return lines
}

// leadingBackground returns the line's opening SGR sequence when it sets a
// background, as the selected row's style does.
func leadingBackground(line string) string {
	if !strings.HasPrefix(line, "\x1b[") {
		return ""
	}
	end := strings.IndexByte(line, 'm')
	if end < 0 {
		return ""
	}
	for _, param := range strings.Split(line[2:end], ";") {
		if param == "48" {
			return line[:end+1]
		}
	}
	return ""
}

// keepBackground applies sgr to the whole line, restoring it after every
// reset inside the line.
func keepBackground(line, sgr string) string {
	for _, reset := range []string{"\x1b[m", "\x1b[0m", "\x1b[49m"} {
		line = strings.ReplaceAll(line, reset, reset+sgr)
	}
	line = strings.TrimSuffix(strings.TrimPrefix(line, sgr), sgr)
	if !strings.HasSuffix(line, "\x1b[m") {
		line += "\x1b[m"
	}
	return sgr + line
}

func (m *model) paneTitle(id paneID, single bool) string {
	name := paneSpecs[id].name
	title := fmt.Sprintf("%s (%d)", name, len(m.panes[id].visible))
	if m.filtersActive() {
		total := 0
		m.eachSourcePR(id, func(pr *github.PullRequest) {
			if m.inScope(pr) && m.inPane(id, pr) {
				total++
			}
		})
		title = fmt.Sprintf("%s (%d of %d)", name, len(m.panes[id].visible), total)
	}
	// Like the change summary, the waiting count is dropped when it does not
	// fit.
	if waiting := m.waitingCount(id); waiting > 0 {
		if suffix := fmt.Sprintf(" · %d waiting on others", waiting); 2+lipgloss.Width(title)+lipgloss.Width(suffix) <= m.width {
			title += suffix
		}
	}
	if hidden := m.hiddenDrafts(id); hidden > 0 {
		if suffix := " · " + plural(hidden, "draft") + " hidden"; 2+lipgloss.Width(title)+lipgloss.Width(suffix) <= m.width {
			title += suffix
		}
	}
	if single {
		title += " · tab: other list"
	}
	if m.collapsed[id] {
		title += " · collapsed, H to show"
	}
	// The summary is dropped rather than truncated so the title stays whole;
	// the title's two-cell prefix and the separator count toward its width.
	if summary := m.changeSummary(id); summary != "" && 2+lipgloss.Width(title)+3+lipgloss.Width(summary) <= m.width {
		title += " · " + summary
	}
	if id != m.focus {
		return "  " + lipgloss.NewStyle().Faint(true).Render(title)
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).Render("▸ " + title)
}

// waitingCount counts the reviewed pull requests a pane shows that wait on
// someone other than the viewer.
func (m *model) waitingCount(id paneID) int {
	if id != paneReview {
		return 0
	}
	count := 0
	for _, index := range m.panes[id].visible {
		if m.paneSource(id)[index].ReviewStatus.Waiting() {
			count++
		}
	}
	return count
}

// changeSummary counts a pane's marked rows in the current scope.
func (m *model) changeSummary(id paneID) string {
	pane := &m.panes[id]
	added, changed := 0, 0
	for row := range len(pane.visible) {
		if pr, _, ok := m.paneRow(id, row); ok {
			switch m.rowMark(id, pr).kind {
			case markNew:
				added++
			case markChanged:
				changed++
			}
		}
	}
	var parts []string
	if added > 0 {
		parts = append(parts, fmt.Sprintf("+%d new", added))
	}
	if changed > 0 {
		parts = append(parts, fmt.Sprintf("%d changed", changed))
	}
	if gone := len(pane.gone); gone > 0 {
		parts = append(parts, fmt.Sprintf("%d gone", gone))
	}
	return strings.Join(parts, " · ")
}

func (m *model) emptyPaneLine(id paneID) string {
	text := paneSpecs[id].empty(m)
	if m.filtersActive() {
		return "  No pull requests match the filters; esc clears them."
	}
	if m.watchlist.Name != "" {
		text += " in " + singleLine(m.watchlist.Name)
	} else if m.selectedRepository != "" {
		text += " in " + singleLine(m.selectedRepository)
	} else if m.owner != "" {
		text += " in " + ownerLabel(m.owner)
	}
	if hidden := m.hiddenDrafts(id); hidden == 1 {
		text += "; 1 draft hidden, D shows it"
	} else if hidden > 1 {
		text += "; " + plural(hidden, "draft") + " hidden, D shows them"
	}
	return "  " + text + "."
}

// snoozedShows reports whether the Snoozed pane shows rows of list.
func (m *model) snoozedShows(list listID) bool {
	authored := len(m.snapshot.PullRequests)
	for _, index := range m.panes[paneSnoozed].visible {
		if from, _ := splitIndex(index, authored); from == list {
			return true
		}
	}
	return false
}

// setFocus moves key input to a pane. When only the focused pane fits on
// screen, the tables are rebuilt so the newly shown pane gets the room.
func (m *model) setFocus(id paneID) {
	m.focus = id
	if m.layoutPanes().single {
		m.rebuildPRTable(false)
		return
	}
	m.applyFocusStyles()
}

// moveCursor selects row one step at a time. Bubbles' table keeps its
// viewport in sync with the cursor only for incremental moves; SetCursor and
// multi-row MoveDown/MoveUp jumps can leave the selected row off-screen.
func moveCursor(t *table.Model, row int) {
	row = min(max(row, 0), max(len(t.Rows())-1, 0))
	for t.Cursor() > row {
		t.MoveUp(1)
	}
	for t.Cursor() < row {
		t.MoveDown(1)
	}
}
