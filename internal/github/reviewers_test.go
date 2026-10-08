package github

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDecodeReviewersOffersReviewersAndPendingRequests(t *testing.T) {
	data := []byte(`{"data":{"repository":{"pullRequest":{
		"author":{"login":"me"},
		"headRefOid":"head",
		"latestReviews":{"nodes":[
			{"state":"APPROVED","author":{"__typename":"User","login":"alice"},"commit":{"oid":"head"}},
			{"state":"CHANGES_REQUESTED","author":{"__typename":"User","login":"bob"},"commit":{"oid":"old"}},
			{"state":"COMMENTED","author":{"__typename":"Bot","login":"ci-bot"},"commit":{"oid":"old"}},
			{"state":"COMMENTED","author":{"__typename":"User","login":"me"},"commit":{"oid":"old"}},
			{"state":"COMMENTED","author":{"__typename":"User","login":"Carol"},"commit":{"oid":"old"}},
			null,
			{"state":"DISMISSED","author":null,"commit":null},
			{"state":"COMMENTED","author":{"__typename":"User","login":"dave"},"commit":null}
		]},
		"reviewRequests":{"nodes":[
			{"requestedReviewer":{"__typename":"User","login":"carol"}},
			{"requestedReviewer":{"__typename":"User","login":"erin"}},
			{"requestedReviewer":{"__typename":"User","login":"me"}},
			{"requestedReviewer":{"__typename":"Team"}},
			{"requestedReviewer":null}
		]}
	}}}}`)
	list, err := decodeReviewers(data)
	if err != nil {
		t.Fatal(err)
	}
	got := list.Reviewers
	want := []Reviewer{
		{Login: "alice", State: "APPROVED", Stale: false},
		{Login: "bob", State: "CHANGES_REQUESTED", Stale: true},
		{Login: "Carol", State: "COMMENTED", Stale: true, Pending: true},
		{Login: "dave", State: "COMMENTED", Stale: true},
		{Login: "erin", Pending: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reviewers = %+v, want %+v", got, want)
	}
}

func TestDecodeReviewersRejectsErrorsAndMissingPullRequests(t *testing.T) {
	for name, data := range map[string]string{
		"not JSON":      `[`,
		"GraphQL error": `{"errors":[{"message":"Could not resolve to a Repository"}],"data":{"repository":null}}`,
		"no repository": `{"data":{"repository":null}}`,
		"no pull":       `{"data":{"repository":{"pullRequest":null}}}`,
	} {
		if _, err := decodeReviewers([]byte(data)); err == nil {
			t.Errorf("%s: decoded without an error", name)
		}
	}
}

func TestReviewersQueryIsNotTakenForAListQuery(t *testing.T) {
	// Stand-ins for gh tell queries apart by this text.
	for _, text := range []string{"search(", "mergeStateStatus", "rateLimit"} {
		if strings.Contains(reviewersQuery, text) {
			t.Errorf("reviewer query contains %q", text)
		}
	}
}

func TestNudgeErrorsNeverContainTheToken(t *testing.T) {
	client, _ := fakeGH(t, true)
	client.UseAccount("alice")
	err := client.Nudge(context.Background(), "acme/app", 12, NudgeNormal, []string{"bob"}, "")
	if err == nil || strings.Contains(err.Error(), fakeToken) || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("err = %v", err)
	}
	if _, err := client.Reviewers(context.Background(), "acme/app", 12); err == nil || strings.Contains(err.Error(), fakeToken) {
		t.Fatalf("reviewers err = %v", err)
	}
}

func TestDecodeReviewersKeepsTheApprovalOfAReviewerAskedAgain(t *testing.T) {
	// While a review is requested again, latestReviews leaves the reviewer
	// out, though the approval still counts.
	data := []byte(`{"data":{"repository":{"pullRequest":{
		"author":{"login":"me"},
		"headRefOid":"head",
		"latestReviews":{"nodes":[
			{"state":"COMMENTED","submittedAt":"2026-10-03T09:05:00Z","author":{"__typename":"User","login":"bob"},"commit":{"oid":"head"}},
			{"state":"PENDING","submittedAt":null,"author":{"__typename":"User","login":"drafter"},"commit":{"oid":"head"}}
		]},
		"latestOpinionatedReviews":{"nodes":[
			{"state":"APPROVED","submittedAt":"2026-10-03T09:01:00Z","author":{"__typename":"User","login":"alice"},"commit":{"oid":"head"}},
			{"state":"APPROVED","submittedAt":"2026-10-03T09:00:00Z","author":{"__typename":"User","login":"bob"},"commit":{"oid":"old"}}
		]},
		"reviewRequests":{"nodes":[{"requestedReviewer":{"__typename":"User","login":"alice"}}]}
	}}}}`)
	list, err := decodeReviewers(data)
	if err != nil {
		t.Fatal(err)
	}
	got := list.Reviewers
	want := []Reviewer{
		// bob approved, then commented on the head commit.
		{Login: "bob", State: "APPROVED", Stale: false, ReviewedAt: time.Date(2026, 10, 3, 9, 5, 0, 0, time.UTC)},
		{Login: "alice", State: "APPROVED", Stale: false, Pending: true, ReviewedAt: time.Date(2026, 10, 3, 9, 1, 0, 0, time.UTC)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reviewers = %+v, want %+v", got, want)
	}
}

func TestDecodeReviewersOffersTheMembersOfRequestedTeams(t *testing.T) {
	data := []byte(`{"data":{"repository":{"pullRequest":{
		"author":{"login":"me"},
		"headRefOid":"head",
		"latestReviews":{"nodes":[
			{"state":"COMMENTED","author":{"__typename":"User","login":"bob"},"commit":{"oid":"old"}}
		]},
		"reviewRequests":{"nodes":[
			{"asCodeOwner":false,"requestedReviewer":{"__typename":"Team","combinedSlug":"acme/docs",
				"members":{"totalCount":2,"nodes":[{"login":"lee"},{"login":"priya"}]}}},
			{"asCodeOwner":true,"requestedReviewer":{"__typename":"Team","combinedSlug":"acme/backend",
				"members":{"totalCount":140,"nodes":[{"login":"Bob"},{"login":"me"},{"login":"priya"},{"login":"not a login"},null]}}},
			{"asCodeOwner":false,"requestedReviewer":{"__typename":"User","login":"erin"}}
		]}
	}}}}`)
	list, err := decodeReviewers(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []Reviewer{
		{Login: "bob", State: "COMMENTED", Stale: true, Teams: []string{"acme/backend"}, CodeOwner: true},
		{Login: "erin", Pending: true},
		{Login: "priya", Teams: []string{"acme/backend", "acme/docs"}, CodeOwner: true},
		{Login: "lee", Teams: []string{"acme/docs"}},
	}
	if !reflect.DeepEqual(list.Reviewers, want) {
		t.Fatalf("reviewers = %+v, want %+v", list.Reviewers, want)
	}
	teams := []TeamRequest{
		{Name: "acme/backend", CodeOwner: true, Members: 140, Logins: []string{"Bob", "priya"}},
		{Name: "acme/docs", Members: 2, Logins: []string{"lee", "priya"}},
	}
	if !reflect.DeepEqual(list.Teams, teams) || list.UnreadableTeams != 0 {
		t.Fatalf("teams = %+v, unreadable %d", list.Teams, list.UnreadableTeams)
	}
}

func TestDecodeReviewersKeepsTheRestWhenATeamIsNotReadable(t *testing.T) {
	data := []byte(`{"errors":[{"message":"Your token has not been granted the required scopes","path":["repository","pullRequest","reviewRequests","nodes",0,"requestedReviewer","members"]}],
	"data":{"repository":{"pullRequest":{
		"author":{"login":"me"},
		"headRefOid":"head",
		"latestReviews":{"nodes":[{"state":"COMMENTED","author":{"__typename":"User","login":"bob"},"commit":{"oid":"head"}}]},
		"reviewRequests":{"nodes":[{"asCodeOwner":true,"requestedReviewer":null}]}
	}}}}`)
	list, err := decodeReviewers(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Reviewers) != 1 || list.Reviewers[0].Login != "bob" || list.UnreadableTeams != 1 {
		t.Fatalf("list = %+v", list)
	}
	// An error elsewhere still fails the query.
	other := strings.Replace(string(data), `"reviewRequests","nodes",0,"requestedReviewer","members"`, `"latestReviews"`, 1)
	if _, err := decodeReviewers([]byte(other)); err == nil {
		t.Fatal("an error outside review requests was ignored")
	}
}

func TestDecodeReviewersNamesTheTeamsAReviewAnswered(t *testing.T) {
	data := []byte(`{"data":{"repository":{"pullRequest":{
		"author":{"login":"me"},
		"headRefOid":"head",
		"latestReviews":{"nodes":[
			{"state":"APPROVED","author":{"__typename":"User","login":"alice"},"commit":{"oid":"head"},
				"onBehalfOf":{"nodes":[{"combinedSlug":"acme/web"},{"combinedSlug":"acme/api"}]}},
			{"state":"DISMISSED","author":{"__typename":"User","login":"bob"},"commit":{"oid":"old"},
				"onBehalfOf":{"nodes":[{"combinedSlug":"acme/infra"}]}}
		]},
		"latestOpinionatedReviews":{"nodes":[
			{"state":"APPROVED","author":{"__typename":"User","login":"alice"},"commit":{"oid":"head"},
				"onBehalfOf":{"nodes":[{"combinedSlug":"acme/web"}]}}
		]},
		"reviewRequests":{"nodes":[]}
	}}}}`)
	list, err := decodeReviewers(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Reviewers) != 2 || list.AnswersUnknown {
		t.Fatalf("list = %+v", list)
	}
	if got := list.Reviewers[0].OnBehalfOf; !reflect.DeepEqual(got, []string{"acme/web", "acme/api"}) {
		t.Errorf("alice answered %q", got)
	}
	// A dismissed review answers no team.
	if got := list.Reviewers[1].OnBehalfOf; len(got) != 0 {
		t.Errorf("bob answered %q", got)
	}
}

func TestDecodeReviewersKeepsTheRestWhenAnsweredTeamsAreNotReadable(t *testing.T) {
	data := []byte(`{"errors":[{"message":"Your token has not been granted the required scopes","path":["repository","pullRequest","latestReviews","nodes",0,"onBehalfOf"]}],
	"data":{"repository":{"pullRequest":{
		"author":{"login":"me"},
		"headRefOid":"head",
		"latestReviews":{"nodes":[{"state":"APPROVED","author":{"__typename":"User","login":"alice"},"commit":{"oid":"head"},"onBehalfOf":null}]},
		"reviewRequests":{"nodes":[]}
	}}}}`)
	list, err := decodeReviewers(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Reviewers) != 1 || list.Reviewers[0].State != "APPROVED" || !list.AnswersUnknown {
		t.Fatalf("list = %+v", list)
	}
}

func TestDecodeReviewersNotesANulledAnsweredTeam(t *testing.T) {
	data := []byte(`{"errors":[{"message":"not visible","path":["repository","pullRequest","latestReviews","nodes",0,"onBehalfOf","nodes",0]}],
	"data":{"repository":{"pullRequest":{
		"author":{"login":"me"},
		"headRefOid":"head",
		"latestReviews":{"nodes":[{"state":"APPROVED","author":{"__typename":"User","login":"alice"},"commit":{"oid":"head"},
			"onBehalfOf":{"nodes":[null,{"combinedSlug":"acme/web"}]}}]},
		"reviewRequests":{"nodes":[]}
	}}}}`)
	list, err := decodeReviewers(data)
	if err != nil {
		t.Fatal(err)
	}
	if !list.AnswersUnknown || !reflect.DeepEqual(list.Reviewers[0].OnBehalfOf, []string{"acme/web"}) {
		t.Fatalf("list = %+v", list)
	}
}

func TestNudgePostsOneComment(t *testing.T) {
	var got []string
	c := &Client{path: "gh", api: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		got = args
		return []byte(`{}`), nil
	}}
	if err := c.Nudge(context.Background(), "acme/api", 7, NudgeNormal, []string{"alice"}, "please"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "--method POST repos/acme/api/issues/7/comments") {
		t.Fatalf("args = %q", got)
	}
	if slices.Contains(got, "-F") {
		t.Fatalf("the body went through -F, which reads files: %q", got)
	}
	i := slices.Index(got, "-f")
	if i < 0 || !strings.HasPrefix(got[i+1], "body=@alice ") || !strings.Contains(got[i+1], "urgency=normal to=alice") ||
		!strings.Contains(got[i+1], "> please") {
		t.Fatalf("body arg = %q", got)
	}
}

func TestNudgeRefusesBadTargetsWithoutCallingGh(t *testing.T) {
	calls := 0
	c := &Client{path: "gh", api: func(context.Context, string, ...string) ([]byte, error) { calls++; return nil, nil }}
	for _, tc := range []struct {
		repo   string
		number int
		logins []string
	}{{"acme", 7, []string{"alice"}}, {"acme/api", 0, []string{"alice"}}, {"acme/api", 7, []string{"bad login"}}, {"acme/api", 7, nil}} {
		if err := c.Nudge(context.Background(), tc.repo, tc.number, NudgeLow, tc.logins, ""); err == nil {
			t.Errorf("%+v: no error", tc)
		}
	}
	if calls != 0 {
		t.Fatalf("gh ran %d times for refused nudges", calls)
	}
}

func TestReviewersReadThePastNudges(t *testing.T) {
	body, _ := FormatNudge(NudgeUrgent, []string{"alice", "carol"}, "")
	quoted, _ := json.Marshal(body)
	data := `{"data":{"repository":{"pullRequest":{"author":{"login":"bob"},"headRefOid":"h",
		"latestReviews":{"nodes":[]},"latestOpinionatedReviews":{"nodes":[]},"reviewRequests":{"nodes":[
		{"asCodeOwner":false,"requestedReviewer":{"__typename":"User","login":"alice"}}]},
		"comments":{"nodes":[{"author":{"login":"bob"},"createdAt":"2026-10-01T10:00:00Z","body":` + string(quoted) + `},
		{"author":{"login":"mallory"},"createdAt":"2026-10-02T10:00:00Z","body":` + string(quoted) + `}]}}}}}`
	list, err := decodeReviewers([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	n, ok := list.Nudged["alice"]
	if !ok || n.Urgency != NudgeUrgent || !n.At.Equal(time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("alice's last nudge = %+v %v; only the author's comment counts", n, ok)
	}
}
