package tui

import (
	"errors"
	"fmt"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/DragosMocrii/prpr/internal/github"
)

// accountPicker chooses the GitHub CLI account prpr uses. Its first row
// follows gh's active account; the others pin one account.
type accountPicker struct {
	accounts  []github.Account
	cursor    int
	busy      bool
	err       error
	requestID uint64
}

type accountsListedMsg struct {
	requestID uint64
	accounts  []github.Account
	err       error
}

// openAccountPicker lists gh's accounts for github.com.
func (m *model) openAccountPicker() tea.Cmd {
	m.accountRequestID++
	m.accounts = &accountPicker{busy: true, requestID: m.accountRequestID}
	return tea.Batch(m.listAccountsCmd(), m.spinner.Tick)
}

func (m *model) listAccountsCmd() tea.Cmd {
	list, ctx, id := m.listAccounts, m.ctx, m.accounts.requestID
	return func() tea.Msg {
		accounts, err := list(ctx)
		return accountsListedMsg{requestID: id, accounts: accounts, err: err}
	}
}

func (m *model) handleAccountsListed(msg accountsListedMsg) {
	p := m.accounts
	if p == nil || msg.requestID != p.requestID {
		return
	}
	p.busy, p.err, p.accounts = false, msg.err, msg.accounts
	p.cursor = 0
	for i, account := range p.accounts {
		if account.Login == m.pinnedAccount && m.pinnedAccount != "" {
			p.cursor = i + 1
		}
	}
}

func (m *model) closeAccountPicker() {
	m.accounts = nil
	m.accountRequestID++
}

func (m *model) updateAccountPicker(msg tea.KeyPressMsg) tea.Cmd {
	p := m.accounts
	k := m.keys.AccountPicker
	switch {
	case key.Matches(msg, k.Cancel):
		m.closeAccountPicker()
	case key.Matches(msg, k.Up):
		p.cursor = max(0, p.cursor-1)
	case key.Matches(msg, k.Down):
		p.cursor = min(len(p.accounts), p.cursor+1)
	case key.Matches(msg, k.Retry):
		m.accountRequestID++
		p.busy, p.err, p.requestID = true, nil, m.accountRequestID
		return tea.Batch(m.listAccountsCmd(), m.spinner.Tick)
	case key.Matches(msg, k.Apply):
		login := ""
		if p.cursor > 0 {
			login = p.accounts[p.cursor-1].Login
		}
		m.closeAccountPicker()
		return m.chooseAccount(login)
	}
	return nil
}

// chooseAccount pins login, or follows gh's active account when it is
// empty, saves the choice, and fetches as that account. Results of requests
// made as the previous account are dropped.
func (m *model) chooseAccount(login string) tea.Cmd {
	if login == m.pinnedAccount {
		return nil
	}
	m.pinnedAccount = login
	m.useAccount(login)
	m.accountGeneration++
	if err := m.preferences.SavePinnedAccount(login); err != nil {
		m.preferenceErr = fmt.Errorf("Account choice not saved: %w", err)
		m.keepPreferenceErr = true
	}
	// The rows on screen belong to the previous account.
	m.snapshot = github.Snapshot{}
	m.err = nil
	m.details = false
	m.searching = nil
	m.quotaKnown, m.quotaStale = false, false
	m.rebuildVisiblePRs()
	return m.startFetch()
}

// accountLabel names the account in titles, marking a pinned one.
func (m *model) accountLabel() string {
	label := "@" + singleLine(m.snapshot.Login)
	if m.pinnedAccount != "" {
		label += " (pinned)"
	}
	return label
}

func (m *model) accountPickerLines() []string {
	p := m.accounts
	lines := []string{"Choose the GitHub CLI account prpr uses", ""}
	faint := lipgloss.NewStyle().Faint(true)
	row := func(i int, text string) {
		marker := "  "
		if i == p.cursor {
			marker = "> "
		}
		lines = append(lines, marker+text)
	}
	follow := "Follow gh's active account"
	if m.pinnedAccount == "" {
		follow += " (in use)"
	}
	row(0, follow)
	for i, account := range p.accounts {
		text := "@" + singleLine(account.Login)
		if account.Active {
			text += " · active in gh"
		}
		if account.Login == m.pinnedAccount {
			text += " (in use)"
		}
		if !account.OK {
			detail := "token not working"
			if account.Error != "" {
				detail += ": " + singleLine(account.Error)
			}
			text += " " + faint.Render(detail)
		}
		row(i+1, text)
	}
	lines = append(lines, "")
	switch {
	case p.busy:
		lines = append(lines, m.spinner.View()+" Checking GitHub CLI accounts...")
	case p.err != nil:
		lines = append(lines, singleLine(p.err.Error()))
	default:
		lines = append(lines, faint.Render("A pinned account stays in use when gh's active account changes. Add accounts with gh auth login."))
	}
	return append(lines, m.helpLines(keyMap.accountPickerHelp)...)
}

// tokenRecheckInterval is how often a pinned account that failed
// authentication is checked for a new token. The check reads gh's local
// store only.
const tokenRecheckInterval = time.Minute

type tokenRecheckMsg struct {
	generation uint64
	account    uint64
}

type tokenCheckedMsg struct {
	generation uint64
	account    uint64
	changed    bool
}

// awaitingToken reports whether the pinned account failed authentication,
// so prpr waits for gh to have a new token for it.
func (m *model) awaitingToken() bool {
	var authErr *github.AuthError
	return m.pinnedAccount != "" && errors.As(m.err, &authErr)
}

// scheduleTokenRecheck starts waiting for a new token after an
// authentication failure. Any fetch or account choice ends the wait.
func (m *model) scheduleTokenRecheck() tea.Cmd {
	if !m.awaitingToken() {
		return nil
	}
	generation, account := m.refreshGeneration, m.accountGeneration
	return tea.Tick(tokenRecheckInterval, func(time.Time) tea.Msg {
		return tokenRecheckMsg{generation: generation, account: account}
	})
}

func (m *model) tokenWaitCurrent(generation, account uint64) bool {
	return generation == m.refreshGeneration && account == m.accountGeneration && m.awaitingToken()
}

func (m *model) handleTokenRecheck(msg tokenRecheckMsg) tea.Cmd {
	if !m.tokenWaitCurrent(msg.generation, msg.account) {
		return nil
	}
	changed, ctx := m.tokenChanged, m.ctx
	return func() tea.Msg {
		return tokenCheckedMsg{generation: msg.generation, account: msg.account, changed: changed(ctx)}
	}
}

// handleTokenChecked fetches again once gh has a new token, unless login or
// a picker is open, and otherwise keeps waiting.
func (m *model) handleTokenChecked(msg tokenCheckedMsg) tea.Cmd {
	if !m.tokenWaitCurrent(msg.generation, msg.account) {
		return nil
	}
	if msg.changed && !m.loginActive && m.accounts == nil {
		return m.startFetch()
	}
	return m.scheduleTokenRecheck()
}
