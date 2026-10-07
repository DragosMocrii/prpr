package github

import (
	"context"
	"strings"
	"testing"
)

func mergedNode(id string, number int, mergedAt string) map[string]any {
	return map[string]any{"id": id, "number": number, "title": "PR " + id,
		"url": "https://github.com/acme/api/pull/" + strings.TrimPrefix(id, "PR_"), "mergedAt": mergedAt,
		"mergedBy": map[string]any{"login": "bob"}, "additions": 3, "deletions": 1,
		"repository": map[string]any{"nameWithOwner": "acme/api"}}
}

func TestMergedQuerySelectsOnlyItsFields(t *testing.T) {
	q := mergedQuery(5)
	for _, part := range []string{"first: 20", "states: [MERGED]", "orderBy: {field: UPDATED_AT, direction: DESC}", "mergedAt", "mergedBy { login }"} {
		if !strings.Contains(q, part) {
			t.Errorf("merged query lacks %q:\n%s", part, q)
		}
	}
	for _, slow := range []string{"mergeStateStatus", "timelineItems", "comments(", "statusCheckRollup", "$endCursor"} {
		if strings.Contains(q, slow) {
			t.Errorf("merged query selects %s", slow)
		}
	}
}

func TestDecodeMergedSortsByMergeTimeAndLimits(t *testing.T) {
	// Fetched by update time: #1 merged long ago but was commented on since.
	data := `{"data":{"viewer":{"login":"alice","pullRequests":{"nodes":[
	  {"id":"PR_1","number":1,"title":"old","url":"https://github.com/acme/api/pull/1","mergedAt":"2026-09-01T10:00:00Z","mergedBy":{"login":"bob"},"additions":1,"deletions":1,"repository":{"nameWithOwner":"acme/api"}},
	  null,
	  {"id":"PR_3","number":3,"title":"newest","url":"https://github.com/acme/api/pull/3","mergedAt":"2026-10-05T10:00:00Z","mergedBy":null,"additions":1,"deletions":1,"repository":{"nameWithOwner":"acme/api"}},
	  {"id":"PR_2","number":2,"title":"newer","url":"https://github.com/acme/api/pull/2","mergedAt":"2026-10-04T10:00:00Z","mergedBy":{"login":"bob"},"additions":1,"deletions":1,"repository":{"nameWithOwner":"acme/api"}}]}}}}`
	prs, err := decodeMerged([]byte(data), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 2 || prs[0].Number != 3 || prs[1].Number != 2 {
		t.Fatalf("merged %+v, want #3 then #2", prs)
	}
	if prs[0].MergedBy != "" || prs[1].MergedBy != "bob" || prs[1].Repository != "acme/api" || prs[1].MergedAt.IsZero() {
		t.Fatalf("fields %+v", prs)
	}
	for _, bad := range []string{`{"errors":[{"message":"boom"}]}`, `{"data":{"viewer":null}}`, `not json`} {
		if _, err := decodeMerged([]byte(bad), 2); err == nil {
			t.Errorf("decoded %s", bad)
		}
	}
}

func TestFetchRunsTheMergedQueryOnlyWhenEnabled(t *testing.T) {
	f := cleanFake(t)
	f.merged = []map[string]any{mergedNode("PR_7", 7, "2026-10-05T10:00:00Z")}
	c := f.client()
	snapshot, _ := fetch(t, f, c)
	if f.mergedQueries != 0 || snapshot.Merged != nil {
		t.Fatalf("off: %d queries, merged %+v", f.mergedQueries, snapshot.Merged)
	}
	c.SetMerged(5)
	// The cache from the first fetch makes this one incremental; ForceFull
	// makes the next complete. Both list the merged pull requests.
	for _, want := range []string{"signature", "complete"} {
		if want == "complete" {
			c.ForceFull()
		}
		snapshot, kind := fetch(t, f, c)
		if kind != want || len(snapshot.Merged) != 1 || snapshot.Merged[0].Number != 7 {
			t.Fatalf("%s fetch (ran %s) merged %+v", want, kind, snapshot.Merged)
		}
	}
	if f.mergedQueries != 2 {
		t.Fatalf("%d merged queries, want one per fetch", f.mergedQueries)
	}
}

func TestFailedMergedQueryFailsTheFetch(t *testing.T) {
	f := cleanFake(t)
	c := f.client()
	c.SetMerged(5)
	fetch(t, f, c)
	f.failMerged = true
	if _, err := c.Fetch(context.Background()); err == nil {
		t.Fatal("fetch succeeded without the merged list")
	}
	f.failMerged = false
	if _, kind := fetch(t, f, c); kind != "complete" {
		t.Fatalf("fetch after a failure was %s, want complete", kind)
	}
}

func TestPreviewNeverRunsTheMergedQuery(t *testing.T) {
	f := cleanFake(t)
	c := f.client()
	c.SetMerged(5)
	if _, err := c.Preview(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.mergedQueries != 0 {
		t.Fatalf("preview ran %d merged queries", f.mergedQueries)
	}
}
