package github

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// QueueState is where a merge queue holds a pull request. The removed
// states mean the queue let it go, so it is the author's again.
type QueueState int

const (
	QueueSubmitted QueueState = iota + 1
	QueueQueued
	QueueTesting
	QueueFailing
	QueuePassed
	QueueUnknown
	QueueRemovedFailed
	QueueRemovedCanceled
)

// InQueue reports whether the queue still holds the pull request.
func (s QueueState) InQueue() bool {
	return s >= QueueSubmitted && s <= QueueUnknown
}

// QueueEntry is a merge queue's word on an authored pull request, whichever
// queue it came from.
type QueueEntry struct {
	// Provider names the queue: "Trunk" or "GitHub".
	Provider string
	State    QueueState
	// Detail is a short single line, such as "testing on #77"; it may be empty.
	Detail string
	// URL is the queue's page for the pull request, kept only when safe.
	URL string
}

// maxQueueDetail caps Detail, which comes from text others write.
const maxQueueDetail = 40

// queueComment is a conversation comment as the Trunk provider reads it.
type queueComment struct {
	Author string
	Body   string
}

// trunkLogin is Trunk's GitHub App; trunkMarker starts its one comment,
// which it edits on every state change.
const (
	trunkLogin  = "trunk-io"
	trunkMarker = "<!-- Trunk Merge -->"
)

// trunkStates match the first line of Trunk's comment after the marker by
// its leading emoji and a phrase, for both "pull request" and "stack".
var trunkStates = []struct {
	prefix, phrase string
	state          QueueState
}{
	{"✨", "ubmitted to Merge", QueueSubmitted},
	{"⏳", "aiting to start tests", QueueQueued},
	{"🧪", "Running tests", QueueTesting},
	{"⚠", "has failed", QueueFailing}, // with or without U+FE0F
	{"👍", "will be merged soon", QueuePassed},
	{"❌", "removed from the merge queue because it failed tests", QueueRemovedFailed},
	{"🚫", "removed from the merge queue because it was canceled", QueueRemovedCanceled},
}

var (
	trunkTestPR    = regexp.MustCompile(`(?:testing on PR|tested on PR|PR) \[#(\d+)\]`)
	trunkFailCheck = regexp.MustCompile("required check \\[`([^`]+)`\\]")
	trunkLink      = regexp.MustCompile(`\]\((https://app\.trunk\.io/[^)\s]*)\)`)
	// trunkUnchecked is the template's unchecked submit box, which stays
	// however the template's first line is worded. It decides only text no
	// state matches, so a state that keeps the box still reads as that state.
	trunkUnchecked = regexp.MustCompile(`-\s*\[ \]\s*<!-- End PR Submit Checkbox -->`)
)

// trunkEntry reads Trunk's comment: the first by trunk-io holding the
// marker, or without it, which Trunk drops when it edits the template into
// a state, whose first line matches a state. The unsubmitted template (its
// first line, or an unchecked submit box under a line no state matches), a
// merged pull request (which is closed), and no comment at all give nil;
// marked text it does not recognize is Unknown, and unmarked text is
// another comment.
func trunkEntry(comments []queueComment) *QueueEntry {
	for _, comment := range comments {
		if !strings.EqualFold(comment.Author, trunkLogin) {
			continue
		}
		before, rest, marked := strings.Cut(comment.Body, trunkMarker)
		if !marked {
			rest = before
		}
		line := firstLine(rest)
		if marked && (line == "" || strings.HasPrefix(line, "Merging to") || strings.HasPrefix(line, "😎")) {
			return nil
		}
		entry := &QueueEntry{Provider: "Trunk", State: QueueUnknown}
		for _, known := range trunkStates {
			if strings.HasPrefix(line, known.prefix) && strings.Contains(line, known.phrase) {
				entry.State = known.state
				break
			}
		}
		if entry.State == QueueUnknown && !marked {
			continue
		}
		if entry.State == QueueUnknown && trunkUnchecked.MatchString(rest) {
			return nil
		}
		entry.Detail = trunkDetail(entry.State, line)
		if link := trunkLink.FindStringSubmatch(line); link != nil && safeTrunkURL(link[1]) {
			entry.URL = link[1]
		}
		return entry
	}
	return nil
}

// firstLine is the first non-blank line of text, trimmed.
func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// trunkDetail is the short, capped detail Trunk's line gives for a state.
func trunkDetail(state QueueState, line string) string {
	detail := ""
	switch state {
	case QueueQueued:
		if strings.Contains(line, "ahead of it failed") {
			detail = "a PR ahead failed"
		}
	case QueueTesting:
		if match := trunkTestPR.FindStringSubmatch(line); match != nil {
			detail = "testing on #" + match[1]
		}
	case QueuePassed, QueueRemovedFailed:
		if match := trunkTestPR.FindStringSubmatch(line); match != nil {
			detail = "tested on #" + match[1]
		}
	case QueueFailing:
		if match := trunkFailCheck.FindStringSubmatch(line); match != nil {
			detail = match[1] + " failed"
		}
	}
	return capDetail(detail)
}

// capDetail keeps printable characters on one line, at most maxQueueDetail.
func capDetail(text string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)
	if runes := []rune(text); len(runes) > maxQueueDetail {
		text = string(runes[:maxQueueDetail-1]) + "…"
	}
	return text
}

// containsControl reports whether text holds a control character.
func containsControl(text string) bool {
	return strings.ContainsFunc(text, unicode.IsControl)
}

// maxQueueURL caps URL, which comes from text others write.
const maxQueueURL = 200

// safeTrunkURL accepts only https links to app.trunk.io without user info,
// at most maxQueueURL bytes.
func safeTrunkURL(raw string) bool {
	if raw == "" || len(raw) > maxQueueURL || containsControl(raw) {
		return false
	}
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host == "app.trunk.io" && parsed.User == nil
}

// githubQueueEntry maps GitHub's MergeQueueEntryState; an empty state means
// the pull request is not in GitHub's queue.
func githubQueueEntry(state string, position int) *QueueEntry {
	if state == "" {
		return nil
	}
	entry := &QueueEntry{Provider: "GitHub", State: QueueUnknown}
	switch state {
	case "QUEUED", "LOCKED":
		entry.State = QueueQueued
	case "AWAITING_CHECKS":
		entry.State = QueueTesting
	case "MERGEABLE":
		entry.State = QueuePassed
	case "UNMERGEABLE":
		entry.State = QueueFailing
	}
	if position > 0 {
		entry.Detail = "position " + strconv.Itoa(position)
	}
	return entry
}

// Queue names a merge queue prpr can read.
type Queue string

const (
	QueueTrunk  Queue = "trunk"
	QueueGitHub Queue = "github"
)

// DefaultQueues reads both queues.
const DefaultQueues = "trunk,github"

// ParseQueues reads comma-separated queue names; empty reads none.
func ParseQueues(value string) ([]Queue, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var queues []Queue
	for _, entry := range strings.Split(value, ",") {
		queue := Queue(strings.ToLower(strings.TrimSpace(entry)))
		if queue != QueueTrunk && queue != QueueGitHub {
			return nil, fmt.Errorf("queue %q: want trunk or github", strings.TrimSpace(entry))
		}
		if slices.Contains(queues, queue) {
			return nil, fmt.Errorf("queue %q is listed twice", queue)
		}
		queues = append(queues, queue)
	}
	return queues, nil
}

// trunkComments is how many conversation comments are read for Trunk's,
// which is almost always the first.
const trunkComments = 10

// queueFields are the authored query's fields for queues.
func queueFields(queues []Queue) string {
	var fields string
	if slices.Contains(queues, QueueGitHub) {
		fields += `
        mergeQueueEntry { state position }`
	}
	if slices.Contains(queues, QueueTrunk) {
		fields += `
        queueComments: comments(first: ` + strconv.Itoa(trunkComments) + `) { nodes { author { login } body } }`
	}
	return fields
}

// queueNodes decode queueFields; a field not selected stays nil.
type queueNodes struct {
	MergeQueueEntry *struct {
		State    string `json:"state"`
		Position int    `json:"position"`
	} `json:"mergeQueueEntry"`
	QueueComments *struct {
		Nodes []*struct {
			Author *struct {
				Login string `json:"login"`
			} `json:"author"`
			Body string `json:"body"`
		} `json:"nodes"`
	} `json:"queueComments"`
}

// queueEntry asks GitHub's queue first, so a stale Trunk comment never
// overrides it.
func (n *queueNodes) queueEntry() *QueueEntry {
	if entry := n.MergeQueueEntry; entry != nil {
		if found := githubQueueEntry(entry.State, entry.Position); found != nil {
			return found
		}
	}
	if n.QueueComments == nil {
		return nil
	}
	var comments []queueComment
	for _, node := range n.QueueComments.Nodes {
		if node == nil || node.Author == nil {
			continue
		}
		comments = append(comments, queueComment{Author: node.Author.Login, Body: node.Body})
	}
	return trunkEntry(comments)
}

// SetQueues chooses the merge queues later fetches read.
func (c *Client) SetQueues(queues []Queue) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queues = queues
	c.dropCache()
}

func (c *Client) currentQueues() []Queue {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.queues
}
