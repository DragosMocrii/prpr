package tui

import (
	"slices"
	"strings"

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
)

type rowMark struct {
	kind  markKind
	cells changedCells
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
	for i := range list {
		pr := &list[i]
		key := keyOf(pr)
		current[key] = true
		old, ok := previous[key]
		if !ok {
			c.gone = slices.DeleteFunc(c.gone, func(g github.PullRequest) bool { return keyOf(&g) == key })
			c.marks[key] = rowMark{kind: markNew}
			continue
		}
		mark := c.marks[key]
		if cells := cellChanges(old, pr); cells != 0 {
			mark.kind = max(mark.kind, markChanged)
			mark.cells |= cells
		} else if !old.UpdatedAt.Equal(pr.UpdatedAt) {
			mark.kind = max(mark.kind, markActivity)
		}
		if mark.kind != markNone {
			c.marks[key] = mark
		}
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
	return cells
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
			if pr, _, ok := m.paneRow(id, row); ok && m.tracker(id).mark(pr).kind != markNone {
				return true
			}
		}
	}
	return false
}
