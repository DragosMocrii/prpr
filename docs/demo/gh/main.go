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
	// nudged is the time since the author's urgent nudge of the viewer; 0
	// for none.
	nudged time.Duration
	head   time.Duration
	bots   bots
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
		"id":         fmt.Sprintf("PR_%s_%d", strings.ReplaceAll(p.repo, "/", "_"), p.number),
		"headRefOid": fmt.Sprintf("%x", int64(p.head)),
		"number":     p.number, "title": p.title, "isDraft": p.draft,
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
	activity := []any{}
	if p.nudged > 0 {
		activity = append(activity, map[string]any{"__typename": "IssueComment", "author": map[string]any{"login": p.author},
			"createdAt": ago(p.nudged),
			"body":      "@" + login + " 🚨 This is blocking: please review it as soon as you can.\n\n<!-- prpr:nudge v1 urgency=urgent to=" + login + " -->"})
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
		"activity":                 map[string]any{"nodes": activity},
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
		decision: "REVIEW_REQUIRED", created: 3 * day, requested: 2 * day, nudged: 40 * time.Minute, head: 4 * hour, bots: bots{copilotReview: 30 * hour, threads: 1}}
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

// reviewers answers the reviewer query for any pull request: a comment
// review of the head commit, an approval that answered a third code-owner
// team, and two code-owner teams that share a member.
func reviewers() any {
	team := func(slug string, logins ...string) map[string]any {
		members := []any{}
		for _, l := range logins {
			members = append(members, map[string]any{"login": l})
		}
		return map[string]any{"asCodeOwner": true, "requestedReviewer": map[string]any{"__typename": "Team", "combinedSlug": "acme/" + slug,
			"members": map[string]any{"totalCount": len(logins), "nodes": members}}}
	}
	review := map[string]any{"state": "COMMENTED", "submittedAt": ago(day), "author": map[string]any{"__typename": "User", "login": "jordan-k"},
		"commit": map[string]any{"oid": "head"}}
	approval := map[string]any{"state": "APPROVED", "submittedAt": ago(2 * time.Hour), "author": map[string]any{"__typename": "User", "login": "mira-s"},
		"commit": map[string]any{"oid": "head"}, "onBehalfOf": map[string]any{"nodes": []any{map[string]any{"combinedSlug": "acme/mobile"}}}}
	return map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
		"author": map[string]any{"login": login}, "headRefOid": "head",
		"comments":                 map[string]any{"nodes": []any{}},
		"latestReviews":            map[string]any{"nodes": []any{review, approval}},
		"latestOpinionatedReviews": map[string]any{"nodes": []any{approval}},
		"reviewRequests": map[string]any{"nodes": []any{
			team("platform", "sam-lee", "priya-n", "dana-r"),
			team("api-owners", "jordan-k", "lee-t", "priya-n", "omar-b", "kim-s"),
		}},
	}}}}
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

// ran reports whether a full fetch of list already ran.
func ran(list string) bool {
	_, err := os.Stat(filepath.Join(os.Getenv("DEMO_STATE"), list))
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
	case args == "--version":
		fmt.Println("gh version 2.97.0 (demo)")
		return
	case strings.HasPrefix(args, "auth status"):
		return
	case strings.Contains(args, "--method POST") && strings.Contains(args, "/comments"):
		// A nudge: the comment is accepted and goes nowhere.
		data = map[string]any{}
	case !strings.HasPrefix(args, "api graphql"):
		fmt.Fprintln(os.Stderr, "demo gh: unsupported command:", args)
		os.Exit(1)
	case strings.Contains(args, "node(id:"):
		// A detail query: answer each $idN from the current data.
		all := append(mine(ran("mine")), reviews(ran("reviews"))...)
		answer := map[string]any{}
		for i := 0; i+1 < len(os.Args); i++ {
			name, id, ok := strings.Cut(os.Args[i+1], "=")
			if os.Args[i] != "-f" || !ok || !strings.HasPrefix(name, "id") {
				continue
			}
			for _, p := range all {
				if n := p.node(true); n["id"] == id {
					answer["p"+strings.TrimPrefix(name, "id")] = n
				}
			}
		}
		data = map[string]any{"data": answer}
	case strings.Contains(args, "combinedSlug"):
		data = reviewers()
	case strings.Contains(args, "mentions:@me"):
		// The demo's only nudge is on a pending request. The mentions
		// search reads one page, without --slurp, so it is one object.
		data = map[string]any{"data": map[string]any{"search": map[string]any{"nodes": []any{}}}}
	case strings.Contains(args, "reviewed-by:@me"):
		// The demo has no pull requests the viewer already reviewed.
		data = []any{map[string]any{"data": map[string]any{"search": page(nil, true)}}}
	case strings.Contains(args, "states: [MERGED]"):
		merged := []any{
			map[string]any{"id": "PR_merged_1", "number": 405, "title": "Retry failed webhooks", "url": "https://github.com/acme/api/pull/405",
				"mergedAt": ago(5 * hour), "mergedBy": map[string]any{"login": login}, "additions": 64, "deletions": 12,
				"repository": map[string]any{"nameWithOwner": "acme/api"}},
			map[string]any{"id": "PR_merged_2", "number": 1279, "title": "Lazy-load the dashboard charts", "url": "https://github.com/acme/web/pull/1279",
				"mergedAt": ago(2 * day), "mergedBy": map[string]any{"login": "sam-lee"}, "additions": 210, "deletions": 95,
				"repository": map[string]any{"nameWithOwner": "acme/web"}},
		}
		if ran("mine") {
			// #1301 left the authored list after the first fetch: it merged.
			flaky := map[string]any{"id": "PR_acme_web_1301", "number": 1301, "title": "Fix the flaky checkout test", "url": "https://github.com/acme/web/pull/1301",
				"mergedAt": ago(3 * time.Minute), "mergedBy": map[string]any{"login": login}, "additions": 12, "deletions": 9,
				"repository": map[string]any{"nameWithOwner": "acme/web"}}
			merged = append([]any{flaky}, merged...)
		}
		data = map[string]any{"data": map[string]any{"viewer": map[string]any{"login": login, "pullRequests": map[string]any{"nodes": merged}}}}
	case strings.Contains(args, "rateLimit"):
		data = map[string]any{"data": map[string]any{"rateLimit": map[string]any{
			"limit": 5000, "remaining": 4874, "resetAt": now.Add(41 * time.Minute).Format(time.RFC3339)}}}
	default:
		full := strings.Contains(args, "mergeStateStatus")
		signature := !full && strings.Contains(args, "headRefOid")
		review := strings.Contains(args, "search(")
		list := map[bool]string{false: "mine", true: "reviews"}[review]
		changed := false
		switch {
		case full:
			time.Sleep(1500 * time.Millisecond)
			changed = fetched(list)
		case signature:
			changed = ran(list)
		}
		if review {
			data = []any{map[string]any{"data": map[string]any{"search": page(reviews(changed), full || signature)}}}
		} else {
			data = []any{map[string]any{"data": map[string]any{"viewer": map[string]any{
				"login": login, "pullRequests": page(mine(changed), full || signature)}}}}
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(data); err != nil {
		os.Exit(1)
	}
}
