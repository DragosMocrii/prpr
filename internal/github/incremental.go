package github

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
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
