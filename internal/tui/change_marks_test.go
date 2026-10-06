package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

func TestPassingAMarkedRowKeepsItsMark(t *testing.T) {
	one, two, three := changePR(1, "acme/a"), changePR(2, "acme/a"), changePR(3, "acme/a")
	m := changesModel(t, one, two, three)
	two.Comments = 4
	updateSnapshot(m, "alice", one, two, three)
	down := tea.Key{Code: 'j', Text: "j"}
	press(m, down)
	press(m, down)
	if selected, _ := m.selectedPR(); selected.Number != 3 || markers(m, paneMine) != " • " {
		t.Fatalf("after passing #2: selected %+v, markers %q", selected, markers(m, paneMine))
	}
	// A rest that ended after the cursor moved on reads nothing.
	press(m, tea.Key{Code: 'k', Text: "k"})
	stale := m.restGeneration
	press(m, down)
	m.Update(restMsg{stale})
	press(m, tea.Key{Code: 'k', Text: "k"})
	press(m, down)
	if got := markers(m, paneMine); got != " • " {
		t.Fatalf("markers after a stale rest = %q", got)
	}
}

func TestAChangeAfterReadingMarksTheRowUnread(t *testing.T) {
	one, two := changePR(1, "acme/a"), changePR(2, "acme/a")
	m := changesModel(t, one, two)
	one.Comments = 1
	updateSnapshot(m, "alice", one, two)
	rest(m)
	one.Checks = "FAILURE"
	updateSnapshot(m, "alice", one, two)
	press(m, tea.Key{Code: 'j', Text: "j"})
	if got := markers(m, paneMine); got != "▼ " {
		t.Fatalf("markers after leaving a row changed again = %q", got)
	}
}

func TestDetailsAndOpeningReadTheMark(t *testing.T) {
	one, two := changePR(1, "acme/a"), changePR(2, "acme/a")
	m := changesModel(t, one, two)
	one.Comments, two.Comments = 1, 1
	updateSnapshot(m, "alice", one, two)
	press(m, tea.Key{Code: tea.KeyEnter})
	press(m, tea.Key{Code: tea.KeyEscape})
	press(m, tea.Key{Code: 'j', Text: "j"})
	if got := markers(m, paneMine); got != " •" {
		t.Fatalf("markers after leaving a row shown in details = %q", got)
	}
	press(m, tea.Key{Code: 'y', Text: "y"})
	press(m, tea.Key{Code: 'k', Text: "k"})
	if got := markers(m, paneMine); got != "  " {
		t.Fatalf("markers after leaving a copied row = %q", got)
	}
}

func TestChangeLinesReadFromWhereTheChangesStarted(t *testing.T) {
	pr := changePR(1, "acme/a")
	m := changesModel(t, pr)
	pr.Checks = "SUCCESS"
	updateSnapshot(m, "alice", pr)
	pr.Checks, pr.Comments = "FAILURE", 2
	updateSnapshot(m, "alice", pr)
	lines := m.changeLines(paneMine, &m.snapshot.PullRequests[0])
	var texts []string
	for _, line := range lines {
		texts = append(texts, line.text())
	}
	if got := strings.Join(texts, "; "); got != "CI: pending → failing; Comments: 0 → 2" {
		t.Fatalf("change lines = %q", got)
	}
	if got := markers(m, paneMine); got != "▼" {
		t.Fatalf("marker = %q", got)
	}
	pr.Checks = "PENDING"
	updateSnapshot(m, "alice", pr)
	if got := m.changeLines(paneMine, &m.snapshot.PullRequests[0])[0].text(); got != "CI: pending → … → pending" {
		t.Fatalf("a change that came back = %q", got)
	}
}

func TestChangeDirections(t *testing.T) {
	base := changePR(1, "acme/a")
	for _, tt := range []struct {
		name   string
		change func(*github.PullRequest)
		dir    direction
	}{
		{"CI passing", func(pr *github.PullRequest) { pr.Checks = "SUCCESS" }, dirGood},
		{"CI failing", func(pr *github.PullRequest) { pr.Checks = "ERROR" }, dirBad},
		{"approved", func(pr *github.PullRequest) { pr.ReviewDecision, pr.Approvals = "APPROVED", 1 }, dirGood},
		{"changes requested", func(pr *github.PullRequest) { pr.ReviewDecision = "CHANGES_REQUESTED" }, dirBad},
		{"ready", func(pr *github.PullRequest) { pr.MergeState = "CLEAN" }, dirGood},
		{"conflict", func(pr *github.PullRequest) { pr.Mergeable, pr.MergeState = "CONFLICTING", "DIRTY" }, dirBad},
		{"queue failed", func(pr *github.PullRequest) {
			pr.Queue = &github.QueueEntry{Provider: "Trunk", State: github.QueueRemovedFailed}
		}, dirBad},
		{"bot concerns", func(pr *github.PullRequest) {
			pr.Bots = []github.BotReview{{Name: "lint", State: github.BotConcerns, Concerns: 2}}
		}, dirBad},
		{"comments", func(pr *github.PullRequest) { pr.Comments = 9 }, dirNeutral},
		{"good and bad", func(pr *github.PullRequest) { pr.Checks, pr.ReviewDecision = "SUCCESS", "CHANGES_REQUESTED" }, dirBad},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := changesModel(t, base)
			pr := base
			tt.change(&pr)
			updateSnapshot(m, "alice", pr)
			if got := rowDirection(m.changeLines(paneMine, &m.snapshot.PullRequests[0])); got != tt.dir {
				t.Fatalf("direction = %v, want %v", got, tt.dir)
			}
		})
	}
}

func TestReviewRowNeedingYouAgainIsBad(t *testing.T) {
	store := testPreferences(t)
	if err := store.Save("alice", ""); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 140, 30)
	reviewed := changePR(7, "acme/r")
	reviewed.ReviewStatus = github.ReviewWaitingOnAuthor
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: []github.PullRequest{reviewed}}})
	reviewed.ReviewStatus = github.ReviewNewCommits
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: []github.PullRequest{reviewed}}})
	if got := markers(m, paneReview); got != "▼" {
		t.Fatalf("review markers = %q", got)
	}
	lines := m.changeLines(paneReview, &m.snapshot.ReviewRequests[0])
	if len(lines) != 1 || lines[0].text() != "Status: waiting on author → new commits" {
		t.Fatalf("change lines = %+v", lines)
	}
}

func TestChangesShowInDetailsAndTheStatusLine(t *testing.T) {
	pr := changePR(1, "acme/a")
	m := changesModel(t, pr)
	pr.Checks = "FAILURE"
	updateSnapshot(m, "alice", pr)
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "▼ ci pending→failing") {
		t.Fatalf("status line has no change summary:\n%s", view)
	}
	press(m, tea.Key{Code: tea.KeyEnter})
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "CI: pending → failing") {
		t.Fatalf("details have no Changed row:\n%s", view)
	}
	press(m, tea.Key{Code: tea.KeyEscape})
	if got := ansi.Strip(m.changeStatus(14)); got != "▼ ci failing" {
		t.Fatalf("narrow summary = %q", got)
	}
	if got := ansi.Strip(m.changeStatus(8)); got != "▼ ci fa…" {
		t.Fatalf("cut summary = %q", got)
	}
}

func TestARestIsTimedOnlyFromInput(t *testing.T) {
	one, two := changePR(1, "acme/a"), changePR(2, "acme/a")
	m := changesModel(t, one, two)
	one.Comments = 1
	updateSnapshot(m, "alice", one, two)
	// The refresh marked the row under the cursor while nobody pressed a key.
	rest(m)
	press(m, tea.Key{Code: 'j', Text: "j"})
	if got := markers(m, paneMine); got != "• " {
		t.Fatalf("markers after leaving a row marked while idle = %q", got)
	}
	// Losing focus stops a rest.
	press(m, tea.Key{Code: 'k', Text: "k"})
	m.Update(tea.BlurMsg{})
	rest(m)
	press(m, tea.Key{Code: 'j', Text: "j"})
	if got := markers(m, paneMine); got != "• " {
		t.Fatalf("markers after a rest without focus = %q", got)
	}
	press(m, tea.Key{Code: 'k', Text: "k"})
	rest(m)
	press(m, tea.Key{Code: 'j', Text: "j"})
	if got := markers(m, paneMine); got != "  " {
		t.Fatalf("markers after resting = %q", got)
	}
}

func TestReviewRowDirections(t *testing.T) {
	for _, tt := range []struct {
		name       string
		before     func(*github.PullRequest)
		after      func(*github.PullRequest)
		wantMarker string
	}{
		{"requested again", func(pr *github.PullRequest) { pr.ReviewStatus = github.ReviewWaitingOnAuthor },
			func(pr *github.PullRequest) { pr.ReviewStatus = github.ReviewRequested }, "▼"},
		{"your own change request", func(pr *github.PullRequest) { pr.ReviewStatus = github.ReviewRequested },
			func(pr *github.PullRequest) {
				pr.ReviewStatus, pr.ReviewDecision = github.ReviewWaitingOnAuthor, "CHANGES_REQUESTED"
			}, " "},
		{"failing CI on someone else's", func(*github.PullRequest) {},
			func(pr *github.PullRequest) { pr.Checks = "FAILURE" }, "•"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := testPreferences(t)
			if err := store.Save("alice", ""); err != nil {
				t.Fatal(err)
			}
			m := testModel(store, 140, 30)
			pr := changePR(7, "acme/r")
			tt.before(&pr)
			m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: []github.PullRequest{pr}}})
			tt.after(&pr)
			m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: []github.PullRequest{pr}}})
			if got := markers(m, paneReview); got != tt.wantMarker {
				t.Fatalf("review marker = %q, want %q", got, tt.wantMarker)
			}
		})
	}
}
