// Command gh is a stand-in for GitHub CLI that answers prpr's queries with
// made-up pull requests, for recording the README demo. Dates are relative to
// now. The first full fetch of each list returns the starting state; later
// fetches return a changed state, so a refresh shows change marks. DEMO_STATE
// names the directory that records which fetches have run.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const login = "alice"

// now is fixed at the first call, so unchanged pull requests keep their
// update times and a refresh marks only real changes.
var now = start()

func start() time.Time {
	path := filepath.Join(os.Getenv("DEMO_STATE"), "now")
	if data, err := os.ReadFile(path); err == nil {
		if t, err := time.Parse(time.RFC3339, string(data)); err == nil {
			return t
		}
	}
	t := time.Now().UTC().Truncate(time.Second)
	_ = os.WriteFile(path, []byte(t.Format(time.RFC3339)), 0o600)
	return t
}

func ago(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }

const (
	hour = time.Hour
	day  = 24 * time.Hour
)

type pr struct {
	repo, title, author        string
	number, add, del, comments int
	draft                      bool
	mergeable, mergeState      string
	checks, decision           string
	approvals                  int
	created, ready, requested  time.Duration
	head                       time.Duration
	bots                       bots
}

// bots describes the review-bot activity on a pull request.
type bots struct {
	copilotReview time.Duration // time since Copilot's review; 0 for none
	threads       int           // unresolved threads Copilot started
	codexReaction time.Duration // time since Codex reacted 👍; 0 for none
	claudeRunning bool          // Claude's check run is in progress
	copilotFailed bool          // Copilot's check run failed
}

func (p pr) node(full bool) map[string]any {
	n := map[string]any{
		"number": p.number, "title": p.title, "isDraft": p.draft,
		"url":        fmt.Sprintf("https://github.com/%s/pull/%d", p.repo, p.number),
		"mergeable":  p.mergeable,
		"updatedAt":  ago(p.head),
		"createdAt":  ago(p.created),
		"additions":  p.add,
		"deletions":  p.del,
		"repository": map[string]any{"nameWithOwner": p.repo},
		"author":     map[string]any{"login": p.author},
	}
	if !full {
		return n
	}
	ready := []any{}
	if p.ready > 0 {
		ready = append(ready, map[string]any{"createdAt": ago(p.ready)})
	}
	reviews := []any{}
	for range p.approvals {
		reviews = append(reviews, map[string]any{"state": "APPROVED"})
	}
	var rollup any
	if p.checks != "" {
		contexts := []any{map[string]any{"name": "build", "status": "COMPLETED", "conclusion": "SUCCESS"}}
		if p.bots.claudeRunning {
			contexts = append(contexts, map[string]any{"name": "Claude Code Review", "status": "IN_PROGRESS", "conclusion": nil})
		}
		if p.bots.copilotFailed {
			contexts = append(contexts, map[string]any{"name": "copilot-pull-request-reviewer", "status": "COMPLETED", "conclusion": "FAILURE"})
		}
		rollup = map[string]any{"state": p.checks, "contexts": map[string]any{"nodes": contexts}}
	}
	var decision any
	if p.decision != "" {
		decision = p.decision
	}
	botReviews := []any{}
	if p.bots.copilotReview > 0 {
		botReviews = append(botReviews, map[string]any{"author": map[string]any{"login": "copilot-pull-request-reviewer"}, "submittedAt": ago(p.bots.copilotReview)})
	}
	reactions := []any{}
	if p.bots.codexReaction > 0 {
		reactions = append(reactions, map[string]any{"content": "THUMBS_UP", "createdAt": ago(p.bots.codexReaction), "user": map[string]any{"login": "chatgpt-codex-connector[bot]"}})
	}
	threads := []any{}
	for range p.bots.threads {
		threads = append(threads, map[string]any{"isResolved": false, "isOutdated": false,
			"comments": map[string]any{"nodes": []any{map[string]any{"author": map[string]any{"login": "copilot-pull-request-reviewer"}}}}})
	}
	requests := []any{}
	if p.requested > 0 {
		requests = append(requests, map[string]any{"createdAt": ago(p.requested), "requestedReviewer": map[string]any{"login": login}})
	}
	for k, v := range map[string]any{
		"mergeStateStatus":         p.mergeState,
		"reviewDecision":           decision,
		"commentCount":             map[string]any{"totalCount": p.comments},
		"readyEvents":              map[string]any{"nodes": ready},
		"latestOpinionatedReviews": map[string]any{"nodes": reviews},
		"commits":                  map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"committedDate": ago(p.head), "statusCheckRollup": rollup}}}},
		"reviews":                  map[string]any{"nodes": botReviews},
		"comments":                 map[string]any{"nodes": []any{}},
		"reactions":                map[string]any{"nodes": reactions},
		"reviewThreads":            map[string]any{"nodes": threads},
		"requestEvents":            map[string]any{"nodes": requests},
	} {
		n[k] = v
	}
	return n
}

func mine(changed bool) []pr {
	limits := pr{repo: "acme/api", number: 412, title: "Add rate limiting to the public API", author: login,
		add: 184, del: 32, comments: 6, mergeable: "MERGEABLE", mergeState: "CLEAN", checks: "SUCCESS",
		decision: "APPROVED", approvals: 2, created: 4 * day, ready: 3 * day, head: 20 * hour,
		bots: bots{copilotReview: 18 * hour}}
	settings := pr{repo: "acme/web", number: 1287, title: "Migrate the settings page to the new design system", author: login,
		add: 612, del: 418, comments: 3, mergeable: "MERGEABLE", mergeState: "BLOCKED", checks: "PENDING",
		decision: "REVIEW_REQUIRED", created: 2 * day, head: 3 * hour, bots: bots{copilotReview: 2 * hour, threads: 2}}
	flaky := pr{repo: "acme/web", number: 1301, title: "Fix the flaky checkout test", author: login,
		add: 12, del: 9, comments: 1, mergeable: "MERGEABLE", mergeState: "BEHIND", checks: "SUCCESS",
		decision: "APPROVED", approvals: 1, created: 30 * hour, head: 26 * hour, bots: bots{codexReaction: 28 * hour}}
	state := pr{repo: "acme/infra", number: 88, title: "Split staging and production Terraform state", author: login,
		add: 240, del: 197, comments: 11, mergeable: "CONFLICTING", mergeState: "DIRTY", checks: "FAILURE",
		decision: "CHANGES_REQUESTED", created: 6 * day, head: 5 * hour, bots: bots{claudeRunning: true}}
	retries := pr{repo: "acme/api", number: 419, title: "Prototype webhook retries", author: login, draft: true,
		add: 95, del: 4, mergeable: "MERGEABLE", mergeState: "DRAFT", checks: "SUCCESS", created: 1 * day, head: 9 * hour}
	if !changed {
		return []pr{settings, flaky, limits, state, retries}
	}
	settings.checks, settings.decision, settings.approvals, settings.comments = "SUCCESS", "APPROVED", 1, 5
	limits.comments = 8
	bump := pr{repo: "acme/api", number: 423, title: "Bump Go to 1.26", author: login,
		add: 6, del: 6, mergeable: "MERGEABLE", mergeState: "BLOCKED", checks: "SUCCESS",
		decision: "REVIEW_REQUIRED", created: 10 * time.Minute, head: 10 * time.Minute, bots: bots{copilotFailed: true}}
	return []pr{bump, settings, limits, state, retries}
}

func reviews(changed bool) []pr {
	avatars := pr{repo: "acme/web", number: 1290, title: "Cache avatar images at the edge", author: "sam-lee",
		add: 77, del: 21, comments: 4, mergeable: "MERGEABLE", mergeState: "BLOCKED", checks: "SUCCESS",
		decision: "REVIEW_REQUIRED", created: 8 * hour, requested: 5 * hour, head: 6 * hour, bots: bots{copilotReview: 5 * hour}}
	offline := pr{repo: "acme/mobile", number: 530, title: "Offline mode for the order list", author: "priya-n",
		add: 1310, del: 260, comments: 9, mergeable: "MERGEABLE", mergeState: "BLOCKED", checks: "PENDING",
		decision: "REVIEW_REQUIRED", created: 3 * day, requested: 2 * day, head: 4 * hour, bots: bots{copilotReview: 30 * hour, threads: 1}}
	cursors := pr{repo: "acme/api", number: 418, title: "Document pagination cursors", author: "jordan-k",
		add: 58, mergeable: "MERGEABLE", mergeState: "CLEAN", checks: "SUCCESS",
		decision: "REVIEW_REQUIRED", created: 26 * hour, requested: 26 * hour, head: 26 * hour}
	if !changed {
		return []pr{avatars, offline, cursors}
	}
	rotate := pr{repo: "acme/infra", number: 91, title: "Rotate database credentials automatically", author: "sam-lee",
		add: 143, del: 38, mergeable: "MERGEABLE", mergeState: "BLOCKED", checks: "PENDING",
		decision: "REVIEW_REQUIRED", created: 15 * time.Minute, requested: 15 * time.Minute, head: 15 * time.Minute}
	return []pr{rotate, avatars, offline, cursors}
}

// fetched reports whether a full fetch of list already ran, and records it.
func fetched(list string) bool {
	marker := filepath.Join(os.Getenv("DEMO_STATE"), list)
	_, err := os.Stat(marker)
	if err != nil {
		_ = os.WriteFile(marker, nil, 0o600)
	}
	return err == nil
}

func nodes(prs []pr, full bool) []any {
	out := make([]any, len(prs))
	for i, p := range prs {
		out[i] = p.node(full)
	}
	return out
}

func main() {
	args := strings.Join(os.Args[1:], " ")
	var data any
	page := func(prs []pr, full bool) map[string]any {
		return map[string]any{"nodes": nodes(prs, full), "pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil}}
	}
	switch {
	case strings.HasPrefix(args, "auth status"):
		return
	case !strings.HasPrefix(args, "api graphql"):
		fmt.Fprintln(os.Stderr, "demo gh: unsupported command:", args)
		os.Exit(1)
	case strings.Contains(args, "rateLimit"):
		data = map[string]any{"data": map[string]any{"rateLimit": map[string]any{
			"limit": 5000, "remaining": 4874, "resetAt": now.Add(41 * time.Minute).Format(time.RFC3339)}}}
	default:
		full := strings.Contains(args, "mergeStateStatus")
		review := strings.Contains(args, "search(")
		changed := false
		if full {
			time.Sleep(1500 * time.Millisecond)
			changed = fetched(map[bool]string{false: "mine", true: "reviews"}[review])
		}
		if review {
			data = []any{map[string]any{"data": map[string]any{"search": page(reviews(changed), full)}}}
		} else {
			data = []any{map[string]any{"data": map[string]any{"viewer": map[string]any{
				"login": login, "pullRequests": page(mine(changed), full)}}}}
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(data); err != nil {
		os.Exit(1)
	}
}
