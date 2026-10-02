package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/notifier"
	"github.com/DragosMocrii/prpr/internal/preferences"
	"github.com/DragosMocrii/prpr/internal/readiness"
)

const (
	minimumWidth  = 40
	minimumHeight = 8
)

type model struct {
	ctx                context.Context
	client             *github.Client
	preferences        *preferences.Store
	cancel             context.CancelFunc
	snapshot           github.Snapshot
	selectedRepository string
	// watchlist is the watchlist scope; its empty name means none.
	watchlist   preferences.Watchlist
	filterLogin string
	panes       [2]prPane
	changes     [2]paneChanges
	// changesLogin is the account the change baseline belongs to.
	changesLogin        string
	focus               paneID
	keys                keyMap
	help                help.Model
	spinner             spinner.Model
	darkBackground      bool
	picker              *repositoryPicker
	repositoryRequestID uint64
	width               int
	height              int
	loading             bool
	loginActive         bool
	scopeChosen         bool
	scopeChoiceCursor   int
	preferenceErr       error
	err                 error
	now                 func() time.Time
	refreshInterval     time.Duration
	// refreshGeneration increments with every fetch so that auto-refresh
	// ticks scheduled before it are ignored.
	refreshGeneration uint64
	// refreshDue is when the scheduled auto-refresh fires; zero when none is
	// pending. countdownGeneration increments with every schedule so only the
	// newest countdown tick chain keeps running.
	refreshDue          time.Time
	countdownGeneration uint64
	// unknownRechecks counts the fetches in a row that left a merge state in
	// scope unknown; while it is positive, auto-refresh comes sooner.
	unknownRechecks int
	quota           github.RateLimit
	quotaKnown      bool
	quotaStale      bool
	quotaPaused     bool
	// bots shows the Bots column and the selected PR's bot breakdown.
	bots bool
	// mouse is mouse mode: hover, click, and wheel input instead of the
	// terminal's own selection. pointer is where the mouse was last seen.
	mouse   bool
	pointer pointer
	// openBrowser opens a pull request URL; it is the client's in the app.
	openBrowser func(context.Context, string) error
	// notice reports the last open or copy action in the status line until
	// the next key press. noticeID increments whenever it is set.
	notice   string
	noticeID uint64
	// details is the details screen for the focused pane's selected row.
	details bool
	// search and quick filter both lists; searching is the search input while
	// it is edited, and searchBefore the search that Esc restores.
	search string
	quick  quickFilter
	// category is the attention category shown, numbered from 1; 0 is none.
	category     int
	searching    *textinput.Model
	searchBefore string
	// notify sends a desktop notification when a refresh finds alerts.
	// readiness is each authored pull request's last known merge readiness.
	notify    bool
	readiness map[prKey]bool
	// icons draws status symbols: unicodeIcons or nerdIcons.
	icons *iconSet
	// rules decide when an authored pull request is ready to merge;
	// rulesEditor is the screen that edits them, nil when closed.
	rules       readiness.Rules
	rulesEditor *rulesEditor
	// refetchForRules starts a fetch when the running one finishes, for
	// rules that need fields it does not select.
	refetchForRules bool
	// setTitle sets the terminal title. flashText is an alert the title
	// flashes until flashUntil, the terminal gains focus, or a key or click;
	// ticks from an older flashGeneration are dropped.
	setTitle        bool
	terminalFocus   terminalFocus
	flashText       string
	flashOn         bool
	flashUntil      time.Time
	flashGeneration uint64
	// desktopNotify posts through the system notifier; nil means OSC 9.
	desktopNotify notifier.Func
	// pinnedAccount is the GitHub CLI account prpr uses, or "" for gh's
	// active one. switchAccounts is false when the environment gives gh a
	// token, which wins over any account. accountGeneration increments with
	// each choice, so results requested as another account are dropped.
	pinnedAccount     string
	switchAccounts    bool
	accountGeneration uint64
	accounts          *accountPicker
	// keepPreferenceErr keeps a failed account-choice save in view past the
	// new account's first snapshot.
	keepPreferenceErr bool
	accountRequestID  uint64
	listAccounts      func(context.Context) ([]github.Account, error)
	useAccount        func(string)
	// tokenChanged reports whether gh has a new token for the pinned account.
	tokenChanged func(context.Context) bool
}

type fetchFinishedMsg struct {
	snapshot github.Snapshot
	err      error
	// account is the accountGeneration the fetch ran for.
	account uint64
}

// previewMsg carries a fast first look at both lists for the fetch of that
// generation.
type previewMsg struct {
	generation uint64
	account    uint64
	snapshot   github.Snapshot
	err        error
}

type loginFinishedMsg struct{ err error }

type autoRefreshMsg struct{ generation uint64 }

type countdownTickMsg struct{ generation uint64 }

// New returns the app model. A positive refreshInterval refetches both lists
// that long after each fetch finishes; notify starts with notifications on.
// icons names the icon set for this run; empty uses the saved one. title
// sets the terminal title.
func New(ctx context.Context, client *github.Client, preferences *preferences.Store, refreshInterval time.Duration, notify bool, icons string, title bool) tea.Model {
	appCtx, cancel := context.WithCancel(ctx)
	m := newModel(appCtx, client, preferences)
	m.cancel = cancel
	m.refreshInterval = refreshInterval
	m.bots = len(client.Bots()) > 0
	m.openBrowser = client.OpenInBrowser
	m.notify = notify
	m.icons = startIcons(icons, preferences)
	m.rules = preferences.Rules()
	client.SetNeeds(m.rules.Needs())
	if err := preferences.RulesErr(); err != nil {
		m.preferenceErr = err
	}
	m.setTitle = title
	m.desktopNotify = notifier.Desktop()
	m.listAccounts = client.Accounts
	m.useAccount = client.UseAccount
	m.tokenChanged = client.PinnedTokenChanged
	// An environment token wins over every stored account.
	m.switchAccounts = !github.EnvironmentToken()
	if m.switchAccounts {
		m.pinnedAccount = preferences.PinnedAccount()
		client.UseAccount(m.pinnedAccount)
	}
	return m
}

func newModel(ctx context.Context, client *github.Client, preferences *preferences.Store) *model {
	m := &model{
		ctx:            ctx,
		client:         client,
		preferences:    preferences,
		cancel:         func() {},
		keys:           defaultKeyMap(),
		help:           help.New(),
		spinner:        spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		darkBackground: true,
		now:            time.Now,
		icons:          &unicodeIcons,
		rules:          readiness.DefaultRules(),
	}
	m.panes = [2]prPane{newPRPane(), newPRPane()}
	m.rebuildPRTable(true)
	return m
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.startFetch(), m.pollQuota())
}

func (m *model) startFetch() tea.Cmd {
	m.closeRepositoryPicker()
	m.loading = true
	m.loginActive = false
	m.refreshGeneration++
	m.refreshDue = time.Time{}
	m.err = nil
	client, ctx, account := m.client, m.ctx, m.accountGeneration
	cmds := []tea.Cmd{func() tea.Msg {
		snapshot, err := client.Fetch(ctx)
		return fetchFinishedMsg{snapshot: snapshot, err: err, account: account}
	}, m.spinner.Tick}
	// With no rows on screen, a preview shows rows while the full fetch runs.
	// A refresh keeps the full rows already shown instead.
	if m.snapshot.Login == "" {
		generation := m.refreshGeneration
		cmds = append(cmds, func() tea.Msg {
			snapshot, err := client.Preview(ctx)
			return previewMsg{generation: generation, account: account, snapshot: snapshot, err: err}
		})
	}
	return tea.Batch(cmds...)
}

// unknownRecheckDelay is how soon auto-refresh comes after the first fetch
// that leaves a merge state unknown; each further such fetch doubles it, up
// to the refresh interval. GitHub computes merge states in the background
// and answers UNKNOWN until it is done.
const unknownRecheckDelay = 15 * time.Second

// countUnknownRechecks records whether a full fetch left a merge state in
// scope unknown.
func (m *model) countUnknownRechecks() {
	for _, id := range paneIDs {
		for i := range m.source(id) {
			if pr := &m.source(id)[i]; m.inScope(pr) && unknownMergeState(pr) {
				m.unknownRechecks++
				return
			}
		}
	}
	m.unknownRechecks = 0
}

// scheduleAutoRefresh starts the auto-refresh timer for the current fetch
// generation, sooner while merge states are unknown.
func (m *model) scheduleAutoRefresh() tea.Cmd {
	if m.refreshInterval <= 0 {
		return nil
	}
	delay := m.refreshInterval
	if m.unknownRechecks > 0 && m.err == nil {
		recheck := unknownRecheckDelay << min(m.unknownRechecks-1, 10)
		delay = min(delay, recheck)
	}
	generation := m.refreshGeneration
	m.refreshDue = m.now().Add(delay)
	m.countdownGeneration++
	return tea.Batch(tea.Tick(delay, func(time.Time) tea.Msg {
		return autoRefreshMsg{generation: generation}
	}), m.countdownTick())
}

// countdownTick redraws the countdown when its shown second changes. The
// chain ends when the refresh is due, starts, or is rescheduled.
func (m *model) countdownTick() tea.Cmd {
	remaining := m.refreshDue.Sub(m.now())
	if m.refreshDue.IsZero() || remaining <= 0 {
		return nil
	}
	delay := remaining % time.Second
	if delay == 0 {
		delay = time.Second
	}
	generation := m.countdownGeneration
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return countdownTickMsg{generation: generation}
	})
}

// countdownText is the time left before the auto-refresh, rounded up to the
// second, or empty when none is pending.
func (m *model) countdownText() string {
	if m.refreshDue.IsZero() {
		return ""
	}
	seconds := int((m.refreshDue.Sub(m.now()) + time.Second - 1) / time.Second)
	seconds = max(seconds, 0)
	text := fmt.Sprintf("%d:%02d", seconds/60%60, seconds%60)
	if seconds >= 3600 {
		text = fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
	}
	return "refresh in " + text
}

// refreshing reports whether a fetch is replacing rows that are still shown.
func (m *model) refreshing() bool {
	return m.loading && !m.loginActive && m.snapshot.Login != ""
}

// spinning reports whether a visible request is in flight. Spinner ticks
// that arrive otherwise are dropped, which ends the tick loop.
func (m *model) spinning() bool {
	return (m.loading && !m.loginActive) || (m.picker != nil && m.picker.busy) || (m.accounts != nil && m.accounts.busy)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.update(msg)
	m.closeStaleDetails()
	return model, cmd
}

func (m *model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.SetWidth(msg.Width)
		m.rebuildPRTable(false)
		if m.picker != nil {
			m.picker.setWidth(m.width)
			m.picker.clamp(m.pickerViewportHeight())
		}
		m.setSearchStyle()
		if m.rulesEditor != nil {
			// The form redraws its fields only on a message of its own.
			m.sizeRulesForm()
			return m, m.updateRules(msg)
		}
	case tea.BackgroundColorMsg:
		m.darkBackground = msg.IsDark()
		m.help.Styles = help.DefaultStyles(m.darkBackground)
		m.applyFocusStyles()
		if m.picker != nil {
			m.picker.setDark(m.darkBackground)
		}
		m.setSearchStyle()
		if m.rulesEditor != nil {
			return m, m.updateRules(msg)
		}
	case spinner.TickMsg:
		if m.spinning() {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	case repositoryListFinishedMsg:
		m.handleRepositoryListFinished(msg)
	case repositoryLookupFinishedMsg:
		return m, m.handleRepositoryLookupFinished(msg)
	case rateLimitMsg, quotaTickMsg:
		return m, m.handleQuota(msg)
	case countdownTickMsg:
		if msg.generation != m.countdownGeneration {
			return m, nil
		}
		return m, m.countdownTick()
	case autoRefreshMsg:
		if msg.generation != m.refreshGeneration {
			return m, nil
		}
		// Fetching would close the picker or skip the scope choice, and login
		// fetches when it finishes.
		if m.picker != nil || m.loginActive || (!m.scopeChosen && m.snapshot.Login != "") {
			return m, m.scheduleAutoRefresh()
		}
		return m, m.startFetch()
	case fetchFinishedMsg:
		if msg.account != m.accountGeneration {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			// The retry selects the fields current rules need.
			m.refetchForRules = false
			// The picker can be open when the scope is chosen from a preview.
			m.closeRepositoryPicker()
			// The search input belongs to the list, which the error replaces.
			m.searching = nil
			m.snapshot = github.Snapshot{}
			// Change tracking keeps its baseline, so the next success is
			// compared with the last good lists.
			for _, id := range paneIDs {
				m.panes[id].visible = nil
				m.panes[id].gone = nil
			}
			m.rebuildPRTable(true)
			// Authentication failures need a login, not a retry. A pinned
			// account is fetched again once gh has a new token for it.
			var authErr *github.AuthError
			if errors.As(msg.err, &authErr) {
				return m, m.scheduleTokenRecheck()
			}
		} else {
			notify := m.applySnapshot(msg.snapshot)
			m.countUnknownRechecks()
			if m.refetchForRules {
				m.refetchForRules = false
				return m, tea.Batch(notify, m.startFetch(), m.resumeQuota())
			}
			return m, tea.Batch(notify, m.scheduleAutoRefresh(), m.resumeQuota())
		}
		if m.err == nil {
			return m, tea.Batch(m.scheduleAutoRefresh(), m.resumeQuota())
		}
		return m, m.scheduleAutoRefresh()
	case previewMsg:
		// A preview is dropped once its fetch has finished or been replaced,
		// and when it fails: the full fetch reports errors.
		if msg.err == nil && msg.generation == m.refreshGeneration && msg.account == m.accountGeneration && m.loading && m.snapshot.Login == "" {
			m.applySnapshot(msg.snapshot)
		}
	case loginFinishedMsg:
		m.loginActive = false
		m.loading = false
		if msg.err != nil {
			m.err = fmt.Errorf("GitHub login did not complete: %w", msg.err)
			return m, nil
		}
		return m, m.startFetch()
	case browserOpenedMsg:
		m.handleBrowserOpened(msg)
	case accountsListedMsg:
		m.handleAccountsListed(msg)
	case tokenRecheckMsg:
		return m, m.handleTokenRecheck(msg)
	case tokenCheckedMsg:
		return m, m.handleTokenChecked(msg)
	case desktopNotifiedMsg:
		m.handleDesktopNotified(msg)
	case flashTickMsg:
		return m, m.handleFlashTick(msg)
	case tea.FocusMsg:
		m.handleFocus(focusIn)
	case tea.BlurMsg:
		m.handleFocus(focusOut)
	case tea.PasteMsg:
		if m.rulesEditor != nil {
			return m, m.updateRules(msg)
		}
		if m.picker != nil {
			return m, m.picker.paste(msg.Content, m.pickerViewportHeight())
		}
		if m.searching != nil {
			return m, m.updateSearch(msg)
		}
	case tea.KeyPressMsg:
		m.stopFlash()
		return m, m.handleKey(msg)
	case tea.MouseClickMsg, tea.MouseWheelMsg, tea.MouseMotionMsg:
		if _, click := msg.(tea.MouseClickMsg); click {
			m.stopFlash()
		}
		m.handleMouse(msg.(tea.MouseMsg))
	default:
		// The rule editor's form steps itself with messages of its own.
		if m.rulesEditor != nil {
			return m, m.updateRules(msg)
		}
	}
	return m, nil
}

func (m *model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	m.syncKeys()
	k := m.keys
	if key.Matches(msg, k.ForceQuit) {
		m.closeRepositoryPicker()
		m.cancel()
		return tea.Quit
	}
	if m.rulesEditor != nil {
		return m.updateRules(msg)
	}
	if m.picker != nil {
		return m.updateRepositoryPicker(msg)
	}
	if m.accounts != nil {
		return m.updateAccountPicker(msg)
	}
	if m.searching != nil {
		return m.updateSearch(msg)
	}
	m.notice = ""
	switch {
	case key.Matches(msg, k.Quit):
		m.cancel()
		return tea.Quit
	case key.Matches(msg, k.Refresh, k.Retry):
		return m.startFetch()
	case key.Matches(msg, k.Login):
		m.loading = true
		m.loginActive = true
		return tea.ExecProcess(m.client.LoginCommand(m.ctx), func(err error) tea.Msg {
			return loginFinishedMsg{err: err}
		})
	case key.Matches(msg, k.PickRepository):
		return m.openRepositoryPicker()
	case key.Matches(msg, k.AllRepositories):
		m.chooseRepository("")
	case key.Matches(msg, k.NextPane, k.PrevPane):
		// Two panes: next and previous are the same move.
		m.setFocus(1 - m.focus)
	case key.Matches(msg, k.ClearMarks):
		m.keepingSelection(func() {
			for _, id := range paneIDs {
				m.changes[id].clear()
			}
			m.rebuildVisiblePRs()
		})
	case key.Matches(msg, k.Details):
		m.details = true
	case key.Matches(msg, k.Back):
		m.details = false
	case key.Matches(msg, k.Search):
		return m.openSearch()
	case key.Matches(msg, k.QuickDrafts):
		m.toggleQuick(quickDrafts)
	case key.Matches(msg, k.QuickFailing):
		m.toggleQuick(quickFailing)
	case key.Matches(msg, k.QuickReady):
		m.toggleQuick(quickReady)
	case key.Matches(msg, k.Categories):
		m.toggleCategory(int(msg.String()[0] - '0'))
	case key.Matches(msg, k.ClearFilters):
		m.clearFilters()
	case key.Matches(msg, k.Open):
		return m.openSelected()
	case key.Matches(msg, k.CopyURL):
		return m.copySelected()
	case key.Matches(msg, k.Account):
		return m.openAccountPicker()
	case key.Matches(msg, k.Mouse):
		m.toggleMouse()
	case key.Matches(msg, k.Notify):
		m.toggleNotify()
	case key.Matches(msg, k.Icons):
		m.toggleIcons()
	case key.Matches(msg, k.Rules):
		return m.openRules()
	case key.Matches(msg, k.Help):
		m.help.ShowAll = !m.help.ShowAll
		m.rebuildPRTable(false)
	case key.Matches(msg, k.ChoiceUp):
		m.scopeChoiceCursor = 0
	case key.Matches(msg, k.ChoiceDown):
		m.scopeChoiceCursor = 1
	case key.Matches(msg, k.Continue):
		if m.scopeChoiceCursor == 1 {
			m.chooseRepository("")
			return nil
		}
		return m.openRepositoryPicker()
	case key.Matches(msg, k.Pages.PrevPage, k.Pages.NextPage):
		pane := m.focused()
		left := pane.table.Cursor()
		page := pane.pages.Page
		pane.pages, _ = pane.pages.Update(msg)
		if pane.pages.Page != page {
			moveCursor(&pane.table, pane.pages.Page*pane.pages.PerPage)
		}
		m.syncPages(m.focus)
		m.leaveRow(m.focus, left)
	default:
		pane := m.focused()
		left := pane.table.Cursor()
		pane.table, _ = pane.table.Update(msg)
		m.syncPages(m.focus)
		m.leaveRow(m.focus, left)
	}
	return nil
}

// leaveRow clears the mark of the row the cursor just left, or drops it when
// it is gone. A gone row above the cursor shifts the cursor up with the rows.
func (m *model) leaveRow(id paneID, row int) {
	pane := &m.panes[id]
	if pane.table.Cursor() == row {
		return
	}
	pr, gone, ok := m.paneRow(id, row)
	if !ok {
		return
	}
	if !gone {
		if m.changes[id].see(pr) {
			m.redrawRows(id)
		}
		return
	}
	m.changes[id].dismiss(pr)
	m.rebuildGone(id)
	target := pane.table.Cursor()
	if row < target {
		target--
	}
	m.redrawRows(id)
	moveCursor(&pane.table, target)
	m.syncPages(id)
}

func (m *model) updateRepositoryPicker(msg tea.KeyPressMsg) tea.Cmd {
	p := m.picker
	k := m.keys.Picker
	if p.naming != nil {
		return m.updateWatchlistName(msg)
	}
	// Any other key cancels a pending delete.
	if p.confirmDelete != "" && !key.Matches(msg, k.Delete) {
		p.confirmDelete, p.diagnostic = "", ""
	}
	switch {
	case key.Matches(msg, k.Cancel):
		if len(p.marked) > 0 {
			p.clearMarks()
			return nil
		}
		m.closeRepositoryPicker()
		return nil
	case key.Matches(msg, k.Mark):
		return m.markCandidate()
	case key.Matches(msg, k.Edit):
		m.editWatchlist()
		return nil
	case key.Matches(msg, k.Delete):
		m.deleteWatchlist()
		return nil
	case key.Matches(msg, k.Apply) && len(p.marked) > 0:
		return m.openWatchlistName()
	case key.Matches(msg, k.Retry):
		return m.startRepositoryList()
	case key.Matches(msg, k.Apply):
		candidate := p.selectedCandidate()
		switch candidate.kind {
		case allRepositoriesCandidate:
			m.closeRepositoryPicker()
			m.chooseRepository("")
		case watchlistCandidate:
			m.closeRepositoryPicker()
			m.chooseScope(preferences.Scope{Watchlist: candidate.watchlist})
		case knownRepositoryCandidate:
			m.closeRepositoryPicker()
			m.chooseRepository(candidate.repository)
		case lookupRepositoryCandidate:
			return m.startRepositoryLookup(candidate.repository, false)
		}
		return nil
	case key.Matches(msg, k.Up):
		p.move(-1, m.pickerViewportHeight())
		return nil
	case key.Matches(msg, k.Down):
		p.move(1, m.pickerViewportHeight())
		return nil
	case key.Matches(msg, k.Clear):
		p.setQuery("")
		p.clamp(m.pickerViewportHeight())
		return nil
	}
	// Picker keys that are disabled right now must not fall through to text input.
	if bound(msg, k.Apply, k.Retry, k.Up, k.Down, k.Clear, k.Mark, k.Edit, k.Delete) {
		return nil
	}
	return p.updateInput(msg, m.pickerViewportHeight())
}

func (m *model) rebuildVisiblePRs() {
	for _, id := range paneIDs {
		pane := &m.panes[id]
		pane.visible = pane.visible[:0]
		source := m.source(id)
		for index, pr := range source {
			if m.shown(id, &pr, m.snapshot.Preview) {
				pane.visible = append(pane.visible, index)
			}
		}
		if id == paneMine {
			m.sortMine(pane.visible, source)
		}
		m.rebuildGone(id)
	}
	m.rebuildPRTable(true)
}

// applySnapshot shows a fetched or previewed snapshot. A new account restores
// its saved scope or prompts for one. Only full snapshots are compared for
// change marks and alerts; a preview leaves the baseline alone. The returned
// command sends the notification for the alerts.
func (m *model) applySnapshot(snapshot github.Snapshot) tea.Cmd {
	accountChanged := !strings.EqualFold(m.filterLogin, snapshot.Login)
	if accountChanged {
		// A failed save of the account choice stays shown for the new account.
		if m.filterLogin != "" && !m.keepPreferenceErr {
			m.preferenceErr = nil
		}
		m.keepPreferenceErr = false
		m.filterLogin = snapshot.Login
		if scope, found := m.preferences.Lookup(snapshot.Login); found {
			m.setScope(snapshot.Login, scope)
			m.scopeChosen = true
		} else {
			m.setScope(snapshot.Login, preferences.Scope{})
			m.scopeChosen = false
			m.scopeChoiceCursor = 0
		}
	}
	// A preview cannot sort ready pull requests first, so a cursor still on
	// its first row stays on the first row once the details arrive.
	var atTop [2]bool
	for _, id := range paneIDs {
		atTop[id] = m.snapshot.Preview && !snapshot.Preview && m.panes[id].table.Cursor() == 0
	}
	defer func() {
		for _, id := range paneIDs {
			if atTop[id] {
				moveCursor(&m.panes[id].table, 0)
				m.syncPages(id)
			}
		}
	}()
	var alerts []prAlert
	m.keepingSelection(func() {
		m.snapshot = snapshot
		m.err = nil
		if !snapshot.Preview {
			reset := !strings.EqualFold(m.changesLogin, snapshot.Login)
			m.changesLogin = snapshot.Login
			// The first fetch of an account is the baseline: it never alerts.
			if reset {
				m.resetReadiness()
			} else {
				alerts = m.alerts()
			}
			for _, id := range paneIDs {
				if reset {
					m.changes[id].reset(m.source(id))
				} else {
					m.changes[id].update(m.source(id))
				}
			}
		}
		m.rebuildVisiblePRs()
	})
	if accountChanged {
		focus := paneMine
		if len(m.panes[paneMine].visible) == 0 && len(m.panes[paneReview].visible) > 0 {
			focus = paneReview
		}
		m.setFocus(focus)
	}
	if len(alerts) == 0 {
		return nil
	}
	return tea.Batch(m.notifyAlerts(alerts), m.startFlash(alertText(alerts)))
}

// keepingSelection runs rebuild, then reselects each pane's pull request if
// it is still in the pane.
func (m *model) keepingSelection(rebuild func()) {
	type selection struct {
		repository string
		number     int
		ok         bool
	}
	var previous [2]selection
	for _, id := range paneIDs {
		if pr, ok := m.paneSelectedPR(id); ok {
			previous[id] = selection{pr.Repository, pr.Number, true}
		}
	}
	rebuild()
	for _, id := range paneIDs {
		if previous[id].ok {
			m.selectPR(id, previous[id].repository, previous[id].number)
		}
	}
}

// sortMine orders authored pull requests ready to merge first, then oldest
// created first. Ties keep the fetched order.
func (m *model) sortMine(visible []int, source []github.PullRequest) {
	slices.SortStableFunc(visible, func(a, b int) int {
		x, y := &source[a], &source[b]
		readyX, readyY := m.ready(x), m.ready(y)
		if readyX != readyY {
			if readyX {
				return -1
			}
			return 1
		}
		return x.CreatedAt.Compare(y.CreatedAt)
	})
}

func (m *model) inScope(pr *github.PullRequest) bool {
	if m.watchlist.Name != "" {
		return slices.ContainsFunc(m.watchlist.Repositories, func(repository string) bool {
			return strings.EqualFold(pr.Repository, repository)
		})
	}
	return m.selectedRepository == "" || strings.EqualFold(pr.Repository, m.selectedRepository)
}

// setScope shows one repository, a watchlist of login's, or All
// repositories. A watchlist that no longer exists shows All.
func (m *model) setScope(login string, scope preferences.Scope) {
	m.selectedRepository = scope.Repository
	m.watchlist = preferences.Watchlist{}
	if scope.Watchlist == "" {
		return
	}
	for _, watchlist := range m.preferences.Watchlists(login) {
		if strings.EqualFold(watchlist.Name, scope.Watchlist) {
			m.watchlist = watchlist
		}
	}
}

// scopeLabel names the scope in the title.
func (m *model) scopeLabel() string {
	switch {
	case m.watchlist.Name != "":
		return m.icons.star + " " + singleLine(m.watchlist.Name)
	case m.selectedRepository != "":
		return singleLine(m.selectedRepository)
	default:
		return "All repositories"
	}
}

// rebuildGone lists a pane's gone pull requests in the current scope.
func (m *model) rebuildGone(id paneID) {
	pane := &m.panes[id]
	pane.gone = pane.gone[:0]
	for index := range m.changes[id].gone {
		if m.shown(id, &m.changes[id].gone[index], false) {
			pane.gone = append(pane.gone, index)
		}
	}
}

// applyScope shows a scope for this session without saving it.
func (m *model) applyScope(scope preferences.Scope) {
	m.setScope(m.snapshot.Login, scope)
	m.rebuildVisiblePRs()
}

func (m *model) chooseRepository(repository string) {
	m.chooseScope(preferences.Scope{Repository: repository})
}

// chooseScope applies a scope and saves it for the account.
func (m *model) chooseScope(scope preferences.Scope) {
	m.applyScope(scope)
	m.scopeChosen = true
	if err := m.preferences.SaveScope(m.snapshot.Login, scope); err != nil {
		m.preferenceErr = fmt.Errorf("Selection not saved: %w", err)
		return
	}
	m.preferenceErr = nil
}

func (m *model) View() tea.View {
	var lines []string
	list := m.showingList()
	switch {
	case m.rulesEditor != nil && m.width >= minimumWidth && m.height >= minimumHeight:
		lines = m.rulesLines()
	case m.detailsShown():
		lines = m.detailsView()
	case list:
		lines = m.listLines()
	case m.width < minimumWidth || m.height < minimumHeight:
		lines = wrapWords("Terminal too small; resize or press Ctrl+C to quit.", m.width)
	case m.accounts != nil:
		lines = m.accountPickerLines()
	case m.loading && !m.loginActive && !m.refreshing():
		lines = []string{m.spinner.View() + " Loading open pull requests..."}
	case m.err != nil:
		lines = m.errorLines()
	case m.picker != nil:
		lines = m.repositoryPickerLines()
	case !m.scopeChosen:
		lines = m.scopeChoiceLines()
	default:
		// The panes do not fit.
		lines = wrapWords("Terminal too small; resize or press Ctrl+C to quit.", m.width)
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], m.width, "…")
	}
	view := tea.NewView(strings.Join(lines, "\n"))
	// Screens fill the terminal height; inline rendering lets the terminal
	// scroll the top line away.
	view.AltScreen = true
	// Other screens leave the mouse to the terminal.
	if list && m.mouse {
		view.MouseMode = tea.MouseModeAllMotion
	}
	view.WindowTitle = m.windowTitle()
	view.ReportFocus = m.setTitle
	return view
}

// helpLines renders the help for a screen. Full help is used only when it is
// toggled on and the screen has a full view.
func (m *model) helpLines(screen func(keyMap) helpKeys) []string {
	m.syncKeys()
	keys := screen(m.keys)
	m.help.SetWidth(m.width)
	if m.help.ShowAll && keys.full != nil && m.fullHelpFits(keys) {
		// help.Model can overflow its width when no ellipsis fits, so pass
		// only the leading columns that fit.
		full := m.help.FullHelpView(keys.full[:1])
		for n := 2; n <= len(keys.full); n++ {
			view := m.help.FullHelpView(keys.full[:n])
			if lipgloss.Width(view) > m.width {
				break
			}
			full = view
		}
		return strings.Split(full, "\n")
	}
	return []string{m.help.ShortHelpView(keys.short)}
}

func (m *model) scopeChoiceLines() []string {
	pick, all := "  Pick a repository", "  Show all my PRs"
	if m.scopeChoiceCursor == 0 {
		pick = "> Pick a repository"
	} else {
		all = "> Show all my PRs"
	}
	return append([]string{
		m.titleLine("prpr — "+m.accountLabel(), ""),
		"What would you like to watch?",
		pick,
		all,
	}, m.helpLines(keyMap.scopeChoiceHelp)...)
}

func (m *model) errorLines() []string {
	message := "GitHub request failed: " + m.err.Error()
	var authErr *github.AuthError
	if errors.As(m.err, &authErr) {
		message = m.err.Error()
	}
	lines := []string{message}
	if m.awaitingToken() {
		lines = append(lines, "Waiting for "+singleLine(m.pinnedAccount)+" to log in again; prpr retries when gh has a new token.")
	}
	if m.switchAccounts {
		lines = append(lines, "Press a to choose another GitHub CLI account.")
	}
	if quota := m.quotaText(0); quota != "" {
		lines = append(lines, quota)
	}
	return append(append(lines, m.helpLines(keyMap.errorHelp)...),
		"Login command: gh auth login --hostname github.com --web")
}

// panesFit reports whether every drawn pane with rows has room for its columns.
func (m *model) panesFit() bool {
	layout := m.layoutPanes()
	for _, id := range paneIDs {
		if layout.single && id != m.focus {
			continue
		}
		if pane := &m.panes[id]; rowCount(pane) > 0 && !pane.fits {
			return false
		}
	}
	return true
}

func (m *model) listLines() []string {
	return m.listLinesWith(keyMap.listHelp)
}

// listLinesWith draws the list with the given screen's help.
func (m *model) listLinesWith(screen func(keyMap) helpKeys) []string {
	title := fmt.Sprintf("prpr — %s — %s", m.accountLabel(), m.scopeLabel())
	if filters := m.filterText(); filters != "" {
		title += " · " + filters
	}
	if m.refreshInterval > 0 {
		title += " · auto " + intervalText(m.refreshInterval)
	}
	if m.notify && m.icons.nerd {
		title += " · " + m.icons.bell
	} else if m.notify {
		title += " · notify"
	}
	lines := []string{m.titleLine(title, m.countdownText())}
	if m.summaryShown() {
		lines = append(lines, m.summaryLine())
	}
	layout := m.layoutPanes()
	for _, id := range paneIDs {
		if layout.single && id != m.focus {
			continue
		}
		lines = append(lines, m.paneTitle(id, layout.single))
		if rowCount(&m.panes[id]) == 0 {
			lines = append(lines, m.emptyPaneLine(id))
			continue
		}
		lines = append(lines, m.tableLines(id)...)
	}
	selected := ""
	if pr, ok := m.selectedPR(); ok {
		selected = singleLine(pr.URL)
		// The breakdown is dropped rather than truncated so the URL stays whole.
		if breakdown := botBreakdown(m.icons, pr.Bots); m.bots && breakdown != "" &&
			lipgloss.Width(selected)+2+lipgloss.Width(breakdown) <= m.width {
			selected += "  " + breakdown
		}
	}
	lines = append(lines, selected)
	fixed, legend := "", ""
	if pane := m.focused(); rowCount(pane) > 0 && pane.pages.TotalPages > 1 {
		fixed = m.pageIndicator()
	}
	if m.preferenceErr != nil {
		fixed = strings.TrimLeft(fixed+"  "+m.preferenceErr.Error(), " ")
	} else if m.notice != "" {
		fixed = strings.TrimLeft(fixed+"  "+m.notice, " ")
	} else if !(layout.single && m.focus != paneMine) && rowCount(&m.panes[paneMine]) > 0 {
		legend = m.icons.legend(m.rules.Customized())
	}
	if m.searching != nil {
		lines = append(lines, m.searching.View())
		return append(lines, m.helpLines(keyMap.searchHelp)...)
	}
	lines = append(lines, m.statusLine(fixed, legend))
	return append(lines, m.helpLines(screen)...)
}

// titleLine puts a refresh indicator in the top-right corner while a fetch
// replaces the rows on screen, and otherwise the countdown when one is given.
// The title is truncated to keep the corner visible.
func (m *model) titleLine(title, countdown string) string {
	indicator := countdown
	if m.refreshing() {
		indicator = m.spinner.View() + " Refreshing"
		if m.snapshot.Preview {
			indicator = m.spinner.View() + " Loading details"
		}
	}
	if indicator == "" {
		return title
	}
	space := m.width - lipgloss.Width(indicator) - 1
	if space < 1 {
		return indicator
	}
	title = ansi.Truncate(title, space, "…")
	return title + strings.Repeat(" ", m.width-lipgloss.Width(title)-lipgloss.Width(indicator)) + indicator
}

func singleLine(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
}

func wrapWords(value string, width int) []string {
	if width < 1 {
		return nil
	}
	var lines []string
	line := ""
	for _, word := range strings.Fields(value) {
		for len(word) > width {
			if line != "" {
				lines = append(lines, line)
				line = ""
			}
			lines = append(lines, word[:width])
			word = word[width:]
		}
		if line == "" {
			line = word
		} else if len(line)+1+len(word) <= width {
			line += " " + word
		} else {
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// intervalText drops the zero units time.Duration.String prints, so 5m0s
// reads as 5m.
func intervalText(d time.Duration) string {
	text := d.String()
	if strings.HasSuffix(text, "m0s") {
		text = strings.TrimSuffix(text, "0s")
	}
	if strings.HasSuffix(text, "h0m") {
		text = strings.TrimSuffix(text, "0m")
	}
	return text
}
