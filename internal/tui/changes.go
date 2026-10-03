package tui

import (
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

// markKind is ordered from least to most noteworthy.
type markKind int

const (
	markNone markKind = iota
	// markActivity means GitHub's updated time moved but no shown column did.
	markActivity
	// markChanged means at least one shown column changed.
	markChanged
	// markNew means the pull request joined the pane.
	markNew
)

// changedCells records which shown columns changed, one bit per column.
type changedCells uint16

const (
	cellName changedCells = 1 << iota
	cellState
	cellMerge
	cellAuthor
	cellBots
	cellCI
	cellReview
	cellComments
	cellSize
	cellQueue
)

type rowMark struct {
	kind  markKind
	cells changedCells
	// was is the pull request as the first fetch that marked the row
	// found it, so changes that pile up read from where they started. It
	// is nil for a new pull request.
	was *github.PullRequest
}

// prKey identifies a pull request across refreshes.
type prKey struct {
	repository string
	number     int
}

func keyOf(pr *github.PullRequest) prKey {
	return prKey{strings.ToLower(pr.Repository), pr.Number}
}

// paneChanges compares each successful fetch with the previous one. Marks and
// gone pull requests pile up until they are seen or cleared.
type paneChanges struct {
	baseline []github.PullRequest
	marks    map[prKey]rowMark
	// gone lists pull requests that left the pane, oldest departure first.
	gone []github.PullRequest
	// raised holds the pull requests the latest update marked or marked
	// again.
	raised map[prKey]bool
}

// reset starts over from list with nothing marked.
func (c *paneChanges) reset(list []github.PullRequest) {
	*c = paneChanges{baseline: list}
}

// update marks the differences between the baseline and list, then makes
// list the baseline.
func (c *paneChanges) update(list []github.PullRequest) {
	if c.marks == nil {
		c.marks = make(map[prKey]rowMark)
	}
	previous := make(map[prKey]*github.PullRequest, len(c.baseline))
	for i := range c.baseline {
		previous[keyOf(&c.baseline[i])] = &c.baseline[i]
	}
	current := make(map[prKey]bool, len(list))
	c.raised = make(map[prKey]bool)
	for i := range list {
		pr := &list[i]
		key := keyOf(pr)
		current[key] = true
		old, ok := previous[key]
		if !ok {
			c.gone = slices.DeleteFunc(c.gone, func(g github.PullRequest) bool { return keyOf(&g) == key })
			c.marks[key] = rowMark{kind: markNew}
			c.raised[key] = true
			continue
		}
		mark := c.marks[key]
		cells := cellChanges(old, pr)
		if cells == 0 && old.UpdatedAt.Equal(pr.UpdatedAt) {
			continue
		}
		if cells != 0 {
			mark.kind = max(mark.kind, markChanged)
			mark.cells |= cells
		} else {
			mark.kind = max(mark.kind, markActivity)
		}
		if mark.was == nil && mark.kind != markNew {
			was := *old
			mark.was = &was
		}
		c.marks[key] = mark
		c.raised[key] = true
	}
	for i := range c.baseline {
		if key := keyOf(&c.baseline[i]); !current[key] {
			delete(c.marks, key)
			c.gone = append(c.gone, c.baseline[i])
		}
	}
	c.baseline = list
}

// cellChanges compares the columns a pull request shows. Age is left out: it
// moves with the clock.
func cellChanges(old, pr *github.PullRequest) changedCells {
	var cells changedCells
	// The review status is shown before the name.
	if old.Title != pr.Title || old.ReviewStatus != pr.ReviewStatus {
		cells |= cellName
	}
	if old.Draft != pr.Draft {
		cells |= cellState
	}
	if old.Draft != pr.Draft || old.Mergeable != pr.Mergeable || old.MergeState != pr.MergeState ||
		old.ChangesRequested != pr.ChangesRequested || ruleFieldsChanged(old, pr) {
		cells |= cellMerge
	}
	if old.Author != pr.Author {
		cells |= cellAuthor
	}
	if !slices.Equal(old.Bots, pr.Bots) {
		cells |= cellBots
	}
	if old.Checks != pr.Checks {
		cells |= cellCI
	}
	if old.ReviewDecision != pr.ReviewDecision || old.Approvals != pr.Approvals {
		cells |= cellReview
	}
	if old.Comments != pr.Comments {
		cells |= cellComments
	}
	if old.Additions != pr.Additions || old.Deletions != pr.Deletions {
		cells |= cellSize
	}
	if !sameQueue(old.Queue, pr.Queue) {
		cells |= cellQueue
	}
	return cells
}

// sameQueue compares what the queue pane and removed tags show.
func sameQueue(a, b *github.QueueEntry) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.State == b.State && a.Detail == b.Detail
}

// ruleFieldsChanged compares the fields only rules read, which decide the
// Merge column with the merge state. Each is compared only when both
// fetches read it, so turning a rule on or off marks nothing.
func ruleFieldsChanged(old, pr *github.PullRequest) bool {
	return (old.CodeOwnersKnown && pr.CodeOwnersKnown && !slices.Equal(old.PendingCodeOwners, pr.PendingCodeOwners)) ||
		(old.ThreadsKnown && pr.ThreadsKnown && old.UnresolvedThreads != pr.UnresolvedThreads) ||
		(old.RequiredChecks != "" && pr.RequiredChecks != "" && old.RequiredChecks != pr.RequiredChecks)
}

// see clears the mark of a pull request that is still listed.
func (c *paneChanges) see(pr *github.PullRequest) bool {
	key := keyOf(pr)
	if _, ok := c.marks[key]; !ok {
		return false
	}
	delete(c.marks, key)
	return true
}

// dismiss drops a gone pull request.
func (c *paneChanges) dismiss(pr *github.PullRequest) {
	key := keyOf(pr)
	c.gone = slices.DeleteFunc(c.gone, func(g github.PullRequest) bool { return keyOf(&g) == key })
}

// clear drops every mark and gone pull request, keeping the baseline.
func (c *paneChanges) clear() {
	c.marks = nil
	c.gone = nil
	c.raised = nil
}

func (c *paneChanges) mark(pr *github.PullRequest) rowMark {
	return c.marks[keyOf(pr)]
}

// hasMarks reports whether any pane shows a mark or a gone row in the
// current scope.
func (m *model) hasMarks() bool {
	for _, id := range paneIDs {
		pane := &m.panes[id]
		if len(pane.gone) > 0 {
			return true
		}
		for row := range len(pane.visible) {
			if pr, _, ok := m.paneRow(id, row); ok && m.rowMark(id, pr).kind != markNone {
				return true
			}
		}
	}
	return false
}

// direction says what a change means for the viewer.
type direction int

const (
	dirNeutral direction = iota
	// dirGood is good news: something passed, was approved, or got ready.
	dirGood
	// dirBad is something to act on: a failure, a request, or a conflict.
	dirBad
)

// changeLine is one changed cell of a marked row, worded from its value
// when the row was first marked to its value now.
type changeLine struct {
	cell     changedCells
	label    string
	was, now string
	dir      direction
}

// changeCells lists the cells a change line can name, in the order the
// details screen shows them.
var changeCells = []changedCells{cellName, cellAuthor, cellState, cellMerge, cellQueue, cellCI, cellReview, cellBots, cellComments, cellSize}

// changeLines words each changed cell of a row of pane id, which decides
// whether the rules apply, and a wake from snooze.
func (m *model) changeLines(id paneID, pr *github.PullRequest) []changeLine {
	var lines []changeLine
	cell := cellName
	add := func(label, before, after string, dir direction) {
		lines = append(lines, changeLine{cell, label, before, after, dir})
	}
	if reason, ok := m.woke[keyOf(pr)]; ok && id != paneSnoozed {
		add("Snooze", "", "woke: "+singleLine(reason), dirNeutral)
	}
	mark := m.trackerFor(id, pr).mark(pr)
	if mark.kind != markChanged || mark.was == nil {
		return lines
	}
	was := mark.was
	// Someone else's pull request needs the viewer only through its review
	// status; its failures and conflicts are for its author.
	review := id == paneReview || (id == paneSnoozed && m.snoozeList(pr) == paneReview)
	for _, cell = range changeCells {
		if mark.cells&cell == 0 {
			continue
		}
		switch cell {
		case cellName:
			if was.ReviewStatus != pr.ReviewStatus {
				dir := dirNeutral
				if needsYou(pr.ReviewStatus) && !needsYou(was.ReviewStatus) ||
					pr.ReviewStatus == github.ReviewRequested && was.ReviewStatus != github.ReviewRequested {
					dir = dirBad
				}
				add("Status", reviewWord(was.ReviewStatus), reviewWord(pr.ReviewStatus), dir)
			}
			switch {
			case was.Title != pr.Title:
				add("Name", "", "renamed", dirNeutral)
			case was.ReviewStatus == pr.ReviewStatus:
				add("Name", "", "changed, then back", dirNeutral)
			}
		case cellAuthor:
			add("Author", singleLine(was.Author), singleLine(pr.Author), dirNeutral)
		case cellState:
			add("State", stateWord(was.Draft), stateWord(pr.Draft), dirNeutral)
		case cellMerge:
			before, after := m.mergeWord(id, was), m.mergeWord(id, pr)
			dir := dirNeutral
			switch {
			case before == after || after == "unknown":
			case after == "conflicts" || before == "ready":
				dir = dirBad
			case after == "ready" || before == "conflicts":
				dir = dirGood
			}
			add("Merge", before, after, dir)
		case cellQueue:
			before, after := queueWord(was.Queue), queueWord(pr.Queue)
			dir := dirNeutral
			switch state := queueState(pr); {
			case state == queueState(was):
			case state == github.QueuePassed:
				dir = dirGood
			case state == github.QueueFailing || state == github.QueueRemovedFailed || state == github.QueueRemovedCanceled:
				dir = dirBad
			}
			add("Queue", before, after, dir)
		case cellCI:
			dir := dirNeutral
			switch {
			case checksFailing(pr.Checks):
				dir = dirBad
			case pr.Checks == "SUCCESS":
				dir = dirGood
			}
			add("CI", checksWord(was.Checks), checksWord(pr.Checks), dir)
		case cellReview:
			dir := dirNeutral
			switch {
			case pr.ReviewDecision == "CHANGES_REQUESTED":
				dir = dirBad
			case pr.ReviewDecision == "APPROVED" && (was.ReviewDecision != "APPROVED" || pr.Approvals > was.Approvals):
				dir = dirGood
			}
			add("Review", reviewDecisionWord(was.ReviewDecision, was.Approvals), reviewDecisionWord(pr.ReviewDecision, pr.Approvals), dir)
		case cellBots:
			before, after := botsWorst(was.Bots), botsWorst(pr.Bots)
			dir := dirNeutral
			switch {
			case after.State >= github.BotFailed && (after.State > before.State || after.Concerns > before.Concerns):
				dir = dirBad
			case before.State >= github.BotFailed && after.State < github.BotFailed:
				dir = dirGood
			}
			add("Bots", botWord(before), botWord(after), dir)
		case cellComments:
			add("Comments", strconv.Itoa(was.Comments), strconv.Itoa(pr.Comments), dirNeutral)
		case cellSize:
			add("Size", sizeWord(was), sizeWord(pr), dirNeutral)
		}
	}
	if review {
		for i := range lines {
			if lines[i].label != "Status" && lines[i].dir == dirBad {
				lines[i].dir = dirNeutral
			}
		}
	}
	return lines
}

// cellDirection is the direction of the changes cells stands for.
func cellDirection(lines []changeLine, cells changedCells) direction {
	dir := dirNeutral
	for _, line := range lines {
		if line.cell&cells != 0 {
			dir = max(dir, line.dir)
		}
	}
	return dir
}

// rowDirection is a marked row's direction: bad when any change is, good
// when one is and none is bad.
func rowDirection(lines []changeLine) direction {
	dir := dirNeutral
	for _, line := range lines {
		dir = max(dir, line.dir)
	}
	return dir
}

// needsYou reports whether a reviewed pull request needs the viewer again.
// A pending request is not one of these: it needs the viewer only when it
// comes back.
func needsYou(status github.ReviewStatus) bool {
	return status == github.ReviewNewCommits || status == github.ReviewAuthorReplied || status == github.ReviewDismissed
}

func reviewWord(status github.ReviewStatus) string {
	if status == github.ReviewRequested {
		return "requested"
	}
	return reviewStatusText(status)
}

func stateWord(draft bool) string {
	if draft {
		return "draft"
	}
	return "open"
}

// mergeWord words the Merge column, by the rules where pane id applies them.
func (m *model) mergeWord(id paneID, pr *github.PullRequest) string {
	switch {
	case pr.Mergeable == "CONFLICTING" || pr.MergeState == "DIRTY":
		return "conflicts"
	case pr.Draft:
		return "draft"
	case unknownMergeState(pr):
		return "unknown"
	case m.readyIn(id, pr):
		return "ready"
	case mergeReady(pr.Draft, pr.Mergeable, pr.MergeState):
		return "mergeable"
	default:
		return "blocked"
	}
}

func queueWord(entry *github.QueueEntry) string {
	if entry == nil {
		return "none"
	}
	switch entry.State {
	case github.QueueSubmitted:
		return "submitted"
	case github.QueueQueued:
		return "queued"
	case github.QueueTesting:
		return "testing"
	case github.QueueFailing:
		return "failing"
	case github.QueuePassed:
		return "passed"
	case github.QueueRemovedFailed:
		return "removed: failed"
	case github.QueueRemovedCanceled:
		return "removed: canceled"
	default:
		return "unknown"
	}
}

func checksWord(state string) string {
	switch state {
	case "SUCCESS":
		return "passing"
	case "FAILURE", "ERROR":
		return "failing"
	case "PENDING", "EXPECTED":
		return "pending"
	default:
		return "none"
	}
}

func reviewDecisionWord(decision string, approvals int) string {
	word := "none"
	switch decision {
	case "APPROVED":
		word = "approved"
	case "CHANGES_REQUESTED":
		word = "changes requested"
	case "REVIEW_REQUIRED":
		word = "required"
	}
	if approvals > 0 {
		word += " (" + strconv.Itoa(approvals) + ")"
	}
	return word
}

// botsWorst is the most attention-worthy bot review, with every bot's
// concerns added up.
func botsWorst(bots []github.BotReview) github.BotReview {
	var worst github.BotReview
	concerns := 0
	for _, bot := range bots {
		if bot.State > worst.State {
			worst = bot
		}
		concerns += bot.Concerns
	}
	worst.Concerns = concerns
	return worst
}

func botWord(review github.BotReview) string {
	switch review.State {
	case github.BotConcerns:
		return plural(review.Concerns, "thread")
	case github.BotFailed:
		return "failed"
	case github.BotRunning:
		return "running"
	case github.BotStale:
		return "stale"
	case github.BotPassed:
		return "passed"
	default:
		return "not run"
	}
}

func sizeWord(pr *github.PullRequest) string {
	return "+" + strconv.Itoa(pr.Additions) + " −" + strconv.Itoa(pr.Deletions)
}

// readDelay is how long the cursor rests on a marked row before its mark
// counts as read, so rows the cursor only passes keep their marks.
const readDelay = 1500 * time.Millisecond

// restState is the row the cursor rests on. A rest is timed only from the
// viewer's input, so a row a refresh marks while nobody looks stays unread.
// read means its mark was read: the cursor stayed for readDelay, or the
// details, o, or y showed it.
type restState struct {
	pane   paneID
	key    prKey
	gone   bool
	marked bool
	timing bool
	read   bool
}

// restMsg ends a rest of readDelay; one from an older restGeneration is
// dropped.
type restMsg struct{ generation uint64 }

// resting is the row the cursor is on in the focused pane.
func (m *model) resting() (restState, bool) {
	pr, gone, ok := m.paneRow(m.focus, m.focused().table.Cursor())
	if !ok {
		return restState{}, false
	}
	marked := gone || m.rowMark(m.focus, pr).kind != markNone
	return restState{pane: m.focus, key: keyOf(pr), gone: gone, marked: marked}, true
}

// viewerInput reports whether msg is the viewer acting: a key, a click, or
// the wheel. Hover and focus reports are not.
func viewerInput(msg tea.Msg) bool {
	switch msg.(type) {
	case tea.KeyPressMsg, tea.MouseClickMsg, tea.MouseWheelMsg:
		return true
	}
	return false
}

// trackRest follows the cursor after every message: a row it comes to, or
// one that turns marked under it, starts a new rest, timed from the
// viewer's next input. Losing focus stops the timing.
func (m *model) trackRest(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(tea.BlurMsg); ok {
		m.restGeneration++
		m.rest.timing = false
	}
	at, ok := m.resting()
	if !ok {
		m.rest = restState{}
		return nil
	}
	if at.pane != m.rest.pane || at.key != m.rest.key || at.gone != m.rest.gone || at.marked != m.rest.marked {
		m.restGeneration++
		m.rest = at
	}
	if m.detailsOpen() {
		m.rest.read = true
	}
	if !m.rest.marked || m.rest.read || m.rest.timing || !viewerInput(msg) {
		return nil
	}
	m.rest.timing = true
	generation := m.restGeneration
	return tea.Tick(readDelay, func(time.Time) tea.Msg { return restMsg{generation} })
}

func (m *model) handleRest(msg restMsg) {
	if msg.generation == m.restGeneration && m.rest.timing {
		m.rest.read = true
	}
}

// markRead counts the selected row's mark as read.
func (m *model) markRead() {
	if at, ok := m.resting(); ok && at.pane == m.rest.pane && at.key == m.rest.key && at.gone == m.rest.gone {
		m.rest.read = true
	}
}

// wasRead reports whether the row a pane's cursor just left was read.
func (m *model) wasRead(id paneID, pr *github.PullRequest, gone bool) bool {
	return m.rest.read && m.rest.pane == id && m.rest.key == keyOf(pr) && m.rest.gone == gone
}

// rechanged starts the rest over when a fetch marked the row again.
func (m *model) rechanged() {
	for _, id := range listIDs {
		if m.changes[id].raised[m.rest.key] {
			m.rest = restState{}
			return
		}
	}
}

// text words a change line for the details screen. A cell that changed and
// came back shows both ends.
func (l changeLine) text() string {
	switch {
	case l.was == "":
		return l.label + ": " + l.now
	case l.was == l.now:
		return l.label + ": " + l.was + " → … → " + l.now
	default:
		return l.label + ": " + l.was + " → " + l.now
	}
}

// changedDetail is the details screen's Changed row for the selected row.
func (m *model) changedDetail(pr *github.PullRequest) []string {
	var values []string
	for _, line := range m.changeLines(m.focus, pr) {
		values = append(values, line.text())
	}
	switch m.trackerFor(m.focus, pr).mark(pr).kind {
	case markNew:
		values = append(values, "New in this list since the previous refresh")
	case markActivity:
		values = append(values, "New activity on GitHub; no column changed")
	}
	return values
}

// changeStatus sums up the selected row's changes for the status line, in
// at most width cells: the marker, then each change from its old value,
// leaving out the old values when they do not fit, then cut short.
func (m *model) changeStatus(width int) string {
	pr, gone, ok := m.paneRow(m.focus, m.focused().table.Cursor())
	if !ok || gone || width <= 0 {
		return ""
	}
	lines := m.changeLines(m.focus, pr)
	if len(lines) == 0 {
		return ""
	}
	marker := markText(markChanged, rowDirection(lines), false)
	join := func(withWas bool) string {
		parts := make([]string, len(lines))
		for i, line := range lines {
			text := strings.ToLower(line.label) + " "
			if withWas && line.was != "" {
				text += line.was + "→"
			}
			parts[i] = text + line.now
		}
		return marker + " " + strings.Join(parts, " · ")
	}
	text := join(true)
	if lipgloss.Width(text) > width {
		text = join(false)
	}
	return ansi.Truncate(text, width, "…")
}
