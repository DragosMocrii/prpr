package github

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// signaturePageSize is larger than pageSize: signature fields resolve
// quickly, like the preview's.
const signaturePageSize = 100

// signatureFields select a pull request's node ID and signature.
func signatureFields(queues []Queue) string {
	fields := `
        id
        url
        updatedAt
        headRefOid
        mergeable
        isDraft
        commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }`
	if slices.Contains(queues, QueueGitHub) {
		fields += `
        mergeQueueEntry { state }`
	}
	return fields
}

// authoredSignatureQuery lists the authored pull requests as
// pullRequestsQuery does, selecting only their signatures.
func authoredSignatureQuery(queues []Queue) string {
	return `query($endCursor: String) {
  viewer {
    login
    pullRequests(first: ` + strconv.Itoa(signaturePageSize) + `, after: $endCursor, states: [OPEN],
                 orderBy: {field: UPDATED_AT, direction: DESC}) {
      nodes {` + signatureFields(queues) + `
      }
      pageInfo { hasNextPage endCursor }
    }
  }
}`
}

// searchSignatureQuery lists a pull request search as searchQuery does,
// selecting only signatures.
func searchSignatureQuery(search string) string {
	return `query($endCursor: String) {
  search(type: ISSUE, first: ` + strconv.Itoa(signaturePageSize) + `, after: $endCursor,
         query: "` + search + `") {
    nodes {
      ... on PullRequest {` + signatureFields(nil) + `
      }
    }
    pageInfo { hasNextPage endCursor }
  }
}`
}

// entry is a listed pull request's node ID and signature.
type entry struct {
	id  string
	sig signature
}

func entries(prs []PullRequest) []entry {
	out := make([]entry, 0, len(prs))
	for _, pr := range prs {
		if pr.ID != "" {
			out = append(out, entry{id: pr.ID, sig: pr.sig})
		}
	}
	return out
}

// decodeAuthoredSignatures decodes the authored signature pages.
func decodeAuthoredSignatures(data []byte) (string, []entry, error) {
	snapshot, err := decodePages(data, nil)
	if err != nil {
		return "", nil, err
	}
	return snapshot.Login, entries(snapshot.PullRequests), nil
}

// decodeSearchSignatures decodes a search's signature pages.
func decodeSearchSignatures(data []byte, name string) ([]entry, error) {
	prs, err := decodeSearchPages(data, name, func(node *pullRequestNode) (PullRequest, bool) {
		return PullRequest{ID: node.ID, sig: node.signature()}, true
	})
	if err != nil {
		return nil, err
	}
	return entries(prs), nil
}

// detailBatch is how many pull requests a detail query names: a full
// query's page, so it stays inside GitHub's time limit.
const detailBatch = pageSize

// detailQuery reads n pull requests, $id0 to $id(n-1), with fields.
func detailQuery(n int, fields string) string {
	var b strings.Builder
	b.WriteString("query(")
	for i := range n {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "$id%d: ID!", i)
	}
	b.WriteString(") {")
	for i := range n {
		fmt.Fprintf(&b, "\n  p%d: node(id: $id%d) { ... on PullRequest {%s\n  } }", i, i, fields)
	}
	b.WriteString("\n}")
	return b.String()
}

// decodeDetails decodes a detail query of ids. A pull request GitHub no
// longer finds fails it: the lists named it a moment ago.
func decodeDetails(data []byte, ids []string) ([]*pullRequestNode, error) {
	var response struct {
		Data   map[string]*pullRequestNode `json:"data"`
		Errors []json.RawMessage           `json:"errors"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode GitHub pull request detail response: %w", err)
	}
	if len(response.Errors) != 0 {
		return nil, fmt.Errorf("GitHub pull request detail query returned GraphQL errors: %s", graphQLErrors(response.Errors))
	}
	nodes := make([]*pullRequestNode, len(ids))
	for i, id := range ids {
		if nodes[i] = response.Data["p"+strconv.Itoa(i)]; nodes[i] == nil {
			return nil, fmt.Errorf("GitHub pull request %s was not found", id)
		}
	}
	return nodes, nil
}

// details reads the pull requests ids names with fields, in batches.
func (c *Client) details(ctx context.Context, ids []string, fields string) ([]*pullRequestNode, error) {
	var nodes []*pullRequestNode
	for start := 0; start < len(ids); start += detailBatch {
		batch := ids[start:min(start+detailBatch, len(ids))]
		args := []string{"api", "graphql", "--hostname", "github.com", "-f", "query=" + detailQuery(len(batch), fields)}
		for i, id := range batch {
			args = append(args, "-f", "id"+strconv.Itoa(i)+"="+id)
		}
		data, err := c.output(ctx, "GitHub pull request detail query failed", args...)
		if err != nil {
			return nil, err
		}
		found, err := decodeDetails(data, batch)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, found...)
	}
	return nodes, nil
}

// fetchCache is what an incremental fetch reuses: each list's pull
// requests by node ID, as converted before mergeReviews, the viewer's login
// they were read as, and when the last complete fetch ran.
type fetchCache struct {
	login                                  string
	authored, requests, reviewed, mentions map[string]listed
	lastComplete                           time.Time
}

func byID(prs []listed) map[string]listed {
	m := make(map[string]listed, len(prs))
	for _, l := range prs {
		m[l.pr.ID] = l
	}
	return m
}

func allKept(prs []PullRequest) []listed {
	out := make([]listed, len(prs))
	for i, pr := range prs {
		out[i] = listed{pr: pr, keep: true}
	}
	return out
}

func newFetchCache(l lists, lastComplete time.Time) *fetchCache {
	return &fetchCache{authored: byID(allKept(l.authored)), requests: byID(allKept(l.requests)), reviewed: byID(l.reviewed), mentions: byID(l.mentions), login: l.login, lastComplete: lastComplete}
}

// dropCache makes the next fetch complete. The caller holds c.mu.
func (c *Client) dropCache() {
	c.cache = nil
	c.cacheGeneration++
}

// ForceFull makes the next fetch complete.
func (c *Client) ForceFull() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dropCache()
}

// SetFullRefresh sets how long after a complete fetch the next fetch is
// complete again; zero leaves it to ForceFull and the setters.
func (c *Client) SetFullRefresh(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fullEvery = d
}

// inTransition reports a pull request whose detail may change while its
// signature does not: GitHub is still computing its merge state, its CI
// runs or fails (a failing check wins over a pending one in the rollup, so
// running required checks hide behind it), a merge queue holds it, a bot has
// yet to act on its head or raised concerns, or review threads are
// unresolved (resolving one may not move updatedAt).
func inTransition(pr PullRequest) bool {
	if pr.MergeState == "" || pr.MergeState == "UNKNOWN" || pr.Checks == "PENDING" || pr.Checks == "EXPECTED" ||
		pr.Checks == "FAILURE" || pr.Checks == "ERROR" || (pr.Queue != nil && pr.Queue.State.InQueue()) ||
		(pr.ThreadsKnown && pr.UnresolvedThreads > 0) {
		return true
	}
	for _, bot := range pr.Bots {
		if bot.State == BotRunning || bot.State == BotStale || bot.State == BotConcerns {
			return true
		}
	}
	return false
}

// stale lists the IDs among list whose detail must be read again. With
// keptOnly, a dropped row is read again only when its signature changes,
// not while it is in transition.
func stale(list []entry, cache map[string]listed, keptOnly bool) []string {
	var ids []string
	for _, e := range list {
		if cached, ok := cache[e.id]; !ok || !cached.pr.sig.same(e.sig) || (cached.keep || !keptOnly) && inTransition(cached.pr) {
			ids = append(ids, e.id)
		}
	}
	return ids
}

// fetchIncremental lists every pull request by signature, reads the detail
// of those that are new, changed, or in transition, and takes the rest from
// cache. Every query must succeed. When the signatures name another viewer
// than the cache's, it runs a complete fetch instead and reports true.
func (c *Client) fetchIncremental(ctx context.Context, cache *fetchCache, bots []Bot, needs Needs, queues []Queue, now time.Time) (l lists, complete bool, err error) {
	out := c.graphqlLists(ctx, authoredSignatureQuery(queues), searchSignatureQuery(reviewSearch), searchSignatureQuery(reviewedSearch(now)),
		singlePageSearchQuery(mentionsSearch(now), mentionsLimit, signatureFields(nil)))
	if out[0].err != nil {
		return lists{}, false, out[0].err
	}
	login, authored, err := decodeAuthoredSignatures(out[0].data)
	if err != nil {
		return lists{}, false, err
	}
	if login != cache.login {
		l, err = c.fetchComplete(ctx, bots, needs, queues, now)
		return l, true, err
	}
	if out[1].err != nil {
		return lists{}, false, out[1].err
	}
	requests, err := decodeSearchSignatures(out[1].data, "review request")
	if err != nil {
		return lists{}, false, err
	}
	if out[2].err != nil {
		return lists{}, false, out[2].err
	}
	reviewed, err := decodeSearchSignatures(out[2].data, "reviewed pull request")
	if err != nil {
		return lists{}, false, err
	}

	if out[3].err != nil {
		return lists{}, false, out[3].err
	}
	mentions, err := decodeSearchSignatures(onePage(out[3].data), "mentioned pull request")
	if err != nil {
		return lists{}, false, err
	}

	// The lists' details are independent, so they are read at once.
	hasBots := len(bots) > 0
	type read struct {
		ids   []string
		nodes []*pullRequestNode
		err   error
	}
	start := func(list []entry, cached map[string]listed, fields string, keptOnly bool) chan read {
		result := make(chan read, 1)
		ids := stale(list, cached, keptOnly)
		go func() {
			nodes, err := c.details(ctx, ids, fields)
			result <- read{ids, nodes, err}
		}()
		return result
	}
	// A mention without a nudge for the viewer is never shown, so only a
	// changed signature reads it again.
	a := start(authored, cache.authored, authoredFields(hasBots, needs, queues), false)
	r := start(requests, cache.requests, reviewFields(hasBots, activityField), false)
	d := start(reviewed, cache.reviewed, reviewFields(hasBots, activityField), false)
	mn := start(mentions, cache.mentions, reviewFields(hasBots, activityField), true)
	ra, rr, rd, rm := <-a, <-r, <-d, <-mn
	for _, res := range []read{ra, rr, rd, rm} {
		if res.err != nil {
			return lists{}, false, res.err
		}
	}

	freshAuthored := make([]PullRequest, len(ra.nodes))
	for i, node := range ra.nodes {
		freshAuthored[i] = node.pullRequest("", bots)
	}
	if needs.RequiredChecks {
		if err := c.requiredChecks(ctx, freshAuthored); err != nil {
			return lists{}, false, err
		}
	}
	freshRequests := make([]listed, len(rr.nodes))
	for i, node := range rr.nodes {
		freshRequests[i] = listed{pr: node.pullRequest(login, bots), keep: true}
	}
	freshReviewed := make([]listed, len(rd.nodes))
	for i, node := range rd.nodes {
		freshReviewed[i] = reviewedPullRequest(node, login, bots)
	}

	freshMentions := make([]listed, len(rm.nodes))
	for i, node := range rm.nodes {
		freshMentions[i] = mentionedPullRequest(node, login, bots)
	}

	l = lists{login: login}
	for _, x := range assemble(authored, cache.authored, byID(allKept(freshAuthored))) {
		l.authored = append(l.authored, x.pr)
	}
	for _, x := range assemble(requests, cache.requests, byID(freshRequests)) {
		l.requests = append(l.requests, x.pr)
	}
	l.reviewed = assemble(reviewed, cache.reviewed, byID(freshReviewed))
	l.mentions = assemble(mentions, cache.mentions, byID(freshMentions))
	return l, false, nil
}

// assemble lists the pull requests in signature order, fresh ones first
// and the rest from cache.
func assemble(list []entry, cached, fresh map[string]listed) []listed {
	out := make([]listed, 0, len(list))
	for _, e := range list {
		if x, ok := fresh[e.id]; ok {
			out = append(out, x)
		} else {
			out = append(out, cached[e.id])
		}
	}
	return out
}
