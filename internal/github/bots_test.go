package github

import (
	"strings"
	"testing"
)

func TestParseBots(t *testing.T) {
	bots, err := ParseBots(DefaultBots)
	if err != nil {
		t.Fatal(err)
	}
	want := []Bot{
		{"Copilot", "copilot-pull-request-reviewer", "copilot-pull-request-reviewer"},
		{"Codex", "chatgpt-codex-connector", ""},
		{"Claude", "claude", "Claude Code Review"},
	}
	if len(bots) != len(want) {
		t.Fatalf("default bots = %+v", bots)
	}
	for i := range want {
		if bots[i] != want[i] {
			t.Errorf("default bot %d = %+v, want %+v", i, bots[i], want[i])
		}
	}
	if bots, err := ParseBots(" Rabbit = coderabbitai[bot] "); err != nil || len(bots) != 1 || bots[0] != (Bot{Name: "Rabbit", Login: "coderabbitai"}) {
		t.Errorf("suffixed login = %+v, %v", bots, err)
	}
	if bots, err := ParseBots("  "); err != nil || bots != nil {
		t.Errorf("empty value = %+v, %v; want no bots", bots, err)
	}
	for _, value := range []string{"Claude", "=claude", "Claude=", "Claude=two words", "A=x,B=X", "A=x,"} {
		if bots, err := ParseBots(value); err == nil {
			t.Errorf("ParseBots(%q) = %+v, want error", value, bots)
		}
	}
}

func TestPullRequestQueriesSelectBotFieldsOnlyWhenConfigured(t *testing.T) {
	for _, query := range []string{pullRequestsQuery(false), reviewRequestsQuery(false)} {
		if strings.Contains(query, "reviewThreads") || strings.Contains(query, "contexts") {
			t.Errorf("query without bots selects bot fields:\n%s", query)
		}
	}
	for _, query := range []string{pullRequestsQuery(true), reviewRequestsQuery(true)} {
		if !strings.Contains(query, "reviewThreads") || !strings.Contains(query, "contexts") {
			t.Errorf("query with bots lacks bot fields:\n%s", query)
		}
	}
}

// The fixture follows shapes seen on real pull requests: Copilot resolving its
// own threads, a Copilot quota failure that looks like an empty review, Codex
// passing with a reaction, and Claude passing with an edited comment.
const botPage = `[{"data":{"viewer":{"login":"octocat","pullRequests":{"nodes":[
	{"number":1,"url":"https://github.com/acme/a/pull/1","repository":{"nameWithOwner":"acme/a"},
	 "commits":{"nodes":[{"commit":{"committedDate":"2026-09-20T10:00:00Z","statusCheckRollup":{"state":"SUCCESS","contexts":{"nodes":[{}]}}}}]},
	 "reviews":{"nodes":[{"author":{"login":"copilot-pull-request-reviewer"},"submittedAt":"2026-09-19T10:00:00Z"}]},
	 "comments":{"nodes":[{"author":{"login":"claude"},"createdAt":"2026-09-20T09:00:00Z","lastEditedAt":"2026-09-20T10:02:00Z"}]},
	 "reactions":{"nodes":[{"content":"THUMBS_UP","createdAt":"2026-09-20T10:10:00Z","user":{"login":"chatgpt-codex-connector[bot]"}}]},
	 "reviewThreads":{"nodes":[
		{"isResolved":false,"isOutdated":false,"comments":{"nodes":[{"author":{"login":"copilot-pull-request-reviewer"}}]}},
		{"isResolved":false,"isOutdated":false,"comments":{"nodes":[{"author":{"login":"copilot-pull-request-reviewer"}}]}},
		{"isResolved":true,"isOutdated":false,"comments":{"nodes":[{"author":{"login":"copilot-pull-request-reviewer"}}]}},
		{"isResolved":false,"isOutdated":true,"comments":{"nodes":[{"author":{"login":"copilot-pull-request-reviewer"}}]}},
		{"isResolved":false,"isOutdated":false,"comments":{"nodes":[{"author":null}]}},
		null
	 ]}},
	{"number":2,"url":"https://github.com/acme/a/pull/2","repository":{"nameWithOwner":"acme/a"},
	 "commits":{"nodes":[{"commit":{"committedDate":"2026-09-20T10:00:00Z","statusCheckRollup":{"state":"FAILURE","contexts":{"nodes":[
		{"name":"copilot-pull-request-reviewer","status":"COMPLETED","conclusion":"FAILURE"},
		{"name":"Claude Code Review (auto)","status":"IN_PROGRESS","conclusion":null},
		{"name":"Claude Code Review (comment)","status":"COMPLETED","conclusion":"SKIPPED"}
	 ]}}}}]},
	 "reviews":{"nodes":[{"author":{"login":"copilot-pull-request-reviewer"},"submittedAt":"2026-09-20T10:05:00Z"}]},
	 "comments":{"nodes":[{"author":{"login":"chatgpt-codex-connector"},"createdAt":"2026-09-19T08:00:00Z","lastEditedAt":null}]},
	 "reactions":{"nodes":[]},"reviewThreads":{"nodes":[]}},
	{"number":3,"url":"https://github.com/acme/a/pull/3","repository":{"nameWithOwner":"acme/a"},
	 "commits":{"nodes":[{"commit":{"committedDate":"2026-09-20T10:00:00Z","statusCheckRollup":null}}]},
	 "reactions":{"nodes":[{"content":"EYES","createdAt":"2026-09-20T10:10:00Z","user":{"login":"chatgpt-codex-connector[bot]"}}]},
	 "reviewThreads":{"nodes":[{"isResolved":false,"isOutdated":false,"comments":{"nodes":[{"author":{"login":"human"}}]}}]}}
],"pageInfo":{"hasNextPage":false}}}}}]`

func TestDecodeBotReviews(t *testing.T) {
	bots, err := ParseBots(DefaultBots)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := decodePages([]byte(botPage), bots)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]BotReview{
		{{"Copilot", BotConcerns, 2}, {"Codex", BotPassed, 0}, {"Claude", BotPassed, 0}},
		{{"Copilot", BotFailed, 0}, {"Codex", BotStale, 0}, {"Claude", BotRunning, 0}},
		{{"Copilot", BotNotRun, 0}, {"Codex", BotNotRun, 0}, {"Claude", BotNotRun, 0}},
	}
	if len(snapshot.PullRequests) != len(want) {
		t.Fatalf("got %d pull requests", len(snapshot.PullRequests))
	}
	for i, pr := range snapshot.PullRequests {
		if len(pr.Bots) != len(want[i]) {
			t.Fatalf("PR %d bots = %+v", pr.Number, pr.Bots)
		}
		for j := range want[i] {
			if pr.Bots[j] != want[i][j] {
				t.Errorf("PR %d bot %d = %+v, want %+v", pr.Number, j, pr.Bots[j], want[i][j])
			}
		}
	}
	if snapshot.PullRequests[1].Checks != "FAILURE" {
		t.Errorf("check rollup = %q, want FAILURE", snapshot.PullRequests[1].Checks)
	}

	snapshot, err = decodePages([]byte(botPage), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, pr := range snapshot.PullRequests {
		if pr.Bots != nil {
			t.Errorf("PR %d reports bots with none configured: %+v", pr.Number, pr.Bots)
		}
	}
}
