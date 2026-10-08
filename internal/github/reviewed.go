package github

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

// ReviewStatus is where a review-pane pull request stands with the viewer.
// Authored pull requests and pending review requests have the zero value.
type ReviewStatus int

const (
	// ReviewRequested means a direct review request is pending.
	ReviewRequested ReviewStatus = iota
	// The viewer reviewed, and the pull request needs them again:
	ReviewNewCommits
	ReviewAuthorReplied
	ReviewDismissed
	// ReviewNewActivity means none of the viewer's reviews is among the
	// timeline items fetched, so more happened since than prpr reads.
	ReviewNewActivity
	// ReviewNudged means the author nudged the viewer, who was never
	// requested directly and has not reviewed: found by the mentions search.
	ReviewNudged
	// The viewer reviewed, and the pull request waits on someone else:
	ReviewWaitingOnAuthor
	ReviewApproved
	ReviewBackInDraft
)

// Waiting reports whether the pull request waits on someone other than the
// viewer.
func (s ReviewStatus) Waiting() bool { return s >= ReviewWaitingOnAuthor }

// reviewedWindow bounds the reviewed search to pull requests updated
// recently, so abandoned ones fall off.
const reviewedWindow = 30 * 24 * time.Hour

// reviewedSearch finds open pull requests that the viewer reviewed and did not
// author, updated within reviewedWindow of now.
func reviewedSearch(now time.Time) string {
	return "is:pr is:open reviewed-by:@me -author:@me archived:false updated:>=" +
		now.Add(-reviewedWindow).UTC().Format("2006-01-02") + " sort:updated-desc"
}

// activityItems is how many recent reviews and conversation comments a
// reviewed pull request reads to place the viewer's latest activity.
const activityItems = 50

// reviewedQuery lists the reviewedSearch results with the fields that place
// each one: the head commit, recent reviews and comments, and the direct
// review requests.
func reviewedQuery(bots bool, now time.Time) string {
	return searchQuery(reviewedSearch(now), bots, activityField)
}

// activityField reads a review row's recent reviews and conversation
// comments: they place a reviewed pull request and hold nudges.
var activityField = `
        activity: timelineItems(itemTypes: [PULL_REQUEST_REVIEW, ISSUE_COMMENT], last: ` + strconv.Itoa(activityItems) + `) {
          nodes {
            __typename
            ... on PullRequestReview { author { login } state submittedAt commit { oid } }
            ... on IssueComment { author { login } createdAt body }
          }
        }`

// activityNode is a review or a conversation comment.
type activityNode struct {
	Typename string `json:"__typename"`
	Author   *struct {
		Login string `json:"login"`
	} `json:"author"`
	State string `json:"state"`
	// SubmittedAt is null for a pending review.
	SubmittedAt *time.Time `json:"submittedAt"`
	CreatedAt   time.Time  `json:"createdAt"`
	// Body is a conversation comment's text; nudges are read from it.
	Body   string `json:"body"`
	Commit *struct {
		Oid string `json:"oid"`
	} `json:"commit"`
}

// nudge is the author's active nudge of viewer, if any.
func (node *pullRequestNode) nudge(viewer string) *Nudge {
	if node.Author == nil {
		return nil
	}
	return activeNudge(node.Activity.Nodes, node.Author.Login, viewer)
}

// reviewedStatus places a pull request the viewer reviewed, and returns the
// time its status started. It reports false for one where the viewer was
// never directly requested, such as a drive-by or team-only review.
func (node *pullRequestNode) reviewedStatus(login string, head time.Time) (ReviewStatus, time.Time, bool) {
	requested := false
	for _, event := range node.RequestEvents.Nodes {
		if event != nil && event.RequestedReviewer != nil && strings.EqualFold(event.RequestedReviewer.Login, login) {
			requested = true
		}
	}
	if !requested {
		return 0, time.Time{}, false
	}
	status, since := node.placeReviewed(login, head)
	return status, since, true
}

// placeReviewed places a pull request by the viewer's latest activity.
func (node *pullRequestNode) placeReviewed(login string, head time.Time) (ReviewStatus, time.Time) {
	author := ""
	if node.Author != nil {
		author = node.Author.Login
	}
	// Timeline items are chronological, so later ones replace earlier ones.
	var mine, replied time.Time
	var reviewed, seenHead bool
	last, opinion := "", ""
	for _, item := range node.Activity.Nodes {
		if item == nil || item.Author == nil {
			continue
		}
		at := item.CreatedAt
		review := item.Typename == "PullRequestReview"
		if review {
			if item.SubmittedAt == nil {
				continue
			}
			at = *item.SubmittedAt
		}
		switch {
		case strings.EqualFold(item.Author.Login, login):
			mine = laterOf(mine, at)
			if !review {
				continue
			}
			reviewed, last = true, item.State
			if item.State != "COMMENTED" {
				opinion = item.State
			}
			if item.Commit != nil && item.Commit.Oid == node.HeadRefOid {
				seenHead = true
			}
		case author != "" && strings.EqualFold(item.Author.Login, author):
			// A nudge is covered by Nudge, never an author reply.
			if item.Typename == "IssueComment" {
				if _, _, _, nudge := parseNudge(item.Body); nudge {
					continue
				}
			}
			replied = laterOf(replied, at)
		}
	}
	switch {
	case !reviewed:
		return ReviewNewActivity, node.UpdatedAt
	case last == "DISMISSED":
		return ReviewDismissed, mine
	case node.Draft:
		return ReviewBackInDraft, mine
	case !seenHead:
		return ReviewNewCommits, laterOf(mine, head)
	case replied.After(mine):
		return ReviewAuthorReplied, replied
	case opinion == "APPROVED":
		return ReviewApproved, mine
	default:
		return ReviewWaitingOnAuthor, mine
	}
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// mergeReviews lists pending review requests first, then reviewed and
// mentioned pull requests that need the viewer, then those waiting on
// others, each in fetch order. A pull request in more than one list is
// listed once: a pending request first, then a reviewed one.
func mergeReviews(requests, reviewed, mentioned []PullRequest) []PullRequest {
	type key struct {
		repository string
		number     int
	}
	keyOf := func(pr PullRequest) key { return key{strings.ToLower(pr.Repository), pr.Number} }
	seen := make(map[key]bool, len(requests))
	merged := append([]PullRequest(nil), requests...)
	for _, pr := range requests {
		seen[keyOf(pr)] = true
	}
	var waiting []PullRequest
	for _, pr := range slices.Concat(reviewed, mentioned) {
		switch k := keyOf(pr); {
		case seen[k]:
		case pr.ReviewStatus.Waiting():
			seen[k] = true
			waiting = append(waiting, pr)
		default:
			seen[k] = true
			merged = append(merged, pr)
		}
	}
	return append(merged, waiting...)
}

// listed is a pull request of a list and whether it stays in it: the
// reviewed search keeps only those that requested the viewer directly.
type listed struct {
	pr   PullRequest
	keep bool
}

// reviewedPullRequest converts a reviewed search node and places it.
func reviewedPullRequest(node *pullRequestNode, login string, bots []Bot) listed {
	pr := node.pullRequest("", bots)
	var head time.Time
	if commits := node.Commits.Nodes; len(commits) > 0 && commits[0] != nil {
		head = commits[0].Commit.CommittedDate
	}
	status, since, ok := node.reviewedStatus(login, head)
	pr.ReviewStatus, pr.WaitingSince = status, since
	pr.Nudge = node.nudge(login)
	return listed{pr: pr, keep: ok}
}

// decodeReviewedNodes decodes the reviewed search, kept or not.
func decodeReviewedNodes(data []byte, login string, bots []Bot) ([]listed, error) {
	var all []listed
	_, err := decodeSearchPages(data, "reviewed pull request", func(node *pullRequestNode) (PullRequest, bool) {
		all = append(all, reviewedPullRequest(node, login, bots))
		return PullRequest{}, false
	})
	return all, err
}

// decodeReviewedPages decodes the reviewed search, keeping the pull requests
// that directly requested the viewer's review.
func decodeReviewedPages(data []byte, login string, bots []Bot) ([]PullRequest, error) {
	all, err := decodeReviewedNodes(data, login, bots)
	if err != nil {
		return nil, err
	}
	var kept []PullRequest
	for _, l := range all {
		if l.keep {
			kept = append(kept, l.pr)
		}
	}
	return kept, nil
}

// mentionsLimit is how many pull requests the mentions search reads: one
// page, never more. It is the only search that drops most of its rows, so
// its cost is bounded rather than complete.
const mentionsLimit = pageSize

// mentionsSearch finds open pull requests of others that mention the viewer,
// updated within reviewedWindow: where the author may have nudged them
// without requesting their review directly.
func mentionsSearch(now time.Time) string {
	return "is:pr is:open mentions:@me -author:@me archived:false updated:>=" +
		now.Add(-reviewedWindow).UTC().Format("2006-01-02") + " sort:updated-desc"
}

// singlePageSearchQuery lists the first results of a pull request search,
// with no cursor, so gh reads one page.
func singlePageSearchQuery(search string, first int, fields string) string {
	return `query {
  search(type: ISSUE, first: ` + strconv.Itoa(first) + `, query: "` + search + `") {
    nodes {
      ... on PullRequest {` + fields + `
      }
    }
  }
}`
}

func mentionsQuery(bots bool, now time.Time) string {
	return singlePageSearchQuery(mentionsSearch(now), mentionsLimit, reviewFields(bots, activityField))
}

// mentionedPullRequest converts a mentions search node. It stays only when
// the author's nudge of the viewer is active; it is placed by the viewer's
// reviews when they reviewed, else as nudged.
func mentionedPullRequest(node *pullRequestNode, login string, bots []Bot) listed {
	pr := node.pullRequest("", bots)
	pr.Nudge = node.nudge(login)
	if pr.Nudge == nil {
		return listed{pr: pr}
	}
	var head time.Time
	if commits := node.Commits.Nodes; len(commits) > 0 && commits[0] != nil {
		head = commits[0].Commit.CommittedDate
	}
	status, since := node.placeReviewed(login, head)
	if status == ReviewNewActivity {
		status, since = ReviewNudged, pr.Nudge.At
	}
	pr.ReviewStatus, pr.WaitingSince = status, since
	return listed{pr: pr, keep: true}
}

// decodeMentionedNodes decodes the mentions search's single page, kept or not.
func decodeMentionedNodes(data []byte, login string, bots []Bot) ([]listed, error) {
	var all []listed
	_, err := decodeSearchPages(onePage(data), "mentioned pull request", func(node *pullRequestNode) (PullRequest, bool) {
		all = append(all, mentionedPullRequest(node, login, bots))
		return PullRequest{}, false
	})
	return all, err
}

// onePage wraps a response gh read without --slurp as a list of one page.
func onePage(data []byte) []byte {
	return append(append([]byte{'['}, data...), ']')
}
