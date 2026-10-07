package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/DragosMocrii/prpr/internal/github"
)

// rerequestEditor is the form that requests reviews of an authored pull
// request again: the people who can be asked, grouped by what they can
// answer, and which of them are chosen.
type rerequestEditor struct {
	key   prKey
	title string
	// people are the offered reviewers; groups list them by index.
	people []github.Reviewer
	groups []rerequestGroup
	// chosen holds the chosen people, by lowercase login.
	chosen map[string]bool
	// teams are the pending team requests; unreadable counts those GitHub
	// would not show.
	teams      []github.TeamRequest
	unreadable int
	// approvers reports that an offered reviewer approved.
	approvers bool
	// reviewed are when the offered reviewers last reviewed, by lowercase
	// login, as the form showed them.
	reviewed map[string]time.Time
	// cursor is the row under the cursor, in rows(); offset is the first
	// row drawn.
	cursor, offset int
	// filter narrows the people shown by login; filtering is set while it
	// takes the keys.
	filter    textinput.Model
	filtering bool
	// problem says why Enter sent nothing.
	problem string
}

// reviewersListedMsg brings the reviewers of key that can be asked again.
// noticeID is the notice that announced the lookup.
type reviewersListedMsg struct {
	account   uint64
	noticeID  uint64
	key       prKey
	reviewers github.ReviewerList
	err       error
}

// reviewersRecheckedMsg brings the reviewers of key again when the form is
// sent, to send what still holds: chosen logins, and when they last reviewed
// as the form showed it.
type reviewersRecheckedMsg struct {
	account   uint64
	key       prKey
	chosen    []string
	reviewed  map[string]time.Time
	reviewers github.ReviewerList
	err       error
}

// reviewsRequestedMsg reports how requesting reviews of key again went;
// skipped are the chosen logins that were not asked.
type reviewsRequestedMsg struct {
	account uint64
	key     prKey
	logins  []string
	skipped []string
	err     error
}

// authoredPR finds an authored pull request that is listed now.
func (m *model) authoredPR(key prKey) (*github.PullRequest, bool) {
	for i := range m.snapshot.PullRequests {
		if keyOf(&m.snapshot.PullRequests[i]) == key {
			return &m.snapshot.PullRequests[i], true
		}
	}
	return nil, false
}

// modalOpen reports whether a screen other than the lists takes the keys,
// or the lists are not shown for an error or a login.
func (m *model) modalOpen() bool {
	return m.err != nil || m.loginActive || m.searching != nil || m.overlayOpen()
}

// rerequestSelected looks up who can be asked again to review the focused
// row, an authored pull request; the form opens when they are known.
func (m *model) rerequestSelected() tea.Cmd {
	pr, gone, ok := m.paneRow(m.focus, m.focused().table.Cursor())
	switch {
	case !ok:
		return nil
	case m.snapshot.Preview:
		m.setNotice("Requesting reviews waits for the details to load")
		return nil
	case gone:
		m.setNotice("Gone pull requests cannot be reviewed")
		return nil
	}
	key := keyOf(pr)
	if _, authored := m.authoredPR(key); !authored {
		m.setNotice("Reviews can be requested only on your own pull requests")
		return nil
	}
	m.setNotice(fmt.Sprintf("Finding the reviewers of #%d…", pr.Number))
	ctx, list, repository, number := m.ctx, m.listReviewers, pr.Repository, pr.Number
	account, id := m.accountGeneration, m.noticeID
	return func() tea.Msg {
		reviewers, err := list(ctx, repository, number)
		return reviewersListedMsg{account: account, noticeID: id, key: key, reviewers: reviewers, err: err}
	}
}

// handleReviewersListed opens the form, unless the account changed, another
// notice or screen replaced the lookup, or the pull request is gone.
func (m *model) handleReviewersListed(msg reviewersListedMsg) tea.Cmd {
	if msg.account != m.accountGeneration || msg.noticeID != m.noticeID || m.modalOpen() {
		return nil
	}
	pr, ok := m.authoredPR(msg.key)
	switch {
	case !ok:
		m.setNotice(fmt.Sprintf("%s#%d is no longer listed", msg.key.repository, msg.key.number))
		return nil
	case msg.err != nil:
		m.setNotice(singleLine(msg.err.Error()))
		return nil
	case len(msg.reviewers.Reviewers) == 0:
		text := fmt.Sprintf("No one to ask again on #%d: nobody reviewed it or is requested to", pr.Number)
		if msg.reviewers.UnreadableTeams > 0 {
			text += "; " + unreadableTeams(msg.reviewers.UnreadableTeams)
		}
		m.setNotice(text)
		return nil
	}
	m.notice = ""
	m.rerequest = newRerequestEditor(msg.key, "Request reviews of "+alertName(pr), msg.reviewers, m.darkBackground)
	return nil
}

// reviewWords names a review state.
func reviewWords(state string) string {
	switch state {
	case "APPROVED":
		return "approved"
	case "CHANGES_REQUESTED":
		return "requested changes"
	case "COMMENTED":
		return "commented"
	case "DISMISSED":
		return "review dismissed"
	}
	return "reviewed"
}

// updateRerequest passes a key to the form. Esc cancels; Enter requests
// the chosen reviews.
func (m *model) updateRerequest(msg tea.Msg) tea.Cmd {
	press, ok := msg.(tea.KeyPressMsg)
	if !ok {
		if m.rerequest.filtering {
			var cmd tea.Cmd
			m.rerequest.filter, cmd = m.rerequest.filter.Update(msg)
			return cmd
		}
		return nil
	}
	send, cancel, cmd := m.rerequest.update(press, m.keys.Reviewers, m.rerequestListHeight())
	switch {
	case cancel:
		m.rerequest = nil
	case send:
		e := m.rerequest
		m.rerequest = nil
		return m.recheckReviewers(e)
	}
	return cmd
}

// recheckReviewers reads the reviewers again before sending the form: the
// form may have been open while someone reviewed.
func (m *model) recheckReviewers(e *rerequestEditor) tea.Cmd {
	pr, ok := m.authoredPR(e.key)
	if !ok {
		m.setNotice(fmt.Sprintf("%s#%d is no longer listed", e.key.repository, e.key.number))
		return nil
	}
	chosen := e.chosenLogins()
	if len(chosen) == 0 {
		return nil
	}
	m.setNotice(fmt.Sprintf("Requesting reviews of #%d…", pr.Number))
	ctx, list, repository, number, account := m.ctx, m.listReviewers, pr.Repository, pr.Number, m.accountGeneration
	key, reviewed := e.key, e.reviewed
	return func() tea.Msg {
		reviewers, err := list(ctx, repository, number)
		return reviewersRecheckedMsg{account: account, key: key, chosen: chosen, reviewed: reviewed, reviewers: reviewers, err: err}
	}
}

// handleReviewersRechecked sends the chosen requests that still hold. A
// reviewer who reviewed since the form opened, whatever the verdict, is not
// asked: the review answers the request. Nor is one no longer offered. A request is
// renewed only when it is pending now.
func (m *model) handleReviewersRechecked(msg reviewersRecheckedMsg) tea.Cmd {
	if msg.account != m.accountGeneration {
		return nil
	}
	pr, ok := m.authoredPR(msg.key)
	switch {
	case !ok:
		m.setNotice(fmt.Sprintf("%s#%d is no longer listed", msg.key.repository, msg.key.number))
		return nil
	case msg.err != nil:
		m.setNotice(singleLine("Nothing sent: " + msg.err.Error()))
		return nil
	}
	now := make(map[string]github.Reviewer, len(msg.reviewers.Reviewers))
	for _, reviewer := range msg.reviewers.Reviewers {
		now[strings.ToLower(reviewer.Login)] = reviewer
	}
	var logins, renewed, skipped []string
	for _, login := range msg.chosen {
		reviewer, offered := now[strings.ToLower(login)]
		if !offered || reviewer.ReviewedAt.After(msg.reviewed[strings.ToLower(login)]) {
			skipped = append(skipped, login)
			continue
		}
		logins = append(logins, login)
		if reviewer.Pending {
			renewed = append(renewed, login)
		}
	}
	if len(logins) == 0 {
		m.setNotice(singleLine(fmt.Sprintf("Nothing sent on #%d: %s reviewed it since", pr.Number, strings.Join(skipped, ", "))))
		return nil
	}
	ctx, request, repository, number, account := m.ctx, m.requestReviews, pr.Repository, pr.Number, m.accountGeneration
	key := msg.key
	return func() tea.Msg {
		return reviewsRequestedMsg{account: account, key: key, logins: logins, skipped: skipped,
			err: request(ctx, repository, number, logins, renewed)}
	}
}

// handleReviewsRequested reports the result for the account that sent it.
func (m *model) handleReviewsRequested(msg reviewsRequestedMsg) {
	if msg.account != m.accountGeneration {
		return
	}
	if msg.err != nil {
		m.setNotice(singleLine(msg.err.Error()))
		return
	}
	// The details read the reviewers again.
	delete(m.reviewerLookups, msg.key)
	text := fmt.Sprintf("Requested reviews of #%d from %s", msg.key.number, strings.Join(msg.logins, ", "))
	if len(msg.skipped) > 0 {
		text += "; not " + strings.Join(msg.skipped, ", ") + ", who reviewed it since"
	}
	m.setNotice(singleLine(text))
}
