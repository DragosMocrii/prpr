package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type PullRequest struct {
	Number     int
	Title      string
	URL        string
	Repository string
	Author     string
	Draft      bool
	Mergeable  string
	// MergeState is GitHub's mergeStateStatus, which accounts for branch
	// protection; empty means unknown.
	MergeState string
	UpdatedAt  time.Time
	CreatedAt  time.Time
	// WaitingSince is when the pull request last became ready for review or,
	// for a review request, when the viewer was last requested directly. It is
	// zero for a draft with no direct request.
	WaitingSince time.Time
	// ReviewDecision and Checks are GitHub's raw states; empty means unknown
	// or not applicable.
	ReviewDecision string
	Approvals      int
	Additions      int
	Deletions      int
	Checks         string
	// Comments counts conversation comments, not code review comments.
	Comments int
	// Bots follows the client's configured bots; nil when none are configured.
	Bots []BotReview
	// ReviewStatus places a review-pane pull request; zero for a pending
	// request and for authored pull requests.
	ReviewStatus ReviewStatus
	// RequestedAgain marks a pending review request that asks the viewer
	// again: the timeline names them in more than one request, or they
	// reviewed it before. Set only by a full fetch.
	RequestedAgain bool
	// ChangesRequested counts the latest reviews that request changes.
	ChangesRequested int
	// ID is GitHub's node ID.
	ID string
	// sig is what an incremental fetch compares to tell whether the pull
	// request changed.
	sig signature
	// The fields below are read only for authored pull requests, and only
	// when the client's [Needs] ask for them; each is unknown otherwise.
	// PendingCodeOwners names the code owners whose review is still
	// requested, "" for one GitHub does not name. CodeOwnersKnown reports
	// whether they were read.
	PendingCodeOwners []string
	CodeOwnersKnown   bool
	// UnresolvedThreads counts unresolved review threads, outdated ones
	// included, when ThreadsKnown.
	UnresolvedThreads int
	ThreadsKnown      bool
	// RequiredChecks is "SUCCESS", "PENDING", or "FAILURE" for the head
	// commit's required checks that reported, or "" when unknown.
	// RequiredNotPassed names the required checks that have not passed.
	RequiredChecks    string
	RequiredNotPassed []string
	// Queue is a merge queue's word on an authored pull request, read only
	// in a full fetch with queues enabled; nil when no queue holds it.
	Queue *QueueEntry
	// MergedAt and MergedBy are read only for the merged list.
	MergedAt time.Time
	MergedBy string
}

type Snapshot struct {
	Login        string
	PullRequests []PullRequest
	// ReviewRequests lists pending direct review requests, then pull requests
	// the viewer reviewed after such a request (see [ReviewStatus]).
	ReviewRequests []PullRequest
	// Merged lists the viewer's most recently merged pull requests, newest
	// merge first; nil when the merged list is off and for previews.
	Merged []PullRequest
	// Preview marks a fast first look from [Client.Preview]: merge state, waiting
	// time, checks, reviews, and bots are unknown and left empty.
	Preview bool
}

type Client struct {
	path string
	// mu guards the pinned account and its token, which requests read from
	// several goroutines, and the settings fetches read: bots, needs,
	// queues, and merged.
	mu sync.Mutex
	// bots are the review bots fetches judge.
	bots []Bot
	// login is the pinned account, or "" to follow gh's active account;
	// token is its token once read.
	login string
	token secret
	// needs are the rule fields fetches select.
	needs Needs
	// queues are the merge queues fetches read.
	queues []Queue
	// merged is how many merged pull requests fetches list; 0 is none.
	merged int
	// api answers gh commands in place of gh, for tests; nil runs gh.
	api func(ctx context.Context, message string, args ...string) ([]byte, error)
	// clock is the time fetches use, for tests; nil is time.Now.
	clock func() time.Time
	// cache is what the next incremental fetch reuses; nil makes it complete.
	// cacheGeneration increments when the cache is dropped, so a fetch that
	// started before never writes it.
	cache           *fetchCache
	cacheGeneration uint64
	// fullEvery is how often a fetch is complete; zero leaves it to drops.
	fullEvery time.Duration
}

func (c *Client) currentTime() time.Time {
	if c.clock != nil {
		return c.clock()
	}
	return time.Now()
}

type AuthError struct {
	Err error
}

func (e *AuthError) Error() string { return e.Err.Error() }
func (e *AuthError) Unwrap() error { return e.Err }

// pageSize keeps each GraphQL request well inside GitHub's time limit of
// about 10 seconds, after which it answers 502 or 504. With bot fields a
// pull request takes roughly 0.15 seconds to resolve.
const pageSize = 25

// pullRequestFields are selected for both lists. Nested connections select no
// pageInfo, so gh --paginate follows only the outer connection.
func pullRequestFields(bots bool) string {
	extra, head := "", "\n            committedDate\n            statusCheckRollup { state }"
	if bots {
		extra, head = botFields, headCheckFields
	}
	return `
        id
        number
        title
        url
        isDraft
        mergeable
        mergeStateStatus
        updatedAt
        createdAt
        headRefOid
        additions
        deletions
        reviewDecision
        commentCount: comments { totalCount }
        repository { nameWithOwner }
        readyEvents: timelineItems(itemTypes: [READY_FOR_REVIEW_EVENT], last: 1) {
          nodes { ... on ReadyForReviewEvent { createdAt } }
        }
        latestOpinionatedReviews(first: 20) { nodes { state } }
        commits(last: 1) { nodes { commit {` + head + `
        } } }` + extra
}

// authoredFields are the authored list's fields.
func authoredFields(bots bool, needs Needs, queues []Queue) string {
	return pullRequestFields(bots) + needsFields(needs) + queueFields(queues)
}

// reviewFields are a review search's fields plus extra.
func reviewFields(bots bool, extra string) string {
	return pullRequestFields(bots) + `
        author { login }
        requestEvents: timelineItems(itemTypes: [REVIEW_REQUESTED_EVENT], last: 20) {
          nodes { ... on ReviewRequestedEvent { createdAt requestedReviewer { ... on User { login } } } }
        }` + extra
}

func pullRequestsQuery(bots bool, needs Needs, queues []Queue) string {
	return `query($endCursor: String) {
  viewer {
    login
    pullRequests(first: ` + strconv.Itoa(pageSize) + `, after: $endCursor, states: [OPEN],
                 orderBy: {field: UPDATED_AT, direction: DESC}) {
      nodes {` + authoredFields(bots, needs, queues) + `
      }
      pageInfo { hasNextPage endCursor }
    }
  }
}`
}

// reviewSearch finds open pull requests that request a review from the
// viewer directly; requests to the viewer's teams are excluded.
const reviewSearch = "is:pr is:open user-review-requested:@me archived:false sort:updated-desc"

// reviewRequestsQuery lists the reviewSearch results.
func reviewRequestsQuery(bots bool) string {
	return searchQuery(reviewSearch, bots, "")
}

// searchQuery lists a pull request search with the review-pane fields and
// extra fields.
func searchQuery(search string, bots bool, extra string) string {
	return `query($endCursor: String) {
  search(type: ISSUE, first: ` + strconv.Itoa(pageSize) + `, after: $endCursor,
         query: "` + search + `") {
    nodes {
      ... on PullRequest {` + reviewFields(bots, extra) + `
      }
    }
    pageInfo { hasNextPage endCursor }
  }
}`
}

// previewPageSize is larger than pageSize: preview fields resolve quickly.
const previewPageSize = 100

// previewFields are the fields GitHub answers quickly. mergeStateStatus is
// left out: it takes several times longer than all of these together.
const previewFields = `
        number
        title
        url
        isDraft
        mergeable
        updatedAt
        createdAt
        additions
        deletions
        repository { nameWithOwner }`

func previewPullRequestsQuery() string {
	return `query($endCursor: String) {
  viewer {
    login
    pullRequests(first: ` + strconv.Itoa(previewPageSize) + `, after: $endCursor, states: [OPEN],
                 orderBy: {field: UPDATED_AT, direction: DESC}) {
      nodes {` + previewFields + `
      }
      pageInfo { hasNextPage endCursor }
    }
  }
}`
}

func previewReviewRequestsQuery() string {
	return `query($endCursor: String) {
  search(type: ISSUE, first: ` + strconv.Itoa(previewPageSize) + `, after: $endCursor,
         query: "` + reviewSearch + `") {
    nodes {
      ... on PullRequest {` + previewFields + `
        author { login }
      }
    }
    pageInfo { hasNextPage endCursor }
  }
}`
}

// pullRequestNode decodes pullRequestFields plus the review-only fields.
type pullRequestNode struct {
	ID             string    `json:"id"`
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	URL            string    `json:"url"`
	Draft          bool      `json:"isDraft"`
	Mergeable      string    `json:"mergeable"`
	MergeState     string    `json:"mergeStateStatus"`
	UpdatedAt      time.Time `json:"updatedAt"`
	CreatedAt      time.Time `json:"createdAt"`
	Additions      int       `json:"additions"`
	Deletions      int       `json:"deletions"`
	ReviewDecision string    `json:"reviewDecision"`
	CommentCount   struct {
		TotalCount int `json:"totalCount"`
	} `json:"commentCount"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	ReadyEvents struct {
		Nodes []*struct {
			CreatedAt time.Time `json:"createdAt"`
		} `json:"nodes"`
	} `json:"readyEvents"`
	LatestOpinionatedReviews struct {
		Nodes []*struct {
			State string `json:"state"`
		} `json:"nodes"`
	} `json:"latestOpinionatedReviews"`
	Commits struct {
		Nodes []*struct {
			Commit struct {
				CommittedDate     time.Time `json:"committedDate"`
				StatusCheckRollup *struct {
					State    string `json:"state"`
					Contexts struct {
						Nodes []*checkRun `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
	Author *struct {
		Login string `json:"login"`
	} `json:"author"`
	RequestEvents struct {
		Nodes []*struct {
			CreatedAt         time.Time `json:"createdAt"`
			RequestedReviewer *struct {
				Login string `json:"login"`
			} `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"requestEvents"`
	HeadRefOid string `json:"headRefOid"`
	Activity   struct {
		Nodes []*activityNode `json:"nodes"`
	} `json:"activity"`
	botNodes
	needsNodes
	queueNodes
}

// pullRequest converts a node. A non-empty login selects the latest direct
// review request of that login as the waiting time.
func (node *pullRequestNode) pullRequest(login string, bots []Bot) PullRequest {
	pr := PullRequest{
		Number:         node.Number,
		Title:          node.Title,
		URL:            node.URL,
		Repository:     node.Repository.NameWithOwner,
		Draft:          node.Draft,
		Mergeable:      node.Mergeable,
		MergeState:     node.MergeState,
		UpdatedAt:      node.UpdatedAt,
		CreatedAt:      node.CreatedAt,
		ReviewDecision: node.ReviewDecision,
		Additions:      node.Additions,
		Deletions:      node.Deletions,
		Comments:       node.CommentCount.TotalCount,
		ID:             node.ID,
	}
	if node.Author != nil {
		pr.Author = node.Author.Login
	}
	if !node.Draft {
		pr.WaitingSince = node.CreatedAt
		if events := node.ReadyEvents.Nodes; len(events) > 0 && events[len(events)-1] != nil {
			pr.WaitingSince = events[len(events)-1].CreatedAt
		}
	}
	if login != "" {
		// Timeline events are chronological, so the last match is the latest request.
		requests := 0
		for _, event := range node.RequestEvents.Nodes {
			if event != nil && event.RequestedReviewer != nil && strings.EqualFold(event.RequestedReviewer.Login, login) {
				pr.WaitingSince = event.CreatedAt
				requests++
			}
		}
		pr.RequestedAgain = requests > 1
	}
	for _, review := range node.LatestOpinionatedReviews.Nodes {
		switch {
		case review == nil:
		case review.State == "APPROVED":
			pr.Approvals++
		case review.State == "CHANGES_REQUESTED":
			pr.ChangesRequested++
		}
	}
	var headDate time.Time
	var checks []*checkRun
	if commits := node.Commits.Nodes; len(commits) > 0 && commits[0] != nil {
		headDate = commits[0].Commit.CommittedDate
		if rollup := commits[0].Commit.StatusCheckRollup; rollup != nil {
			pr.Checks = rollup.State
			checks = rollup.Contexts.Nodes
		}
	}
	if len(bots) > 0 {
		pr.Bots = make([]BotReview, len(bots))
		for i, bot := range bots {
			pr.Bots[i] = botReview(bot, &node.botNodes, headDate, checks)
		}
	}
	node.applyNeeds(&pr)
	pr.Queue = node.queueEntry()
	pr.sig = node.signature()
	return pr
}

// NewClient finds gh. Pull requests report on the given review bots.
func NewClient(bots []Bot) (*Client, error) {
	path, err := exec.LookPath("gh")
	if err != nil {
		return nil, err
	}
	return &Client{path: path, bots: bots}, nil
}

// Bots returns the review bots fetches judge.
func (c *Client) Bots() []Bot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bots
}

// SetBots replaces the review bots; a fetch already running keeps the ones
// it started with.
func (c *Client) SetBots(bots []Bot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bots = bots
	c.dropCache()
}

// Fetch checks authentication, then fetches both lists, and the required
// checks of authored pull requests when [Needs] asks for them; every query
// must succeed. A pinned account's token is read from gh again first, so a
// new login or refresh of that account takes effect. A fetch is complete when
// nothing is cached, after ForceFull or a setter dropped the cache, or once
// SetFullRefresh's interval passed since the last complete fetch; otherwise it
// is incremental (see fetchIncremental). Either returns every list in full.
func (c *Client) Fetch(ctx context.Context) (Snapshot, error) {
	if _, err := c.accountToken(ctx, true); err != nil {
		c.ForceFull()
		return Snapshot{}, err
	}
	if _, err := c.output(ctx, "GitHub authentication check failed", "auth", "status", "--active", "--hostname", "github.com"); err != nil {
		c.ForceFull()
		var authErr *AuthError
		if errors.As(err, &authErr) {
			return Snapshot{}, err
		}
		return Snapshot{}, &AuthError{Err: err}
	}
	now := c.currentTime()
	// Settings, cache, and generation are read together, so a setter that
	// drops the cache later makes this fetch's write stale.
	c.mu.Lock()
	bots, needs, queues, mergedLimit := c.bots, c.needs, c.queues, c.merged
	cache, generation := c.cache, c.cacheGeneration
	complete := cache == nil || (c.fullEvery > 0 && now.Sub(cache.lastComplete) >= c.fullEvery)
	c.mu.Unlock()
	type mergedResult struct {
		prs []PullRequest
		err error
	}
	// Either half failing fails the fetch, so it stops the other; the
	// cause is the first failure, not the cancellation it led to.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	mergedDone := make(chan mergedResult, 1)
	go func() {
		if mergedLimit == 0 {
			mergedDone <- mergedResult{}
			return
		}
		prs, err := c.fetchMerged(ctx, mergedLimit)
		if err != nil {
			cancel(err)
		}
		mergedDone <- mergedResult{prs, err}
	}()
	var l lists
	var err error
	lastComplete := now
	if complete {
		l, err = c.fetchComplete(ctx, bots, needs, queues, now)
	} else {
		var became bool
		l, became, err = c.fetchIncremental(ctx, cache, bots, needs, queues, now)
		if !became {
			lastComplete = cache.lastComplete
		}
	}
	if err != nil {
		cancel(err)
	}
	merged := <-mergedDone
	if err != nil || merged.err != nil {
		err = context.Cause(ctx)
	}
	c.mu.Lock()
	if c.cacheGeneration == generation {
		if err != nil {
			c.dropCache()
		} else {
			c.cache = newFetchCache(l, lastComplete)
		}
	}
	c.mu.Unlock()
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := l.snapshot()
	snapshot.Merged = merged.prs
	if len(merged.prs) > 0 {
		// A pull request that merged while the queries ran can be in both;
		// it is no longer open, so the merged list wins. Only the snapshot
		// drops it; the cache is the lists as fetched.
		snapshot.PullRequests = slices.DeleteFunc(slices.Clone(snapshot.PullRequests), func(pr PullRequest) bool {
			return slices.ContainsFunc(merged.prs, func(m PullRequest) bool {
				return (m.ID != "" && m.ID == pr.ID) || (strings.EqualFold(m.Repository, pr.Repository) && m.Number == pr.Number)
			})
		})
	}
	return snapshot, nil
}

// fetchComplete runs every list's full query, then the required checks
// when the rules need them.
func (c *Client) fetchComplete(ctx context.Context, bots []Bot, needs Needs, queues []Queue, now time.Time) (lists, error) {
	hasBots := len(bots) > 0
	l, err := c.fetchLists(ctx, pullRequestsQuery(hasBots, needs, queues), reviewRequestsQuery(hasBots), reviewedQuery(hasBots, now), bots)
	if err != nil || !needs.RequiredChecks {
		return l, err
	}
	if err := c.requiredChecks(ctx, l.authored); err != nil {
		return lists{}, err
	}
	return l, nil
}

// Preview fetches both lists with only the fields GitHub answers quickly, for
// showing rows while [Client.Fetch] is still running. It skips the
// authentication check, which Fetch reports, and reviewed pull requests,
// which need the fields Preview leaves out to be placed.
func (c *Client) Preview(ctx context.Context) (Snapshot, error) {
	l, err := c.fetchLists(ctx, previewPullRequestsQuery(), previewReviewRequestsQuery(), "", nil)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := l.snapshot()
	snapshot.Preview = true
	for _, list := range [][]PullRequest{snapshot.PullRequests, snapshot.ReviewRequests} {
		for i := range list {
			list[i].WaitingSince = time.Time{}
		}
	}
	return snapshot, nil
}

// lists are the three fetched lists before mergeReviews joins them.
type lists struct {
	login              string
	authored, requests []PullRequest
	reviewed           []listed
}

// snapshot joins the lists as Fetch returns them.
func (l lists) snapshot() Snapshot {
	var kept []PullRequest
	for _, r := range l.reviewed {
		if r.keep {
			kept = append(kept, r.pr)
		}
	}
	requests := l.requests
	if len(kept) > 0 {
		requests = mergeReviews(l.requests, kept)
	}
	return Snapshot{Login: l.login, PullRequests: l.authored, ReviewRequests: requests}
}

type listOutput struct {
	data []byte
	err  error
}

// graphqlLists runs the authored, review request, and reviewed list queries
// at once, skipping an empty one.
func (c *Client) graphqlLists(ctx context.Context, authored, requests, reviewed string) [3]listOutput {
	messages := [3]string{"GitHub pull request query failed", "GitHub review request query failed", "GitHub reviewed pull request query failed"}
	var outputs [3]listOutput
	var wg sync.WaitGroup
	for i, query := range [3]string{authored, requests, reviewed} {
		if query == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := c.output(ctx, messages[i], "api", "graphql", "--hostname", "github.com", "--paginate", "--slurp", "-f", "query="+query)
			outputs[i] = listOutput{data, err}
		}()
	}
	wg.Wait()
	return outputs
}

// fetchLists fetches and decodes the lists, the reviewed one unless its
// query is empty. Every query must succeed.
func (c *Client) fetchLists(ctx context.Context, authored, requests, reviewed string, bots []Bot) (lists, error) {
	out := c.graphqlLists(ctx, authored, requests, reviewed)
	if out[0].err != nil {
		return lists{}, out[0].err
	}
	snapshot, err := decodePages(out[0].data, bots)
	if err != nil {
		return lists{}, err
	}
	l := lists{login: snapshot.Login, authored: snapshot.PullRequests}
	if out[1].err != nil {
		return lists{}, out[1].err
	}
	if l.requests, err = decodeReviewPages(out[1].data, l.login, bots); err != nil {
		return lists{}, err
	}
	if reviewed == "" {
		return l, nil
	}
	if out[2].err != nil {
		return lists{}, out[2].err
	}
	if l.reviewed, err = decodeReviewedNodes(out[2].data, l.login, bots); err != nil {
		return lists{}, err
	}
	return l, nil
}

func (c *Client) ListRepositories(ctx context.Context) ([]string, error) {
	data, err := c.output(ctx, "GitHub repository list failed", "api", "--hostname", "github.com", "--paginate", "--slurp",
		"user/repos?visibility=all&affiliation=owner,collaborator,organization_member&sort=full_name&direction=asc&per_page=100")
	if err != nil {
		return nil, err
	}
	return decodeRepositoryPages(data)
}

func (c *Client) ResolveRepository(ctx context.Context, fullName string) (string, error) {
	fullName = strings.TrimSpace(fullName)
	if !ValidRepositoryName(fullName) {
		return "", errors.New("Enter a repository as owner/repo.")
	}
	data, err := c.output(ctx, "GitHub repository lookup failed", "api", "--hostname", "github.com", "repos/"+fullName)
	if err != nil {
		return "", err
	}
	return decodeRepository(data)
}

func ValidRepositoryName(fullName string) bool {
	owner, name, ok := strings.Cut(fullName, "/")
	return ok && ValidOwnerName(owner) && ValidOwnerName(name)
}

// ValidOwnerName reports whether name can be a repository owner: the same
// characters a repository name part allows.
func ValidOwnerName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

func decodeRepositoryPages(data []byte) ([]string, error) {
	var pages []json.RawMessage
	if err := json.Unmarshal(data, &pages); err != nil {
		return nil, fmt.Errorf("decode GitHub repository list: %w", err)
	}
	if len(pages) == 0 {
		return nil, errors.New("decode GitHub repository list: no pages returned")
	}
	names := make([]string, 0)
	seen := make(map[string]struct{})
	for pageIndex, pageData := range pages {
		var repositories []json.RawMessage
		if err := json.Unmarshal(pageData, &repositories); err != nil || repositories == nil {
			return nil, fmt.Errorf("decode GitHub repository page %d: expected repository array", pageIndex+1)
		}
		for repositoryIndex, repositoryData := range repositories {
			name, err := decodeRepository(repositoryData)
			if err != nil {
				return nil, fmt.Errorf("decode GitHub repository page %d item %d: invalid full_name", pageIndex+1, repositoryIndex+1)
			}
			key := strings.ToLower(name)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			names = append(names, name)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	return names, nil
}

func decodeRepository(data []byte) (string, error) {
	var repository struct {
		FullName *string `json:"full_name"`
	}
	if err := json.Unmarshal(data, &repository); err != nil || repository.FullName == nil ||
		!ValidRepositoryName(*repository.FullName) {
		return "", errors.New("decode GitHub repository lookup: invalid full_name")
	}
	return *repository.FullName, nil
}

func (c *Client) LoginCommand(ctx context.Context) *exec.Cmd {
	return exec.CommandContext(ctx, c.path, "auth", "login", "--hostname", "github.com", "--web")
}

// OpenInBrowser opens a pull request's page in the browser that gh is
// configured to use. With an account pinned it opens the URL without gh,
// which would pass the token on to the browser.
func (c *Client) OpenInBrowser(ctx context.Context, url string) error {
	if c.PinnedAccount() != "" {
		return c.openURL(ctx, url)
	}
	if _, err := c.openCommand(ctx, url).Output(); err != nil {
		return commandError("Could not open the browser", err)
	}
	return nil
}

func (c *Client) openCommand(ctx context.Context, url string) *exec.Cmd {
	// "--" keeps the URL from being read as a flag.
	return exec.CommandContext(ctx, c.path, "pr", "view", "--web", "--", url)
}

func commandError(message string, err error) error {
	var detail string
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		detail = strings.TrimSpace(string(exitErr.Stderr))
	}
	if detail != "" {
		return fmt.Errorf("%s: %w: %s", message, err, detail)
	}
	return fmt.Errorf("%s: %w", message, err)
}

func decodePages(data []byte, bots []Bot) (Snapshot, error) {
	var pages []json.RawMessage
	if err := json.Unmarshal(data, &pages); err != nil {
		return Snapshot{}, fmt.Errorf("decode GitHub pull request response: %w", err)
	}
	if len(pages) == 0 {
		return Snapshot{}, errors.New("decode GitHub pull request response: no pages returned")
	}

	var snapshot Snapshot
	for i, pageData := range pages {
		var page struct {
			Data struct {
				Viewer *struct {
					Login        string `json:"login"`
					PullRequests *struct {
						Nodes []*pullRequestNode `json:"nodes"`
					} `json:"pullRequests"`
				} `json:"viewer"`
			} `json:"data"`
			Errors []json.RawMessage `json:"errors"`
		}
		if err := json.Unmarshal(pageData, &page); err != nil {
			return Snapshot{}, fmt.Errorf("decode GitHub pull request page %d: %w", i+1, err)
		}
		if len(page.Errors) != 0 {
			return Snapshot{}, fmt.Errorf("GitHub pull request page %d returned GraphQL errors: %s", i+1, graphQLErrors(page.Errors))
		}
		if page.Data.Viewer == nil {
			return Snapshot{}, fmt.Errorf("GitHub pull request page %d has no viewer", i+1)
		}
		if page.Data.Viewer.PullRequests == nil {
			return Snapshot{}, fmt.Errorf("GitHub pull request page %d has no pull request connection", i+1)
		}
		if i == 0 {
			snapshot.Login = page.Data.Viewer.Login
		}
		for _, node := range page.Data.Viewer.PullRequests.Nodes {
			if node == nil {
				continue
			}
			snapshot.PullRequests = append(snapshot.PullRequests, node.pullRequest("", bots))
		}
	}
	return snapshot, nil
}

func decodeReviewPages(data []byte, login string, bots []Bot) ([]PullRequest, error) {
	return decodeSearchPages(data, "review request", func(node *pullRequestNode) (PullRequest, bool) {
		return node.pullRequest(login, bots), true
	})
}

// decodeSearchPages decodes a pull request search; convert turns each node
// into a pull request or skips it.
func decodeSearchPages(data []byte, name string, convert func(*pullRequestNode) (PullRequest, bool)) ([]PullRequest, error) {
	var pages []json.RawMessage
	if err := json.Unmarshal(data, &pages); err != nil {
		return nil, fmt.Errorf("decode GitHub %s response: %w", name, err)
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("decode GitHub %s response: no pages returned", name)
	}

	var prs []PullRequest
	for i, pageData := range pages {
		var page struct {
			Data struct {
				Search *struct {
					Nodes []*pullRequestNode `json:"nodes"`
				} `json:"search"`
			} `json:"data"`
			Errors []json.RawMessage `json:"errors"`
		}
		if err := json.Unmarshal(pageData, &page); err != nil {
			return nil, fmt.Errorf("decode GitHub %s page %d: %w", name, i+1, err)
		}
		if len(page.Errors) != 0 {
			return nil, fmt.Errorf("GitHub %s page %d returned GraphQL errors: %s", name, i+1, graphQLErrors(page.Errors))
		}
		if page.Data.Search == nil {
			return nil, fmt.Errorf("GitHub %s page %d has no search connection", name, i+1)
		}
		for _, node := range page.Data.Search.Nodes {
			// Search can return non-pull-request nodes, which decode empty.
			if node == nil || node.URL == "" {
				continue
			}
			if pr, ok := convert(node); ok {
				prs = append(prs, pr)
			}
		}
	}
	return prs, nil
}

// graphQLErrors joins a response's raw GraphQL errors for an error message.
func graphQLErrors(messages []json.RawMessage) string {
	result := make([]string, len(messages))
	for i, message := range messages {
		result[i] = string(message)
	}
	return strings.Join(result, "; ")
}

// RateLimit is the viewer's GraphQL quota, which pull request fetches use.
type RateLimit struct {
	Limit     int
	Remaining int
	Reset     time.Time
}

// rateLimitQuery selects only rateLimit, which GitHub does not charge for.
// The REST rate_limit endpoint's graphql resource does not track the points
// GraphQL queries spend, so it is not used.
const rateLimitQuery = `{ rateLimit { limit remaining resetAt } }`

// RateLimit reads the quota without spending it.
func (c *Client) RateLimit(ctx context.Context) (RateLimit, error) {
	data, err := c.output(ctx, "GitHub rate limit query failed", "api", "graphql", "--hostname", "github.com", "-f", "query="+rateLimitQuery)
	if err != nil {
		return RateLimit{}, err
	}
	return decodeRateLimit(data)
}

func decodeRateLimit(data []byte) (RateLimit, error) {
	var response struct {
		Data struct {
			RateLimit *struct {
				Limit     *int       `json:"limit"`
				Remaining *int       `json:"remaining"`
				ResetAt   *time.Time `json:"resetAt"`
			} `json:"rateLimit"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return RateLimit{}, fmt.Errorf("decode GitHub rate limit: %w", err)
	}
	if len(response.Errors) != 0 {
		return RateLimit{}, fmt.Errorf("GitHub rate limit query returned GraphQL errors: %s", graphQLErrors(response.Errors))
	}
	pool := response.Data.RateLimit
	if pool == nil || pool.Limit == nil || pool.Remaining == nil || pool.ResetAt == nil || *pool.Limit <= 0 || *pool.Remaining < 0 {
		return RateLimit{}, errors.New("decode GitHub rate limit: invalid rateLimit")
	}
	return RateLimit{Limit: *pool.Limit, Remaining: *pool.Remaining, Reset: *pool.ResetAt}, nil
}

// signature is what the cheap signature queries read of a pull request:
// enough to tell whether the rest may have changed since it was fetched.
type signature struct {
	updatedAt   time.Time
	headOid     string
	mergeable   string
	draft       bool
	checks      string
	githubQueue string
}

// same reports whether two signatures read the same pull request state.
func (s signature) same(o signature) bool {
	return s.updatedAt.Equal(o.updatedAt) && s.headOid == o.headOid && s.mergeable == o.mergeable &&
		s.draft == o.draft && s.checks == o.checks && s.githubQueue == o.githubQueue
}

// signature reads a node's signature fields, which the full fields include.
func (node *pullRequestNode) signature() signature {
	s := signature{updatedAt: node.UpdatedAt, headOid: node.HeadRefOid, mergeable: node.Mergeable, draft: node.Draft}
	if commits := node.Commits.Nodes; len(commits) > 0 && commits[0] != nil && commits[0].Commit.StatusCheckRollup != nil {
		s.checks = commits[0].Commit.StatusCheckRollup.State
	}
	if entry := node.MergeQueueEntry; entry != nil {
		s.githubQueue = entry.State
	}
	return s
}
