package github

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// reviewedNode is a reviewed-search node by bob, requested from octocat
// unless requests says otherwise, at head "h2".
func reviewedNode(number int, draft bool, requests, activity string) string {
	if requests == "" {
		requests = `{"createdAt":"2026-06-01T00:00:00Z","requestedReviewer":{"login":"OctoCat"}}`
	}
	return fmt.Sprintf(`{"number":%d,"url":"https://github.com/acme/api/pull/%d","isDraft":%t,"updatedAt":"2026-06-09T00:00:00Z",
		"author":{"login":"bob"},"repository":{"nameWithOwner":"acme/api"},"headRefOid":"h2",
		"commits":{"nodes":[{"commit":{"committedDate":"2026-06-03T00:00:00Z"}}]},
		"requestEvents":{"nodes":[%s]},"activity":{"nodes":[%s]}}`, number, number, draft, requests, activity)
}

func review(login, state, at, oid string) string {
	return fmt.Sprintf(`{"__typename":"PullRequestReview","author":{"login":%q},"state":%q,"submittedAt":%q,"commit":{"oid":%q}}`, login, state, at, oid)
}

func comment(login, at string) string {
	return fmt.Sprintf(`{"__typename":"IssueComment","author":{"login":%q},"createdAt":%q}`, login, at)
}

func day(d int) time.Time { return time.Date(2026, 6, d, 0, 0, 0, 0, time.UTC) }

func at(d int) string { return day(d).Format(time.RFC3339) }

func TestDecodeReviewedPagesPlacesEachPullRequest(t *testing.T) {
	cases := []struct {
		name     string
		draft    bool
		requests string
		activity []string
		want     ReviewStatus
		since    time.Time
	}{
		{name: "changes requested", activity: []string{review("octocat", "CHANGES_REQUESTED", at(4), "h2")},
			want: ReviewWaitingOnAuthor, since: day(4)},
		{name: "commented", activity: []string{review("octocat", "COMMENTED", at(4), "h2")},
			want: ReviewWaitingOnAuthor, since: day(4)},
		{name: "approved, then a thread reply", activity: []string{review("octocat", "APPROVED", at(4), "h2"), review("octocat", "COMMENTED", at(5), "h1")},
			want: ReviewApproved, since: day(5)},
		{name: "rebased after the review", activity: []string{review("octocat", "APPROVED", at(4), "h1")},
			want: ReviewNewCommits, since: day(4)},
		{name: "reviewed an earlier head and the current one", activity: []string{review("octocat", "COMMENTED", at(2), "h1"), review("octocat", "COMMENTED", at(4), "h2")},
			want: ReviewWaitingOnAuthor, since: day(4)},
		{name: "author commented after the review", activity: []string{review("octocat", "COMMENTED", at(4), "h2"), comment("bob", at(6))},
			want: ReviewAuthorReplied, since: day(6)},
		{name: "author replied in a thread", activity: []string{review("octocat", "CHANGES_REQUESTED", at(4), "h2"), review("bob", "COMMENTED", at(6), "h2")},
			want: ReviewAuthorReplied, since: day(6)},
		{name: "viewer answered the author", activity: []string{review("octocat", "COMMENTED", at(4), "h2"), comment("bob", at(5)), comment("octocat", at(6))},
			want: ReviewWaitingOnAuthor, since: day(6)},
		{name: "another reviewer commented", activity: []string{review("octocat", "COMMENTED", at(4), "h2"), comment("carol", at(6)), review("carol", "APPROVED", at(7), "h2")},
			want: ReviewWaitingOnAuthor, since: day(4)},
		{name: "review dismissed", activity: []string{review("octocat", "DISMISSED", at(4), "h2")},
			want: ReviewDismissed, since: day(4)},
		{name: "commented after a dismissal", activity: []string{review("octocat", "DISMISSED", at(4), "h1"), review("octocat", "COMMENTED", at(5), "h2")},
			want: ReviewWaitingOnAuthor, since: day(5)},
		{name: "back in draft", draft: true, activity: []string{review("octocat", "COMMENTED", at(4), "h1")},
			want: ReviewBackInDraft, since: day(4)},
		{name: "a pending review is not a review", activity: []string{review("octocat", "COMMENTED", at(4), "h1"),
			`{"__typename":"PullRequestReview","author":{"login":"octocat"},"state":"PENDING","submittedAt":null,"commit":{"oid":"h2"}}`},
			want: ReviewNewCommits, since: day(4)},
		{name: "no review among the items read", activity: []string{comment("octocat", at(4)), comment("bob", at(5))},
			want: ReviewNewActivity, since: day(9)},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := `[{"data":{"search":{"nodes":[` + reviewedNode(i+1, c.draft, c.requests, strings.Join(c.activity, ",")) + `]}}}]`
			prs, err := decodeReviewedPages([]byte(data), "octocat", nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(prs) != 1 || prs[0].ReviewStatus != c.want || !prs[0].WaitingSince.Equal(c.since) {
				t.Fatalf("got %+v, want status %d since %v", prs, c.want, c.since)
			}
		})
	}
}

func TestDecodeReviewedPagesSkipsReviewsThatWereNeverRequestedDirectly(t *testing.T) {
	mine := review("octocat", "COMMENTED", at(4), "h2")
	data := `[{"data":{"search":{"nodes":[
		` + reviewedNode(1, false, `{"createdAt":"2026-06-01T00:00:00Z","requestedReviewer":{}}`, mine) + `,
		` + reviewedNode(2, false, `{"createdAt":"2026-06-01T00:00:00Z","requestedReviewer":{"login":"carol"}}`, mine) + `,
		{}
	]}}}]`
	prs, err := decodeReviewedPages([]byte(data), "octocat", nil)
	if err != nil || len(prs) != 0 {
		t.Fatalf("got %+v, %v; want team-only and drive-by reviews skipped", prs, err)
	}
	for name, data := range map[string]string{
		"no pages":       `[]`,
		"GraphQL errors": `[{"data":{"search":{"nodes":[]}},"errors":[{"message":"rate limited"}]}]`,
		"missing search": `[{"data":{}}]`,
	} {
		if _, err := decodeReviewedPages([]byte(data), "octocat", nil); err == nil {
			t.Errorf("%s: decode succeeded", name)
		}
	}
}

func TestMergeReviewsPutsPendingRequestsFirstAndWaitingLast(t *testing.T) {
	pr := func(repository string, number int, status ReviewStatus) PullRequest {
		return PullRequest{Repository: repository, Number: number, ReviewStatus: status}
	}
	requests := []PullRequest{pr("acme/a", 1, ReviewRequested), pr("acme/a", 2, ReviewRequested)}
	reviewed := []PullRequest{
		pr("acme/b", 3, ReviewApproved),
		pr("ACME/A", 2, ReviewWaitingOnAuthor), // re-requested: the pending request wins
		pr("acme/b", 4, ReviewNewCommits),
		pr("acme/b", 5, ReviewWaitingOnAuthor),
		pr("acme/b", 6, ReviewAuthorReplied),
	}
	var got []string
	for _, p := range mergeReviews(requests, reviewed, nil) {
		got = append(got, fmt.Sprintf("%d:%d", p.Number, p.ReviewStatus))
	}
	want := fmt.Sprintf("1:0 2:0 4:%d 6:%d 3:%d 5:%d", ReviewNewCommits, ReviewAuthorReplied, ReviewApproved, ReviewWaitingOnAuthor)
	if strings.Join(got, " ") != want {
		t.Fatalf("merged = %s, want %s", strings.Join(got, " "), want)
	}
}

func TestReviewedQueryBoundsTheSearch(t *testing.T) {
	query := reviewedQuery(false, time.Date(2026, 10, 2, 1, 0, 0, 0, time.UTC))
	for _, part := range []string{"reviewed-by:@me", "-author:@me", "is:open", "updated:>=2026-09-02", "headRefOid", "committedDate", "REVIEW_REQUESTED_EVENT"} {
		if !strings.Contains(query, part) {
			t.Errorf("reviewed query lacks %s:\n%s", part, query)
		}
	}
}

func TestFetchFailsWhenTheReviewedQueryFailsAndPreviewSkipsIt(t *testing.T) {
	client, _ := fakeGH(t, false)
	script, err := os.ReadFile(client.path)
	if err != nil {
		t.Fatal(err)
	}
	failing := strings.Replace(string(script), "case \"$*\" in\n", "case \"$*\" in\n  *reviewed-by:@me*) echo reviewed boom >&2; exit 1 ;;\n", 1)
	if err := os.WriteFile(client.path, []byte(failing), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := client.Fetch(ctx); err == nil || !strings.Contains(err.Error(), "reviewed boom") {
		t.Fatalf("fetch error = %v, want the reviewed query's failure", err)
	}
	if _, err := client.Preview(ctx); err != nil {
		t.Fatalf("preview ran the reviewed query: %v", err)
	}
}

func TestReviewSearchesReadNudges(t *testing.T) {
	for name, query := range map[string]string{
		"requests": reviewRequestsQuery(false),
		"reviewed": reviewedQuery(false, day(9)),
	} {
		if !strings.Contains(query, "IssueComment { author { login } createdAt body }") {
			t.Errorf("%s query does not read comment bodies", name)
		}
	}
	if strings.Contains(pullRequestsQuery(false, Needs{}, nil), "body") || strings.Contains(previewReviewRequestsQuery(), "body") {
		t.Error("authored or preview queries read comment bodies")
	}
}

func TestReviewRequestsCarryTheirNudge(t *testing.T) {
	node := reviewedNode(1, false, "", nudgeComment("bob", at(5), "urgent", "octocat"))
	data := `[{"data":{"search":{"nodes":[` + node + `],"pageInfo":{"hasNextPage":false}}}}]`
	prs, err := decodeReviewPages([]byte(data), "octocat", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Nudge == nil || prs[0].Nudge.Urgency != NudgeUrgent {
		t.Fatalf("request = %+v, want an urgent nudge", prs)
	}
	reviewed, err := decodeReviewedPages([]byte(data), "octocat", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviewed) != 1 || reviewed[0].Nudge == nil {
		t.Fatalf("reviewed = %+v, want its nudge", reviewed)
	}
}

func TestANudgeIsNotAnAuthorReply(t *testing.T) {
	activity := review("octocat", "APPROVED", at(4), "h2") + "," + nudgeComment("bob", at(5), "low", "octocat")
	data := `[{"data":{"search":{"nodes":[` + reviewedNode(1, false, "", activity) + `],"pageInfo":{"hasNextPage":false}}}}]`
	prs, err := decodeReviewedPages([]byte(data), "octocat", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].ReviewStatus != ReviewApproved || prs[0].Nudge == nil {
		t.Fatalf("reviewed = %+v, want approved and nudged", prs)
	}
	// An ordinary author comment still is a reply.
	activity = review("octocat", "APPROVED", at(4), "h2") + "," + comment("bob", at(5))
	data = `[{"data":{"search":{"nodes":[` + reviewedNode(1, false, "", activity) + `],"pageInfo":{"hasNextPage":false}}}}]`
	if prs, _ = decodeReviewedPages([]byte(data), "octocat", nil); prs[0].ReviewStatus != ReviewAuthorReplied {
		t.Fatalf("status = %v, want author replied", prs[0].ReviewStatus)
	}
}

func TestRequestsAreNoLongerMarkedAskedAgain(t *testing.T) {
	// Two requests of the viewer and a reviewed copy no longer set anything:
	// the merged list is just requests first.
	requests := []PullRequest{{Repository: "acme/api", Number: 1}}
	reviewed := []PullRequest{{Repository: "acme/api", Number: 1, ReviewStatus: ReviewNewCommits}, {Repository: "acme/api", Number: 2, ReviewStatus: ReviewApproved}}
	got := mergeReviews(requests, reviewed, nil)
	if len(got) != 2 || got[0].Number != 1 || got[0].ReviewStatus != ReviewRequested || got[1].Number != 2 {
		t.Fatalf("merged = %+v", got)
	}
}
