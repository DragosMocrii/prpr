package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Reviewer is a user whose review can be requested again: someone who
// reviewed a pull request, or whose review is requested now.
type Reviewer struct {
	Login string
	// State is the reviewer's latest review state, such as "APPROVED",
	// "CHANGES_REQUESTED", "COMMENTED", or "DISMISSED"; "" for no review.
	State string
	// Stale reports that the latest review is not of the head commit.
	Stale bool
	// Pending reports that the review is requested now. Requesting it again
	// changes nothing on GitHub, so the request is renewed instead.
	Pending bool
	// ReviewedAt is when the reviewer's latest review was submitted; zero
	// for no review.
	ReviewedAt time.Time
}

// reviewersQuery reads one pull request's latest review of each reviewer and
// its pending review requests. latestReviews leaves out reviewers whose review
// is requested again, as GitHub's page does, so the latest approval or change
// request of each reviewer is read too. It is not a search and selects no
// merge state, so stand-ins for gh can tell it from the list queries.
const reviewersQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      author { login }
      headRefOid
      latestReviews(first: 100) {
        nodes { ...reviewerReview }
      }
      latestOpinionatedReviews(first: 100) {
        nodes { ...reviewerReview }
      }
      reviewRequests(first: 100) {
        nodes { requestedReviewer { __typename ... on User { login } } }
      }
    }
  }
}
fragment reviewerReview on PullRequestReview { state submittedAt author { __typename login } commit { oid } }`

// Reviewers lists the users whose review of a pull request can be requested
// again, in GitHub's order: everyone who reviewed it but its author, then
// the users whose review is requested and who have not reviewed it. A
// reviewer whose review is requested again shows their latest approval or
// change request; one who only commented shows no review, since GitHub does
// not say. Bots and teams are left out.
func (c *Client) Reviewers(ctx context.Context, repository string, number int) ([]Reviewer, error) {
	owner, name, err := pullRequestTarget(repository, number)
	if err != nil {
		return nil, err
	}
	data, err := c.output(ctx, "GitHub reviewer query failed", "api", "graphql", "--hostname", "github.com",
		"-f", "query="+reviewersQuery, "-f", "owner="+owner, "-f", "name="+name, "-F", "number="+strconv.Itoa(number))
	if err != nil {
		return nil, err
	}
	return decodeReviewers(data)
}

// reviewerReview decodes the reviewerReview fragment.
type reviewerReview struct {
	State       string     `json:"state"`
	SubmittedAt *time.Time `json:"submittedAt"`
	Author      *struct {
		Typename string `json:"__typename"`
		Login    string `json:"login"`
	} `json:"author"`
	Commit *struct {
		Oid string `json:"oid"`
	} `json:"commit"`
}

func decodeReviewers(data []byte) ([]Reviewer, error) {
	var response struct {
		Data struct {
			Repository *struct {
				PullRequest *struct {
					Author *struct {
						Login string `json:"login"`
					} `json:"author"`
					HeadRefOid    string `json:"headRefOid"`
					LatestReviews struct {
						Nodes []*reviewerReview `json:"nodes"`
					} `json:"latestReviews"`
					LatestOpinionatedReviews struct {
						Nodes []*reviewerReview `json:"nodes"`
					} `json:"latestOpinionatedReviews"`
					ReviewRequests struct {
						Nodes []*struct {
							RequestedReviewer *struct {
								Typename string `json:"__typename"`
								Login    string `json:"login"`
							} `json:"requestedReviewer"`
						} `json:"nodes"`
					} `json:"reviewRequests"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode GitHub reviewers: %w", err)
	}
	if len(response.Errors) != 0 {
		return nil, fmt.Errorf("GitHub reviewer query returned GraphQL errors: %s", graphQLErrors(response.Errors))
	}
	if response.Data.Repository == nil || response.Data.Repository.PullRequest == nil {
		return nil, errors.New("GitHub reviewer query found no pull request")
	}
	pr := response.Data.Repository.PullRequest
	var pending []string
	requested := make(map[string]bool)
	for _, node := range pr.ReviewRequests.Nodes {
		if node != nil && node.RequestedReviewer != nil && node.RequestedReviewer.Typename == "User" &&
			ValidLogin(node.RequestedReviewer.Login) {
			pending = append(pending, node.RequestedReviewer.Login)
			requested[strings.ToLower(node.RequestedReviewer.Login)] = true
		}
	}
	skip := make(map[string]bool)
	if pr.Author != nil {
		skip[strings.ToLower(pr.Author.Login)] = true
	}
	// The latest approval or change request of a reviewer names their state,
	// even after a later comment; the latest review of any kind says whether
	// it is of the head commit. latestReviews leaves out reviewers asked
	// again, so their latest opinionated review stands in.
	// An unsubmitted review is a draft only its author sees.
	user := func(review *reviewerReview) (string, bool) {
		if review == nil || review.State == "PENDING" || review.Author == nil || review.Author.Typename != "User" ||
			!ValidLogin(review.Author.Login) {
			return "", false
		}
		return review.Author.Login, true
	}
	opinions := make(map[string]string)
	reviewed := make(map[string]time.Time)
	for _, review := range slices.Concat(pr.LatestReviews.Nodes, pr.LatestOpinionatedReviews.Nodes) {
		login, ok := user(review)
		if !ok {
			continue
		}
		key := strings.ToLower(login)
		if review.SubmittedAt != nil && review.SubmittedAt.After(reviewed[key]) {
			reviewed[key] = *review.SubmittedAt
		}
	}
	for _, review := range pr.LatestOpinionatedReviews.Nodes {
		if login, ok := user(review); ok {
			opinions[strings.ToLower(login)] = review.State
		}
	}
	var reviewers []Reviewer
	for _, review := range slices.Concat(pr.LatestReviews.Nodes, pr.LatestOpinionatedReviews.Nodes) {
		login, ok := user(review)
		if !ok || skip[strings.ToLower(login)] {
			continue
		}
		key := strings.ToLower(login)
		skip[key] = true
		state := review.State
		if opinion, ok := opinions[key]; ok {
			state = opinion
		}
		stale := review.Commit == nil || pr.HeadRefOid == "" || review.Commit.Oid != pr.HeadRefOid
		reviewers = append(reviewers, Reviewer{Login: login, State: state, Stale: stale, Pending: requested[key], ReviewedAt: reviewed[key]})
	}
	for _, login := range pending {
		if !skip[strings.ToLower(login)] {
			skip[strings.ToLower(login)] = true
			reviewers = append(reviewers, Reviewer{Login: login, Pending: true})
		}
	}
	return reviewers, nil
}

// RequestReviews requests a review of a pull request from each login again,
// which notifies them as a first request does. GitHub ignores a request of a
// review that is requested already, so the requests of pending, a subset of
// logins, are removed first and then made again with the others. When
// making them fails, the removed requests are made once more, so a failure
// leaves them as they were when it can. These are prpr's only changes to
// GitHub, and they run as the pinned account like every request.
func (c *Client) RequestReviews(ctx context.Context, repository string, number int, logins, pending []string) error {
	add, err := requestReviewsArgs("POST", repository, number, logins)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		_, err = c.output(ctx, "Could not request reviews", add...)
		return err
	}
	for _, login := range pending {
		if !slices.ContainsFunc(logins, func(l string) bool { return strings.EqualFold(l, login) }) {
			return fmt.Errorf("%s is renewed but not requested", login)
		}
	}
	remove, err := requestReviewsArgs("DELETE", repository, number, pending)
	if err != nil {
		return err
	}
	restore, _ := requestReviewsArgs("POST", repository, number, pending)
	if _, err := c.output(ctx, "Could not renew review requests", remove...); err != nil {
		return err
	}
	// What became of the removed requests leads the error, ahead of gh's
	// message, so a narrow status line still shows it.
	if _, err := c.output(ctx, "GitHub refused", add...); err != nil {
		who := strings.Join(pending, ", ")
		if _, again := c.output(ctx, "", restore...); again != nil {
			return fmt.Errorf("Could not request reviews, and the requests of %s are removed: %w", who, err)
		}
		return fmt.Errorf("Could not request reviews; the requests of %s are as they were: %w", who, err)
	}
	return nil
}

// requestReviewsArgs adds (POST) or removes (DELETE) review requests.
func requestReviewsArgs(method, repository string, number int, logins []string) ([]string, error) {
	owner, name, err := pullRequestTarget(repository, number)
	if err != nil {
		return nil, err
	}
	if len(logins) == 0 {
		return nil, errors.New("no reviewers chosen")
	}
	args := []string{"api", "--hostname", "github.com", "--method", method,
		fmt.Sprintf("repos/%s/%s/pulls/%d/requested_reviewers", owner, name, number)}
	for _, login := range logins {
		if !ValidLogin(login) {
			return nil, fmt.Errorf("%q is not a GitHub login", login)
		}
		args = append(args, "-f", "reviewers[]="+login)
	}
	return args, nil
}

func pullRequestTarget(repository string, number int) (owner, name string, err error) {
	if !ValidRepositoryName(repository) || number <= 0 {
		return "", "", fmt.Errorf("%s#%d is not a pull request", repository, number)
	}
	owner, name, _ = strings.Cut(repository, "/")
	return owner, name, nil
}

// ValidLogin reports whether login has the shape of a GitHub user login:
// letters, digits, and hyphens, not leading, at most 39 characters. Some
// older logins break GitHub's current rules on hyphens, so those are allowed.
func ValidLogin(login string) bool {
	return validLogin(login) && len(login) <= 39 && login[0] != '-'
}
