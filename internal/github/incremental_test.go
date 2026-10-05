package github

import (
	"context"
	"reflect"
	"strings"
	"testing"
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
