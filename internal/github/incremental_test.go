package github

import (
	"context"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const fullAuthoredPage = `[{"data":{"viewer":{"login":"alice","pullRequests":{"nodes":[
  {"id":"PR_1","number":1,"title":"t","url":"https://github.com/acme/api/pull/1","isDraft":false,
   "mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","updatedAt":"2026-10-01T10:00:00Z",
   "createdAt":"2026-09-30T10:00:00Z","headRefOid":"h1","repository":{"nameWithOwner":"acme/api"},
   "commits":{"nodes":[{"commit":{"committedDate":"2026-10-01T09:00:00Z","statusCheckRollup":{"state":"PENDING"}}}]},
   "mergeQueueEntry":{"state":"QUEUED","position":2}}],
  "pageInfo":{"hasNextPage":false,"endCursor":null}}}}}]`

const sparseAuthoredPage = `[{"data":{"viewer":{"login":"alice","pullRequests":{"nodes":[
  {"id":"PR_1","updatedAt":"2026-10-01T10:00:00Z","headRefOid":"h1","mergeable":"MERGEABLE","isDraft":false,
   "commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"PENDING"}}}]},"mergeQueueEntry":{"state":"QUEUED"}}],
  "pageInfo":{"hasNextPage":false,"endCursor":null}}}}}]`

func TestSignatureReadsTheSameFromFullAndSparseNodes(t *testing.T) {
	full, err := decodePages([]byte(fullAuthoredPage), nil)
	if err != nil {
		t.Fatal(err)
	}
	sparse, err := decodePages([]byte(sparseAuthoredPage), nil)
	if err != nil {
		t.Fatal(err)
	}
	a, b := full.PullRequests[0], sparse.PullRequests[0]
	if a.ID != "PR_1" || !a.sig.same(b.sig) {
		t.Fatalf("full %q %+v, sparse %+v", a.ID, a.sig, b.sig)
	}
	for _, change := range [][2]string{
		{`"headRefOid":"h1"`, `"headRefOid":"h2"`},
		{`"updatedAt":"2026-10-01T10:00:00Z"`, `"updatedAt":"2026-10-01T11:00:00Z"`},
		{`"mergeable":"MERGEABLE"`, `"mergeable":"CONFLICTING"`},
		{`"isDraft":false`, `"isDraft":true`},
		{`{"state":"PENDING"}`, `{"state":"FAILURE"}`},
		{`{"state":"QUEUED"}`, `{"state":"MERGEABLE"}`},
	} {
		changed, err := decodePages([]byte(strings.Replace(sparseAuthoredPage, change[0], change[1], 1)), nil)
		if err != nil {
			t.Fatal(err)
		}
		if a.sig.same(changed.PullRequests[0].sig) {
			t.Errorf("signature ignores %s", change[1])
		}
	}
}

func TestFullQueriesSelectTheNodeIDAndHeadCommit(t *testing.T) {
	for _, q := range []string{pullRequestsQuery(false, Needs{}, nil), reviewRequestsQuery(false)} {
		if !strings.Contains(q, "\n        id\n") || !strings.Contains(q, "headRefOid") {
			t.Fatalf("query lacks id or headRefOid:\n%s", q)
		}
	}
}

func TestCompleteFetchThroughTheFakeKeepsListsAndOrder(t *testing.T) {
	f := newFakeAPI(t)
	f.authored = []map[string]any{prNode("PR_2", 2), prNode("PR_1", 1)}
	f.requests = []map[string]any{prNode("PR_10", 10)}
	unrequested := prNode("PR_21", 21)
	unrequested["requestEvents"] = map[string]any{"nodes": []any{}}
	f.reviewed = []map[string]any{prNode("PR_20", 20), unrequested}
	snapshot, err := f.client().Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Login != "alice" || len(snapshot.PullRequests) != 2 || snapshot.PullRequests[0].Number != 2 {
		t.Fatalf("authored %+v", snapshot.PullRequests)
	}
	var numbers []int
	for _, pr := range snapshot.ReviewRequests {
		numbers = append(numbers, pr.Number)
	}
	if !reflect.DeepEqual(numbers, []int{10, 20}) {
		t.Fatalf("review rows %v, want the request then the kept reviewed row", numbers)
	}
}

func TestSignatureQueriesSelectOnlyTheSignature(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	queries := []string{authoredSignatureQuery(nil), searchSignatureQuery(reviewSearch), searchSignatureQuery(reviewedSearch(now))}
	for _, q := range queries {
		for _, slow := range []string{"mergeStateStatus", "timelineItems", "reviewThreads", "reactions", "latestOpinionatedReviews", "comments("} {
			if strings.Contains(q, slow) {
				t.Errorf("signature query selects %s:\n%s", slow, q)
			}
		}
		for _, part := range []string{"first: 100", "id\n", "url\n", "updatedAt", "headRefOid", "mergeable", "isDraft", "statusCheckRollup { state }"} {
			if !strings.Contains(q, part) {
				t.Errorf("signature query lacks %q:\n%s", part, q)
			}
		}
	}
	if !strings.Contains(queries[1], `"`+reviewSearch+`"`) || !strings.Contains(queries[2], `"`+reviewedSearch(now)+`"`) {
		t.Fatal("signature searches differ from the full ones")
	}
	if strings.Contains(authoredSignatureQuery([]Queue{QueueTrunk}), "mergeQueueEntry") ||
		!strings.Contains(authoredSignatureQuery([]Queue{QueueGitHub}), "mergeQueueEntry { state }") {
		t.Fatal("mergeQueueEntry follows GitHub's queue alone")
	}
}

func TestDecodeSignaturesKeepsOrderAndSkipsEmptyNodes(t *testing.T) {
	login, authored, err := decodeAuthoredSignatures([]byte(sparseAuthoredPage))
	if err != nil || login != "alice" || len(authored) != 1 || authored[0].id != "PR_1" || authored[0].sig.headOid != "h1" {
		t.Fatalf("authored %q %+v %v", login, authored, err)
	}
	search := `[{"data":{"search":{"nodes":[{"id":"PR_3","url":"https://github.com/acme/api/pull/3","headRefOid":"a"},null,{},{"id":"PR_2","url":"https://github.com/acme/api/pull/2","headRefOid":"b"}],"pageInfo":{"hasNextPage":true,"endCursor":"x"}}}},
	            {"data":{"search":{"nodes":[{"id":"PR_1","url":"https://github.com/acme/api/pull/1","headRefOid":"c"}],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}]`
	entries, err := decodeSearchSignatures([]byte(search), "review request")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.id)
	}
	if !reflect.DeepEqual(ids, []string{"PR_3", "PR_2", "PR_1"}) {
		t.Fatalf("ids %v", ids)
	}
	if _, err := decodeSearchSignatures([]byte(`[{"errors":[{"message":"nope"}]}]`), "review request"); err == nil {
		t.Fatal("GraphQL errors accepted")
	}
}

func TestDetailQueryNamesEachIDWithTheListFields(t *testing.T) {
	q := detailQuery(2, "\n        number")
	for _, part := range []string{"query($id0: ID!, $id1: ID!)", "p0: node(id: $id0) { ... on PullRequest {", "p1: node(id: $id1)", "number"} {
		if !strings.Contains(q, part) {
			t.Fatalf("detail query lacks %q:\n%s", part, q)
		}
	}
}

func TestDecodeDetailsMapsAliasesAndRejectsMissingNodes(t *testing.T) {
	nodes, err := decodeDetails([]byte(`{"data":{"p1":{"number":2},"p0":{"number":1}}}`), []string{"PR_1", "PR_2"})
	if err != nil || nodes[0].Number != 1 || nodes[1].Number != 2 {
		t.Fatalf("nodes %v, %v", nodes, err)
	}
	if _, err := decodeDetails([]byte(`{"data":{"p0":{"number":1},"p1":null}}`), []string{"PR_1", "PR_2"}); err == nil || !strings.Contains(err.Error(), "PR_2") {
		t.Fatalf("null node: %v", err)
	}
	if _, err := decodeDetails([]byte(`{"errors":[{"message":"nope"}]}`), []string{"PR_1"}); err == nil {
		t.Fatal("GraphQL errors accepted")
	}
}

func TestDetailsBatchesIDs(t *testing.T) {
	f := newFakeAPI(t)
	var ids []string
	for i := range detailBatch + 5 {
		id := "PR_" + strconv.Itoa(i+1)
		ids = append(ids, id)
		f.authored = append(f.authored, prNode(id, i+1))
	}
	nodes, err := f.client().details(context.Background(), ids, pullRequestFields(false))
	if err != nil || len(nodes) != len(ids) || nodes[len(ids)-1].Number != len(ids) {
		t.Fatalf("%d nodes, %v", len(nodes), err)
	}
	if len(f.nodeIDs) != 2 || len(f.nodeIDs[0]) != detailBatch || len(f.nodeIDs[1]) != 5 {
		t.Fatalf("batches %v", f.nodeIDs)
	}
}

// fetch runs one fetch and returns it with the kind of the authored list
// query it ran: "complete" or "signature".
func fetch(t *testing.T, f *fakeAPI, c *Client) (Snapshot, string) {
	t.Helper()
	before := len(f.kinds)
	snapshot, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range f.kinds[before:] {
		if kind != "nodes" {
			return snapshot, kind
		}
	}
	t.Fatal("no list query ran")
	return snapshot, ""
}

func cleanFake(t *testing.T) *fakeAPI {
	f := newFakeAPI(t)
	f.authored = []map[string]any{prNode("PR_1", 1), prNode("PR_2", 2), prNode("PR_3", 3)}
	f.requests = []map[string]any{prNode("PR_10", 10)}
	f.reviewed = []map[string]any{prNode("PR_20", 20)}
	return f
}

func TestIncrementalFetchMatchesACompleteOne(t *testing.T) {
	f := cleanFake(t)
	c := f.client()
	first, kind := fetch(t, f, c)
	if kind != "complete" {
		t.Fatalf("first fetch %s", kind)
	}
	second, kind := fetch(t, f, c)
	if kind != "signature" || len(f.nodeIDs) != 0 || !reflect.DeepEqual(first, second) {
		t.Fatalf("unchanged incremental fetch: kind %s, detail %v, equal %v", kind, f.nodeIDs, reflect.DeepEqual(first, second))
	}

	f.authored[1]["headRefOid"] = "h-new"
	f.authored[1]["mergeStateStatus"] = "DIRTY"
	f.authored[1]["mergeable"] = "CONFLICTING"
	f.reviewed[0]["updatedAt"] = "2026-10-02T10:00:00Z"
	f.reviewed[0]["activity"] = map[string]any{"nodes": []any{map[string]any{"__typename": "PullRequestReview",
		"author": map[string]any{"login": "alice"}, "state": "APPROVED", "submittedAt": "2026-10-02T09:00:00Z", "commit": map[string]any{"oid": "h-PR_20"}}}}
	third, _ := fetch(t, f, c)
	fresh, _ := fetch(t, f, f.client())
	if !reflect.DeepEqual(third, fresh) {
		t.Fatalf("incremental snapshot differs from a complete one:\n%+v\n%+v", third, fresh)
	}
	var asked []string
	for _, ids := range f.nodeIDs {
		asked = append(asked, ids...)
	}
	slices.Sort(asked)
	if !reflect.DeepEqual(asked, []string{"PR_2", "PR_20"}) {
		t.Fatalf("detail fetched %v, want the changed pull requests", asked)
	}
}

func TestInTransitionPullRequestsAreFetchedAgain(t *testing.T) {
	f := cleanFake(t)
	f.authored[0]["mergeStateStatus"] = "UNKNOWN"
	f.authored[1]["commits"] = map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{
		"committedDate": "2026-10-01T09:00:00Z", "statusCheckRollup": map[string]any{"state": "PENDING"}}}}}
	c := f.client()
	fetch(t, f, c)
	fetch(t, f, c)
	if len(f.nodeIDs) != 1 || !reflect.DeepEqual(f.nodeIDs[0], []string{"PR_1", "PR_2"}) {
		t.Fatalf("detail fetched %v, want the unknown and pending pull requests", f.nodeIDs)
	}
}

func TestIncrementalFetchFollowsTheNewOrder(t *testing.T) {
	f := cleanFake(t)
	c := f.client()
	fetch(t, f, c)
	f.authored[0], f.authored[2] = f.authored[2], f.authored[0]
	snapshot, _ := fetch(t, f, c)
	if snapshot.PullRequests[0].Number != 3 || snapshot.PullRequests[2].Number != 1 || len(f.nodeIDs) != 0 {
		t.Fatalf("order %d…%d, detail %v", snapshot.PullRequests[0].Number, snapshot.PullRequests[2].Number, f.nodeIDs)
	}
}

func TestPullRequestMovingBetweenListsIsFetchedForItsNewList(t *testing.T) {
	f := cleanFake(t)
	c := f.client()
	fetch(t, f, c)
	moved := f.requests[0]
	f.requests = nil
	f.reviewed = append(f.reviewed, moved)
	snapshot, _ := fetch(t, f, c)
	if len(f.nodeIDs) != 1 || !reflect.DeepEqual(f.nodeIDs[0], []string{"PR_10"}) {
		t.Fatalf("detail fetched %v", f.nodeIDs)
	}
	fresh, _ := fetch(t, f, f.client())
	if !reflect.DeepEqual(snapshot, fresh) {
		t.Fatalf("moved row differs from a complete fetch")
	}
}

func TestDroppedReviewedPullRequestsAreCachedToo(t *testing.T) {
	f := cleanFake(t)
	unrequested := prNode("PR_21", 21)
	unrequested["requestEvents"] = map[string]any{"nodes": []any{}}
	f.reviewed = append(f.reviewed, unrequested)
	c := f.client()
	fetch(t, f, c)
	fetch(t, f, c)
	if len(f.nodeIDs) != 0 {
		t.Fatalf("detail fetched %v, want none", f.nodeIDs)
	}
}

func TestRequestedAgainIsRecomputedEachFetch(t *testing.T) {
	f := cleanFake(t)
	f.reviewed = append(f.reviewed, prNode("PR_10", 10)) // reviewed and requested again
	c := f.client()
	snapshot, _ := fetch(t, f, c)
	if !snapshot.ReviewRequests[0].RequestedAgain {
		t.Fatal("not requested again")
	}
	f.reviewed = f.reviewed[:1]
	snapshot, _ = fetch(t, f, c)
	if snapshot.ReviewRequests[0].RequestedAgain {
		t.Fatal("requested again stuck from the cache")
	}
}

func TestMissingNodeFailsTheFetchAndTheNextIsComplete(t *testing.T) {
	f := cleanFake(t)
	c := f.client()
	fetch(t, f, c)
	f.authored[0]["headRefOid"] = "h-new"
	f.gone["PR_1"] = true
	if _, err := c.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "PR_1") {
		t.Fatalf("err %v", err)
	}
	delete(f.gone, "PR_1")
	if _, kind := fetch(t, f, c); kind != "complete" {
		t.Fatalf("after a failure: %s", kind)
	}
}

func TestFailedDetailQueryDropsTheCache(t *testing.T) {
	f := cleanFake(t)
	c := f.client()
	fetch(t, f, c)
	f.authored[0]["headRefOid"] = "h-new"
	f.failNodes = true
	if _, err := c.Fetch(context.Background()); err == nil {
		t.Fatal("no error")
	}
	f.failNodes = false
	if _, kind := fetch(t, f, c); kind != "complete" {
		t.Fatalf("after a failure: %s", kind)
	}
}

func TestWhatMakesTheNextFetchComplete(t *testing.T) {
	for name, tc := range map[string]struct {
		act  func(c *Client, f *fakeAPI)
		want string
	}{
		"nothing":            {func(*Client, *fakeAPI) {}, "signature"},
		"ForceFull":          {func(c *Client, _ *fakeAPI) { c.ForceFull() }, "complete"},
		"UseAccount":         {func(c *Client, _ *fakeAPI) { c.UseAccount("") }, "complete"},
		"SetBots":            {func(c *Client, _ *fakeAPI) { c.SetBots(nil) }, "complete"},
		"SetQueues":          {func(c *Client, _ *fakeAPI) { c.SetQueues(nil) }, "complete"},
		"SetNeeds changed":   {func(c *Client, _ *fakeAPI) { c.SetNeeds(Needs{Threads: true}) }, "complete"},
		"SetNeeds unchanged": {func(c *Client, _ *fakeAPI) { c.SetNeeds(Needs{}) }, "signature"},
		"interval elapsed":   {func(c *Client, f *fakeAPI) { c.SetFullRefresh(15 * time.Minute); f.now = f.now.Add(15 * time.Minute) }, "complete"},
		"interval not yet":   {func(c *Client, f *fakeAPI) { c.SetFullRefresh(15 * time.Minute); f.now = f.now.Add(14 * time.Minute) }, "signature"},
		"never":              {func(c *Client, f *fakeAPI) { c.SetFullRefresh(0); f.now = f.now.Add(24 * time.Hour) }, "signature"},
	} {
		t.Run(name, func(t *testing.T) {
			f := cleanFake(t)
			c := f.client()
			fetch(t, f, c)
			tc.act(c, f)
			if _, kind := fetch(t, f, c); kind != tc.want {
				t.Fatalf("next fetch %s, want %s", kind, tc.want)
			}
		})
	}
}

func TestAFetchThatStartedBeforeADropDoesNotWriteTheCache(t *testing.T) {
	f := cleanFake(t)
	c := f.client()
	// The three list queries run at once, so the drop happens once.
	var once sync.Once
	f.onCall = func(kind string) {
		if kind == "complete" {
			once.Do(c.ForceFull)
		}
	}
	fetch(t, f, c)
	if _, kind := fetch(t, f, c); kind != "complete" {
		t.Fatalf("a dropped cache was written: next fetch %s", kind)
	}
}

func TestRequiredChecksRunOnlyForRefetchedAuthoredPullRequests(t *testing.T) {
	f := cleanFake(t)
	c := f.client()
	c.SetNeeds(Needs{RequiredChecks: true})
	fetch(t, f, c)
	if len(f.requiredIDs) != 1 || len(f.requiredIDs[0]) != 3 {
		t.Fatalf("complete fetch required checks %v", f.requiredIDs)
	}
	f.authored[2]["headRefOid"] = "h-new"
	snapshot, _ := fetch(t, f, c)
	if len(f.requiredIDs) != 2 || !reflect.DeepEqual(f.requiredIDs[1], []string{"PR_3"}) {
		t.Fatalf("incremental required checks %v", f.requiredIDs)
	}
	for _, pr := range snapshot.PullRequests {
		if pr.RequiredChecks != "SUCCESS" {
			t.Fatalf("#%d required checks %q, want kept or fresh", pr.Number, pr.RequiredChecks)
		}
	}
}
