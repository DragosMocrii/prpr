package github

import (
	"net/url"
	"regexp"
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
)

// trunkEntry reads Trunk's comment: the first by trunk-io holding the
// marker. The unsubmitted template, a merged pull request (which is closed),
// and no comment at all give nil; text it does not recognize is Unknown.
func trunkEntry(comments []queueComment) *QueueEntry {
	for _, comment := range comments {
		if !strings.EqualFold(comment.Author, trunkLogin) || !strings.Contains(comment.Body, trunkMarker) {
			continue
		}
		_, rest, _ := strings.Cut(comment.Body, trunkMarker)
		line := firstLine(rest)
		if line == "" || strings.HasPrefix(line, "Merging to") || strings.HasPrefix(line, "😎") {
			return nil
		}
		entry := &QueueEntry{Provider: "Trunk", State: QueueUnknown}
		for _, known := range trunkStates {
			if strings.HasPrefix(line, known.prefix) && strings.Contains(line, known.phrase) {
				entry.State = known.state
				break
			}
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
