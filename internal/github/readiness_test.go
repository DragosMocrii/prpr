package github

import (
	"context"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestAuthoredQuerySelectsRuleFieldsOnlyWhenNeeded(t *testing.T) {
	plain := pullRequestsQuery(false, Needs{}, nil)
	for _, field := range []string{"codeOwnerRequests", "openThreads", "\n        id"} {
		if strings.Contains(plain, field) {
			t.Errorf("query without needs selects %q", field)
		}
	}
	all := pullRequestsQuery(false, Needs{CodeOwners: true, Threads: true, RequiredChecks: true}, nil)
	for _, field := range []string{"codeOwnerRequests: reviewRequests", "asCodeOwner", "openThreads: reviewThreads", "\n        id"} {
		if !strings.Contains(all, field) {
			t.Errorf("query with needs lacks %q", field)
		}
	}
	// Review panes never read rule fields.
	if strings.Contains(reviewRequestsQuery(false), "codeOwnerRequests") {
		t.Error("review request query selects rule fields")
	}
}

func TestDecodePagesReadsRuleFields(t *testing.T) {
	data := []byte(`[{"data":{"viewer":{"login":"alice","pullRequests":{"nodes":[
		{"number":1,"url":"https://github.com/acme/api/pull/1","repository":{"nameWithOwner":"acme/api"},"id":"PR_1",
		 "latestOpinionatedReviews":{"nodes":[{"state":"APPROVED"},{"state":"CHANGES_REQUESTED"}]},
		 "codeOwnerRequests":{"totalCount":3,"nodes":[
			{"asCodeOwner":true,"requestedReviewer":{"combinedSlug":"acme/core"}},
			{"asCodeOwner":false,"requestedReviewer":{"login":"bob"}},
			{"asCodeOwner":true,"requestedReviewer":{}}]},
		 "openThreads":{"totalCount":2,"nodes":[{"isResolved":true},{"isResolved":false}]}},
		{"number":2,"url":"https://github.com/acme/api/pull/2","repository":{"nameWithOwner":"acme/api"},
		 "codeOwnerRequests":{"totalCount":60,"nodes":[]},
		 "openThreads":{"totalCount":0,"nodes":[]}},
		{"number":3,"url":"https://github.com/acme/api/pull/3","repository":{"nameWithOwner":"acme/api"}}
	]}}}}]`)
	snapshot, err := decodePages(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, second, third := snapshot.PullRequests[0], snapshot.PullRequests[1], snapshot.PullRequests[2]
	if first.ID != "PR_1" || first.Approvals != 1 || first.ChangesRequested != 1 {
		t.Fatalf("first reviews: %+v", first)
	}
	// An unnamed pending code owner still counts.
	if !first.CodeOwnersKnown || !slices.Equal(first.PendingCodeOwners, []string{"@acme/core", ""}) {
		t.Fatalf("first code owners: %t %q", first.CodeOwnersKnown, first.PendingCodeOwners)
	}
	if !first.ThreadsKnown || first.UnresolvedThreads != 1 {
		t.Fatalf("first threads: %t %d", first.ThreadsKnown, first.UnresolvedThreads)
	}
	// More requests than were read, none of them pending: unknown.
	if second.CodeOwnersKnown || !second.ThreadsKnown || second.UnresolvedThreads != 0 {
		t.Fatalf("second: %+v", second)
	}
	if third.CodeOwnersKnown || third.ThreadsKnown {
		t.Fatalf("fields not selected are known: %+v", third)
	}
}

func TestRequiredStateFollowsOnlyRequiredChecks(t *testing.T) {
	node := func(total int, contexts string) []byte {
		return []byte(`{"data":{"p0":{"commits":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{"totalCount":` +
			strconv.Itoa(total) + `,"nodes":[` + contexts + `]}}}}]}}}}`)
	}
	run := func(name, status, conclusion string, required bool) string {
		return `{"name":"` + name + `","status":"` + status + `","conclusion":"` + conclusion + `","isRequired":` + boolText(required) + `}`
	}
	status := func(context, state string, required bool) string {
		return `{"context":"` + context + `","state":"` + state + `","isRequired":` + boolText(required) + `}`
	}
	cases := []struct {
		name      string
		data      []byte
		state     string
		notPassed []string
	}{
		{"optional failure", node(2, run("lint", "COMPLETED", "FAILURE", false)+","+run("build", "COMPLETED", "SUCCESS", true)), "SUCCESS", nil},
		{"skipped and neutral pass", node(2, run("a", "COMPLETED", "SKIPPED", true)+","+status("ci/x", "SUCCESS", true)), "SUCCESS", nil},
		{"required running", node(1, run("build", "IN_PROGRESS", "", true)), "PENDING", []string{"build"}},
		{"required status failed", node(2, status("ci/deploy", "ERROR", true)+","+run("build", "QUEUED", "", true)), "FAILURE", []string{"ci/deploy", "build"}},
		{"cut short", node(150, run("build", "COMPLETED", "SUCCESS", true)), "", nil},
		{"cut short with a failure", node(150, run("build", "COMPLETED", "TIMED_OUT", true)), "FAILURE", []string{"build"}},
		{"no checks", []byte(`{"data":{"p0":{"commits":{"nodes":[{"commit":{"statusCheckRollup":null}}]}}}}`), "SUCCESS", nil},
	}
	for _, c := range cases {
		nodes, err := decodeRequiredChecks(c.data, 1)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		state, notPassed := nodes[0].requiredState()
		if state != c.state || !slices.Equal(notPassed, c.notPassed) {
			t.Errorf("%s: %q %q, want %q %q", c.name, state, notPassed, c.state, c.notPassed)
		}
	}
	if _, err := decodeRequiredChecks([]byte(`{"data":null,"errors":[{"message":"nope"}]}`), 1); err == nil {
		t.Error("GraphQL errors were accepted")
	}
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// requiredGH patches the fake gh to list one authored pull request and
// answer the required checks query with answer, logging its arguments.
func requiredGH(t *testing.T, answer string) (*Client, string) {
	t.Helper()
	client, log := fakeGH(t, false)
	script, err := os.ReadFile(client.path)
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.Replace(string(script), "case \"$*\" in\n", "case \"$*\" in\n  *isRequired*) printf 'required %s\\n' \"$(printf '%s' \"$*\" | tr '\\n' ' ')\" >> "+
		strconvQuote(log)+"; "+answer+" ;;\n  \"api graphql\"*orderBy*) echo '[{\"data\":{\"viewer\":{\"login\":\"alice\",\"pullRequests\":{\"nodes\":["+
		"{\"number\":1,\"url\":\"https://github.com/acme/api/pull/1\",\"repository\":{\"nameWithOwner\":\"acme/api\"},\"id\":\"PR_1\",\"mergeable\":\"MERGEABLE\",\"mergeStateStatus\":\"BLOCKED\"},"+
		"{\"number\":2,\"url\":\"https://github.com/acme/api/pull/2\",\"repository\":{\"nameWithOwner\":\"acme/api\"},\"id\":\"PR_2\",\"isDraft\":true}"+
		"],\"pageInfo\":{\"hasNextPage\":false,\"endCursor\":null}}}}}]' ;;\n", 1)
	if err := os.WriteFile(client.path, []byte(patched), 0o700); err != nil {
		t.Fatal(err)
	}
	return client, log
}

func strconvQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func TestFetchReadsRequiredChecksOnlyWhenNeededAndFailsWithThem(t *testing.T) {
	answer := `echo '{"data":{"p0":{"commits":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{"totalCount":1,"nodes":[{"name":"build","status":"COMPLETED","conclusion":"FAILURE","isRequired":true}]}}}}]}}}}'`
	client, log := requiredGH(t, answer)
	ctx := context.Background()
	snapshot, err := client.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.PullRequests[0].RequiredChecks != "" || slices.ContainsFunc(calls(t, log), func(c string) bool { return strings.HasPrefix(c, "required") }) {
		t.Fatal("required checks were read without being needed")
	}
	client.SetNeeds(Needs{RequiredChecks: true})
	snapshot, err = client.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.PullRequests[0]; got.RequiredChecks != "FAILURE" || !slices.Equal(got.RequiredNotPassed, []string{"build"}) {
		t.Fatalf("required checks = %q %q", got.RequiredChecks, got.RequiredNotPassed)
	}
	// The draft is skipped, and the ID is a variable, not query text.
	var required []string
	for _, c := range calls(t, log) {
		if strings.HasPrefix(c, "required") {
			required = append(required, c)
		}
	}
	if len(required) != 1 || !strings.Contains(required[0], "id0=PR_1") || strings.Contains(required[0], "PR_2") {
		t.Fatalf("required check calls = %q", required)
	}
	if snapshot.PullRequests[1].RequiredChecks != "" {
		t.Fatal("the draft's required checks were read")
	}

	failing, _ := requiredGH(t, "echo required boom >&2; exit 1")
	failing.SetNeeds(Needs{RequiredChecks: true})
	if _, err := failing.Fetch(ctx); err == nil || !strings.Contains(err.Error(), "required boom") {
		t.Fatalf("fetch error = %v, want the required check query's failure", err)
	}
}
