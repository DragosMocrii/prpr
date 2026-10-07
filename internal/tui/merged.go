package tui

import (
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/preferences"
)

// movedOn tells pane id's tracker which pull requests that left its list
// are not gone: an authored pull request that merged moved to the Merged
// pane, and one falling out of the Merged pane's last N is not news.
func (m *model) movedOn(id paneID) func(prKey) bool {
	switch id {
	case paneMine:
		merged := make(map[prKey]bool, len(m.snapshot.Merged))
		for i := range m.snapshot.Merged {
			merged[keyOf(&m.snapshot.Merged[i])] = true
		}
		return func(key prKey) bool { return merged[key] }
	case paneMerged:
		return func(prKey) bool { return true }
	}
	return nil
}

// mergedDetail says when and by whom a pull request merged.
func mergedDetail(pr *github.PullRequest, now time.Time) string {
	text := "Merged " + dateDetail(pr.MergedAt, now)
	if pr.MergedBy != "" {
		text += " by " + singleLine(pr.MergedBy)
	}
	return text
}

// nextMerged is the first choice after n; a value between choices goes to
// the next larger one, and the last wraps to off.
func nextMerged(n int) int {
	for _, choice := range preferences.MergedChoices {
		if choice > n {
			return choice
		}
	}
	return preferences.MergedChoices[0]
}

func formatMerged(n int) string {
	if n == 0 {
		return "off"
	}
	return strconv.Itoa(n)
}

// applyMerged uses and saves how many merged pull requests to list. Like
// new bots or queues, it fetches again, and the result becomes the change
// baseline without marking.
// toggleMergedCollapsed draws the Merged pane as its title alone, or with
// its table again, and saves it. Focus leaves a pane it collapses.
func (m *model) toggleMergedCollapsed() {
	m.mergedCollapsed = !m.mergedCollapsed
	m.settingSaved(m.preferences.SaveMergedCollapsed(m.mergedCollapsed))
	m.rebuildPRTable(false)
}

func (m *model) applyMerged(n int) tea.Cmd {
	m.merged = n
	if m.client != nil {
		m.client.SetMerged(n)
	}
	m.settingSaved(m.preferences.SaveMerged(n))
	return m.settingsFetch()
}
