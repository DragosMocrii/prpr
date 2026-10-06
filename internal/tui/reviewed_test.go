package tui

import (
	"strings"
	"testing"

	"github.com/DragosMocrii/prpr/internal/github"
)

func reviewedPR(number int, status github.ReviewStatus) github.PullRequest {
	pr := changePR(number, "acme/b")
	pr.ReviewStatus = status
	return pr
}

func reviewRow(m *model, row int) string {
	return strings.Join(m.panes[paneReview].table.Rows()[row], " ")
}

func TestReviewedPullRequestStaysListedWhileWaitingOnTheAuthor(t *testing.T) {
	m := notifyModel(t, "")
	requested := reviewedPR(9, github.ReviewRequested)
	fetch(m, nil, []github.PullRequest{requested, reviewedPR(8, github.ReviewRequested)})

	// Reviewing #9 moves it to the waiting rows instead of out of the list.
	waiting := reviewedPR(9, github.ReviewWaitingOnAuthor)
	if got := fetch(m, nil, []github.PullRequest{reviewedPR(8, github.ReviewRequested), waiting}); got != nil {
		t.Fatalf("starting to wait notified: %q", got)
	}
	if len(m.panes[paneReview].gone) != 0 || len(m.panes[paneReview].visible) != 2 {
		t.Fatalf("the reviewed PR left the list: %d visible, %d gone", len(m.panes[paneReview].visible), len(m.panes[paneReview].gone))
	}
	row := reviewRow(m, 1)
	if !strings.Contains(row, "waiting") || !strings.Contains(row, waitingOn) {
		t.Fatalf("waiting row = %q, want its status, dimmed", row)
	}
	if strings.Contains(reviewRow(m, 0), waitingOn) {
		t.Fatal("a pending request was dimmed")
	}
	if title := m.paneTitle(paneReview, false); !strings.Contains(title, "1 waiting on others") {
		t.Fatalf("title = %q", title)
	}
	if got := m.categoryCount(6); got != 1 {
		t.Fatalf("awaiting review = %d, want the pending request only", got)
	}
	// The viewer's own review leaves no mark.
	if mark := m.changes[paneReview].mark(&waiting); mark.kind != markNone {
		t.Fatalf("status change mark = %+v, want none", mark)
	}

	// New commits bring it back, with one notification.
	back := reviewedPR(9, github.ReviewNewCommits)
	got := fetch(m, nil, []github.PullRequest{reviewedPR(8, github.ReviewRequested), back})
	if len(got) != 1 || !strings.Contains(got[0], "acme/b#9 new commits") {
		t.Fatalf("notifications = %q, want new commits for #9", got)
	}
	if row := reviewRow(m, 1); strings.Contains(row, waitingOn) || !strings.Contains(row, "new commits") {
		t.Fatalf("row needing review again = %q", row)
	}
	if got := m.categoryCount(6); got != 2 {
		t.Fatalf("awaiting review = %d, want both", got)
	}
	// Still needing the viewer does not notify again.
	if got := fetch(m, nil, []github.PullRequest{reviewedPR(8, github.ReviewRequested), back}); got != nil {
		t.Fatalf("an unchanged status notified again: %q", got)
	}
}

func TestReviewedRowsNotifyOnlyWhenTheyStartNeedingTheViewer(t *testing.T) {
	m := notifyModel(t, "")
	fetch(m, nil, []github.PullRequest{reviewedPR(9, github.ReviewApproved)})
	if got := fetch(m, nil, []github.PullRequest{reviewedPR(9, github.ReviewApproved), reviewedPR(7, github.ReviewWaitingOnAuthor)}); got != nil {
		t.Fatalf("a waiting row arriving notified: %q", got)
	}
	// A re-request is a pending request again.
	got := fetch(m, nil, []github.PullRequest{reviewedPR(9, github.ReviewRequested), reviewedPR(7, github.ReviewAuthorReplied)})
	if len(got) != 1 || !strings.Contains(got[0], "acme/b#9 review requested") || !strings.Contains(got[0], "acme/b#7 author replied") {
		t.Fatalf("notifications = %q", got)
	}
}

func TestDetailsSayWhereAReviewedPullRequestStands(t *testing.T) {
	m := notifyModel(t, "")
	fetch(m, nil, []github.PullRequest{reviewedPR(9, github.ReviewApproved)})
	m.setFocus(paneReview)
	m.details = true
	if view := m.View().Content; !strings.Contains(view, "You approved") {
		t.Fatalf("details = %q", view)
	}
}
