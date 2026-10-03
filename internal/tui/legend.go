package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

// legendSection is one row of the icon legend: a label, then each symbol
// with its meaning.
type legendSection struct {
	label string
	items []string
}

// legendItem pairs a drawn symbol with its meaning.
func legendItem(symbol, meaning string) string {
	return symbol + " " + meaning
}

// legendSections explains every symbol the list can draw in the current icon
// set, drawn by the table's own renderers so the two cannot drift apart.
// Rows that only the Nerd set needs come first there, since its column
// headers are icons too; the Unicode set draws those as words.
func (m *model) legendSections() []legendSection {
	ic := m.icons
	item := legendItem
	var sections []legendSection
	queuePane := slices.Contains(m.drawnPanes(), paneQueue)
	snoozePane := slices.Contains(m.drawnPanes(), paneSnoozed)
	if ic.nerd {
		columns := []string{"State", "Merge", "Age", "Bots", "CI", "Review", "Comments", "Size", "Queue", "Wakes"}
		var items []string
		for _, column := range columns {
			if (column == "Bots" && !m.bots) || (column == "Queue" && !queuePane) || (column == "Wakes" && !snoozePane) {
				continue
			}
			items = append(items, item(ic.header(column), column))
		}
		sections = append(sections,
			legendSection{"Columns", items},
			legendSection{"State", []string{item(ic.stateText(false), "open"), item(ic.stateText(true), "draft")}})
	}
	merge := func(mergeable, state string) string { return mergeIcon(ic, false, mergeable, state, false) }
	items := []string{item(mergeIcon(ic, false, "MERGEABLE", "CLEAN", true), "ready")}
	if m.rules.Customized() {
		items = append(items, item(merge("MERGEABLE", "CLEAN"), "GitHub would merge, your rules not yet"))
	}
	items = append(items,
		item(merge("MERGEABLE", "BLOCKED"), "blocked"),
		item(merge("MERGEABLE", "BEHIND"), "behind"),
		item(merge("CONFLICTING", "DIRTY"), "conflicts"),
		item(merge("UNKNOWN", "UNKNOWN"), "unknown"),
		item(mergeIcon(ic, true, "MERGEABLE", "BLOCKED", false), "draft"))
	sections = append(sections,
		legendSection{"Merge", items},
		legendSection{"CI", []string{
			item(checksIcon(ic, "SUCCESS"), "passing"),
			item(checksIcon(ic, "FAILURE"), "failing"),
			item(checksIcon(ic, "PENDING"), "running"),
			item(checksIcon(ic, ""), "none"),
		}},
		legendSection{"Review", []string{
			item(reviewText(ic, "APPROVED", 0), "approved"),
			item(reviewText(ic, "CHANGES_REQUESTED", 0), "changes requested"),
			item(reviewText(ic, "REVIEW_REQUIRED", 0), "review required"),
			item(reviewText(ic, "", 0), "no decision"),
			item(reviewText(ic, "APPROVED", 0)+ic.gap+"N", "N approvals"),
		}})
	if m.bots {
		bot := func(state github.BotState) string { return botStateText(ic, github.BotReview{State: state}) }
		sections = append(sections, legendSection{"Bots", []string{
			item(bot(github.BotPassed), "passed"),
			item(bot(github.BotStale), "passed before the last commit"),
			item(bot(github.BotRunning), "running"),
			item(bot(github.BotFailed), "check failed"),
			item(coloredIcon(ic.failed+ic.gap+"N", "1"), "N open threads"),
			item(bot(github.BotNotRun), "not run"),
		}})
	}
	if ic.nerd {
		var reviewed []string
		for _, status := range []github.ReviewStatus{github.ReviewNewCommits, github.ReviewAuthorReplied, github.ReviewDismissed,
			github.ReviewNewActivity, github.ReviewWaitingOnAuthor, github.ReviewApproved, github.ReviewBackInDraft} {
			reviewed = append(reviewed, item(reviewStatusTag(ic, status), reviewStatusText(status)))
		}
		sections = append(sections, legendSection{"Reviewed", reviewed})
	}
	// With queues off, or nothing queued or removed, the legend is today's.
	if queuePane || m.removedQueueShown() {
		sections = append(sections, legendSection{"Queue", []string{
			item(queueText(ic, github.QueuePassed), "passed, merging soon"),
			item(queueText(ic, github.QueueFailing), "a check failed"),
			item(queueText(ic, github.QueueTesting), "testing"),
			item(queueText(ic, github.QueueQueued), "waiting to test"),
			item(queueText(ic, github.QueueSubmitted), "submitted"),
			item(queueText(ic, github.QueueUnknown), "state not recognized"),
			item(removedQueueTag(ic, github.QueueRemovedFailed), "removed: failed"),
			item(removedQueueTag(ic, github.QueueRemovedCanceled), "removed: canceled"),
		}})
	}
	if snoozePane || m.wokeShown() {
		sections = append(sections, legendSection{"Snoozed", []string{
			item(wokeTag(ic, "reason"), "woke from a snooze"),
		}})
	}
	sections = append(sections,
		legendSection{"Marks", []string{
			item(markText(markNew, dirNeutral, false), "new"),
			item(markText(markChanged, dirGood, false), "better"),
			item(markText(markChanged, dirBad, false), "needs you"),
			item(markText(markChanged, dirNeutral, false), "changed"),
			item(markText(markActivity, dirNeutral, false), "updated"),
			item(markText(markNone, dirNeutral, true), "gone"),
		}},
		legendSection{"Other", []string{item(ageText(time.Time{}, time.Time{}), "no age (draft)"), item(pendingText, "still loading")}})
	title := []string{item(ic.star, "watchlist")}
	if ic.nerd {
		title = append(title, item(ic.pin, "pinned account"), item(ic.bell, "notifications on"))
	}
	return append(sections, legendSection{"Title", title})
}

// removedQueueShown reports whether My PRs shows a pull request tagged as
// removed from a merge queue.
func (m *model) removedQueueShown() bool {
	for _, index := range m.panes[paneMine].visible {
		if removedQueueTag(m.icons, queueState(&m.snapshot.PullRequests[index])) != "" {
			return true
		}
	}
	return false
}

// wokeShown reports whether a visible row of My PRs, Merge queue, or Review
// requested carries a woke tag.
func (m *model) wokeShown() bool {
	for _, id := range []paneID{paneMine, paneQueue, paneReview} {
		for _, index := range m.panes[id].visible {
			var pr *github.PullRequest
			if id == paneReview {
				pr = &m.snapshot.ReviewRequests[index]
			} else {
				pr = &m.snapshot.PullRequests[index]
			}
			if _, ok := m.woke[keyOf(pr)]; ok {
				return true
			}
		}
	}
	return false
}

// legendSectionLines draws each section as rows of at most width cells,
// wrapping between symbols under the first one.
func (m *model) legendSectionLines(width int) [][]string {
	sections := m.legendSections()
	labelWidth := 0
	for _, section := range sections {
		labelWidth = max(labelWidth, lipgloss.Width(section.label))
	}
	bold := lipgloss.NewStyle().Bold(true)
	indent := strings.Repeat(" ", labelWidth+2)
	drawn := make([][]string, len(sections))
	for i, section := range sections {
		line := bold.Render(section.label) + strings.Repeat(" ", labelWidth+2-lipgloss.Width(section.label))
		start := true
		for _, entry := range section.items {
			switch {
			case start:
				line += entry
			case lipgloss.Width(line)+2+lipgloss.Width(entry) <= width:
				line += "  " + entry
			default:
				drawn[i] = append(drawn[i], ansi.Truncate(line, width, "…"))
				line = indent + entry
			}
			start = false
		}
		drawn[i] = append(drawn[i], ansi.Truncate(line, width, "…"))
	}
	return drawn
}

// legendRoom is how many lines the legend may take: what the list leaves
// once the panes keep the smallest tables of their current layout, so
// opening the legend never switches the panes to one at a time.
func (m *model) legendRoom() int {
	avail := m.height - m.listChromeBase()
	need := func(id paneID) int {
		if rowCount(&m.panes[id]) > 0 {
			return 1 + minDualTableHeight
		}
		return 2
	}
	dual := 0
	for _, id := range m.drawnPanes() {
		dual += need(id)
	}
	if avail >= dual {
		return avail - dual
	}
	return avail - 1 - minTableHeight
}

// legendLines is the docked legend panel: a rule, then the sections that
// fit, whole. It is empty when the legend is closed or no section fits.
func (m *model) legendLines() []string {
	if !m.legend || m.width <= 0 {
		return nil
	}
	room := m.legendRoom() - 1
	sections := m.legendSectionLines(m.width)
	var body []string
	shown := 0
	for _, rows := range sections {
		if len(body)+len(rows) > room {
			break
		}
		body = append(body, rows...)
		shown++
	}
	if shown == 0 {
		return nil
	}
	rule := "── Legend "
	if shown < len(sections) {
		rule += "(more on a taller terminal) "
	}
	rule = ansi.Truncate(rule, m.width, "")
	rule += strings.Repeat("─", max(0, m.width-lipgloss.Width(rule)))
	return append([]string{lipgloss.NewStyle().Faint(true).Render(rule)}, body...)
}

// legendHidden reports whether the legend is open but has no room, given
// the lines legendLines drew.
func (m *model) legendHidden(lines []string) bool {
	return m.legend && m.width > 0 && len(lines) == 0
}

// toggleLegend opens or closes the legend and saves the choice. A failed
// save keeps the new state for the session and shows the warning.
func (m *model) toggleLegend() {
	m.legend = !m.legend
	if err := m.preferences.SaveLegend(m.legend); err != nil {
		m.preferenceErr = fmt.Errorf("Legend choice not saved: %w", err)
	}
	m.rebuildPRTable(false)
}
