package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

// stubLookups makes the model list list as every pull request's reviewers,
// counting the lookups by number.
func stubLookups(m *model, list github.ReviewerList, err error) map[int]int {
	calls := make(map[int]int)
	m.listReviewers = func(_ context.Context, _ string, number int) (github.ReviewerList, error) {
		calls[number]++
		return list, err
	}
	return calls
}

func reviewerList() github.ReviewerList {
	return github.ReviewerList{
		Reviewers: []github.Reviewer{
			{Login: "alice", State: "APPROVED", OnBehalfOf: []string{"acme/web"}},
			{Login: "bob", State: "CHANGES_REQUESTED", Stale: true, Teams: []string{"acme/api"}},
			{Login: "carol", Pending: true, Teams: []string{"acme/api"}},
			{Login: "dave", Teams: []string{"acme/infra"}},
		},
		Teams: []github.TeamRequest{
			{Name: "acme/api", CodeOwner: true, Members: 5, Logins: []string{"bob", "carol"}},
			{Name: "acme/infra", CodeOwner: true, Members: 12, Logins: []string{"dave"}},
			{Name: "acme/design", Members: 3},
		},
	}
}

func TestDetailsListTheReviewersAndHowEachTeamStands(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 50, mine, review)
	calls := stubLookups(m, reviewerList(), nil)
	m.setFocus(paneMine)
	m.selectPR(paneMine, "acme/api", 2)
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if calls[2] != 1 {
		t.Fatalf("lookups = %v, want one of #2", calls)
	}
	view := detailsText(m)
	for _, want := range []string{"alice", "bob", "carol"} {
		if !strings.Contains(view, want) {
			t.Errorf("details do not name %s:\n%s", want, view)
		}
	}
	// dave neither reviewed nor is requested.
	if strings.Contains(view, "dave") {
		t.Errorf("details name a member who did nothing:\n%s", view)
	}
	for team, want := range map[string][]string{
		"web":    {"✓", "alice"},
		"api":    {"◐", "bob", "carol"},
		"infra":  {"✗", "12"},
		"design": {"✗", "code owner"},
	} {
		line := teamLine(view, team)
		for _, text := range want {
			if !strings.Contains(line, text) {
				t.Errorf("team %s: %q lacks %q", team, line, text)
			}
		}
	}
}

// teamLine is the details' line of a team, found by its slug after a mark.
func teamLine(view, slug string) string {
	for _, line := range strings.Split(view, "\n") {
		for _, mark := range []string{"✓ ", "◐ ", "✗ "} {
			if i := strings.Index(line, mark+slug); i >= 0 {
				return line[i:]
			}
		}
	}
	return ""
}

func TestAStaleApprovalAnswersOnlyATeamGitHubTookAsAnswered(t *testing.T) {
	list := github.ReviewerList{
		Reviewers: []github.Reviewer{
			{Login: "erin", State: "APPROVED", Stale: true, Teams: []string{"acme/api"}},
			{Login: "finn", State: "APPROVED", Stale: true, OnBehalfOf: []string{"acme/web"}},
		},
		Teams: []github.TeamRequest{{Name: "acme/api", CodeOwner: true, Members: 4, Logins: []string{"erin"}}},
	}
	got := make(map[string]string)
	for _, answer := range teamAnswers(list) {
		got[shortTeam(answer.name)] = ansi.Strip(answer.line())
	}
	if !strings.HasPrefix(got["api"], "◐") || !strings.Contains(got["api"], "erin") {
		t.Errorf("pending team = %q, want ◐ naming erin", got["api"])
	}
	if !strings.HasPrefix(got["web"], "✓") || !strings.Contains(got["web"], "finn") {
		t.Errorf("answered team = %q, want ✓ naming finn", got["web"])
	}
}

func TestDetailsReadReviewersOncePerUpdate(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 50, mine, review)
	calls := stubLookups(m, reviewerList(), nil)
	m.setFocus(paneMine)
	m.selectPR(paneMine, "acme/api", 2)
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyUp})
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if calls[1] != 1 || calls[2] != 1 {
		t.Fatalf("lookups = %v, want one each", calls)
	}
	// A refresh that changes #2 reads it again; #1 is unchanged.
	changed := append([]github.PullRequest(nil), mine...)
	changed[1].UpdatedAt = time.Now()
	_, cmd := updateFetch(m, fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: changed, ReviewRequests: review}})
	drive(m, cmd)
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if calls[1] != 1 || calls[2] != 2 {
		t.Fatalf("lookups = %v, want #2 read again", calls)
	}
}

func TestDetailsReadNoReviewersForOtherRows(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 50, mine, review)
	calls := stubLookups(m, reviewerList(), nil)
	m.setFocus(paneReview)
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(calls) != 0 || strings.Contains(detailsText(m), "Reviewers") {
		t.Fatalf("a review row read reviewers: %v", calls)
	}
	// The list closes and opens with the details on an authored row; with
	// the details closed nothing is read.
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	m.setFocus(paneMine)
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if len(calls) != 0 {
		t.Fatalf("closed details read reviewers: %v", calls)
	}
}

func TestDetailsDropReviewersOfAnotherAccount(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 50, mine, review)
	m.listReviewers = func(context.Context, string, int) (github.ReviewerList, error) { return reviewerList(), nil }
	m.setFocus(paneMine)
	m.selectPR(paneMine, "acme/api", 2)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.accountGeneration++
	for _, msg := range runQuick(cmd) {
		if _, ok := msg.(detailReviewersMsg); ok {
			m.Update(msg)
		}
	}
	if lookup := m.reviewerLookups[prKey{"acme/api", 2}]; lookup != nil && lookup.done {
		t.Fatal("kept the reviewers another account read")
	}
}

func TestDetailsShowAFailedLookupAndTryAgainWhenReopened(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 50, mine, review)
	calls := stubLookups(m, github.ReviewerList{}, errors.New("GitHub reviewer query failed"))
	m.setFocus(paneMine)
	m.selectPR(paneMine, "acme/api", 2)
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(detailsText(m), "GitHub reviewer query failed") {
		t.Fatalf("details do not show the failure:\n%s", detailsText(m))
	}
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if calls[2] != 2 {
		t.Fatalf("lookups = %v, want the failed one tried again", calls)
	}
}

func TestDetailsCapLongReviewerLists(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 100, 24, mine, review)
	var list github.ReviewerList
	for i := range 12 {
		login := "person" + string(rune('a'+i))
		list.Reviewers = append(list.Reviewers, github.Reviewer{Login: login, Pending: true})
	}
	stubLookups(m, list, nil)
	m.setFocus(paneMine)
	m.selectPR(paneMine, "acme/api", 2)
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	lines := assertBounded(t, m, 100, 24)
	view := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.Contains(view, "+5 more") {
		t.Fatalf("long list not capped:\n%s", view)
	}
}
