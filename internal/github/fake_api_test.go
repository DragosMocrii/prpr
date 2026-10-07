package github

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAPI answers prpr's gh commands in process from one data set, so the
// complete, signature, and detail queries all see the same pull requests.
// Each list holds full node JSON; signature queries get the same JSON, whose
// extra fields the sparse decoding ignores.
type fakeAPI struct {
	t                            *testing.T
	mu                           sync.Mutex
	authored, requests, reviewed []map[string]any
	// gone makes detail queries answer null for these IDs.
	gone map[string]bool
	// failNodes makes detail queries fail.
	failNodes bool
	// failSignature makes signature list queries fail.
	failSignature bool
	// login is the viewer's login.
	login string
	// onCall runs at the start of each list or detail query with its kind.
	onCall func(kind string)
	// kinds records "complete" or "signature" per authored list query, and
	// "nodes" per detail query, in order.
	kinds       []string
	nodeIDs     [][]string
	requiredIDs [][]string
	now         time.Time
	// merged answers the merged query; failMerged makes it fail, and
	// mergedQueries counts it.
	merged        []map[string]any
	failMerged    bool
	mergedQueries int
}

func newFakeAPI(t *testing.T) *fakeAPI {
	return &fakeAPI{t: t, login: "alice", gone: map[string]bool{}, now: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
}

func (f *fakeAPI) client() *Client {
	return &Client{path: "gh", api: f.call, clock: func() time.Time { return f.now }}
}

// prNode is a clean, mergeable, passing pull request requesting alice's
// review, with no activity of hers.
func prNode(id string, number int) map[string]any {
	return map[string]any{
		"id": id, "number": number, "title": "PR " + id, "url": "https://github.com/acme/api/pull/" + strings.TrimPrefix(id, "PR_"),
		"isDraft": false, "mergeable": "MERGEABLE", "mergeStateStatus": "CLEAN",
		"updatedAt": "2026-10-01T10:00:00Z", "createdAt": "2026-09-30T10:00:00Z", "headRefOid": "h-" + id,
		"additions": 1, "deletions": 1, "reviewDecision": nil, "commentCount": map[string]any{"totalCount": 0},
		"repository": map[string]any{"nameWithOwner": "acme/api"}, "author": map[string]any{"login": "bob"},
		"readyEvents": map[string]any{"nodes": []any{}}, "latestOpinionatedReviews": map[string]any{"nodes": []any{}},
		"commits": map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{
			"committedDate": "2026-10-01T09:00:00Z", "statusCheckRollup": map[string]any{"state": "SUCCESS"}}}}},
		"requestEvents": map[string]any{"nodes": []any{map[string]any{"createdAt": "2026-10-01T08:00:00Z",
			"requestedReviewer": map[string]any{"login": "alice"}}}},
		"activity": map[string]any{"nodes": []any{}},
	}
}

func page(key string, nodes []map[string]any, login string) []any {
	connection := map[string]any{"nodes": nodes, "pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil}}
	if login != "" {
		return []any{map[string]any{"data": map[string]any{"viewer": map[string]any{"login": login, key: connection}}}}
	}
	return []any{map[string]any{"data": map[string]any{key: connection}}}
}

func (f *fakeAPI) call(_ context.Context, _ string, args ...string) ([]byte, error) {
	if len(args) > 0 && args[0] == "auth" {
		return nil, nil
	}
	var query string
	var ids []string
	for _, arg := range args {
		if q, ok := strings.CutPrefix(arg, "query="); ok {
			query = q
		} else if name, value, ok := strings.Cut(arg, "="); ok && strings.HasPrefix(name, "id") {
			ids = append(ids, value)
		}
	}
	if strings.Contains(query, "states: [MERGED]") {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.mergedQueries++
		if f.failMerged {
			return nil, errors.New("GitHub merged pull request query failed: boom")
		}
		return json.Marshal(map[string]any{"data": map[string]any{"viewer": map[string]any{"login": f.login,
			"pullRequests": map[string]any{"nodes": f.merged}}}})
	}
	kind := "signature"
	switch {
	case strings.Contains(query, "isRequired"):
		kind = "required"
	case strings.Contains(query, "node(id:"):
		kind = "nodes"
	case strings.Contains(query, "mergeStateStatus"):
		kind = "complete"
	}
	if f.onCall != nil && kind != "required" {
		f.onCall(kind)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if kind == "signature" && f.failSignature {
		return nil, errors.New("GitHub list query failed: boom")
	}
	var data any
	switch {
	case kind == "required":
		f.requiredIDs = append(f.requiredIDs, ids)
		answer := map[string]any{}
		for i := range ids {
			answer["p"+strconv.Itoa(i)] = map[string]any{"commits": map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{
				"statusCheckRollup": map[string]any{"contexts": map[string]any{"totalCount": 1, "nodes": []any{
					map[string]any{"name": "build", "status": "COMPLETED", "conclusion": "SUCCESS", "isRequired": true}}}}}}}}}
		}
		data = map[string]any{"data": answer}
	case kind == "nodes":
		f.kinds = append(f.kinds, kind)
		f.nodeIDs = append(f.nodeIDs, ids)
		if f.failNodes {
			return nil, errors.New("GitHub pull request detail query failed: boom")
		}
		answer := map[string]any{}
		for i, id := range ids {
			answer["p"+strconv.Itoa(i)] = f.find(id)
		}
		data = map[string]any{"data": answer}
	case strings.HasPrefix(query, "query($endCursor: String) {\n  viewer {"):
		f.kinds = append(f.kinds, kind)
		data = page("pullRequests", f.authored, f.login)
	case strings.Contains(query, "reviewed-by:@me"):
		data = page("search", f.reviewed, "")
	default:
		data = page("search", f.requests, "")
	}
	return json.Marshal(data)
}

// find returns the node with id, or nil when it is gone or unknown.
func (f *fakeAPI) find(id string) any {
	if f.gone[id] {
		return nil
	}
	for _, list := range [][]map[string]any{f.authored, f.requests, f.reviewed} {
		for _, node := range list {
			if node["id"] == id {
				return node
			}
		}
	}
	return nil
}
