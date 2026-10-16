package tui

import "github.com/DragosMocrii/prpr/internal/github"

// lastBlockerText words a review row whose only unanswered review request is
// the viewer's.
const lastBlockerText = "only your review left"

// lastBlocker reports whether a review row waits on the viewer alone: a
// pending direct request (the request search already proves the viewer is
// requested) that is the pull request's only unanswered user or team
// request, while GitHub still requires a review and nothing else is known to
// block it: no draft, no conflicts, a known merge state, and CI neither
// failing nor running. GitHub does not tell a non-admin how many approvals a
// branch needs, so this never claims the viewer's approval is enough.
func lastBlocker(pr *github.PullRequest) bool {
	switch {
	case pr.ReviewStatus != github.ReviewRequested, pr.OpenRequests != 1,
		pr.ReviewDecision != "REVIEW_REQUIRED", pr.Draft, unknownMergeState(pr),
		pr.Mergeable == "CONFLICTING", pr.MergeState == "DIRTY":
		return false
	}
	switch pr.Checks {
	case "FAILURE", "ERROR", "PENDING", "EXPECTED":
		return false
	}
	return true
}

// lastBlockerTag leads the name of a review row that waits on the viewer
// alone.
func lastBlockerTag(ic *iconSet) string {
	return coloredIcon(ic.lastBlocker, "3")
}

// lastBlockerShown reports whether a visible Review requested row carries the
// last-blocker tag.
func (m *model) lastBlockerShown() bool {
	for _, index := range m.panes[paneReview].visible {
		if lastBlocker(&m.snapshot.ReviewRequests[index]) {
			return true
		}
	}
	return false
}
