package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

// reviewerLookup is what the details know of an authored pull request's
// reviewers: read once per update of the pull request, as R reads them.
type reviewerLookup struct {
	// updated is the pull request's UpdatedAt when it was looked up; a
	// review, a request, or a push changes it.
	updated time.Time
	done    bool
	list    github.ReviewerList
	err     error
}

// detailReviewersMsg brings the reviewers of key for the details.
type detailReviewersMsg struct {
	account uint64
	key     prKey
	updated time.Time
	list    github.ReviewerList
	err     error
}

// Caps on the details' reviewer rows, so the rows below them stay in view.
const (
	detailReviewersMax = 8
	detailTeamsMax     = 6
)

// reviewerErrorWidth caps a failed lookup's error in the details, since gh's
// errors can run long.
const reviewerErrorWidth = 80

// lookupDetailReviewers reads the reviewers of the authored pull request the
// details show, unless they were read since it last changed or are being
// read. Failed reads are tried again when the details open next.
func (m *model) lookupDetailReviewers() tea.Cmd {
	if m.reviewerAccount != m.accountGeneration {
		m.reviewerLookups, m.reviewerAccount = nil, m.accountGeneration
	}
	if !m.details {
		for key, lookup := range m.reviewerLookups {
			if lookup.err != nil {
				delete(m.reviewerLookups, key)
			}
		}
	}
	if m.listReviewers == nil || !m.detailsShown() {
		return nil
	}
	pr, ok := m.detailsAuthoredPR()
	if !ok {
		return nil
	}
	key := keyOf(pr)
	if lookup := m.reviewerLookups[key]; lookup != nil && lookup.updated.Equal(pr.UpdatedAt) {
		return nil
	}
	if m.reviewerLookups == nil {
		m.reviewerLookups = make(map[prKey]*reviewerLookup)
	}
	m.reviewerLookups[key] = &reviewerLookup{updated: pr.UpdatedAt}
	ctx, list, repository, number := m.ctx, m.listReviewers, pr.Repository, pr.Number
	account, updated := m.accountGeneration, pr.UpdatedAt
	return tea.Batch(func() tea.Msg {
		reviewers, err := list(ctx, repository, number)
		return detailReviewersMsg{account: account, key: key, updated: updated, list: reviewers, err: err}
	}, m.spinner.Tick)
}

// loadingReviewers reports whether the details show reviewers still being
// read, which keeps the spinner turning.
func (m *model) loadingReviewers() bool {
	if !m.detailsShown() {
		return false
	}
	pr, ok := m.detailsAuthoredPR()
	if !ok {
		return false
	}
	lookup := m.reviewerLookups[keyOf(pr)]
	return lookup != nil && !lookup.done
}

// detailsAuthoredPR is the listed authored pull request the details show:
// not a preview, gone, review, or merged row.
func (m *model) detailsAuthoredPR() (*github.PullRequest, bool) {
	if m.snapshot.Preview || m.focus == paneMerged {
		return nil, false
	}
	pr, gone, ok := m.paneRow(m.focus, m.focused().table.Cursor())
	if !ok || gone {
		return nil, false
	}
	return m.authoredPR(keyOf(pr))
}

// handleDetailReviewers keeps the reviewers of the lookup still wanted, and
// forgets those of pull requests no longer listed.
func (m *model) handleDetailReviewers(msg detailReviewersMsg) {
	lookup := m.reviewerLookups[msg.key]
	if msg.account != m.accountGeneration || lookup == nil || lookup.done || !lookup.updated.Equal(msg.updated) {
		return
	}
	lookup.done, lookup.list, lookup.err = true, msg.list, msg.err
	for key := range m.reviewerLookups {
		if _, ok := m.authoredPR(key); !ok {
			delete(m.reviewerLookups, key)
		}
	}
}

// reviewerDetailRows are the details' Reviewers and Teams rows of an
// authored pull request.
func (m *model) reviewerDetailRows(pr *github.PullRequest) []detailRowText {
	lookup := m.reviewerLookups[keyOf(pr)]
	switch {
	case lookup == nil || !lookup.done:
		return []detailRowText{
			{"Reviewers", []string{m.spinner.View() + " Loading reviewers"}},
			{"Teams", []string{m.spinner.View() + " Loading teams"}},
		}
	case lookup.err != nil:
		reason := ansi.Truncate(singleLine(lookup.err.Error()), reviewerErrorWidth, "…")
		return []detailRowText{
			{"Reviewers", []string{rerequestBad.Render("✗") + " Couldn't load reviewers: " + reason}},
			{"Teams", []string{"Unknown; reopen the details to try again"}},
		}
	}
	list := lookup.list
	var people []string
	for _, reviewer := range list.Reviewers {
		if reviewer.State == "" && !reviewer.Pending {
			continue
		}
		text := singleLine(reviewer.Login) + ": " + reviewerDetail(reviewer)
		if len(reviewer.OnBehalfOf) > 0 {
			names := make([]string, len(reviewer.OnBehalfOf))
			for i, team := range reviewer.OnBehalfOf {
				names[i] = shortTeam(singleLine(team))
			}
			text += ", for " + strings.Join(names, ", ")
		}
		people = append(people, text)
	}
	if len(people) == 0 {
		people = []string{"No one reviewed it or is requested to"}
	}
	rows := []detailRowText{{"Reviewers", capped(people, detailReviewersMax)}}
	var teams []string
	answers := teamAnswers(list)
	approved := 0
	for _, answer := range answers {
		if len(answer.approved) > 0 {
			approved++
		}
		teams = append(teams, answer.line())
	}
	teams = capped(teams, detailTeamsMax)
	if len(answers) > 0 {
		teams = append(teams, fmt.Sprintf("%d of %d approved", approved, len(answers)))
	}
	if list.UnreadableTeams > 0 {
		teams = append(teams, unreadableTeams(list.UnreadableTeams))
	}
	if list.AnswersUnknown {
		teams = append(teams, "Teams that reviews answered are not readable"+readOrgHint)
	}
	if len(teams) == 0 {
		teams = []string{"No team reviews requested"}
	}
	return append(rows, detailRowText{"Teams", teams})
}

// capped keeps the first n values, and says how many more there are.
func capped(values []string, n int) []string {
	if len(values) <= n {
		return values
	}
	return append(slices.Clone(values[:n-1]), fmt.Sprintf("+%d more", len(values)-n+1))
}

// teamAnswer is how a team's review stands: a pending team request, or a
// team a review answered, which GitHub no longer lists as requested.
type teamAnswer struct {
	name string
	// requested reports a pending request; codeOwner and members are known
	// only for one.
	requested bool
	codeOwner bool
	members   int
	// approved and others word the members who approved, and those who
	// reviewed otherwise or are requested in person.
	approved, others []string
}

// teamAnswers lists the pending team requests, code owners first, then the
// teams reviews answered.
func teamAnswers(list github.ReviewerList) []teamAnswer {
	var answers []teamAnswer
	index := make(map[string]int)
	for _, team := range list.Teams {
		index[strings.ToLower(team.Name)] = len(answers)
		answers = append(answers, teamAnswer{name: team.Name, requested: true, codeOwner: team.CodeOwner, members: team.Members})
	}
	for _, reviewer := range list.Reviewers {
		var teams []string
		for _, team := range reviewer.Teams {
			if !slices.ContainsFunc(reviewer.OnBehalfOf, func(t string) bool { return strings.EqualFold(t, team) }) {
				teams = append(teams, team)
			}
		}
		teams = append(teams, reviewer.OnBehalfOf...)
		for _, team := range teams {
			i, ok := index[strings.ToLower(team)]
			if !ok {
				i = len(answers)
				index[strings.ToLower(team)] = i
				answers = append(answers, teamAnswer{name: team})
			}
			login := singleLine(reviewer.Login)
			switch {
			// A team still requested was not answered by an approval
			// before the latest commits; one GitHub took as answered was.
			case reviewer.State == "APPROVED" && reviewer.Stale && answers[i].requested:
				answers[i].others = append(answers[i].others, login+" approved before the latest commits")
			case reviewer.State == "APPROVED" && reviewer.Stale:
				answers[i].approved = append(answers[i].approved, login+" approved before the latest commits")
			case reviewer.State == "APPROVED":
				answers[i].approved = append(answers[i].approved, login+" approved")
			case reviewer.Pending:
				answers[i].others = append(answers[i].others, login+" requested")
			case reviewer.State != "" && reviewer.State != "DISMISSED":
				answers[i].others = append(answers[i].others, login+" "+reviewWords(reviewer.State))
			}
		}
	}
	return answers
}

// line marks the team ✓ when a member approved (the latest commits, for a
// team still requested), ◐ when members reviewed otherwise or are requested
// in person, else ✗, and names them.
func (a teamAnswer) line() string {
	mark := rerequestBad.Render("✗")
	switch {
	case len(a.approved) > 0:
		mark = rerequestGood.Render("✓")
	case len(a.others) > 0:
		mark = rerequestPartial.Render("◐")
	}
	line := mark + " " + shortTeam(singleLine(a.name))
	if a.requested && !a.codeOwner {
		line += " (not a code owner)"
	}
	names := slices.Concat(a.approved, a.others)
	switch {
	case len(names) > 0:
		line += ": " + strings.Join(names, ", ")
	case a.requested:
		line += fmt.Sprintf(": needs 1 of %d", a.members)
	}
	return line
}
