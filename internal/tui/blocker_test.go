package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

// blockingReview is a pending request that waits on the viewer alone.
func blockingReview(number int) github.PullRequest {
	pr := changePR(number, "acme/b")
	pr.Checks, pr.OpenRequests = "SUCCESS", 1
	return pr
}

func TestLastBlocker(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(*github.PullRequest)
		want bool
	}{
		"blocking":             {func(*github.PullRequest) {}, true},
		"no CI":                {func(pr *github.PullRequest) { pr.Checks = "" }, true},
		"others requested":     {func(pr *github.PullRequest) { pr.OpenRequests = 2 }, false},
		"count unknown":        {func(pr *github.PullRequest) { pr.OpenRequests = 0 }, false},
		"reviews not required": {func(pr *github.PullRequest) { pr.ReviewDecision = "" }, false},
		"changes requested":    {func(pr *github.PullRequest) { pr.ReviewDecision = "CHANGES_REQUESTED" }, false},
		"draft":                {func(pr *github.PullRequest) { pr.Draft = true }, false},
		"conflicts":            {func(pr *github.PullRequest) { pr.Mergeable, pr.MergeState = "CONFLICTING", "DIRTY" }, false},
		"merge state unknown":  {func(pr *github.PullRequest) { pr.MergeState = "UNKNOWN" }, false},
		"CI failing":           {func(pr *github.PullRequest) { pr.Checks = "FAILURE" }, false},
		"CI running":           {func(pr *github.PullRequest) { pr.Checks = "PENDING" }, false},
		"reviewed":             {func(pr *github.PullRequest) { pr.ReviewStatus = github.ReviewNewCommits }, false},
		"waiting":              {func(pr *github.PullRequest) { pr.ReviewStatus = github.ReviewWaitingOnAuthor }, false},
		"mentions only":        {func(pr *github.PullRequest) { pr.ReviewStatus = github.ReviewNudged }, false},
	} {
		pr := blockingReview(1)
		tc.edit(&pr)
		if got := lastBlocker(&pr); got != tc.want {
			t.Errorf("%s: lastBlocker = %t, want %t", name, got, tc.want)
		}
	}
}

func TestLastBlockerTagsTheNameDetailsAndStatusLine(t *testing.T) {
	for _, set := range []*iconSet{&unicodeIcons, &nerdIcons} {
		other := blockingReview(2)
		other.OpenRequests = 2
		m := newPaneModel(t, 160, 40, nil, []github.PullRequest{blockingReview(1), other})
		m.icons = set
		m.rebuildPRTable(false)
		rows := m.panes[paneReview].table.Rows()
		if !strings.Contains(strings.Join(rows[0], ""), set.lastBlocker) {
			t.Errorf("nerd %t: blocking row has no tag: %q", set.nerd, rows[0])
		}
		if strings.Contains(strings.Join(rows[1], ""), set.lastBlocker) {
			t.Errorf("nerd %t: row with other requests is tagged: %q", set.nerd, rows[1])
		}
		m.focus = paneReview
		if facts := strings.Join(m.rowStatus(), " "); !strings.Contains(facts, lastBlockerText) {
			t.Errorf("nerd %t: status line = %q", set.nerd, facts)
		}
		var labels []string
		for _, row := range m.detailRows(&m.snapshot.ReviewRequests[0], false) {
			labels = append(labels, row.label)
		}
		if !strings.Contains(strings.Join(labels, ","), "Blocking") {
			t.Errorf("nerd %t: details rows = %v", set.nerd, labels)
		}
		pressL(m)
		if legend := ansi.Strip(strings.Join(m.legendLines(), "\n")); !strings.Contains(legend, lastBlockerText) {
			t.Errorf("nerd %t: legend lacks the tag:\n%s", set.nerd, legend)
		}
	}
}

func TestBecomingTheLastBlockerAlertsOnce(t *testing.T) {
	m := notifyModel(t, "")
	pr := blockingReview(1)
	pr.OpenRequests = 2
	fetch(m, nil, []github.PullRequest{pr})
	if got := fetch(m, nil, []github.PullRequest{pr}); got != nil {
		t.Fatalf("an unchanged request notified: %q", got)
	}
	pr.OpenRequests = 1
	got := fetch(m, nil, []github.PullRequest{pr})
	if len(got) != 1 || !strings.Contains(got[0], lastBlockerText) || strings.Contains(got[0], "review requested") {
		t.Fatalf("turning the last blocker = %q, want one notification naming it", got)
	}
	if got := fetch(m, nil, []github.PullRequest{pr}); got != nil {
		t.Fatalf("staying the last blocker notified again: %q", got)
	}
	pr.Checks = "PENDING"
	if got := fetch(m, nil, []github.PullRequest{pr}); got != nil {
		t.Fatalf("CI rerunning notified: %q", got)
	}
	pr.Checks = "SUCCESS"
	if got := fetch(m, nil, []github.PullRequest{pr}); len(got) != 1 {
		t.Fatalf("CI passing again = %q, want a notification", got)
	}
}

func TestAnUnknownMergeStateDoesNotRealertTheLastBlocker(t *testing.T) {
	m := notifyModel(t, "")
	pr := blockingReview(1)
	pr.OpenRequests = 2
	fetch(m, nil, []github.PullRequest{pr})
	pr.OpenRequests = 1
	if got := fetch(m, nil, []github.PullRequest{pr}); len(got) != 1 {
		t.Fatalf("turning the last blocker = %q, want a notification", got)
	}
	pr.MergeState = "UNKNOWN"
	if got := fetch(m, nil, []github.PullRequest{pr}); got != nil {
		t.Fatalf("an unknown merge state notified: %q", got)
	}
	pr.MergeState = "BLOCKED"
	if got := fetch(m, nil, []github.PullRequest{pr}); got != nil {
		t.Fatalf("the merge state coming back notified again: %q", got)
	}
}

func TestLastBlockerNeverAlertsOnFirstFetchOrWhenNew(t *testing.T) {
	m := notifyModel(t, "")
	if got := fetch(m, nil, []github.PullRequest{blockingReview(1)}); got != nil {
		t.Fatalf("first fetch notified: %q", got)
	}
	got := fetch(m, nil, []github.PullRequest{blockingReview(1), blockingReview(2)})
	if len(got) != 1 || !strings.Contains(got[0], "review requested") || !strings.Contains(got[0], lastBlockerText) {
		t.Fatalf("a new blocking request = %q, want one notification with both kinds", got)
	}
}
