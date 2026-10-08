package tui

import (
	"fmt"
	"slices"
	"strconv"
	"time"

	"charm.land/bubbles/v2/table"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

// columnSpec is one table column: how it is titled, sized, and filled.
type columnSpec struct {
	title func(ic *iconSet) string
	width func(c *columnContext) int
	text  func(r *rowContext) string
	// changed is the change bits that underline the cell; 0 never.
	changed changedCells
	// name is the PR name column, which takes the width the others leave.
	name bool
	// present reports whether the column is drawn at all; nil means always.
	present func(c *columnContext) bool
	// rank 0 is always kept; ranked columns are kept in rank order while
	// the name keeps minStatsNameWidth, and drawn in list order.
	rank int
	// preview columns are unknown in a preview and drawn as pendingText.
	preview bool
	// repository marks the Repository column, whose absence moves the
	// repository into the name.
	repository bool
}

// columnContext is what sizing a pane's columns needs.
type columnContext struct {
	m     *model
	id    paneID
	ic    *iconSet
	width int
	// Longest values among the pane's rows.
	number, repository, author, mergedBy int
	shared                               sharedRepositories
}

// rowContext is what filling one row's cells needs.
type rowContext struct {
	m        *model
	pr       *github.PullRequest
	gone     bool
	preview  bool
	now      time.Time
	ic       *iconSet
	layout   *tableLayout
	mark     rowMark
	lines    []changeLine
	markCell string
	name     string
}

// tableLayout is a pane's columns for the current width and scope.
type tableLayout struct {
	columns []columnSpec
	widths  []int
	fits    bool
	// shared is what every row has in common, which the tables leave out.
	shared sharedRepositories
	// repository reports whether the Repository column is drawn.
	repository bool
	// nudge reports whether the mark column is wide enough for a nudge icon.
	nudge bool
}

// plainTitle is a column title drawn as words in both icon sets.
func plainTitle(text string) func(*iconSet) string { return func(*iconSet) string { return text } }

// iconTitle is a column title the Nerd set draws as an icon.
func iconTitle(text string) func(*iconSet) string {
	return func(ic *iconSet) string { return ic.header(text) }
}
func fixedWidth(w int) func(*columnContext) int { return func(*columnContext) int { return w } }

// iconWidth is w, or nerd with the Nerd set, whose header is one icon.
func iconWidth(w, nerd int) func(*columnContext) int {
	return func(c *columnContext) int {
		if c.ic.nerd {
			return nerd
		}
		return w
	}
}

var (
	markColumn = columnSpec{
		title: plainTitle(""),
		width: func(c *columnContext) int {
			if c.id == paneReview && c.m.nudgeShown() {
				return nudgeWidth
			}
			return 1
		},
		text: func(r *rowContext) string {
			if r.layout.nudge {
				return r.m.nudgeMark(r.pr, r.gone, r.markCell)
			}
			return r.markCell
		},
	}
	repositoryColumn = columnSpec{
		title: plainTitle("Repository"), repository: true,
		width: func(c *columnContext) int {
			return min(28, max(12, c.width/4), max(len("Repository"), c.repository))
		},
		text: func(r *rowContext) string { return r.layout.shared.repositoryText(r.pr) },
		// One repository shared by every row is named in the title instead.
		present: func(c *columnContext) bool {
			return c.m.selectedRepository == "" && c.shared.repository == "" && c.width >= 80
		},
	}
	numberColumn = columnSpec{
		title: plainTitle("Number"),
		width: func(c *columnContext) int { return c.number },
		text:  func(r *rowContext) string { return prNumberLink(r.pr.Number, r.pr.URL) },
	}
)

// nameColumn is the PR name, underlined by changed.
func nameColumn(changed changedCells) columnSpec {
	return columnSpec{title: plainTitle("PR name"), name: true, changed: changed,
		text: func(r *rowContext) string { return r.name }}
}

// The statistics columns, in drawn order, ranked as My PRs
// drops them: Size first, Age last.
var (
	ageColumn = columnSpec{title: iconTitle("Age"), width: iconWidth(4, 4), rank: 1, preview: true,
		text: func(r *rowContext) string { return ageText(r.pr.WaitingSince, r.now) }}
	botsColumn = columnSpec{title: iconTitle("Bots"), width: iconWidth(4, 4), rank: 2, preview: true, changed: cellBots,
		text:    func(r *rowContext) string { return botsText(r.ic, r.pr.Bots) },
		present: func(c *columnContext) bool { return c.m.bots }}
	ciColumn = columnSpec{title: iconTitle("CI"), width: iconWidth(2, 1), rank: 3, preview: true, changed: cellCI,
		text: func(r *rowContext) string { return checksIcon(r.ic, r.pr.Checks) }}
	reviewColumn = columnSpec{title: iconTitle("Review"), width: iconWidth(6, 4), rank: 4, preview: true, changed: cellReview,
		text: func(r *rowContext) string { return reviewText(r.ic, r.pr.ReviewDecision, r.pr.Approvals) }}
	commentsColumn = columnSpec{title: iconTitle("Comments"), width: iconWidth(8, 4), rank: 5, preview: true, changed: cellComments,
		text: func(r *rowContext) string { return strconv.Itoa(r.pr.Comments) }}
	sizeColumn = columnSpec{title: iconTitle("Size"), width: iconWidth(11, 11), rank: 6, changed: cellSize,
		text: func(r *rowContext) string { return sizeText(r.pr.Additions, r.pr.Deletions) }}
)

// ranked is column with another rank.
func ranked(column columnSpec, rank int) columnSpec {
	column.rank = rank
	return column
}

// layoutColumns chooses and sizes a pane's columns for the current width.
func (m *model) layoutColumns(id paneID) tableLayout {
	pane := &m.panes[id]
	c := &columnContext{m: m, id: id, ic: m.icons, width: max(1, m.width), number: 6, shared: m.sharedRows}
	for row := range rowCount(pane) {
		if pr, _, ok := m.paneRow(id, row); ok {
			c.number = max(c.number, ansi.StringWidth(fmt.Sprintf("#%d", pr.Number)))
			c.repository = max(c.repository, ansi.StringWidth(c.shared.repositoryText(pr)))
			c.author = max(c.author, ansi.StringWidth(singleLine(pr.Author)))
			c.mergedBy = max(c.mergedBy, ansi.StringWidth(singleLine(pr.MergedBy)))
		}
	}
	var present []columnSpec
	for _, column := range paneSpecs[id].columns {
		if column.present == nil || column.present(c) {
			present = append(present, column)
		}
	}
	widthOf := func(column columnSpec) int {
		if column.name {
			return 0
		}
		return column.width(c)
	}
	remaining := func(columns []columnSpec) int {
		widths := make([]table.Column, len(columns))
		for i, column := range columns {
			widths[i].Width = widthOf(column)
		}
		return remainingWidth(c.width, widths)
	}
	// Ranked columns are kept in rank order while the name keeps room.
	kept := make(map[int]bool)
	var chosen []columnSpec
	for i, column := range present {
		if column.rank == 0 {
			kept[i] = true
			chosen = append(chosen, column)
		}
	}
	order := make([]int, 0, len(present))
	for i, column := range present {
		if column.rank > 0 {
			order = append(order, i)
		}
	}
	slices.SortStableFunc(order, func(a, b int) int { return present[a].rank - present[b].rank })
	for _, i := range order {
		candidate := append(slices.Clone(chosen), present[i])
		if remaining(candidate) < minStatsNameWidth {
			break
		}
		kept[i], chosen = true, candidate
	}
	layout := tableLayout{shared: c.shared}
	for i, column := range present {
		if !kept[i] {
			continue
		}
		layout.columns = append(layout.columns, column)
		layout.widths = append(layout.widths, widthOf(column))
		layout.repository = layout.repository || column.repository
	}
	nameWidth := remaining(layout.columns)
	layout.fits = nameWidth >= 8
	for i, column := range layout.columns {
		if column.name {
			layout.widths[i] = max(8, nameWidth)
		}
	}
	// The mark column, first in every pane, widens only for a nudge icon.
	layout.nudge = layout.widths[0] == nudgeWidth
	return layout
}

// tableColumns are the layout's Bubbles columns.
func (l tableLayout) tableColumns(ic *iconSet) []table.Column {
	columns := make([]table.Column, len(l.columns))
	for i, column := range l.columns {
		columns[i] = table.Column{Title: column.title(ic), Width: l.widths[i]}
	}
	return columns
}

// buildRows fills a pane's rows: its visible pull requests, then gone ones.
func (m *model) buildRows(id paneID, layout tableLayout) []table.Row {
	pane := &m.panes[id]
	spec := &paneSpecs[id]
	now := m.now()
	rows := make([]table.Row, 0, rowCount(pane))
	for row := range rowCount(pane) {
		pr, gone, ok := m.paneRow(id, row)
		if !ok {
			continue
		}
		r := &rowContext{m: m, pr: pr, gone: gone, now: now, ic: m.icons, layout: &layout,
			preview: m.snapshot.Preview && !gone, mark: m.rowMark(id, pr)}
		if !gone {
			r.lines = m.changeLines(id, pr)
		}
		r.markCell = markText(r.mark.kind, rowDirection(r.lines), gone)
		r.name = m.nameText(spec, r)
		cells := make(table.Row, len(layout.columns))
		for i, column := range layout.columns {
			// Unknown in a preview: pending, never underlined.
			if r.preview && column.preview {
				cells[i] = pendingText
				continue
			}
			text := column.text(r)
			if column.changed != 0 && r.mark.kind == markChanged && r.mark.cells&column.changed != 0 {
				text = restyle(text, underlineOn(cellDirection(r.lines, column.changed)), underlineOff)
			}
			cells[i] = text
		}
		if spec.rowStyle != nil {
			if on, off := spec.rowStyle(r); on != "" {
				for i := 1; i < len(cells); i++ {
					cells[i] = restyle(cells[i], on, off)
				}
			}
		}
		rows = append(rows, cells)
	}
	return rows
}

// nameText is a row's PR name with its tags, by the pane's flags.
func (m *model) nameText(spec *paneSpec, r *rowContext) string {
	ic, pr := r.ic, r.pr
	name := singleLine(pr.Title)
	if m.selectedRepository == "" && r.layout.shared.repository == "" && !r.layout.repository {
		name = r.layout.shared.repositoryText(pr) + " — " + name
	}
	// A review row back in draft already says so in its status tag.
	if pr.Draft && pr.ReviewStatus != github.ReviewBackInDraft {
		name = ic.draftTag() + nameTagSeparator(ic) + name
	}
	if spec.statusTag {
		if status := reviewStatusTag(ic, pr.ReviewStatus, m.waiting(pr)); status != "" && ic.nerd {
			name = status + " " + name
		} else if status != "" {
			name = status + " · " + name
		}
	}
	if spec.queueTag {
		if tag := removedQueueTag(ic, queueState(pr)); tag != "" && ic.nerd {
			name = tag + " " + name
		} else if tag != "" {
			name = tag + " · " + name
		}
	}
	if spec.wokeTag {
		if reason, ok := m.woke[keyOf(pr)]; ok {
			name = wokeTag(ic, singleLine(reason)) + " · " + name
		}
	}
	return name
}
