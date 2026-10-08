package github

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

// NudgeUrgency is how urgently a pull request's author asks for a review.
// Zero is no nudge.
type NudgeUrgency int

const (
	NudgeLow NudgeUrgency = iota + 1
	NudgeNormal
	NudgeUrgent
)

var urgencyNames = [...]string{NudgeLow: "low", NudgeNormal: "normal", NudgeUrgent: "urgent"}

func (u NudgeUrgency) String() string {
	if u < NudgeLow || u > NudgeUrgent {
		return ""
	}
	return urgencyNames[u]
}

// Nudge is the author's latest nudge of the viewer that the viewer has not
// answered: its urgency, when it was posted, and its note.
type Nudge struct {
	Urgency NudgeUrgency
	At      time.Time
	Note    string
}

// nudgeNoteMax caps a nudge's note, in runes.
const nudgeNoteMax = 200

// nudgeMarker is a nudge comment's last line. Receivers read only this.
var nudgeMarker = regexp.MustCompile(`^<!-- prpr:nudge v1 urgency=(low|normal|urgent) to=([A-Za-z0-9-]+(?:,[A-Za-z0-9-]+)*) -->$`)

// nudgeOpening is the text after the mentions; receivers never read it.
var nudgeOpening = [...]string{
	NudgeLow:    "👋 Friendly nudge: could you take a look when you get a chance?",
	NudgeNormal: "🔔 Could you take a look at this pull request?",
	NudgeUrgent: "🚨 This is blocking: please review it as soon as you can.",
}

// cleanNote reduces a note to one line without control characters, capped
// at nudgeNoteMax runes.
func cleanNote(note string) string {
	note = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, note)
	note = strings.Join(strings.Fields(note), " ")
	if runes := []rune(note); len(runes) > nudgeNoteMax {
		note = string(runes[:nudgeNoteMax-1]) + "…"
	}
	return note
}

// FormatNudge writes a nudge comment: the mentions and fixed text, the note
// quoted when there is one, then the marker.
func FormatNudge(urgency NudgeUrgency, logins []string, note string) (string, error) {
	if urgency.String() == "" {
		return "", errors.New("no urgency chosen")
	}
	if len(logins) == 0 {
		return "", errors.New("no one chosen")
	}
	mentions := make([]string, len(logins))
	for i, login := range logins {
		if !ValidLogin(login) {
			return "", fmt.Errorf("%q is not a GitHub login", login)
		}
		mentions[i] = "@" + login
	}
	var b strings.Builder
	b.WriteString(strings.Join(mentions, " ") + " " + nudgeOpening[urgency] + "\n\n")
	if note = cleanNote(note); note != "" {
		b.WriteString("> " + note + "\n\n")
	}
	fmt.Fprintf(&b, "<!-- prpr:nudge v1 urgency=%s to=%s -->", urgency, strings.Join(logins, ","))
	return b.String(), nil
}

// parseNudge reads a comment's marker, the last non-empty line, and its
// quoted note. ok is false for anything that is not exactly a v1 marker.
func parseNudge(body string) (urgency NudgeUrgency, to []string, note string, ok bool) {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	last := len(lines) - 1
	for last >= 0 && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	if last < 0 {
		return 0, nil, "", false
	}
	match := nudgeMarker.FindStringSubmatch(strings.TrimSpace(lines[last]))
	if match == nil {
		return 0, nil, "", false
	}
	for u := NudgeLow; u <= NudgeUrgent; u++ {
		if u.String() == match[1] {
			urgency = u
		}
	}
	to = strings.Split(match[2], ",")
	for _, login := range to {
		if !ValidLogin(login) {
			return 0, nil, "", false
		}
	}
	for _, line := range lines[:last] {
		if rest, quoted := strings.CutPrefix(strings.TrimSpace(line), "> "); quoted {
			note = cleanNote(rest)
			break
		}
	}
	return urgency, to, note, true
}

// activeNudge finds the latest nudge of viewer by author among timeline
// items, unless the viewer reviewed or commented at or after it.
func activeNudge(items []*activityNode, author, viewer string) *Nudge {
	if author == "" || viewer == "" {
		return nil
	}
	var nudge *Nudge
	var mine time.Time
	for _, item := range items {
		if item == nil || item.Author == nil {
			continue
		}
		at := item.CreatedAt
		if item.Typename == "PullRequestReview" {
			if item.SubmittedAt == nil {
				continue
			}
			at = *item.SubmittedAt
		}
		switch {
		case strings.EqualFold(item.Author.Login, viewer):
			mine = laterOf(mine, at)
		case item.Typename == "IssueComment" && strings.EqualFold(item.Author.Login, author):
			urgency, to, note, ok := parseNudge(item.Body)
			if ok && slices.ContainsFunc(to, func(l string) bool { return strings.EqualFold(l, viewer) }) &&
				(nudge == nil || !at.Before(nudge.At)) {
				nudge = &Nudge{Urgency: urgency, At: at, Note: note}
			}
		}
	}
	if nudge == nil || !mine.Before(nudge.At) {
		return nil
	}
	return nudge
}
