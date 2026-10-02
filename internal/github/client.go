package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
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
	// ChangesRequested counts the latest reviews that request changes.
	ChangesRequested int
	// The fields below are read only for authored pull requests, and only
	// when the client's [Needs] ask for them; each is unknown otherwise.
	// ID is GitHub's node ID, read for the required checks.
	ID string
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
}

type Snapshot struct {
	Login        string
	PullRequests []PullRequest
	// ReviewRequests lists pending direct review requests, then pull requests
	// the viewer reviewed after such a request (see [ReviewStatus]).
	ReviewRequests []PullRequest
	// Preview marks a fast first look from [Client.Preview]: merge state, waiting
	// time, checks, reviews, and bots are unknown and left empty.
	Preview bool
}

type Client struct {
	path string
	bots []Bot
	// mu guards the pinned account and its token, which requests read from
	// several goroutines.
	mu sync.Mutex
	// login is the pinned account, or "" to follow gh's active account;
	// token is its token once read.
	login string
	token secret
	// needs are the rule fields fetches select.
	needs Needs
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
        number
        title
        url
        isDraft
        mergeable
        mergeStateStatus
        updatedAt
        createdAt
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

func pullRequestsQuery(bots bool, needs Needs) string {
	return `query($endCursor: String) {
  viewer {
    login
    pullRequests(first: ` + strconv.Itoa(pageSize) + `, after: $endCursor, states: [OPEN],
                 orderBy: {field: UPDATED_AT, direction: DESC}) {
      nodes {` + pullRequestFields(bots) + needsFields(needs) + `
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
      ... on PullRequest {` + pullRequestFields(bots) + `
        author { login }
        requestEvents: timelineItems(itemTypes: [REVIEW_REQUESTED_EVENT], last: 20) {
          nodes { ... on ReviewRequestedEvent { createdAt requestedReviewer { ... on User { login } } } }
        }` + extra + `
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
		for _, event := range node.RequestEvents.Nodes {
			if event != nil && event.RequestedReviewer != nil && strings.EqualFold(event.RequestedReviewer.Login, login) {
				pr.WaitingSince = event.CreatedAt
			}
		}
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

// Bots returns the configured review bots.
func (c *Client) Bots() []Bot { return c.bots }

// Fetch checks authentication, then fetches both lists, and the required
// checks of authored pull requests when [Needs] asks for them; every query
// must succeed. A pinned account's token is read from gh again first, so a
// new login or refresh of that account takes effect.
func (c *Client) Fetch(ctx context.Context) (Snapshot, error) {
	if _, err := c.accountToken(ctx, true); err != nil {
		return Snapshot{}, err
	}
	if _, err := c.output(ctx, "GitHub authentication check failed", "auth", "status", "--active", "--hostname", "github.com"); err != nil {
		var authErr *AuthError
		if errors.As(err, &authErr) {
			return Snapshot{}, err
		}
		return Snapshot{}, &AuthError{Err: err}
	}
	bots, needs := len(c.bots) > 0, c.currentNeeds()
	snapshot, err := c.run(ctx, pullRequestsQuery(bots, needs), reviewRequestsQuery(bots), reviewedQuery(bots, time.Now()), c.bots)
	if err != nil || !needs.RequiredChecks {
		return snapshot, err
	}
	if err := c.requiredChecks(ctx, snapshot.PullRequests); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

// Preview fetches both lists with only the fields GitHub answers quickly, for
// showing rows while [Client.Fetch] is still running. It skips the
// authentication check, which Fetch reports, and reviewed pull requests,
// which need the fields Preview leaves out to be placed.
func (c *Client) Preview(ctx context.Context) (Snapshot, error) {
	snapshot, err := c.run(ctx, previewPullRequestsQuery(), previewReviewRequestsQuery(), "", nil)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.Preview = true
	for _, list := range [][]PullRequest{snapshot.PullRequests, snapshot.ReviewRequests} {
		for i := range list {
			list[i].WaitingSince = time.Time{}
		}
	}
	return snapshot, nil
}

// run fetches both lists with the given queries, and the pull requests the
// viewer reviewed unless reviewed is empty. Every query must succeed.
func (c *Client) run(ctx context.Context, pullRequests, reviewRequests, reviewed string, bots []Bot) (Snapshot, error) {
	// The lists are independent, so the queries run at once.
	type output struct {
		data []byte
		err  error
	}
	search := func(message, query string) chan output {
		result := make(chan output, 1)
		if query == "" {
			result <- output{}
			return result
		}
		go func() {
			data, err := c.output(ctx, message, "api", "graphql", "--hostname", "github.com", "--paginate", "--slurp", "-f", "query="+query)
			result <- output{data, err}
		}()
		return result
	}
	reviews := search("GitHub review request query failed", reviewRequests)
	reviewedOutput := search("GitHub reviewed pull request query failed", reviewed)

	data, err := c.output(ctx, "GitHub pull request query failed", "api", "graphql", "--hostname", "github.com", "--paginate", "--slurp", "-f", "query="+pullRequests)
	review, past := <-reviews, <-reviewedOutput
	if err != nil {
		return Snapshot{}, err
	}
	snapshot, err := decodePages(data, bots)
	if err != nil {
		return Snapshot{}, err
	}
	if review.err != nil {
		return Snapshot{}, review.err
	}
	snapshot.ReviewRequests, err = decodeReviewPages(review.data, snapshot.Login, bots)
	if err != nil {
		return Snapshot{}, err
	}
	if reviewed == "" {
		return snapshot, nil
	}
	if past.err != nil {
		return Snapshot{}, past.err
	}
	done, err := decodeReviewedPages(past.data, snapshot.Login, bots)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.ReviewRequests = mergeReviews(snapshot.ReviewRequests, done)
	return snapshot, nil
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
	parts := strings.Split(fullName, "/")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, r := range part {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.') {
				return false
			}
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
			var repository struct {
				FullName *string `json:"full_name"`
			}
			if err := json.Unmarshal(repositoryData, &repository); err != nil || repository.FullName == nil ||
				!ValidRepositoryName(*repository.FullName) {
				return nil, fmt.Errorf("decode GitHub repository page %d item %d: invalid full_name", pageIndex+1, repositoryIndex+1)
			}
			name := *repository.FullName
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
			return Snapshot{}, fmt.Errorf("GitHub pull request page %d returned GraphQL errors: %s", i+1, strings.Join(rawMessages(page.Errors), "; "))
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

// decodeReviewedPages decodes the reviewed search, keeping the pull requests
// that directly requested the viewer's review.
func decodeReviewedPages(data []byte, login string, bots []Bot) ([]PullRequest, error) {
	return decodeSearchPages(data, "reviewed pull request", func(node *pullRequestNode) (PullRequest, bool) {
		pr := node.pullRequest("", bots)
		var head time.Time
		if commits := node.Commits.Nodes; len(commits) > 0 && commits[0] != nil {
			head = commits[0].Commit.CommittedDate
		}
		status, since, ok := node.reviewedStatus(login, head)
		pr.ReviewStatus, pr.WaitingSince = status, since
		return pr, ok
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
			return nil, fmt.Errorf("GitHub %s page %d returned GraphQL errors: %s", name, i+1, strings.Join(rawMessages(page.Errors), "; "))
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

func rawMessages(messages []json.RawMessage) []string {
	result := make([]string, len(messages))
	for i, message := range messages {
		result[i] = string(message)
	}
	return result
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
		return RateLimit{}, fmt.Errorf("GitHub rate limit query returned GraphQL errors: %s", strings.Join(rawMessages(response.Errors), "; "))
	}
	pool := response.Data.RateLimit
	if pool == nil || pool.Limit == nil || pool.Remaining == nil || pool.ResetAt == nil || *pool.Limit <= 0 || *pool.Remaining < 0 {
		return RateLimit{}, errors.New("decode GitHub rate limit: invalid rateLimit")
	}
	return RateLimit{Limit: *pool.Limit, Remaining: *pool.Remaining, Reset: *pool.ResetAt}, nil
}
