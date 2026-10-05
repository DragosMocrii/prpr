package github

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
)

// signaturePageSize is larger than pageSize: signature fields resolve
// quickly, like the preview's.
const signaturePageSize = 100

// signatureFields select a pull request's node ID and signature.
func signatureFields(queues []Queue) string {
	fields := `
        id
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
	var pages []json.RawMessage
	if err := json.Unmarshal(data, &pages); err != nil {
		return nil, fmt.Errorf("decode GitHub %s response: %w", name, err)
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("decode GitHub %s response: no pages returned", name)
	}

	var result []entry
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
			// Skip null or empty nodes, but keep nodes with an ID even if they lack other fields.
			if node == nil || node.ID == "" {
				continue
			}
			result = append(result, entry{id: node.ID, sig: node.signature()})
		}
	}
	return result, nil
}
