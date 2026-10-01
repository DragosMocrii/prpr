package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
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
	UpdatedAt  time.Time
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
}

type Snapshot struct {
	Login          string
	PullRequests   []PullRequest
	ReviewRequests []PullRequest
}

type Client struct {
	path string
}

type AuthError struct {
	Err error
}

func (e *AuthError) Error() string { return e.Err.Error() }
func (e *AuthError) Unwrap() error { return e.Err }

// pullRequestFields are selected for both lists. Nested connections select no
// pageInfo, so gh --paginate follows only the outer connection.
const pullRequestFields = `
        number
        title
        url
        isDraft
        mergeable
        updatedAt
        createdAt
        additions
        deletions
        reviewDecision
        repository { nameWithOwner }
        readyEvents: timelineItems(itemTypes: [READY_FOR_REVIEW_EVENT], last: 1) {
          nodes { ... on ReadyForReviewEvent { createdAt } }
        }
        latestOpinionatedReviews(first: 20) { nodes { state } }
        commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }`

const pullRequestsQuery = `query($endCursor: String) {
  viewer {
    login
    pullRequests(first: 100, after: $endCursor, states: [OPEN],
                 orderBy: {field: UPDATED_AT, direction: DESC}) {
      nodes {` + pullRequestFields + `
      }
      pageInfo { hasNextPage endCursor }
    }
  }
}`

// reviewRequestsQuery lists open pull requests that request a review from the
// viewer directly or through one of the viewer's teams.
const reviewRequestsQuery = `query($endCursor: String) {
  search(type: ISSUE, first: 100, after: $endCursor,
         query: "is:pr is:open review-requested:@me archived:false sort:updated-desc") {
    nodes {
      ... on PullRequest {` + pullRequestFields + `
        author { login }
        requestEvents: timelineItems(itemTypes: [REVIEW_REQUESTED_EVENT], last: 20) {
          nodes { ... on ReviewRequestedEvent { createdAt requestedReviewer { ... on User { login } } } }
        }
      }
    }
    pageInfo { hasNextPage endCursor }
  }
}`

// pullRequestNode decodes pullRequestFields plus the review-only fields.
type pullRequestNode struct {
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	URL            string    `json:"url"`
	Draft          bool      `json:"isDraft"`
	Mergeable      string    `json:"mergeable"`
	UpdatedAt      time.Time `json:"updatedAt"`
	CreatedAt      time.Time `json:"createdAt"`
	Additions      int       `json:"additions"`
	Deletions      int       `json:"deletions"`
	ReviewDecision string    `json:"reviewDecision"`
	Repository     struct {
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
				StatusCheckRollup *struct {
					State string `json:"state"`
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
}

// pullRequest converts a node. A non-empty login selects the latest direct
// review request of that login as the waiting time.
func (node *pullRequestNode) pullRequest(login string) PullRequest {
	pr := PullRequest{
		Number:         node.Number,
		Title:          node.Title,
		URL:            node.URL,
		Repository:     node.Repository.NameWithOwner,
		Draft:          node.Draft,
		Mergeable:      node.Mergeable,
		UpdatedAt:      node.UpdatedAt,
		ReviewDecision: node.ReviewDecision,
		Additions:      node.Additions,
		Deletions:      node.Deletions,
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
		if review != nil && review.State == "APPROVED" {
			pr.Approvals++
		}
	}
	if commits := node.Commits.Nodes; len(commits) > 0 && commits[0] != nil && commits[0].Commit.StatusCheckRollup != nil {
		pr.Checks = commits[0].Commit.StatusCheckRollup.State
	}
	return pr
}

func NewClient() (*Client, error) {
	path, err := exec.LookPath("gh")
	if err != nil {
		return nil, err
	}
	return &Client{path: path}, nil
}

func (c *Client) Fetch(ctx context.Context) (Snapshot, error) {
	auth := exec.CommandContext(ctx, c.path, "auth", "status", "--active", "--hostname", "github.com")
	if _, err := auth.Output(); err != nil {
		return Snapshot{}, &AuthError{Err: commandError("GitHub authentication check failed", err)}
	}

	api := exec.CommandContext(ctx, c.path, "api", "graphql", "--hostname", "github.com", "--paginate", "--slurp", "-f", "query="+pullRequestsQuery)
	data, err := api.Output()
	if err != nil {
		return Snapshot{}, commandError("GitHub pull request query failed", err)
	}
	snapshot, err := decodePages(data)
	if err != nil {
		return Snapshot{}, err
	}

	review := exec.CommandContext(ctx, c.path, "api", "graphql", "--hostname", "github.com", "--paginate", "--slurp", "-f", "query="+reviewRequestsQuery)
	data, err = review.Output()
	if err != nil {
		return Snapshot{}, commandError("GitHub review request query failed", err)
	}
	snapshot.ReviewRequests, err = decodeReviewPages(data, snapshot.Login)
	if err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (c *Client) ListRepositories(ctx context.Context) ([]string, error) {
	api := exec.CommandContext(ctx, c.path, "api", "--hostname", "github.com", "--paginate", "--slurp",
		"user/repos?visibility=all&affiliation=owner,collaborator,organization_member&sort=full_name&direction=asc&per_page=100")
	data, err := api.Output()
	if err != nil {
		return nil, commandError("GitHub repository list failed", err)
	}
	return decodeRepositoryPages(data)
}

func (c *Client) ResolveRepository(ctx context.Context, fullName string) (string, error) {
	fullName = strings.TrimSpace(fullName)
	if !ValidRepositoryName(fullName) {
		return "", errors.New("Enter a repository as owner/repo.")
	}
	api := exec.CommandContext(ctx, c.path, "api", "--hostname", "github.com", "repos/"+fullName)
	data, err := api.Output()
	if err != nil {
		return "", commandError("GitHub repository lookup failed", err)
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

func decodePages(data []byte) (Snapshot, error) {
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
			snapshot.PullRequests = append(snapshot.PullRequests, node.pullRequest(""))
		}
	}
	return snapshot, nil
}

func decodeReviewPages(data []byte, login string) ([]PullRequest, error) {
	var pages []json.RawMessage
	if err := json.Unmarshal(data, &pages); err != nil {
		return nil, fmt.Errorf("decode GitHub review request response: %w", err)
	}
	if len(pages) == 0 {
		return nil, errors.New("decode GitHub review request response: no pages returned")
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
			return nil, fmt.Errorf("decode GitHub review request page %d: %w", i+1, err)
		}
		if len(page.Errors) != 0 {
			return nil, fmt.Errorf("GitHub review request page %d returned GraphQL errors: %s", i+1, strings.Join(rawMessages(page.Errors), "; "))
		}
		if page.Data.Search == nil {
			return nil, fmt.Errorf("GitHub review request page %d has no search connection", i+1)
		}
		for _, node := range page.Data.Search.Nodes {
			// Search can return non-pull-request nodes, which decode empty.
			if node == nil || node.URL == "" {
				continue
			}
			prs = append(prs, node.pullRequest(login))
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
