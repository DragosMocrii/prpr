package tui

import (
	"time"

	"github.com/DragosMocrii/prpr/internal/github"
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
