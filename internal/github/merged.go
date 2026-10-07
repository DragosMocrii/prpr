package github

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"
)

// mergedQuery lists the viewer's merged pull requests by update time.
// GitHub cannot order them by merge time, and a merge updates the pull
// request, so recent merges come first; the margin past limit leaves room
// for older merged pull requests updated since, such as by a comment.
func mergedQuery(limit int) string {
	return `query {
  viewer {
    login
    pullRequests(first: ` + strconv.Itoa(2*limit+10) + `, states: [MERGED],
                 orderBy: {field: UPDATED_AT, direction: DESC}) {
      nodes {
        id
        number
        title
        url
        mergedAt
        mergedBy { login }
        additions
        deletions
        repository { nameWithOwner }
      }
    }
  }
}`
}

// decodeMerged decodes the merged query's answer, newest merge first (ties
// in fetch order), cut to limit.
func decodeMerged(data []byte, limit int) ([]PullRequest, error) {
	var page struct {
		Data struct {
			Viewer *struct {
				PullRequests *struct {
					Nodes []*struct {
						ID       string    `json:"id"`
						Number   int       `json:"number"`
						Title    string    `json:"title"`
						URL      string    `json:"url"`
						MergedAt time.Time `json:"mergedAt"`
						MergedBy *struct {
							Login string `json:"login"`
						} `json:"mergedBy"`
						Additions  int `json:"additions"`
						Deletions  int `json:"deletions"`
						Repository struct {
							NameWithOwner string `json:"nameWithOwner"`
						} `json:"repository"`
					} `json:"nodes"`
				} `json:"pullRequests"`
			} `json:"viewer"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, fmt.Errorf("decode GitHub merged pull request response: %w", err)
	}
	if len(page.Errors) != 0 {
		return nil, fmt.Errorf("GitHub merged pull request query returned GraphQL errors: %s", graphQLErrors(page.Errors))
	}
	if page.Data.Viewer == nil || page.Data.Viewer.PullRequests == nil {
		return nil, errors.New("GitHub merged pull request response has no pull request connection")
	}
	var prs []PullRequest
	for _, node := range page.Data.Viewer.PullRequests.Nodes {
		if node == nil {
			continue
		}
		pr := PullRequest{ID: node.ID, Number: node.Number, Title: node.Title, URL: node.URL,
			Repository: node.Repository.NameWithOwner, MergedAt: node.MergedAt,
			Additions: node.Additions, Deletions: node.Deletions}
		if node.MergedBy != nil {
			pr.MergedBy = node.MergedBy.Login
		}
		prs = append(prs, pr)
	}
	slices.SortStableFunc(prs, func(a, b PullRequest) int { return cmp.Compare(b.MergedAt.UnixNano(), a.MergedAt.UnixNano()) })
	return prs[:min(len(prs), limit)], nil
}

// fetchMerged lists the viewer's limit most recently merged pull requests.
func (c *Client) fetchMerged(ctx context.Context, limit int) ([]PullRequest, error) {
	data, err := c.output(ctx, "GitHub merged pull request query failed", "api", "graphql", "--hostname", "github.com", "-f", "query="+mergedQuery(limit))
	if err != nil {
		return nil, err
	}
	return decodeMerged(data, limit)
}

// SetMerged chooses how many merged pull requests later fetches list; 0
// lists none. The merged list is never cached, so the cache stays.
func (c *Client) SetMerged(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.merged = n
}
