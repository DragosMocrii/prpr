package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/DragosMocrii/prpr/internal/github"
)

// detailsLabelWidth fits the longest label, "Comments", and a space.
const detailsLabelWidth = 9

const loadingDetails = "Loading details…"

// detailsShown reports whether the details screen is drawn: it was opened and
// the focused pane still has a selected row to describe.
func (m *model) detailsShown() bool {
	return m.width >= minimumWidth && m.height >= minimumHeight && m.detailsOpen()
}

func (m *model) detailsOpen() bool {
	if !m.details || (m.loading && !m.loginActive && !m.refreshing()) ||
		m.err != nil || m.picker != nil || m.accounts != nil || m.rulesEditor != nil || m.snoozeEditor != nil || !m.scopeChosen {
		return false
	}
	_, ok := m.selectedPR()
	return ok
}

// closeStaleDetails forgets an open details screen once it has nothing to
// describe, so it does not reappear when rows come back. A terminal too
// small to draw it keeps it open.
func (m *model) closeStaleDetails() {
	if m.details && !m.detailsOpen() {
		m.details = false
	}
}

// Wide, tall terminals show the details as a box over the list; smaller ones
// give them the whole screen.
const (
	modalMinWidth  = 100
	modalMinHeight = 24
	modalMaxWidth  = 96
)

func (m *model) detailsModal() bool {
	return m.width >= modalMinWidth && m.height >= modalMinHeight && m.panesFit()
}

// detailsView draws the details as a box over the list, or as a screen.
func (m *model) detailsView() []string {
	if m.detailsModal() {
		return m.detailsModalLines()
	}
	return m.detailsLines()
}

// detailsBody describes the focused pane's selected pull request in words,
// wrapped to width.
func (m *model) detailsBody(width int) (*github.PullRequest, []string) {
	pane := m.focused()
	pr, gone, _ := m.paneRow(m.focus, pane.table.Cursor())
	var body []string
	if gone {
		faint := lipgloss.NewStyle().Faint(true)
		for _, line := range wrapWords("This pull request left the list: it was merged or closed, the review request was withdrawn, or a pull request you reviewed went 30 days without an update. These details are from the last refresh that listed it.", width) {
			body = append(body, faint.Render(line))
		}
		body = append(body, "")
	}
	bold := lipgloss.NewStyle().Bold(true)
	for _, line := range wrapWords(singleLine(pr.Title), width) {
		body = append(body, bold.Render(line))
	}
	body = append(body, "")
	for _, row := range m.detailRows(pr, gone) {
		body = append(body, detailRow(row.label, row.values, width)...)
	}
	return pr, body
}

// detailsLines gives the details the whole screen. The body is cut to fit;
// the URL, status line, and help stay at the bottom.
func (m *model) detailsLines() []string {
	pr, body := m.detailsBody(m.width)
	title := fmt.Sprintf("prpr — %s — #%d %s", m.accountLabel(), pr.Number, singleLine(pr.Repository))
	lines := []string{m.titleLine(title, m.countdownText()), ""}
	fixed := m.notice
	if m.preferenceErr != nil {
		fixed = m.preferenceErr.Error()
	}
	footer := append([]string{singleLine(pr.URL), m.statusLine(fixed, "")}, m.helpLines(keyMap.detailsHelp)...)
	room := max(m.height-len(lines)-len(footer), 0)
	if len(body) > room {
		body = body[:room]
	}
	for len(body) < room {
		body = append(body, "")
	}
	return append(append(lines, body...), footer...)
}

// detailsModalLines draws the details in a bordered box centered over the
// list, whose help line shows the details keys. The box keeps clear of the
// title line and the bottom three lines: URL, status, and help.
func (m *model) detailsModalLines() []string {
	base := m.listLinesWith(keyMap.detailsHelp)
	width := min(m.width-8, modalMaxWidth)
	// The border and one column of padding on each side.
	inner := width - 4
	pr, body := m.detailsBody(inner)
	header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).
		Render(fmt.Sprintf("#%d %s", pr.Number, singleLine(pr.Repository)))
	content := append([]string{header, ""}, body...)
	footer := []string{"", singleLine(pr.URL)}
	room := max(m.height-1-3-2-len(footer), 0)
	if len(content) > room {
		content = content[:room]
	}
	content = append(content, footer...)
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("6")).
		Padding(0, 1).Width(width).Render(strings.Join(content, "\n"))
	x := (m.width - lipgloss.Width(box)) / 2
	y := max(1, (m.height-3-lipgloss.Height(box))/2)
	canvas := lipgloss.NewCompositor(
		lipgloss.NewLayer(strings.Join(base, "\n")),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	)
	return strings.Split(canvas.Render(), "\n")
}

type detailRowText struct {
	label  string
	values []string
}

func (m *model) detailRows(pr *github.PullRequest, gone bool) []detailRowText {
	// A preview row knows its conflicts, title, size, and dates only.
	preview := m.snapshot.Preview && !gone
	review := m.focus == paneReview || (m.focus == paneSnoozed && m.snoozeList(pr) == paneReview)
	now := m.now()
	pending := func(text string) string {
		if preview {
			return loadingDetails
		}
		return text
	}

	var rows []detailRowText
	add := func(label string, values ...string) {
		rows = append(rows, detailRowText{label, values})
	}
	if !gone {
		if changed := m.changedDetail(pr); len(changed) > 0 {
			add("Changed", changed...)
		}
	}
	if review {
		add("Author", singleLine(pr.Author))
	}
	merge := mergeDetail(pr.Draft, pr.Mergeable, pr.MergeState, pr.ReviewDecision, pr.Checks)
	if preview && pr.Mergeable != "CONFLICTING" {
		merge = loadingDetails
	}
	add("Merge", merge)
	if !review && !preview {
		if lines := m.ruleDetails(pr); len(lines) > 0 {
			// The Merge row is GitHub's answer, which the rules may overrule.
			rows[len(rows)-1].values[0] = strings.Replace(merge, "Ready to merge", "GitHub allows merging", 1)
			add("Ready", lines...)
		}
	}
	if pr.Queue != nil && !review {
		values := []string{singleLine(pr.Queue.Provider) + ": " + queueDetail(pr.Queue)}
		if pr.Queue.URL != "" {
			values = append(values, singleLine(pr.Queue.URL))
		}
		add("Queue", values...)
	}
	add("CI", pending(checksDetail(pr.Checks)))
	add("Review", pending(reviewDetail(pr.ReviewDecision, pr.Approvals)))
	if m.bots {
		if preview || len(pr.Bots) == 0 {
			add("Bots", pending("–"))
		} else {
			bots := make([]string, len(pr.Bots))
			for i, bot := range pr.Bots {
				bots[i] = singleLine(bot.Name) + ": " + botDetail(bot)
			}
			add("Bots", bots...)
		}
	}
	add("Waiting", pending(waitingDetail(pr, review, now)))
	add("Opened", dateDetail(pr.CreatedAt, now))
	add("Updated", dateDetail(pr.UpdatedAt, now))
	add("Size", fmt.Sprintf("+%d −%d lines", pr.Additions, pr.Deletions))
	add("Comments", pending(plural(pr.Comments, "conversation comment")))
	return rows
}

// queueDetail words a queue entry for the details screen.
func queueDetail(entry *github.QueueEntry) string {
	text := map[github.QueueState]string{
		github.QueueSubmitted: "submitted, waiting for branch rules", github.QueueQueued: "waiting to start tests",
		github.QueueTesting: "testing", github.QueueFailing: "a required check failed; waiting for other pull requests",
		github.QueuePassed: "passed; merging soon", github.QueueRemovedFailed: "removed from the queue: tests failed",
		github.QueueRemovedCanceled: "removed from the queue: canceled",
	}[entry.State]
	if text == "" {
		text = "in the queue; state not recognized"
	}
	if entry.Detail != "" {
		text += " (" + singleLine(entry.Detail) + ")"
	}
	return text
}

// detailRow draws a label beside its values, each value wrapped under the
// value column.
func detailRow(label string, values []string, width int) []string {
	width -= detailsLabelWidth
	labelText := lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Render(label) +
		strings.Repeat(" ", detailsLabelWidth-lipgloss.Width(label))
	indent := strings.Repeat(" ", detailsLabelWidth)
	var lines []string
	for _, value := range values {
		for _, line := range wrapWords(value, width) {
			if len(lines) == 0 {
				lines = append(lines, labelText+line)
			} else {
				lines = append(lines, indent+line)
			}
		}
	}
	if len(lines) == 0 {
		lines = append(lines, labelText)
	}
	return lines
}

// mergeDetail explains GitHub's merge state. It names a blocker only when the
// data proves it: a review decision is set only when reviews are required,
// but nothing here says whether a check is required.
func mergeDetail(draft bool, mergeable, state, decision, checks string) string {
	if mergeable == "CONFLICTING" || state == "DIRTY" {
		return "Has conflicts with the base branch."
	}
	if draft {
		return "Draft: GitHub won't merge it until it is marked ready for review."
	}
	switch state {
	case "CLEAN":
		return "Ready to merge."
	case "HAS_HOOKS":
		return "Ready to merge; merge hooks will run."
	case "UNSTABLE":
		return "Ready to merge; checks that are not required are failing or pending."
	case "BEHIND":
		return "Behind the base branch; GitHub requires it to be updated before merging."
	case "BLOCKED":
		var text string
		switch decision {
		case "REVIEW_REQUIRED":
			text = "Blocked: an approving review is required."
		case "CHANGES_REQUESTED":
			text = "Blocked: changes were requested."
		default:
			text = "Blocked by a branch rule GitHub does not name, such as a code owner review, unresolved conversations, or required checks."
		}
		switch checks {
		case "FAILURE", "ERROR":
			text += " Checks are failing; GitHub does not say whether they are required."
		case "PENDING", "EXPECTED":
			text += " Checks are still running; GitHub does not say whether they are required."
		}
		return text
	case "", "UNKNOWN":
		return "GitHub has not computed the merge status yet."
	default:
		return "GitHub reports the merge state " + singleLine(state) + "."
	}
}

func checksDetail(state string) string {
	switch state {
	case "SUCCESS":
		return "All checks passed."
	case "FAILURE", "ERROR":
		return "Some checks failed."
	case "PENDING", "EXPECTED":
		return "Checks are pending or running."
	default:
		return "No checks reported."
	}
}

func reviewDetail(decision string, approvals int) string {
	var text string
	switch decision {
	case "APPROVED":
		text = "Approved"
	case "CHANGES_REQUESTED":
		text = "Changes requested"
	case "REVIEW_REQUIRED":
		text = "Review required"
	default:
		text = "No review decision from GitHub"
	}
	return text + " · " + plural(approvals, "approval")
}

func botDetail(review github.BotReview) string {
	switch review.State {
	case github.BotConcerns:
		return plural(review.Concerns, "unresolved thread")
	case github.BotFailed:
		return "check run failed on the latest commit"
	case github.BotRunning:
		return "check run in progress on the latest commit"
	case github.BotStale:
		return "no open threads, but last acted before the latest commit"
	case github.BotPassed:
		return "no open threads; acted since the latest commit"
	default:
		return "has not acted"
	}
}

func waitingDetail(pr *github.PullRequest, review bool, now time.Time) string {
	if review && pr.ReviewStatus != github.ReviewRequested {
		return reviewedDetail(pr, now)
	}
	switch {
	case pr.WaitingSince.IsZero() && pr.Draft:
		return "Draft, not waiting for review."
	case pr.WaitingSince.IsZero():
		return "–"
	case review:
		return "Review requested " + ageText(pr.WaitingSince, now) + " ago, " + dateText(pr.WaitingSince) + "."
	default:
		return ageText(pr.WaitingSince, now) + ", ready for review since " + dateText(pr.WaitingSince) + "."
	}
}

// reviewedDetail says where a pull request the viewer reviewed stands, and
// since when.
func reviewedDetail(pr *github.PullRequest, now time.Time) string {
	since := ageText(pr.WaitingSince, now) + " ago, " + dateText(pr.WaitingSince)
	switch pr.ReviewStatus {
	case github.ReviewNewCommits:
		return "New commits since your review, " + since + "."
	case github.ReviewAuthorReplied:
		return "The author replied after your review, " + since + "."
	case github.ReviewDismissed:
		return "Your review was dismissed; you last reviewed " + since + "."
	case github.ReviewNewActivity:
		return "More activity since your review than prpr reads; updated " + since + "."
	case github.ReviewApproved:
		return "You approved " + since + "; waiting on the author to merge."
	case github.ReviewBackInDraft:
		return "Back in draft since your review, " + since + "."
	default:
		return "You reviewed " + since + "; waiting on the author."
	}
}

func dateDetail(t, now time.Time) string {
	if t.IsZero() {
		return "–"
	}
	return dateText(t) + " (" + ageText(t, now) + " ago)"
}

func dateText(t time.Time) string {
	return t.Local().Format("2 Jan 2006 15:04")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
