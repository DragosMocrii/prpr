package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

// rerequestGroup is a heading of the R form and the people under it: those
// who reviewed or are requested, or the members of one requested team.
type rerequestGroup struct {
	title string
	// team indexes the editor's teams; -1 for the reviewers' group.
	team   int
	people []int
}

// rerequestRow is a row of the R form: a group's heading (person -1) or one
// of its people.
type rerequestRow struct {
	group, person int
}

// rerequestPanelMin is the narrowest terminal that draws the team panel
// beside the people; narrower ones draw a line of teams above them.
const rerequestPanelMin = 100

// newRerequestEditor groups the offered reviewers: the reviewers and
// requested people first, then each requested team's members, code owners
// first and smaller teams first, since those are the harder to cover. A
// person in several teams is under each one.
func newRerequestEditor(key prKey, title string, list github.ReviewerList, dark bool) *rerequestEditor {
	e := &rerequestEditor{key: key, title: title, people: list.Reviewers, teams: list.Teams,
		unreadable: list.UnreadableTeams, chosen: make(map[string]bool), reviewed: make(map[string]time.Time)}
	index := make(map[string]int, len(e.people))
	reviewers := rerequestGroup{title: "Reviewed or requested", team: -1}
	for i, reviewer := range e.people {
		login := strings.ToLower(reviewer.Login)
		index[login] = i
		e.reviewed[login] = reviewer.ReviewedAt
		if reviewer.State == "APPROVED" {
			e.approvers = true
		}
		// A pending request is asked again only on purpose: renewing it
		// takes it away for a moment. Nor is an approver: an approval
		// rarely needs a nudge. Team members who did not review are picked
		// by hand.
		if reviewer.Stale && !reviewer.Pending && reviewer.State != "APPROVED" {
			e.chosen[login] = true
		}
		if reviewer.State != "" || reviewer.Pending {
			reviewers.people = append(reviewers.people, i)
		}
	}
	if len(reviewers.people) > 0 {
		e.groups = append(e.groups, reviewers)
	}
	order := make([]int, len(e.teams))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		x, y := e.teams[a], e.teams[b]
		if x.CodeOwner != y.CodeOwner {
			if x.CodeOwner {
				return -1
			}
			return 1
		}
		return cmp.Compare(x.Members, y.Members)
	})
	for _, t := range order {
		team := e.teams[t]
		group := rerequestGroup{title: team.Name, team: t}
		for _, login := range team.Logins {
			if i, ok := index[strings.ToLower(login)]; ok {
				group.people = append(group.people, i)
			}
		}
		if len(group.people) > 0 {
			e.groups = append(e.groups, group)
		}
	}
	e.filter = textinput.New()
	e.filter.Prompt = "Filter: "
	styles := textinput.DefaultStyles(dark)
	styles.Cursor.Blink = false
	e.filter.SetStyles(styles)
	e.cursor = e.step(e.rows(), -1, 1)
	return e
}

// rows are the groups and people shown: under a filter, only matching
// people and the groups that have any.
func (e *rerequestEditor) rows() []rerequestRow {
	query := strings.ToLower(strings.TrimSpace(e.filter.Value()))
	var rows []rerequestRow
	for g, group := range e.groups {
		heading := false
		for _, p := range group.people {
			if query != "" && !strings.Contains(strings.ToLower(e.people[p].Login), query) {
				continue
			}
			if !heading {
				rows = append(rows, rerequestRow{group: g, person: -1})
				heading = true
			}
			rows = append(rows, rerequestRow{group: g, person: p})
		}
	}
	return rows
}

// step finds the person row n rows of people from row from, stopping at the
// first or last; from -1 starts before the first row.
func (e *rerequestEditor) step(rows []rerequestRow, from, n int) int {
	at := from
	for n != 0 {
		dir := 1
		if n < 0 {
			dir = -1
		}
		next := at + dir
		for next >= 0 && next < len(rows) && rows[next].person < 0 {
			next += dir
		}
		if next < 0 || next >= len(rows) {
			break
		}
		at, n = next, n-dir
	}
	if at < 0 && len(rows) > 1 {
		return 1
	}
	return max(at, 0)
}

// jumpGroup moves to the first person of the next or previous group.
func (e *rerequestEditor) jumpGroup(rows []rerequestRow, dir int) int {
	if len(rows) == 0 {
		return 0
	}
	group := rows[e.cursor].group
	for i := e.cursor + dir; i >= 0 && i < len(rows); i += dir {
		if rows[i].person >= 0 && rows[i].group != group {
			// Back to the start of that group.
			for i > 0 && rows[i-1].person >= 0 {
				i--
			}
			return i
		}
	}
	return e.cursor
}

// update handles a key: it reports whether to send or cancel the form.
func (e *rerequestEditor) update(msg tea.KeyPressMsg, k reviewerKeyMap, height int) (send, cancel bool, cmd tea.Cmd) {
	if e.filtering {
		switch {
		case key.Matches(msg, k.KeepFilter):
			e.filtering = false
			e.filter.Blur()
		case key.Matches(msg, k.ClearFilter):
			e.filtering = false
			e.filter.Blur()
			e.filter.SetValue("")
			e.cursor, e.offset = e.step(e.rows(), -1, 1), 0
		default:
			before := e.filter.Value()
			e.filter, cmd = e.filter.Update(msg)
			if e.filter.Value() != before {
				e.cursor, e.offset = e.step(e.rows(), -1, 1), 0
			}
		}
		return false, false, cmd
	}
	rows := e.rows()
	switch {
	case key.Matches(msg, k.Cancel):
		return false, true, nil
	case key.Matches(msg, k.Send):
		if len(e.chosenLogins()) == 0 {
			e.problem = "Choose at least one reviewer, or press Esc."
			return false, false, nil
		}
		return true, false, nil
	case key.Matches(msg, k.Filter):
		e.filtering = true
		return false, false, e.filter.Focus()
	case len(rows) == 0:
	case key.Matches(msg, k.Up):
		e.cursor = e.step(rows, e.cursor, -1)
	case key.Matches(msg, k.Down):
		e.cursor = e.step(rows, e.cursor, 1)
	case key.Matches(msg, k.PageUp):
		e.cursor = e.step(rows, e.cursor, -max(height-1, 1))
	case key.Matches(msg, k.PageDown):
		e.cursor = e.step(rows, e.cursor, max(height-1, 1))
	case key.Matches(msg, k.NextGroup):
		e.cursor = e.jumpGroup(rows, 1)
	case key.Matches(msg, k.PrevGroup):
		e.cursor = e.jumpGroup(rows, -1)
	case key.Matches(msg, k.Toggle):
		if p := rows[e.cursor].person; p >= 0 {
			login := strings.ToLower(e.people[p].Login)
			e.chosen[login] = !e.chosen[login]
			e.problem = ""
		}
	}
	return false, false, nil
}

// chosenLogins are the chosen people, in the order offered.
func (e *rerequestEditor) chosenLogins() []string {
	var logins []string
	for _, person := range e.people {
		if e.chosen[strings.ToLower(person.Login)] {
			logins = append(logins, person.Login)
		}
	}
	return logins
}

// covering names who answers a team: chosen members, and members whose
// approval is of the head commit.
func (e *rerequestEditor) covering(team github.TeamRequest) []string {
	var names []string
	for _, login := range team.Logins {
		lower := strings.ToLower(login)
		if e.chosen[lower] {
			names = append(names, login)
			continue
		}
		for _, person := range e.people {
			if strings.EqualFold(person.Login, login) && person.State == "APPROVED" && !person.Stale {
				names = append(names, login+" approved")
			}
		}
	}
	return names
}

// teamOrder is the teams in the order of their groups, then any without
// people to offer.
func (e *rerequestEditor) teamOrder() []int {
	var order []int
	for _, group := range e.groups {
		if group.team >= 0 {
			order = append(order, group.team)
		}
	}
	for t := range e.teams {
		if !slices.Contains(order, t) {
			order = append(order, t)
		}
	}
	return order
}

// shortTeam is a team's slug without its organization.
func shortTeam(name string) string {
	if _, slug, ok := strings.Cut(name, "/"); ok {
		return slug
	}
	return name
}

// unreadableTeams says that GitHub would not show some team requests.
func unreadableTeams(n int) string {
	return plural(n, "team request") + " not readable; gh may need the read:org scope (gh auth refresh -s read:org)"
}

var (
	rerequestHeading = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	rerequestGood    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	rerequestBad     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	rerequestFaint   = lipgloss.NewStyle().Faint(true)
	rerequestBold    = lipgloss.NewStyle().Bold(true)
)

// rerequestHeader is the form's lines above the people.
func (m *model) rerequestHeader() []string {
	e := m.rerequest
	lines := []string{
		m.titleLine("prpr — "+m.accountLabel()+" — request reviews", ""),
		"",
		rerequestBold.Render(singleLine(e.title)),
	}
	notes := []string{"Space chooses people, Enter requests their reviews; GitHub notifies each one."}
	if len(e.teams) > 0 {
		notes = append(notes, "Any member's approval answers a team's request.")
	}
	if e.approvers {
		notes = append(notes, "Approvers asked again show as awaiting review; approvals still count.")
	}
	if e.unreadable > 0 {
		notes = append(notes, unreadableTeams(e.unreadable)+".")
	}
	for _, line := range wrapWords(strings.Join(notes, " "), m.width) {
		lines = append(lines, rerequestFaint.Render(line))
	}
	if len(e.teams) > 0 && m.width < rerequestPanelMin {
		var marks []string
		for _, t := range e.teamOrder() {
			marks = append(marks, e.teamMark(t)+" "+shortTeam(e.teams[t].Name))
		}
		lines = append(lines, strings.Join(marks, " · "))
	}
	return append(lines, "")
}

// rerequestFooter is the form's lines below the people.
func (m *model) rerequestFooter() []string {
	e := m.rerequest
	status := rerequestBad.Render(e.problem)
	switch {
	case e.filtering || e.filter.Value() != "":
		status = e.filter.View()
	case e.problem == "":
		status = rerequestFaint.Render(plural(len(e.chosenLogins()), "reviewer") + " chosen")
	}
	help := m.shortHelp(m.keys.rerequestHelp())
	if e.filtering {
		help = m.shortHelp(m.keys.rerequestFilterHelp())
	}
	return []string{"", status, help}
}

// rerequestListHeight is how many rows of people fit.
func (m *model) rerequestListHeight() int {
	return max(m.height-len(m.rerequestHeader())-len(m.rerequestFooter()), 1)
}

// teamMark is ✓ for a team someone answers, else ✗.
func (e *rerequestEditor) teamMark(t int) string {
	if len(e.covering(e.teams[t])) > 0 {
		return rerequestGood.Render("✓")
	}
	return rerequestBad.Render("✗")
}

// rerequestLines draws the form: the people, and beside them on wide
// terminals, which requested teams the chosen people answer.
func (m *model) rerequestLines() []string {
	e := m.rerequest
	header, footer := m.rerequestHeader(), m.rerequestFooter()
	height := max(m.height-len(header)-len(footer), 1)
	rows := e.rows()
	e.cursor = min(e.cursor, max(len(rows)-1, 0))
	// Keep the cursor in view, with its group's heading when it is first.
	top := e.cursor
	if top > 0 && rows[top-1].person < 0 {
		top--
	}
	e.offset = min(e.offset, top)
	e.offset = max(e.offset, e.cursor-height+1, 0)
	listWidth, panel := m.width, []string(nil)
	if len(e.teams) > 0 && m.width >= rerequestPanelMin {
		panelWidth := min(48, m.width/3)
		listWidth = m.width - panelWidth - 3
		panel = e.panelLines(panelWidth)
	}
	loginWidth := 0
	for _, person := range e.people {
		loginWidth = max(loginWidth, ansi.StringWidth(person.Login))
	}
	var list []string
	for i := e.offset; i < len(rows) && len(list) < height; i++ {
		list = append(list, ansi.Truncate(e.rowLine(rows[i], i == e.cursor, loginWidth), listWidth, "…"))
	}
	if len(rows) == 0 {
		list = append(list, rerequestFaint.Render("  No one matches the filter."))
	}
	lines := header
	for i := range max(len(list), min(len(panel), height)) {
		line := ""
		if i < len(list) {
			line = list[i]
		}
		if panel != nil {
			line += strings.Repeat(" ", max(listWidth-ansi.StringWidth(line), 0)) + " " + rerequestFaint.Render("│") + " "
			if i < len(panel) {
				line += panel[i]
			}
		}
		lines = append(lines, line)
	}
	for len(lines) < len(header)+height {
		lines = append(lines, "")
	}
	return append(lines, footer...)
}

// rowLine draws a heading or a person.
func (e *rerequestEditor) rowLine(row rerequestRow, selected bool, loginWidth int) string {
	group := e.groups[row.group]
	if row.person < 0 {
		title := group.title
		if group.team >= 0 {
			team := e.teams[group.team]
			if team.CodeOwner {
				title += " · code owner"
			}
			title += fmt.Sprintf(" (%d)", team.Members)
			if team.Members > len(team.Logins)+1 {
				title += fmt.Sprintf(", first %d shown", len(team.Logins))
			}
		}
		return rerequestHeading.Render(title)
	}
	person := e.people[row.person]
	marker, box := "  ", "[ ]"
	if selected {
		marker = "> "
	}
	if e.chosen[strings.ToLower(person.Login)] {
		box = rerequestGood.Render("[x]")
	}
	login := person.Login + strings.Repeat(" ", loginWidth-ansi.StringWidth(person.Login))
	if selected {
		login = rerequestBold.Render(login)
	}
	line := marker + box + " " + login
	if detail := reviewerDetail(person); detail != "" {
		line += "  " + detail
	}
	var others []string
	for _, team := range person.Teams {
		if group.team < 0 || !strings.EqualFold(team, e.teams[group.team].Name) {
			others = append(others, "+"+shortTeam(team))
		}
	}
	if len(others) > 0 {
		line += "  " + rerequestFaint.Render(strings.Join(others, " "))
	}
	return line
}

// reviewerDetail words a person's review and request.
func reviewerDetail(reviewer github.Reviewer) string {
	switch {
	case reviewer.State == "" && reviewer.Pending:
		return "requested, no review yet"
	case reviewer.State == "":
		return ""
	}
	text := reviewWords(reviewer.State)
	if reviewer.Pending {
		text += ", requested again since"
	}
	if reviewer.Stale {
		text += ", before the latest commits"
	}
	return text
}

// panelLines lists the requested teams and who answers each.
func (e *rerequestEditor) panelLines(width int) []string {
	codeOwners, covered := 0, 0
	for _, team := range e.teams {
		if team.CodeOwner {
			codeOwners++
		}
	}
	title := "Teams requested"
	if codeOwners > 0 {
		title = "Code owners"
	}
	lines := []string{rerequestHeading.Render(title)}
	for _, t := range e.teamOrder() {
		team := e.teams[t]
		if codeOwners > 0 && !team.CodeOwner {
			continue
		}
		names := e.covering(team)
		line := e.teamMark(t) + " " + shortTeam(team.Name)
		if len(names) > 0 {
			covered++
			line += "  " + strings.Join(names, ", ")
		} else {
			line += "  " + rerequestFaint.Render(fmt.Sprintf("needs 1 of %d", team.Members))
		}
		lines = append(lines, ansi.Truncate(line, width, "…"))
	}
	total := len(e.teams)
	if codeOwners > 0 {
		total = codeOwners
	}
	lines = append(lines, "", fmt.Sprintf("%d of %d covered", covered, total))
	if codeOwners > 0 && codeOwners < len(e.teams) {
		lines = append(lines, "", rerequestHeading.Render("Other teams"))
		for _, t := range e.teamOrder() {
			if team := e.teams[t]; !team.CodeOwner {
				line := e.teamMark(t) + " " + shortTeam(team.Name)
				if names := e.covering(team); len(names) > 0 {
					line += "  " + strings.Join(names, ", ")
				}
				lines = append(lines, ansi.Truncate(line, width, "…"))
			}
		}
	}
	return lines
}
