package tui

import (
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/DragosMocrii/prpr/internal/github"
)

// attentionCategory is a summary line entry: pull requests in one pane, or
// both, that share a status. Categories read existing status fields only;
// there is no score.
type attentionCategory struct {
	label string
	// short replaces label when the summary does not fit the width.
	short string
	// color is the icon's color; the icon itself is the icon set's.
	color string
	// both means the category covers both panes; otherwise pane.
	both  bool
	pane  paneID
	match func(m *model, pr *github.PullRequest) bool
}

func unknownMergeState(pr *github.PullRequest) bool {
	return pr.Mergeable != "CONFLICTING" && (pr.MergeState == "" || pr.MergeState == "UNKNOWN")
}

// attentionCategories are numbered from 1 in this order; the numbers never
// change, so an empty category leaves a gap.
var attentionCategories = []attentionCategory{
	{label: "ready to merge", short: "ready", color: "2", pane: paneMine,
		match: func(m *model, pr *github.PullRequest) bool { return m.ready(pr) }},
	{label: "changes requested", short: "changes", color: "1", pane: paneMine,
		match: func(_ *model, pr *github.PullRequest) bool { return pr.ReviewDecision == "CHANGES_REQUESTED" }},
	{label: "failing CI", short: "failing", color: "1", pane: paneMine,
		match: func(_ *model, pr *github.PullRequest) bool { return checksFailing(pr.Checks) }},
	{label: "conflicts", short: "conflicts", color: "1", pane: paneMine,
		match: func(_ *model, pr *github.PullRequest) bool {
			return pr.Mergeable == "CONFLICTING" || pr.MergeState == "DIRTY"
		}},
	{label: "bot threads", short: "bots", color: "1", pane: paneMine,
		match: func(_ *model, pr *github.PullRequest) bool {
			for _, bot := range pr.Bots {
				if bot.State == github.BotConcerns {
					return true
				}
			}
			return false
		}},
	{label: "awaiting your review", short: "to review", color: "3", pane: paneReview,
		match: func(_ *model, pr *github.PullRequest) bool { return !pr.ReviewStatus.Waiting() }},
	{label: "status unknown", short: "unknown", color: "3", both: true,
		match: func(_ *model, pr *github.PullRequest) bool { return unknownMergeState(pr) }},
}

// categoryConflicts is the one category a preview already knows.
const categoryConflicts = 4

// minSummaryHeight is the terminal height below which the summary line is
// dropped to leave rows for the lists.
const minSummaryHeight = 14

func (m *model) summaryShown() bool {
	return m.height >= minSummaryHeight
}

// covers reports whether a category limits pane id. The queue pane shares
// the authored list, so counting it too would count those rows twice; the
// Snoozed and Merged panes' rows count nowhere.
func (c attentionCategory) covers(id paneID) bool {
	if id == paneQueue || id == paneSnoozed || id == paneMerged {
		return false
	}
	return c.both || c.pane == id
}

// categoryKnown reports whether a preview can tell the category apart.
func categoryKnown(number int, preview bool) bool {
	return !preview || number == categoryConflicts
}

// categoryMatches reports whether a pull request in pane id is in category
// number (from 1). Preview rows match only categories a preview knows. A
// draft is in no category, shown or not: its author is still working on it.
func (m *model) categoryMatches(number int, id paneID, pr *github.PullRequest, preview bool) bool {
	if pr.Draft || !m.inPane(id, pr) {
		return false
	}
	category := attentionCategories[number-1]
	return category.covers(id) && categoryKnown(number, preview) && category.match(m, pr)
}

// categoryCount counts the current pull requests in category number within
// the repository scope; the search and quick filters do not change it.
func (m *model) categoryCount(number int) int {
	count := 0
	for _, id := range paneIDs {
		source := m.source(id)
		for i := range source {
			if m.inScope(&source[i]) && m.categoryMatches(number, id, &source[i], m.snapshot.Preview) {
				count++
			}
		}
	}
	return count
}

// summaryLine lists each non-empty category with its number key. The active
// category is drawn in reverse video.
func (m *model) summaryLine() string {
	counts := make([]int, len(attentionCategories))
	for i := range attentionCategories {
		if categoryKnown(i+1, m.snapshot.Preview) {
			counts[i] = m.categoryCount(i + 1)
		}
	}
	// Labels shorten one at a time, the longest first, then all go, before
	// the line is cut.
	levels := make([]int, len(attentionCategories))
	byLength := make([]int, len(attentionCategories))
	for i := range byLength {
		byLength[i] = i
	}
	slices.SortStableFunc(byLength, func(a, b int) int {
		return len(attentionCategories[b].label) - len(attentionCategories[a].label)
	})
	for _, i := range byLength {
		if line := m.summaryText(counts, levels); lipgloss.Width(line) <= m.width {
			return line
		}
		levels[i] = summaryShort
	}
	if line := m.summaryText(counts, levels); lipgloss.Width(line) <= m.width {
		return line
	}
	for i := range levels {
		levels[i] = summaryCounts
	}
	return m.summaryText(counts, levels)
}

// Summary levels of a category, from widest to narrowest.
const (
	summaryFull   = iota // full label
	summaryShort         // short label
	summaryCounts        // icon and count only
)

// summaryText draws the summary with each category at its level.
func (m *model) summaryText(counts, levels []int) string {
	key := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	bracket := lipgloss.NewStyle().Faint(true)
	var parts []string
	for i, category := range attentionCategories {
		number := i + 1
		if !categoryKnown(number, m.snapshot.Preview) {
			continue
		}
		count := counts[i]
		if count == 0 && m.category != number {
			continue
		}
		// The key is bracketed so it never reads as part of the count.
		part := bracket.Render("[") + key.Render(strconv.Itoa(number)) + bracket.Render("]") + " " +
			coloredIcon(m.icons.categories[i], category.color)
		switch {
		case levels[i] == summaryFull:
			part += " " + strconv.Itoa(count) + " " + category.label
		case levels[i] == summaryShort:
			part += " " + strconv.Itoa(count) + " " + category.short
		case m.icons.nerd:
			// A Nerd icon may draw wider than its cell, so a space follows it.
			part += " " + strconv.Itoa(count)
		default:
			part += strconv.Itoa(count)
		}
		if m.category == number {
			part = restyle(part, reverseOn, reverseOff)
		}
		parts = append(parts, part)
	}
	faint := lipgloss.NewStyle().Faint(true)
	if m.snapshot.Preview {
		parts = append(parts, faint.Render("other counts after details load"))
	} else if len(parts) == 0 {
		return faint.Render("Nothing ready, blocked, failing, waiting on your review, or unknown.")
	}
	return strings.Join(parts, " · ")
}

// toggleCategory filters to category number, focusing its pane, or clears it
// when it is already active. It replaces the quick filter.
func (m *model) toggleCategory(number int) {
	if m.category == number {
		m.category = 0
		m.applyFilters()
		return
	}
	if m.categoryCount(number) == 0 {
		return
	}
	m.category = number
	m.quick = quickNone
	m.applyFilters()
	if category := attentionCategories[number-1]; !category.both && m.focus != category.pane {
		m.setFocus(category.pane)
	}
}
