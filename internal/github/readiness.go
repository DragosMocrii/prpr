package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Needs names the fields that only ready-to-merge rules use. Fetch selects
// them for authored pull requests alone, and only when asked, since each
// adds to the time GitHub takes to answer.
type Needs struct {
	CodeOwners     bool
	Threads        bool
	RequiredChecks bool
}

// SetNeeds chooses the rule fields later fetches select.
func (c *Client) SetNeeds(needs Needs) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.needs = needs
}

func (c *Client) currentNeeds() Needs {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.needs
}

// codeOwnerRequests is how many pending review requests are read; more
// leave the code owners unknown unless one of those read is still pending.
const codeOwnerRequests = 50

// threadCount is how many review threads are read; more leave the count
// unknown unless one of those read is unresolved.
const threadCount = 100

// needsFields are the authored query's fields for needs. The node ID the required checks query names is in every query's fields.
func needsFields(needs Needs) string {
	var fields string
	if needs.CodeOwners {
		fields += `
        codeOwnerRequests: reviewRequests(first: ` + strconv.Itoa(codeOwnerRequests) + `) {
          totalCount
          nodes { asCodeOwner requestedReviewer { ... on User { login } ... on Team { combinedSlug } ... on Bot { login } ... on Mannequin { login } } }
        }`
	}
	if needs.Threads {
		fields += `
        openThreads: reviewThreads(first: ` + strconv.Itoa(threadCount) + `) { totalCount nodes { isResolved } }`
	}
	return fields
}

type needsNodes struct {
	ID                string `json:"id"`
	CodeOwnerRequests *struct {
		TotalCount int `json:"totalCount"`
		Nodes      []*struct {
			AsCodeOwner       bool `json:"asCodeOwner"`
			RequestedReviewer *struct {
				Login        string `json:"login"`
				CombinedSlug string `json:"combinedSlug"`
			} `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"codeOwnerRequests"`
	OpenThreads *struct {
		TotalCount int `json:"totalCount"`
		Nodes      []*struct {
			IsResolved bool `json:"isResolved"`
		} `json:"nodes"`
	} `json:"openThreads"`
}

// applyNeeds copies the rule fields a node selected. A pending code owner
// that GitHub does not name, such as one hidden from the viewer, is kept as
// "", so it still counts as pending.
func (n *needsNodes) applyNeeds(pr *PullRequest) {
	if requests := n.CodeOwnerRequests; requests != nil {
		pr.PendingCodeOwners = nil
		for _, request := range requests.Nodes {
			if request == nil || !request.AsCodeOwner {
				continue
			}
			name := ""
			if reviewer := request.RequestedReviewer; reviewer != nil {
				switch {
				case reviewer.CombinedSlug != "":
					name = "@" + reviewer.CombinedSlug
				case reviewer.Login != "":
					name = "@" + reviewer.Login
				}
			}
			pr.PendingCodeOwners = append(pr.PendingCodeOwners, name)
		}
		pr.CodeOwnersKnown = requests.TotalCount <= len(requests.Nodes) || len(pr.PendingCodeOwners) > 0
	}
	if threads := n.OpenThreads; threads != nil {
		pr.UnresolvedThreads = 0
		for _, thread := range threads.Nodes {
			if thread != nil && !thread.IsResolved {
				pr.UnresolvedThreads++
			}
		}
		pr.ThreadsKnown = threads.TotalCount <= len(threads.Nodes) || pr.UnresolvedThreads > 0
	}
}

// requiredChecksBatch is how many pull requests one required checks query
// covers.
const requiredChecksBatch = 10

// requiredContexts is how many head-commit checks are read per pull request;
// more leave the required checks unknown unless one read has failed.
const requiredContexts = 100

// requiredChecksQuery reads the head-commit checks of n pull requests, $id0
// to $id(n-1), with whether each is required. GitHub answers isRequired
// only for a named pull request, so the list queries cannot select it.
func requiredChecksQuery(n int) string {
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
		fmt.Fprintf(&b, `
  p%[1]d: node(id: $id%[1]d) {
    ... on PullRequest {
      commits(last: 1) { nodes { commit { statusCheckRollup { contexts(first: %[2]d) {
        totalCount
        nodes {
          ... on CheckRun { name status conclusion isRequired(pullRequestId: $id%[1]d) }
          ... on StatusContext { context state isRequired(pullRequestId: $id%[1]d) }
        }
      } } } } }
    }
  }`, i, requiredContexts)
	}
	b.WriteString("\n}")
	return b.String()
}

type requiredContext struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Context    string `json:"context"`
	State      string `json:"state"`
	IsRequired bool   `json:"isRequired"`
}

type requiredNode struct {
	Commits struct {
		Nodes []*struct {
			Commit struct {
				StatusCheckRollup *struct {
					Contexts struct {
						TotalCount int                `json:"totalCount"`
						Nodes      []*requiredContext `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

// requiredState summarizes a head commit's required checks: "SUCCESS" when
// every required check that reported passed, "PENDING" when one is still
// running, "FAILURE" when one failed, or "" when too many checks were
// reported to tell. notPassed names the required checks that have not
// passed. A commit with no checks has nothing required that reported.
func (node *requiredNode) requiredState() (state string, notPassed []string) {
	commits := node.Commits.Nodes
	if len(commits) == 0 || commits[0] == nil || commits[0].Commit.StatusCheckRollup == nil {
		return "SUCCESS", nil
	}
	contexts := commits[0].Commit.StatusCheckRollup.Contexts
	failed, pending := false, false
	for _, check := range contexts.Nodes {
		if check == nil || !check.IsRequired {
			continue
		}
		passed, running := false, false
		name := check.Name
		if check.Context != "" || check.State != "" {
			name = check.Context
			passed = check.State == "SUCCESS"
			running = check.State == "PENDING" || check.State == "EXPECTED"
		} else {
			running = check.Status != "COMPLETED"
			passed = check.Conclusion == "SUCCESS" || check.Conclusion == "NEUTRAL" || check.Conclusion == "SKIPPED"
		}
		switch {
		case running:
			pending = true
		case passed:
			continue
		default:
			failed = true
		}
		notPassed = append(notPassed, name)
	}
	switch {
	case failed:
		return "FAILURE", notPassed
	case contexts.TotalCount > len(contexts.Nodes):
		return "", nil
	case pending:
		return "PENDING", notPassed
	}
	return "SUCCESS", nil
}

// requiredChecks fills in the required checks of authored pull requests that
// could be ready: drafts and conflicts never are, so they are skipped and
// stay unknown.
func (c *Client) requiredChecks(ctx context.Context, prs []PullRequest) error {
	var pending []int
	for i := range prs {
		if prs[i].ID != "" && !prs[i].Draft && prs[i].Mergeable != "CONFLICTING" && prs[i].MergeState != "DIRTY" {
			pending = append(pending, i)
		}
	}
	for start := 0; start < len(pending); start += requiredChecksBatch {
		batch := pending[start:min(start+requiredChecksBatch, len(pending))]
		args := []string{"api", "graphql", "--hostname", "github.com", "-f", "query=" + requiredChecksQuery(len(batch))}
		for i, index := range batch {
			args = append(args, "-f", "id"+strconv.Itoa(i)+"="+prs[index].ID)
		}
		data, err := c.output(ctx, "GitHub required check query failed", args...)
		if err != nil {
			return err
		}
		nodes, err := decodeRequiredChecks(data, len(batch))
		if err != nil {
			return err
		}
		for i, index := range batch {
			if nodes[i] != nil {
				prs[index].RequiredChecks, prs[index].RequiredNotPassed = nodes[i].requiredState()
			}
		}
	}
	return nil
}

// decodeRequiredChecks decodes a required checks query of n pull requests.
// A pull request GitHub no longer finds is nil.
func decodeRequiredChecks(data []byte, n int) ([]*requiredNode, error) {
	var response struct {
		Data   map[string]*requiredNode `json:"data"`
		Errors []json.RawMessage        `json:"errors"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode GitHub required check response: %w", err)
	}
	if len(response.Errors) != 0 {
		return nil, fmt.Errorf("GitHub required check query returned GraphQL errors: %s", graphQLErrors(response.Errors))
	}
	if response.Data == nil {
		return nil, fmt.Errorf("GitHub required check response has no data")
	}
	nodes := make([]*requiredNode, n)
	for i := range nodes {
		nodes[i] = response.Data["p"+strconv.Itoa(i)]
	}
	return nodes, nil
}
