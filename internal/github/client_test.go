package github

import (
	"strings"
	"testing"
	"time"
)

func TestDecodePagesPreservesAllRowsAndOrder(t *testing.T) {
	data := []byte(`[
		{"data":{"viewer":{"login":"octocat","pullRequests":{"nodes":[
			{"number":12,"title":"Newest","url":"https://github.com/acme/one/pull/12","isDraft":true,"mergeable":"MERGEABLE","updatedAt":"2026-06-01T12:00:00Z","repository":{"nameWithOwner":"acme/one"}},
			null,
			{"number":8,"title":"Second","url":"https://github.com/acme/two/pull/8","isDraft":false,"mergeable":"CONFLICTING","mergeStateStatus":"DIRTY","updatedAt":"2026-05-31T12:00:00Z","repository":{"nameWithOwner":"acme/two"}}
		],"pageInfo":{"hasNextPage":true,"endCursor":"cursor"}}}}},
		{"data":{"viewer":{"login":"octocat","pullRequests":{"nodes":[
			{"number":4,"title":"Oldest","url":"https://github.com/acme/one/pull/4","isDraft":false,"mergeable":"UNKNOWN","updatedAt":"2026-05-30T12:00:00Z","repository":{"nameWithOwner":"acme/one"}}
		],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}}
	]`)

	snapshot, err := decodePages(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Login != "octocat" {
		t.Fatalf("login = %q, want octocat", snapshot.Login)
	}
	if len(snapshot.PullRequests) != 3 {
		t.Fatalf("got %d pull requests, want 3", len(snapshot.PullRequests))
	}
	want := []PullRequest{
		{Number: 12, Title: "Newest", Repository: "acme/one", Draft: true, Mergeable: "MERGEABLE", UpdatedAt: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)},
		{Number: 8, Title: "Second", Repository: "acme/two", Mergeable: "CONFLICTING", MergeState: "DIRTY", UpdatedAt: time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)},
		{Number: 4, Title: "Oldest", Repository: "acme/one", Mergeable: "UNKNOWN", UpdatedAt: time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)},
	}
	for i, expected := range want {
		got := snapshot.PullRequests[i]
		if got.Number != expected.Number || got.Title != expected.Title || got.Repository != expected.Repository || got.Draft != expected.Draft || got.Mergeable != expected.Mergeable || got.MergeState != expected.MergeState || !got.UpdatedAt.Equal(expected.UpdatedAt) {
			t.Errorf("pull request %d = %+v, want %+v", i, got, expected)
		}
	}
	if !snapshot.PullRequests[0].Draft || snapshot.PullRequests[0].Mergeable != "MERGEABLE" {
		t.Fatalf("draft and mergeability were not independent: %+v", snapshot.PullRequests[0])
	}
}

func TestDecodePagesAcceptsEmptyConnection(t *testing.T) {
	snapshot, err := decodePages([]byte(`[{"data":{"viewer":{"login":"octocat","pullRequests":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}]`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Login != "octocat" || len(snapshot.PullRequests) != 0 {
		t.Fatalf("snapshot = %+v, want authenticated user with no PRs", snapshot)
	}
}

func TestDecodePagesRejectsInvalidOrPartialResponses(t *testing.T) {
	cases := map[string]string{
		"malformed JSON":      `[`,
		"empty pages":         `[]`,
		"null viewer":         `[{"data":{"viewer":null}}]`,
		"missing viewer":      `[{"data":{}}]`,
		"null connection":     `[{"data":{"viewer":{"login":"octocat","pullRequests":null}}}]`,
		"missing connection":  `[{"data":{"viewer":{"login":"octocat"}}}]`,
		"later GraphQL error": `[{"data":{"viewer":{"login":"octocat","pullRequests":{"nodes":[{"number":1}]}}}},{"errors":[{"message":"rate limited"}]}]`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			snapshot, err := decodePages([]byte(input), nil)
			if err == nil {
				t.Fatalf("decodePages() = %+v, want error", snapshot)
			}
			if snapshot.Login != "" || len(snapshot.PullRequests) != 0 {
				t.Fatalf("partial snapshot returned on error: %+v", snapshot)
			}
		})
	}
}

func TestDecodeRepositoryPagesSortsAndDeduplicatesNames(t *testing.T) {
	data := []byte(`[
		[{"full_name":"zeta/last"},{"full_name":"Acme/Repo"},{"full_name":"acme/repo"}],
		[{"full_name":"acme/empty"},{"full_name":"Beta/One"}]
	]`)
	got, err := decodeRepositoryPages(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"acme/empty", "Acme/Repo", "Beta/One", "zeta/last"}
	if len(got) != len(want) {
		t.Fatalf("repositories = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("repositories = %v, want %v", got, want)
		}
	}
}

func TestDecodeRepositoryPagesAcceptsEmptyListing(t *testing.T) {
	got, err := decodeRepositoryPages([]byte(`[[]]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("repositories = %v, want empty listing", got)
	}
}

func TestDecodeRepositoryPagesRejectsInvalidOrPartialResponses(t *testing.T) {
	for name, input := range map[string]string{
		"malformed":       `[`,
		"empty pages":     `[]`,
		"null outer":      `null`,
		"null page":       `[null]`,
		"object page":     `[{}]`,
		"null repository": `[[null]]`,
		"missing name":    `[[{}]]`,
		"null name":       `[[{"full_name":null}]]`,
		"empty name":      `[[{"full_name":""}]]`,
		"invalid name":    `[[{"full_name":"owner/repo/extra"}]]`,
		"later invalid":   `[[{"full_name":"valid/repo"}],[{"full_name":""}]]`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := decodeRepositoryPages([]byte(input))
			if err == nil {
				t.Fatalf("decodeRepositoryPages() = %v, want error", got)
			}
			if got != nil {
				t.Fatalf("partial repositories returned: %v", got)
			}
		})
	}
}

func TestDecodeRepositoryReturnsCanonicalName(t *testing.T) {
	got, err := decodeRepository([]byte(`{"id":42,"full_name":"Canonical/Name"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "Canonical/Name" {
		t.Fatalf("repository = %q, want Canonical/Name", got)
	}
	for _, input := range []string{`{`, `null`, `{}`, `{"full_name":null}`, `{"full_name":"bad/name/extra"}`} {
		if got, err := decodeRepository([]byte(input)); err == nil || got != "" {
			t.Errorf("decodeRepository(%s) = %q, %v; want error and empty result", input, got, err)
		}
	}
}

func TestValidRepositoryName(t *testing.T) {
	for _, name := range []string{"owner/repo", "Org_Name/repo.name-1", "a.b/_repo"} {
		if !ValidRepositoryName(name) {
			t.Errorf("ValidRepositoryName(%q) = false", name)
		}
	}
	for _, name := range []string{
		"", "owner", "/repo", "owner/", "owner/repo/extra", "../repo", "owner/..",
		"./repo", "owner/.", "https://github.com/owner/repo", "owner/repo?tab=x",
		"owner/repo#issues", "owner/repo\\name", "owner/re po", "owner/ repo", "owner/…",
	} {
		if ValidRepositoryName(name) {
			t.Errorf("ValidRepositoryName(%q) = true", name)
		}
	}
}

func TestDecodeReviewPagesPreservesOrderAndSkipsNonPullRequests(t *testing.T) {
	data := []byte(`[
		{"data":{"search":{"nodes":[
			{"number":45,"title":"Bump deps","url":"https://github.com/acme/api/pull/45","isDraft":false,"mergeable":"MERGEABLE","mergeStateStatus":"BLOCKED","updatedAt":"2026-06-02T12:00:00Z","author":{"login":"bob"},"repository":{"nameWithOwner":"acme/api"}},
			null,
			{},
			{"number":9,"title":"Ghost","url":"https://github.com/acme/lib/pull/9","isDraft":true,"mergeable":"UNKNOWN","updatedAt":"2026-06-01T12:00:00Z","author":null,"repository":{"nameWithOwner":"acme/lib"}}
		],"pageInfo":{"hasNextPage":true,"endCursor":"c1"}}}},
		{"data":{"search":{"nodes":[
			{"number":3,"title":"Oldest","url":"https://github.com/acme/api/pull/3","isDraft":false,"mergeable":"CONFLICTING","updatedAt":"2026-05-01T12:00:00Z","author":{"login":"carol"},"repository":{"nameWithOwner":"acme/api"}}
		],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}
	]`)
	prs, err := decodeReviewPages(data, "octocat", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []PullRequest{
		{Number: 45, Title: "Bump deps", URL: "https://github.com/acme/api/pull/45", Repository: "acme/api", Mergeable: "MERGEABLE", MergeState: "BLOCKED", Author: "bob", UpdatedAt: time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)},
		{Number: 9, Title: "Ghost", URL: "https://github.com/acme/lib/pull/9", Repository: "acme/lib", Draft: true, Mergeable: "UNKNOWN", Author: "", UpdatedAt: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)},
		{Number: 3, Title: "Oldest", URL: "https://github.com/acme/api/pull/3", Repository: "acme/api", Mergeable: "CONFLICTING", Author: "carol", UpdatedAt: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)},
	}
	if len(prs) != len(want) {
		t.Fatalf("got %d review requests, want %d: %+v", len(prs), len(want), prs)
	}
	for i, expected := range want {
		got := prs[i]
		if got.Number != expected.Number || got.Title != expected.Title || got.URL != expected.URL || got.Repository != expected.Repository || got.Draft != expected.Draft || got.Mergeable != expected.Mergeable || got.MergeState != expected.MergeState || got.Author != expected.Author || !got.UpdatedAt.Equal(expected.UpdatedAt) {
			t.Errorf("review request %d = %+v, want %+v", i, got, expected)
		}
	}
}

func TestDecodeReviewPagesAcceptsEmptySearch(t *testing.T) {
	prs, err := decodeReviewPages([]byte(`[{"data":{"search":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}]`), "octocat", nil)
	if err != nil || len(prs) != 0 {
		t.Fatalf("empty search = %+v, %v", prs, err)
	}
}

func TestDecodeReviewPagesRejectsInvalidOrPartialResponses(t *testing.T) {
	for name, data := range map[string]string{
		"invalid JSON":   `not json`,
		"no pages":       `[]`,
		"GraphQL errors": `[{"data":{"search":{"nodes":[],"pageInfo":{"hasNextPage":false}}},"errors":[{"message":"rate limited"}]}]`,
		"missing search": `[{"data":{}}]`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeReviewPages([]byte(data), "octocat", nil); err == nil {
				t.Fatal("decode succeeded")
			}
		})
	}
}

func TestDecodePagesStatistics(t *testing.T) {
	data := []byte(`[{"data":{"viewer":{"login":"octocat","pullRequests":{"nodes":[
		{"number":1,"url":"https://github.com/acme/api/pull/1","isDraft":false,"createdAt":"2026-06-01T00:00:00Z","readyEvents":{"nodes":[]},
		 "reviewDecision":"APPROVED","latestOpinionatedReviews":{"nodes":[{"state":"APPROVED"},{"state":"COMMENTED"},{"state":"APPROVED"}]},
		 "additions":120,"deletions":30,"commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"FAILURE"}}}]}},
		{"number":2,"url":"https://github.com/acme/api/pull/2","isDraft":false,"createdAt":"2026-06-01T00:00:00Z","readyEvents":{"nodes":[{"createdAt":"2026-06-03T00:00:00Z"}]},
		 "reviewDecision":null,"latestOpinionatedReviews":null,"commits":{"nodes":[{"commit":{"statusCheckRollup":null}}]}},
		{"number":3,"url":"https://github.com/acme/api/pull/3","isDraft":true,"createdAt":"2026-06-01T00:00:00Z","readyEvents":{"nodes":[{"createdAt":"2026-06-03T00:00:00Z"}]},"commits":{"nodes":[]}}
	]}}}}]`)
	snapshot, err := decodePages(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	prs := snapshot.PullRequests
	if len(prs) != 3 {
		t.Fatalf("got %d pull requests", len(prs))
	}
	never, ready, draft := prs[0], prs[1], prs[2]
	if !never.WaitingSince.Equal(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)) || never.ReviewDecision != "APPROVED" || never.Approvals != 2 ||
		never.Additions != 120 || never.Deletions != 30 || never.Checks != "FAILURE" {
		t.Errorf("never-draft PR = %+v", never)
	}
	if !ready.WaitingSince.Equal(time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)) || ready.ReviewDecision != "" || ready.Approvals != 0 || ready.Checks != "" {
		t.Errorf("ready-after-draft PR = %+v", ready)
	}
	if !draft.WaitingSince.IsZero() || draft.Checks != "" {
		t.Errorf("draft PR = %+v", draft)
	}
}

func TestDecodeReviewPagesWaitsSinceViewerRequest(t *testing.T) {
	data := []byte(`[{"data":{"search":{"nodes":[
		{"number":1,"url":"https://github.com/acme/api/pull/1","isDraft":false,"createdAt":"2026-06-01T00:00:00Z","readyEvents":{"nodes":[]},
		 "requestEvents":{"nodes":[
			{"createdAt":"2026-06-02T00:00:00Z","requestedReviewer":{"login":"OctoCat"}},
			{"createdAt":"2026-06-03T00:00:00Z","requestedReviewer":{"login":"bob"}},
			{"createdAt":"2026-06-04T00:00:00Z","requestedReviewer":{"login":"octocat"}},
			{"createdAt":"2026-06-05T00:00:00Z","requestedReviewer":{}}
		 ]}},
		{"number":2,"url":"https://github.com/acme/api/pull/2","isDraft":false,"createdAt":"2026-06-01T00:00:00Z","readyEvents":{"nodes":[{"createdAt":"2026-06-02T00:00:00Z"}]},
		 "requestEvents":{"nodes":[{"createdAt":"2026-06-03T00:00:00Z","requestedReviewer":{}},{"createdAt":"2026-06-04T00:00:00Z","requestedReviewer":null}]}},
		{"number":3,"url":"https://github.com/acme/api/pull/3","isDraft":true,"createdAt":"2026-06-01T00:00:00Z",
		 "requestEvents":{"nodes":[{"createdAt":"2026-06-04T00:00:00Z","requestedReviewer":{"login":"octocat"}}]}}
	]}}}]`)
	prs, err := decodeReviewPages(data, "octocat", nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []time.Time{
		time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC), // latest direct request, matched case-insensitively
		time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC), // no direct request event falls back to ready time
		time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC), // a direct request on a draft still counts
	} {
		if !prs[i].WaitingSince.Equal(want) {
			t.Errorf("review request %d waiting since %v, want %v", i, prs[i].WaitingSince, want)
		}
	}
}

func TestDecodeRateLimitReadsGraphQLPool(t *testing.T) {
	got, err := decodeRateLimit([]byte(`{"data":{"rateLimit":{"limit":5000,"remaining":4579,"resetAt":"2026-10-01T16:17:30Z"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Limit != 5000 || got.Remaining != 4579 || !got.Reset.Equal(time.Date(2026, 10, 1, 16, 17, 30, 0, time.UTC)) {
		t.Fatalf("rate limit = %+v", got)
	}
}

func TestDecodeRateLimitRejectsInvalidResponses(t *testing.T) {
	for name, data := range map[string]string{
		"invalid JSON":      `not json`,
		"missing data":      `{}`,
		"null rate limit":   `{"data":{"rateLimit":null}}`,
		"GraphQL error":     `{"data":{"rateLimit":{"limit":5000,"remaining":1,"resetAt":"2026-10-01T16:17:30Z"}},"errors":[{"message":"boom"}]}`,
		"missing remaining": `{"data":{"rateLimit":{"limit":5000,"resetAt":"2026-10-01T16:17:30Z"}}}`,
		"missing reset":     `{"data":{"rateLimit":{"limit":5000,"remaining":1}}}`,
		"zero limit":        `{"data":{"rateLimit":{"limit":0,"remaining":0,"resetAt":"2026-10-01T16:17:30Z"}}}`,
		"negative":          `{"data":{"rateLimit":{"limit":5000,"remaining":-1,"resetAt":"2026-10-01T16:17:30Z"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeRateLimit([]byte(data)); err == nil {
				t.Fatal("decode succeeded")
			}
		})
	}
}

func TestReviewRequestsQueryExcludesTeamRequests(t *testing.T) {
	for _, bots := range []bool{false, true} {
		query := reviewRequestsQuery(bots)
		if !strings.Contains(query, `"is:pr is:open user-review-requested:@me archived:false sort:updated-desc"`) ||
			strings.Contains(query, " review-requested:") {
			t.Errorf("review query does not ask for direct requests only:\n%s", query)
		}
	}
}
