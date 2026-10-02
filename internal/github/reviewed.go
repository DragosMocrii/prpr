package github

import (
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
	return searchQuery(reviewedSearch(now), bots, `
        headRefOid
        activity: timelineItems(itemTypes: [PULL_REQUEST_REVIEW, ISSUE_COMMENT], last: `+strconv.Itoa(activityItems)+`) {
          nodes {
            __typename
            ... on PullRequestReview { author { login } state submittedAt commit { oid } }
            ... on IssueComment { author { login } createdAt }
          }
        }`)
}

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
	Commit      *struct {
		Oid string `json:"oid"`
	} `json:"commit"`
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
			replied = laterOf(replied, at)
		}
	}
	switch {
	case !reviewed:
		return ReviewNewActivity, node.UpdatedAt, true
	case last == "DISMISSED":
		return ReviewDismissed, mine, true
	case node.Draft:
		return ReviewBackInDraft, mine, true
	case !seenHead:
		return ReviewNewCommits, laterOf(mine, head), true
	case replied.After(mine):
		return ReviewAuthorReplied, replied, true
	case opinion == "APPROVED":
		return ReviewApproved, mine, true
	default:
		return ReviewWaitingOnAuthor, mine, true
	}
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// mergeReviews lists pending review requests first, then reviewed pull
// requests that need the viewer, then those waiting on others, each in fetch
// order. A pull request in both lists is a pending request.
func mergeReviews(requests, reviewed []PullRequest) []PullRequest {
	type key struct {
		repository string
		number     int
	}
	pending := make(map[key]bool, len(requests))
	for _, pr := range requests {
		pending[key{strings.ToLower(pr.Repository), pr.Number}] = true
	}
	merged := append([]PullRequest(nil), requests...)
	var waiting []PullRequest
	for _, pr := range reviewed {
		switch {
		case pending[key{strings.ToLower(pr.Repository), pr.Number}]:
		case pr.ReviewStatus.Waiting():
			waiting = append(waiting, pr)
		default:
			merged = append(merged, pr)
		}
	}
	return append(merged, waiting...)
}
