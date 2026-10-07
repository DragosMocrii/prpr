package github

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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

func TestRequestReviewsArgsPostEachLoginAsAField(t *testing.T) {
	got, err := requestReviewsArgs("POST", "acme/app", 12, []string{"alice", "bob-2"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"api", "--hostname", "github.com", "--method", "POST", "repos/acme/app/pulls/12/requested_reviewers",
		"-f", "reviewers[]=alice", "-f", "reviewers[]=bob-2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
	for name, call := range map[string]func() ([]string, error){
		"no logins":      func() ([]string, error) { return requestReviewsArgs("POST", "acme/app", 12, nil) },
		"flag login":     func() ([]string, error) { return requestReviewsArgs("POST", "acme/app", 12, []string{"-x"}) },
		"path login":     func() ([]string, error) { return requestReviewsArgs("POST", "acme/app", 12, []string{"a/b"}) },
		"bad repository": func() ([]string, error) { return requestReviewsArgs("POST", "acme/../x", 12, []string{"alice"}) },
		"bad number":     func() ([]string, error) { return requestReviewsArgs("POST", "acme/app", 0, []string{"alice"}) },
		"long login": func() ([]string, error) {
			return requestReviewsArgs("POST", "acme/app", 1, []string{strings.Repeat("a", 40)})
		},
		"empty repository": func() ([]string, error) { return requestReviewsArgs("POST", "", 1, []string{"alice"}) },
	} {
		if _, err := call(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRequestReviewsRunsAsThePinnedAccount(t *testing.T) {
	client, log := fakeGH(t, false)
	client.UseAccount("alice")
	if err := client.RequestReviews(context.Background(), "acme/app", 12, []string{"bob"}, nil); err != nil {
		t.Fatal(err)
	}
	got := calls(t, log)
	last := got[len(got)-1]
	if !strings.HasPrefix(last, "token="+fakeToken+" args=api --hostname github.com") {
		t.Fatalf("request = %q, want an API call as the pinned account", last)
	}
}

func TestRequestReviewsErrorsNeverContainTheToken(t *testing.T) {
	client, _ := fakeGH(t, true)
	client.UseAccount("alice")
	err := client.RequestReviews(context.Background(), "acme/app", 12, []string{"bob"}, []string{"bob"})
	if err == nil || strings.Contains(err.Error(), fakeToken) || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("err = %v", err)
	}
	if _, err := client.Reviewers(context.Background(), "acme/app", 12); err == nil || strings.Contains(err.Error(), fakeToken) {
		t.Fatalf("reviewers err = %v", err)
	}
}

// requestGH writes a gh stand-in that logs each call and fails the review
// requests that match fail, a shell pattern.
func requestGH(t *testing.T, fail string) (*Client, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
case "$*" in
  %s) echo "HTTP 422" >&2; exit 1 ;;
esac
echo '{}'
`, log, fail)
	path := filepath.Join(dir, "gh")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return &Client{path: path}, log
}

func TestRequestReviewsRenewsPendingRequests(t *testing.T) {
	const target = "api --hostname github.com --method %s repos/acme/app/pulls/12/requested_reviewers"
	client, log := requestGH(t, "no-match")
	if err := client.RequestReviews(context.Background(), "acme/app", 12, []string{"bob", "carol"}, []string{"carol"}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		fmt.Sprintf(target, "DELETE") + " -f reviewers[]=carol",
		fmt.Sprintf(target, "POST") + " -f reviewers[]=bob -f reviewers[]=carol",
	}
	if got := calls(t, log); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %q, want %q", got, want)
	}

	// A failed request makes the removed ones again.
	client, log = requestGH(t, `*"--method POST"*bob*`)
	err := client.RequestReviews(context.Background(), "acme/app", 12, []string{"bob", "carol"}, []string{"carol"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 422") || !strings.Contains(err.Error(), "as they were") {
		t.Fatalf("err = %v", err)
	}
	if got := calls(t, log); len(got) != 3 || got[2] != fmt.Sprintf(target, "POST")+" -f reviewers[]=carol" {
		t.Fatalf("calls = %q, want the removed request made again", got)
	}

	client, _ = requestGH(t, `*"--method POST"*`)
	err = client.RequestReviews(context.Background(), "acme/app", 12, []string{"carol"}, []string{"carol"})
	if err == nil || !strings.Contains(err.Error(), "are removed") {
		t.Fatalf("err = %v", err)
	}

	// Renewing a request that is not made again would only remove it.
	client, log = requestGH(t, "no-match")
	if err := client.RequestReviews(context.Background(), "acme/app", 12, []string{"bob"}, []string{"carol"}); err == nil {
		t.Fatal("renewed a request it does not make again")
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatalf("called gh: %q", calls(t, log))
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
