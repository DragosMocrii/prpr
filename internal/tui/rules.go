package tui

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/readiness"
)

// readyResult checks an authored pull request against its owner's rule.
func (m *model) readyResult(pr *github.PullRequest) readiness.Result {
	rule, _ := m.rules.For(pr.Repository)
	return rule.Evaluate(pr, m.bots)
}

// ready reports whether an authored pull request is ready to merge by the
// rules.
func (m *model) ready(pr *github.PullRequest) bool {
	return m.readyResult(pr).Ready
}

// readyIn is ready for pane id. Rules read fields that only the authored
// query selects, so review rows are ready when GitHub would merge them.
func (m *model) readyIn(id paneID, pr *github.PullRequest) bool {
	if id == paneReview {
		return mergeReady(pr.Draft, pr.Mergeable, pr.MergeState)
	}
	return m.ready(pr)
}

// mergeCell is an authored pull request's Merge column.
func (m *model) mergeCell(pr *github.PullRequest) string {
	return mergeIcon(m.icons, pr.Draft, pr.Mergeable, pr.MergeState, m.ready(pr))
}

// ruleDetails lists each condition of a pull request's rule as met or not,
// for the details screen. It is empty while the rule is GitHub's own, which
// the Merge row already explains.
func (m *model) ruleDetails(pr *github.PullRequest) []string {
	rule, own := m.rules.For(pr.Repository)
	if rule == readiness.Default {
		return nil
	}
	result := rule.Evaluate(pr, m.bots)
	scope := "your default rules"
	if own {
		scope = singleLine(readiness.Owner(pr.Repository)) + "'s rules"
	}
	lines := []string{"Not ready by " + scope + "."}
	if result.Ready {
		lines[0] = "Ready by " + scope + "."
	}
	for _, check := range result.Checks {
		line := coloredIcon(m.icons.check, "2") + " " + check.Condition
		switch check.Status {
		case readiness.Unmet:
			line = coloredIcon(m.icons.cross, "1") + " " + check.Condition + ": " + singleLine(check.Detail)
		case readiness.Unknown:
			line = coloredIcon(m.icons.unknown, "3") + " " + check.Condition + ": " + singleLine(check.Detail)
		}
		lines = append(lines, line)
	}
	return lines
}

// applyRules uses new rules. When they read fields the last fetch left out,
// readiness is unknown until a fetch selects them, so the readiness baseline
// is dropped and that fetch records it without alerting; otherwise it is
// recorded now, also without alerting.
func (m *model) applyRules(rules readiness.Rules) tea.Cmd {
	before := m.rules.Needs()
	m.rules = rules
	needs := rules.Needs()
	if m.client != nil {
		m.client.SetNeeds(needs)
	}
	m.keepingSelection(m.rebuildVisiblePRs)
	if more := (needs.CodeOwners && !before.CodeOwners) || (needs.Threads && !before.Threads) ||
		(needs.RequiredChecks && !before.RequiredChecks); more {
		m.readiness = make(map[prKey]bool)
		// A fetch already running selected the old fields; a second one
		// could finish first, so the next fetch starts once it is done.
		if m.loading {
			m.refetchForRules = true
			return nil
		}
		if m.snapshot.Login != "" {
			return m.startFetch()
		}
		return nil
	}
	m.resetReadiness()
	return nil
}

// Rule editor choices that no owner can be named.
const (
	rulesDefault = ":default"
	rulesOther   = ":other"
	rulesSave    = ":save"
	rulesRemove  = ":remove"
)

// Condition keys in the editor, in the order a Result lists them.
const (
	conditionMergeButton        = "merge_button"
	conditionRequiredChecks     = "required_checks"
	conditionAllChecks          = "all_checks"
	conditionNoChangesRequested = "no_changes_requested"
	conditionCodeOwners         = "code_owners"
	conditionResolvedThreads    = "resolved_threads"
	conditionBotsClear          = "bots_clear"
)

type rulesStage int

const (
	rulesChoosing rulesStage = iota
	rulesNaming
	rulesEditing
)

// rulesEditor is the ready-to-merge rules screen: choose the default or an
// owner, name another owner, then edit the rule. Its form fields write to
// the editor's values.
type rulesEditor struct {
	form  *huh.Form
	stage rulesStage
	// owner is the lower-case owner being edited; "" is the default rule.
	owner      string
	choice     string
	name       string
	conditions []string
	approvals  string
	action     string
}

// rulesWidth caps the editor's width so its descriptions stay readable.
const rulesWidth = 76

// openRules opens the rule editor on its first step.
func (m *model) openRules() tea.Cmd {
	m.rulesEditor = &rulesEditor{}
	return m.showRulesForm(m.ownerForm())
}

func (m *model) closeRules() {
	m.rulesEditor = nil
}

// rulesChrome counts the editor's lines around the form's fields: the title
// and a blank line above, and below the form's own help with the blank line
// before it, then a blank line and the editor's help.
const rulesChrome = 6

// showRulesForm makes form the editor's current step.
func (m *model) showRulesForm(form *huh.Form) tea.Cmd {
	dark := m.darkBackground
	form.WithAccessible(false).WithShowHelp(true).
		WithTheme(huh.ThemeFunc(func(bool) *huh.Styles { return rulesTheme(dark) }))
	form.SubmitCmd, form.CancelCmd = nil, nil
	m.rulesEditor.form = form
	m.sizeRulesForm()
	return form.Init()
}

// rulesTheme is the editor's theme for a dark or light background. huh's
// ThemeCharm (through v2.0.3) passes its colors to lipgloss.LightDark dark
// first, so it draws near-black options on a dark background; asking for the
// other background gets the colors it means. When huh fixes that,
// TestRulesEditorTextContrastsWithTheBackground fails: pass dark as is.
func rulesTheme(dark bool) *huh.Styles {
	return huh.ThemeCharm(!dark)
}

// sizeRulesForm fits the form to the terminal. A form sized once ignores
// later window sizes, so every resize sets it again.
func (m *model) sizeRulesForm() {
	m.rulesEditor.form.WithWidth(max(min(m.width, rulesWidth), 1)).WithHeight(max(m.height-rulesChrome, 1))
}

// ownerForm chooses the rule to edit: the default, an owner with its own
// rule, an owner of a listed pull request, or another owner.
func (m *model) ownerForm() *huh.Form {
	e := m.rulesEditor
	e.choice = rulesDefault
	owners := make([]string, 0, len(m.rules.Owners))
	for owner := range m.rules.Owners {
		owners = append(owners, owner)
	}
	for _, list := range [][]github.PullRequest{m.snapshot.PullRequests, m.snapshot.ReviewRequests} {
		for i := range list {
			if owner := readiness.Owner(list[i].Repository); readiness.ValidOwner(owner) && !slices.Contains(owners, owner) {
				owners = append(owners, owner)
			}
		}
	}
	slices.Sort(owners)
	options := []huh.Option[string]{huh.NewOption("Default — every owner without rules of its own", rulesDefault)}
	for _, owner := range owners {
		label := owner + " — uses the default"
		if _, own := m.rules.Owners[owner]; own {
			label = owner + " — rules of its own"
		}
		options = append(options, huh.NewOption(label, owner))
	}
	options = append(options, huh.NewOption("Another owner…", rulesOther))
	return huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Ready-to-merge rules").
			Description("Choose the rules to edit. An owner's rules replace the default for its repositories.").
			Options(options...).Value(&e.choice),
	))
}

// nameForm names an owner that is not listed.
func (m *model) nameForm() *huh.Form {
	e := m.rulesEditor
	return huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Owner").Description("A GitHub user or organization, as in owner/repo.").
			CharLimit(39).Value(&e.name).Validate(func(name string) error {
			if !readiness.ValidOwner(strings.TrimSpace(name)) {
				return errors.New("enter a GitHub user or organization name")
			}
			return nil
		}),
	))
}

// editForm edits the rule of e.owner, starting from its current rule.
func (m *model) editForm() *huh.Form {
	e := m.rulesEditor
	rule, own := m.rules.Default, false
	scope := "Default"
	if e.owner != "" {
		rule, own = m.rules.For(e.owner + "/")
		scope = e.owner
	}
	e.conditions = nil
	option := func(label, key string, selected bool) huh.Option[string] {
		if selected {
			e.conditions = append(e.conditions, key)
		}
		return huh.NewOption(label, key).Selected(selected)
	}
	options := []huh.Option[string]{
		option(readiness.MergeButtonLabel, conditionMergeButton, rule.MergeButton),
		option(readiness.RequiredChecksLabel, conditionRequiredChecks, rule.RequiredChecks),
		option(readiness.AllChecksLabel, conditionAllChecks, rule.AllChecks),
		option(readiness.NoChangesRequestedLabel, conditionNoChangesRequested, rule.NoChangesRequested),
		option(readiness.CodeOwnersLabel, conditionCodeOwners, rule.CodeOwners),
		option(readiness.ResolvedThreadsLabel, conditionResolvedThreads, rule.ResolvedThreads),
	}
	if m.bots {
		options = append(options, option(readiness.BotsClearLabel+": no concerns, running, or failed bots", conditionBotsClear, rule.BotsClear))
	}
	e.approvals = strconv.Itoa(rule.Approvals)

	fields := []huh.Field{
		huh.NewMultiSelect[string]().Title(scope + ": ready to merge when all of these hold").
			Description("Space toggles, Enter continues. Drafts, conflicts, and merge states GitHub has not computed always block.").
			Options(options...).Filterable(false).Value(&e.conditions),
		huh.NewInput().Title("Minimum approvals").Description("0 requires none; at most " + strconv.Itoa(readiness.MaxApprovals) + ".").
			CharLimit(2).Value(&e.approvals).Validate(func(text string) error {
			if _, err := parseApprovals(text); err != nil {
				return err
			}
			return nil
		}),
		huh.NewNote().Title("What prpr cannot see").Description(
			"Required checks: a required check that has not started yet is not listed, so keep the merge button on to cover it, unless you bypass branch rules.\n" +
				"Code owners: approval means no code-owner review request is pending; a request removed without an approval looks the same."),
	}
	if e.owner != "" {
		e.action = rulesSave
		actions := []huh.Option[string]{huh.NewOption("Save rules for "+e.owner, rulesSave)}
		if own {
			actions = append(actions, huh.NewOption("Remove "+e.owner+"'s rules and use the default", rulesRemove))
		}
		fields = append(fields, huh.NewSelect[string]().Title("Save").Options(actions...).Value(&e.action))
	}
	return huh.NewForm(huh.NewGroup(fields...))
}

func parseApprovals(text string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n < 0 || n > readiness.MaxApprovals {
		return 0, fmt.Errorf("enter a number from 0 to %d", readiness.MaxApprovals)
	}
	return n, nil
}

// editedRule is the rule the edit form describes.
func (e *rulesEditor) editedRule() readiness.Rule {
	has := func(key string) bool { return slices.Contains(e.conditions, key) }
	approvals, _ := parseApprovals(e.approvals)
	return readiness.Rule{
		MergeButton:        has(conditionMergeButton),
		RequiredChecks:     has(conditionRequiredChecks),
		AllChecks:          has(conditionAllChecks),
		Approvals:          approvals,
		NoChangesRequested: has(conditionNoChangesRequested),
		CodeOwners:         has(conditionCodeOwners),
		ResolvedThreads:    has(conditionResolvedThreads),
		BotsClear:          has(conditionBotsClear),
	}
}

// updateRules passes a message to the editor's form. Esc cancels without
// saving; a finished step moves to the next one.
func (m *model) updateRules(msg tea.Msg) tea.Cmd {
	e := m.rulesEditor
	if press, ok := msg.(tea.KeyPressMsg); ok && press.String() == "esc" {
		m.closeRules()
		return nil
	}
	form, cmd := e.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		e.form = f
	}
	switch e.form.State {
	case huh.StateAborted:
		m.closeRules()
		return nil
	case huh.StateCompleted:
		return m.nextRulesStep()
	}
	return cmd
}

func (m *model) nextRulesStep() tea.Cmd {
	e := m.rulesEditor
	switch e.stage {
	case rulesChoosing:
		switch e.choice {
		case rulesOther:
			e.stage = rulesNaming
			return m.showRulesForm(m.nameForm())
		case rulesDefault:
			e.owner = ""
		default:
			e.owner = e.choice
		}
	case rulesNaming:
		e.owner = strings.ToLower(strings.TrimSpace(e.name))
	case rulesEditing:
		return m.saveRules()
	}
	e.stage = rulesEditing
	return m.showRulesForm(m.editForm())
}

// saveRules saves the edited rule and uses it. A failed save keeps it for
// the session and shows the warning.
func (m *model) saveRules() tea.Cmd {
	e := m.rulesEditor
	m.closeRules()
	rules := m.rules.Clone()
	switch {
	case e.owner == "":
		rules.Default = e.editedRule()
	case e.action == rulesRemove:
		delete(rules.Owners, e.owner)
	default:
		if rules.Owners == nil {
			rules.Owners = make(map[string]readiness.Rule)
		}
		rules.Owners[e.owner] = e.editedRule()
	}
	if err := m.preferences.SaveRules(rules); err != nil {
		m.preferenceErr = fmt.Errorf("Rules not saved: %w", err)
	} else {
		m.preferenceErr = nil
		m.setNotice("Ready-to-merge rules saved")
	}
	return m.applyRules(rules)
}

// rulesLines draws the rule editor.
func (m *model) rulesLines() []string {
	lines := []string{m.titleLine("prpr — "+m.accountLabel()+" — ready-to-merge rules", ""), ""}
	lines = append(lines, strings.Split(m.rulesEditor.form.View(), "\n")...)
	return append(lines, "", m.help.ShortHelpView(m.keys.rulesHelp().short))
}
