package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/DragosMocrii/prpr/internal/github"
)

// rerequestEditor is the form that requests reviews of an authored pull
// request again. Its field writes to chosen.
type rerequestEditor struct {
	form   *huh.Form
	key    prKey
	title  string
	chosen []string
	// reviewed are when the offered reviewers last reviewed, by lowercase
	// login, as the form showed them.
	reviewed map[string]time.Time
}

// reviewersListedMsg brings the reviewers of key that can be asked again.
// noticeID is the notice that announced the lookup.
type reviewersListedMsg struct {
	account   uint64
	noticeID  uint64
	key       prKey
	reviewers []github.Reviewer
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
	reviewers []github.Reviewer
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
	case len(msg.reviewers) == 0:
		m.setNotice(fmt.Sprintf("No one to ask again on #%d: nobody reviewed it or is requested to", pr.Number))
		return nil
	}
	m.notice = ""
	e := &rerequestEditor{key: msg.key, title: "Request reviews of " + alertName(pr) + " again",
		reviewed: make(map[string]time.Time)}
	options := make([]huh.Option[string], len(msg.reviewers))
	approvers := false
	for i, reviewer := range msg.reviewers {
		label := reviewer.Login + " — "
		switch {
		case reviewer.State == "":
			label += "requested, no review yet"
		case reviewer.Pending:
			label += reviewWords(reviewer.State) + ", requested again since"
		default:
			label += reviewWords(reviewer.State)
		}
		if reviewer.Stale && reviewer.State != "" {
			label += ", before the latest commits"
		}
		// GitHub shows an approver asked again as awaiting review, though
		// the approval still counts.
		if reviewer.State == "APPROVED" {
			approvers = true
		}
		// A pending request is asked again only on purpose: renewing it
		// takes it away for a moment. Nor is an approver: an approval
		// rarely needs a nudge.
		chosen := reviewer.Stale && !reviewer.Pending && reviewer.State != "APPROVED"
		if chosen {
			e.chosen = append(e.chosen, reviewer.Login)
		}
		e.reviewed[strings.ToLower(reviewer.Login)] = reviewer.ReviewedAt
		options[i] = huh.NewOption(singleLine(label), reviewer.Login).Selected(chosen)
	}
	description := "Space toggles, Enter requests. GitHub notifies each one."
	if approvers {
		description += "\nApprovers asked again show as awaiting review; approvals still count."
	}
	m.rerequest = e
	e.form = huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().Title(e.title).
			Description(description).
			Options(options...).Filterable(false).Value(&e.chosen).
			Validate(func(chosen []string) error {
				if len(chosen) == 0 {
					return errors.New("choose at least one reviewer, or press Esc")
				}
				return nil
			}).
			// Sized after the title and description, with room for them to
			// wrap: with few options, huh otherwise leaves them no room.
			Height(len(options) + 4),
	))
	dark := m.darkBackground
	e.form.WithAccessible(false).WithShowHelp(true).
		WithTheme(huh.ThemeFunc(func(bool) *huh.Styles { return rulesTheme(dark) }))
	e.form.SubmitCmd, e.form.CancelCmd = nil, nil
	m.sizeRerequestForm()
	return e.form.Init()
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

// sizeRerequestForm fits the form to the terminal; every resize sets it again.
func (m *model) sizeRerequestForm() {
	m.rerequest.form.WithWidth(max(min(m.width, rulesWidth), 1)).WithHeight(max(m.height-rulesChrome, 1))
}

// updateRerequest passes a message to the form. Esc cancels; a finished form
// requests the reviews.
func (m *model) updateRerequest(msg tea.Msg) tea.Cmd {
	e := m.rerequest
	if press, ok := msg.(tea.KeyPressMsg); ok && press.String() == "esc" {
		m.rerequest = nil
		return nil
	}
	form, cmd := e.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		e.form = f
	}
	switch e.form.State {
	case huh.StateAborted:
		m.rerequest = nil
		return nil
	case huh.StateCompleted:
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
	if len(e.chosen) == 0 {
		return nil
	}
	m.setNotice(fmt.Sprintf("Requesting reviews of #%d…", pr.Number))
	ctx, list, repository, number, account := m.ctx, m.listReviewers, pr.Repository, pr.Number, m.accountGeneration
	key, chosen, reviewed := e.key, slices.Clone(e.chosen), e.reviewed
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
	now := make(map[string]github.Reviewer, len(msg.reviewers))
	for _, reviewer := range msg.reviewers {
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
	text := fmt.Sprintf("Requested reviews of #%d again from %s", msg.key.number, strings.Join(msg.logins, ", "))
	if len(msg.skipped) > 0 {
		text += "; not " + strings.Join(msg.skipped, ", ") + ", who reviewed it since"
	}
	m.setNotice(singleLine(text))
}

// rerequestLines draws the form.
func (m *model) rerequestLines() []string {
	lines := []string{m.titleLine("prpr — "+m.accountLabel()+" — request reviews again", ""), ""}
	lines = append(lines, strings.Split(m.rerequest.form.View(), "\n")...)
	return append(lines, "", m.shortHelp(m.keys.snoozeHelp()))
}
