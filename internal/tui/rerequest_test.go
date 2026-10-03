package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/DragosMocrii/prpr/internal/github"
)

// requests records the reviews a model requests.
type requests struct {
	repository string
	number     int
	logins     []string
	renewed    []string
	calls      int
}

// stubReviewers makes the model list reviewers and record requests.
func stubReviewers(m *model, reviewers []github.Reviewer, listErr, requestErr error) *requests {
	sent := &requests{}
	m.listReviewers = func(context.Context, string, int) ([]github.Reviewer, error) { return reviewers, listErr }
	m.requestReviews = func(_ context.Context, repository string, number int, logins, renewed []string) error {
		sent.repository, sent.number, sent.logins, sent.renewed = repository, number, logins, renewed
		sent.calls++
		return requestErr
	}
	return sent
}

func TestRerequestAsksTheChosenReviewersAgain(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	sent := stubReviewers(m, []github.Reviewer{
		{Login: "alice", State: "APPROVED"},
		{Login: "bob", State: "CHANGES_REQUESTED", Stale: true},
	}, nil, nil)
	m.setFocus(paneMine)
	m.selectPR(paneMine, "acme/api", 2)
	pressMsg(m, letter("R"))
	if m.rerequest == nil {
		t.Fatalf("R did not open the form; notice %q", m.notice)
	}
	if view := m.rerequest.form.View(); !strings.Contains(view, "alice") || !strings.Contains(view, "bob") {
		t.Fatalf("form does not show both reviewers:\n%s", view)
	}
	// Reviews before the latest commits start chosen; space adds alice.
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyUp})
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.rerequest != nil {
		t.Fatal("form still open after Enter")
	}
	if sent.calls != 1 || sent.repository != "acme/api" || sent.number != 2 {
		t.Fatalf("requested %+v", sent)
	}
	if slices.Sort(sent.logins); !slices.Equal(sent.logins, []string{"alice", "bob"}) {
		t.Fatalf("logins = %v", sent.logins)
	}
	if len(sent.renewed) != 0 {
		t.Fatalf("renewed %v", sent.renewed)
	}
	if !strings.Contains(statusText(m), "alice") || !strings.Contains(statusText(m), "#2") {
		t.Fatalf("status %q", statusText(m))
	}
}

func TestRerequestNeedsAChoiceAndEscCancels(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	sent := stubReviewers(m, []github.Reviewer{{Login: "alice", State: "APPROVED"}}, nil, nil)
	m.setFocus(paneMine)
	pressMsg(m, letter("R"))
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.rerequest == nil || sent.calls != 0 {
		t.Fatal("Enter with no one chosen requested or closed the form")
	}
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.rerequest != nil || sent.calls != 0 {
		t.Fatal("Esc requested or kept the form")
	}
}

func TestRerequestReportsFailuresAndEmptyLists(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	m.setFocus(paneMine)
	stubReviewers(m, nil, nil, nil)
	pressMsg(m, letter("R"))
	if m.rerequest != nil || !strings.Contains(statusText(m), "No one to ask again") {
		t.Fatalf("no reviewers: form %v, status %q", m.rerequest, statusText(m))
	}
	stubReviewers(m, nil, errors.New("GitHub reviewer query failed: HTTP 502\nbad"), nil)
	pressMsg(m, letter("R"))
	if m.rerequest != nil || !strings.Contains(statusText(m), "HTTP 502 bad") {
		t.Fatalf("lookup failure: status %q", statusText(m))
	}
	stubReviewers(m, []github.Reviewer{{Login: "bob", Stale: true}}, nil, errors.New("Could not request reviews: HTTP 422"))
	pressMsg(m, letter("R"))
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(statusText(m), "HTTP 422") {
		t.Fatalf("request failure: status %q", statusText(m))
	}
}

func TestRerequestOnlyOnListedAuthoredRows(t *testing.T) {
	mine, review := snoozePRs()
	store := testPreferences(t)
	if err := store.Save("alice", ""); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 140, 40)
	sent := stubReviewers(m, []github.Reviewer{{Login: "bob", Stale: true}}, nil, nil)
	m.loading = true
	m.Update(previewMsg{snapshot: github.Snapshot{Login: "alice", Preview: true, PullRequests: mine, ReviewRequests: review}})
	pressMsg(m, letter("R"))
	if m.rerequest != nil {
		t.Fatal("a preview row opened the form")
	}
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine, ReviewRequests: review}})
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine[1:], ReviewRequests: review}})
	m.setFocus(paneMine)
	m.selectPR(paneMine, "acme/api", 1) // gone row
	pressMsg(m, letter("R"))
	if m.rerequest != nil {
		t.Fatal("a gone row opened the form")
	}
	m.setFocus(paneReview)
	pressMsg(m, letter("R"))
	if m.rerequest != nil {
		t.Fatal("a review request opened the form")
	}
	if sent.calls != 0 {
		t.Fatalf("requested %+v", sent)
	}
}

func TestStaleReviewerResultsAreDropped(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	stubReviewers(m, []github.Reviewer{{Login: "bob", Stale: true}}, nil, nil)
	m.setFocus(paneMine)
	lookup := m.handleKey(letter("R"))
	listed := lookup().(reviewersListedMsg)

	m.accountGeneration++
	m.Update(listed)
	if m.rerequest != nil {
		t.Fatal("a lookup for another account opened the form")
	}
	m.accountGeneration--
	m.handleKey(letter("y"))
	m.Update(listed)
	if m.rerequest != nil {
		t.Fatal("a lookup that a newer notice replaced opened the form")
	}

	m.Update(reviewsRequestedMsg{account: m.accountGeneration + 1, key: keyOf(&mine[0]), logins: []string{"bob"}})
	if strings.Contains(m.notice, "Requested") {
		t.Fatalf("a result for another account set %q", m.notice)
	}
}

func TestRerequestDropsAPullRequestThatLeftTheList(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	sent := stubReviewers(m, []github.Reviewer{{Login: "bob", Stale: true}}, nil, nil)
	m.setFocus(paneMine)
	m.selectPR(paneMine, "acme/api", 1)
	pressMsg(m, letter("R"))
	if m.rerequest == nil {
		t.Fatal("form did not open")
	}
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine[1:], ReviewRequests: review}})
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if sent.calls != 0 || !strings.Contains(m.notice, "no longer listed") {
		t.Fatalf("requested %+v; notice %q", sent, m.notice)
	}
}

func TestRerequestRenewsAChosenPendingRequest(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	sent := stubReviewers(m, []github.Reviewer{
		{Login: "bob", State: "COMMENTED", Stale: true},
		{Login: "carol", Pending: true},
	}, nil, nil)
	m.setFocus(paneMine)
	pressMsg(m, letter("R"))
	if m.rerequest == nil || !strings.Contains(m.rerequest.form.View(), "carol") {
		t.Fatal("a pending reviewer is not offered")
	}
	// The pending request starts unchosen; the cursor is on bob, the chosen one.
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyDown})
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if slices.Sort(sent.logins); !slices.Equal(sent.logins, []string{"bob", "carol"}) || !slices.Equal(sent.renewed, []string{"carol"}) {
		t.Fatalf("requested %v, renewed %v", sent.logins, sent.renewed)
	}
}

func TestRerequestNeverChoosesAnApproverItself(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	stubReviewers(m, []github.Reviewer{{Login: "alice", State: "APPROVED", Stale: true}}, nil, nil)
	m.setFocus(paneMine)
	pressMsg(m, letter("R"))
	if m.rerequest == nil || len(m.rerequest.chosen) != 0 {
		t.Fatalf("an approver of older commits starts chosen: %+v", m.rerequest)
	}
	if !strings.Contains(m.rerequest.form.View(), "awaiting review") {
		t.Fatalf("form does not warn about the approval:\n%s", m.rerequest.form.View())
	}
}

func TestRerequestSkipsAnyoneWhoReviewedSinceTheFormOpened(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	sent := stubReviewers(m, nil, nil, nil)
	before := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	after := before.Add(time.Minute)
	lists := [][]github.Reviewer{
		{
			{Login: "bob", State: "COMMENTED", Stale: true, ReviewedAt: before},
			{Login: "carol", State: "CHANGES_REQUESTED", Stale: true, ReviewedAt: before},
			{Login: "dave", State: "COMMENTED", Stale: true, ReviewedAt: before},
			{Login: "erin", Pending: true},
		},
		// While the form was open, bob approved, carol requested changes
		// again, dave's review was requested, and erin reviewed.
		{
			{Login: "bob", State: "APPROVED", ReviewedAt: after},
			{Login: "carol", State: "CHANGES_REQUESTED", ReviewedAt: after},
			{Login: "dave", State: "COMMENTED", Stale: true, ReviewedAt: before, Pending: true},
			{Login: "erin", State: "COMMENTED", ReviewedAt: after},
		},
	}
	calls := 0
	m.listReviewers = func(context.Context, string, int) ([]github.Reviewer, error) {
		list := lists[min(calls, len(lists)-1)]
		calls++
		return list, nil
	}
	m.setFocus(paneMine)
	pressMsg(m, letter("R"))
	// bob, carol, and dave start chosen; choose erin too.
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyDown})
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyDown})
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyDown})
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if calls != 2 {
		t.Fatalf("reviewers read %d times, want again when sending", calls)
	}
	if !slices.Equal(sent.logins, []string{"dave"}) || !slices.Equal(sent.renewed, []string{"dave"}) {
		t.Fatalf("requested %v, renewed %v", sent.logins, sent.renewed)
	}
	for _, skipped := range []string{"bob", "carol", "erin"} {
		if !strings.Contains(m.notice, skipped) {
			t.Fatalf("notice %q does not name %s", m.notice, skipped)
		}
	}

	// When nothing still holds, nothing is sent.
	sent.calls, calls = 0, 0
	lists[0], lists[1] = lists[0][:1], lists[1][:1]
	pressMsg(m, letter("R"))
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if sent.calls != 0 || !strings.Contains(m.notice, "Nothing sent") {
		t.Fatalf("sent %+v; notice %q", sent, m.notice)
	}
}

func TestRerequestSendsNothingWhenTheRecheckFails(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	sent := stubReviewers(m, []github.Reviewer{{Login: "bob", State: "COMMENTED", Stale: true}}, nil, nil)
	m.setFocus(paneMine)
	pressMsg(m, letter("R"))
	m.listReviewers = func(context.Context, string, int) ([]github.Reviewer, error) { return nil, errors.New("HTTP 502") }
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if sent.calls != 0 || !strings.Contains(m.notice, "Nothing sent: HTTP 502") {
		t.Fatalf("sent %+v; notice %q", sent, m.notice)
	}
}
