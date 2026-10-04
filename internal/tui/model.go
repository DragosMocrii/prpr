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
	"github.com/DragosMocrii/prpr/internal/schedule"
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
	watchlist preferences.Watchlist
	// owner is the owner scope: every repository of one owner, or "" for
	// none.
	owner       string
	filterLogin string
	panes       [len(paneIDs)]prPane
	changes     [2]paneChanges
	// snoozes are the account's snoozed pull requests.
	snoozes map[prKey]preferences.Snooze
	// snoozeClosed holds snoozed pull requests that closed: their list, for
	// gone rows.
	snoozeClosed map[prKey]paneID
	// woke holds woken pull requests: the reason, until their row is left
	// or marks are cleared.
	woke map[prKey]string
	// snoozeEditor is the snooze form, nil when closed. lastSnooze is the
	// pull request U would unsnooze.
	snoozeEditor *snoozeEditor
	lastSnooze   *prKey
	// rerequest is the form that requests reviews again, nil when closed.
	// listReviewers and requestReviews are the client's in the app.
	rerequest      *rerequestEditor
	listReviewers  func(context.Context, string, int) (github.ReviewerList, error)
	requestReviews func(context.Context, string, int, []string, []string) error
	// snoozeGeneration counts snooze timers; ticks from an older one are
	// dropped.
	snoozeGeneration uint64
	// rest is the row the cursor rests on; a rest that ends restGeneration
	// is dropped once the cursor moved on.
	rest           restState
	restGeneration uint64
	// changesLogin is the account the change baseline belongs to.
	changesLogin string
	focus        paneID
	keys         keyMap
	help         help.Model
	// helpOpen shows the help overlay, scrolled down helpScroll lines.
	helpOpen            bool
	helpScroll          int
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
	quotaFetchedAt  time.Time
	// bots shows the Bots column and the selected PR's bot breakdown.
	bots bool
	// legend is whether the icon legend panel is open; it is saved.
	legend bool
	// showDrafts puts draft pull requests in scope; it is saved.
	showDrafts bool
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
	// settingsBaseline makes the next full fetch replace the change
	// baseline without marking, alerting, or waking snoozes: bots or queues
	// changed, so its differences come from the settings, not GitHub.
	settingsBaseline bool
	// settingsNotice is what Settings says about its last change.
	settingsNotice string
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
	// schedule controls when automatic GitHub work and notifications may run.
	scheduleConfig     schedule.Config
	scheduleWindow     schedule.Window
	scheduleErr        error
	sleeping           bool
	manualWakeUntil    time.Time
	scheduleGeneration uint64
	// scheduleTimerAt is when the pending schedule tick fires; zero for none.
	scheduleTimerAt     time.Time
	scheduleInitialized bool
	wakeFetchPending    bool
	activityCtx         context.Context
	activityCancel      context.CancelFunc
	activityDeadline    time.Time
	activityGeneration  uint64
	fetchCancel         context.CancelFunc
	fetchQuiet          bool
	lastSuccessAt       time.Time
	scheduleEditor      *scheduleEditor
	quotaCancel         context.CancelFunc
	// dismissed holds, per review request, the request time at which the
	// viewer dismissed its request-again marker; it is saved.
	dismissed map[prKey]time.Time
	// pleadOff is the marker's blink phase; pleadGeneration drops ticks of
	// an older chain, and pleadTicking says one runs.
	pleadOff        bool
	pleadGeneration uint64
	pleadTicking    bool
	quotaGeneration uint64
}

type fetchFinishedMsg struct {
	snapshot   github.Snapshot
	err        error
	generation uint64
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

// New returns the app model, set up from the saved settings.
func New(ctx context.Context, client *github.Client, preferences *preferences.Store) tea.Model {
	appCtx, cancel := context.WithCancel(ctx)
	m := newModel(appCtx, client, preferences)
	m.cancel = cancel
	m.refreshInterval = preferences.Refresh()
	m.bots = len(client.Bots()) > 0
	m.openBrowser = client.OpenInBrowser
	m.listReviewers = client.Reviewers
	m.requestReviews = client.RequestReviews
	m.notify = preferences.Notify()
	m.mouse = preferences.Mouse()
	m.icons = iconsNamed(preferences.Icons())
	m.legend = preferences.Legend()
	m.showDrafts = preferences.ShowDrafts()
	m.rules = preferences.Rules()
	client.SetNeeds(m.rules.Needs())
	if err := preferences.RulesErr(); err != nil {
		m.preferenceErr = err
	}
	if err := preferences.SettingsErr(); err != nil && m.preferenceErr == nil {
		m.preferenceErr = err
	}
	if m.scheduleErr != nil && m.preferenceErr == nil {
		m.preferenceErr = fmt.Errorf("Active-hours schedule needs repair: %w", m.scheduleErr)
	}
	m.setTitle = preferences.Title()
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
		scheduleConfig: preferences.Schedule(),
		scheduleErr:    preferences.ScheduleErr(),
	}
	if window, err := schedule.Compile(m.scheduleConfig); err == nil {
		m.scheduleWindow = window
	} else if m.scheduleErr == nil {
		m.scheduleErr = err
	}
	m.panes = [len(paneIDs)]prPane{newPRPane(), newPRPane(), newPRPane(), newPRPane()}
	m.rebuildPRTable(true)
	return m
}

func (m *model) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.RequestBackgroundColor, m.reconcileSchedule()}
	if m.pollingAllowed() {
		cmds = append(cmds, m.startAutomaticFetch(), m.pollQuota())
	}
	return tea.Batch(cmds...)
}

// overlayOpen reports whether the repository picker, the account picker, or
// a form covers the lists.
func (m *model) overlayOpen() bool {
	return m.picker != nil || m.accounts != nil || m.rulesEditor != nil || m.snoozeEditor != nil || m.rerequest != nil || m.scheduleEditor != nil
}

func (m *model) startFetch() tea.Cmd {
	m.closeRepositoryPicker()
	m.cancelFetch()
	m.loading = true
	m.loginActive = false
	m.refreshGeneration++
	m.refreshDue = time.Time{}
	m.err = nil
	generation, client, account := m.refreshGeneration, m.client, m.accountGeneration
	// A quiet fetch while asleep is not bound to the activity deadline.
	quiet := !m.pollingAllowed()
	base, deadline := m.ctx, time.Time{}
	if !quiet {
		base, deadline = m.activityContext(), m.activityDeadline
	}
	ctx, cancel := context.WithCancel(base)
	m.fetchCancel, m.fetchQuiet = cancel, quiet
	live := liveUntil(ctx, deadline, m.now)
	fetch := func() tea.Msg {
		if !live() {
			return fetchFinishedMsg{err: context.Canceled, generation: generation, account: account}
		}
		snapshot, err := client.Fetch(ctx)
		return fetchFinishedMsg{snapshot: snapshot, err: err, generation: generation, account: account}
	}
	cmds := []tea.Cmd{fetch, m.spinner.Tick}
	if m.snapshot.Login == "" {
		cmds = append(cmds, func() tea.Msg {
			if !live() {
				return previewMsg{generation: generation, account: account, err: context.Canceled}
			}
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
	for _, id := range listIDs {
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
	if m.refreshInterval <= 0 || !m.pollingAllowed() || m.fetchQuiet {
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
	if tick, ok := msg.(scheduleTickMsg); ok && tick.generation == m.scheduleGeneration {
		m.scheduleTimerAt = time.Time{}
	}
	scheduleCmd := m.reconcileSchedule()
	model, cmd := m.update(msg)
	m.closeStaleDetails()
	m.closeStaleHelp()
	postScheduleCmd := m.reconcileSchedule()
	return model, tea.Batch(scheduleCmd, cmd, postScheduleCmd, m.trackRest(msg), m.schedulePlead())
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
		// A form redraws its fields only on a message of its own.
		m.sizeForm()
		if cmd, ok := m.updateForm(msg); ok {
			return m, cmd
		}
	case tea.BackgroundColorMsg:
		m.darkBackground = msg.IsDark()
		m.help.Styles = help.DefaultStyles(m.darkBackground)
		m.applyFocusStyles()
		if m.picker != nil {
			m.picker.setDark(m.darkBackground)
		}
		m.setSearchStyle()
		if cmd, ok := m.updateForm(msg); ok {
			return m, cmd
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
	case scheduleTickMsg:
		return m, nil
	case countdownTickMsg:
		if msg.generation != m.countdownGeneration {
			return m, nil
		}
		return m, m.countdownTick()
	case autoRefreshMsg:
		if msg.generation != m.refreshGeneration {
			return m, nil
		}
		if m.fetchDeferred() {
			return m, m.scheduleAutoRefresh()
		}
		return m, m.startAutomaticFetch()
	case fetchFinishedMsg:
		if msg.generation != m.refreshGeneration || msg.account != m.accountGeneration {
			return m, nil
		}
		quiet := m.fetchQuiet
		m.cancelFetch()
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			m.refetchForRules = false
			m.closeRepositoryPicker()
			m.searching = nil
			keepSnapshot := quiet && m.sleeping && !m.snapshot.Preview && !m.lastSuccessAt.IsZero()
			if !keepSnapshot {
				m.snapshot = github.Snapshot{}
				for _, id := range paneIDs {
					m.panes[id].visible = nil
					m.panes[id].gone = nil
				}
				m.rebuildPRTable(true)
			}
			m.fetchQuiet = false
			var authErr *github.AuthError
			if errors.As(msg.err, &authErr) {
				return m, m.scheduleTokenRecheck()
			}
			if quiet {
				return m, nil
			}
			return m, m.scheduleAutoRefresh()
		}
		m.lastSuccessAt = m.now()
		notify := m.applySnapshot(msg.snapshot)
		m.fetchQuiet = false
		m.countUnknownRechecks()
		if quiet {
			return m, notify
		}
		if m.refetchForRules {
			m.refetchForRules = false
			return m, tea.Batch(notify, m.startAutomaticFetch(), m.resumeQuota())
		}
		return m, tea.Batch(notify, m.scheduleAutoRefresh(), m.resumeQuota())
	case previewMsg:
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
	case reviewersListedMsg:
		return m, m.handleReviewersListed(msg)
	case reviewersRecheckedMsg:
		return m, m.handleReviewersRechecked(msg)
	case reviewsRequestedMsg:
		m.handleReviewsRequested(msg)
	case accountsListedMsg:
		m.handleAccountsListed(msg)
	case tokenRecheckMsg:
		return m, m.handleTokenRecheck(msg)
	case tokenCheckedMsg:
		return m, m.handleTokenChecked(msg)
	case desktopNotifiedMsg:
		m.handleDesktopNotified(msg)
	case restMsg:
		m.handleRest(msg)
	case snoozeTickMsg:
		return m, m.handleSnoozeTick(msg)
	case pleadTickMsg:
		return m, m.handlePleadTick(msg)
	case flashTickMsg:
		return m, m.handleFlashTick(msg)
	case tea.FocusMsg:
		m.handleFocus(focusIn)
	case tea.BlurMsg:
		m.handleFocus(focusOut)
	case tea.PasteMsg:
		if cmd, ok := m.updateForm(msg); ok {
			return m, cmd
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
		// A form steps itself with messages of its own.
		if cmd, ok := m.updateForm(msg); ok {
			return m, cmd
		}
	}
	return m, nil
}

// updateForm passes msg to the open form, if any. At most one is open:
// forms take every key, and modalOpen keeps R from opening over another.
func (m *model) updateForm(msg tea.Msg) (tea.Cmd, bool) {
	switch {
	case m.rulesEditor != nil:
		return m.updateRules(msg), true
	case m.snoozeEditor != nil:
		return m.updateSnooze(msg), true
	case m.rerequest != nil:
		return m.updateRerequest(msg), true
	case m.scheduleEditor != nil:
		return m.updateSchedule(msg), true
	}
	return nil, false
}

// sizeForm fits the open form, if any, to the terminal.
func (m *model) sizeForm() {
	switch {
	case m.rulesEditor != nil:
		m.sizeRulesForm()
	case m.snoozeEditor != nil:
		m.sizeSnoozeForm()
	case m.scheduleEditor != nil:
		m.sizeScheduleForm()
	}
}

func (m *model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	m.syncKeys()
	k := m.keys
	if key.Matches(msg, k.ForceQuit) {
		m.closeRepositoryPicker()
		m.cancel()
		return tea.Quit
	}
	if cmd, ok := m.updateForm(msg); ok {
		return cmd
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
	if m.helpShown() {
		return m.updateHelp(msg)
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
		step := 1
		if key.Matches(msg, k.PrevPane) {
			step = -1
		}
		m.setFocus(m.nextPane(step))
	case key.Matches(msg, k.ClearMarks):
		m.keepingSelection(func() {
			for _, id := range listIDs {
				m.changes[id].clear()
			}
			m.woke = nil
			m.snoozeClosed = nil
			m.rebuildVisiblePRs()
		})
	case key.Matches(msg, k.Details):
		m.details = true
	case key.Matches(msg, k.Back):
		m.details = false
	case key.Matches(msg, k.Search):
		return m.openSearch()
	case key.Matches(msg, k.Drafts):
		m.toggleDrafts()
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
	case key.Matches(msg, k.Legend):
		m.toggleLegend()
	case key.Matches(msg, k.Rules):
		return m.openRules()
	case key.Matches(msg, k.Schedule):
		return m.openSchedule()
	case key.Matches(msg, k.Wake):
		return m.wakeForHour()
	case key.Matches(msg, k.Snooze):
		if m.focus == paneSnoozed {
			return m.wakeNow()
		}
		return m.openSnooze()
	case key.Matches(msg, k.Undo):
		return m.undoSnooze()
	case key.Matches(msg, k.DismissPlead):
		m.dismissPlead()
	case key.Matches(msg, k.Rerequest):
		return m.rerequestSelected()
	case key.Matches(msg, k.Help):
		m.helpOpen, m.helpScroll = true, 0
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
	if !m.wasRead(id, pr, gone) {
		return
	}
	key := keyOf(pr)
	if !gone {
		seen := m.trackerFor(id, pr).see(pr)
		if _, ok := m.woke[key]; ok {
			delete(m.woke, key)
			seen = true
		}
		if seen {
			m.redrawRows(id)
		}
		return
	}
	m.trackerFor(id, pr).dismiss(pr)
	if id == paneSnoozed {
		delete(m.snoozeClosed, key)
	}
	// Panes share trackers, and the Snoozed pane's gone indexes run over
	// both, so every pane's gone rows are rebuilt.
	for _, p := range paneIDs {
		m.rebuildGone(p)
	}
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
		case ownerCandidate:
			m.closeRepositoryPicker()
			m.chooseScope(preferences.Scope{Owner: candidate.owner})
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
		if id == paneSnoozed {
			m.rebuildSnoozed()
			continue
		}
		source := m.source(id)
		for index, pr := range source {
			if m.shown(id, &pr, m.snapshot.Preview) {
				pane.visible = append(pane.visible, index)
			}
		}
		if id == paneMine {
			m.sortMine(pane.visible, source)
		}
		if id == paneQueue {
			slices.SortStableFunc(pane.visible, func(a, b int) int {
				return queueRank(source[a].Queue.State) - queueRank(source[b].Queue.State)
			})
		}
		m.rebuildGone(id)
	}
	// An emptied queue pane is no longer drawn, so it cannot keep the focus.
	if drawn := m.drawnPanes(); !slices.Contains(drawn, m.focus) {
		m.focus = paneMine
		for _, id := range drawn {
			if len(m.panes[id].visible) > 0 {
				m.focus = id
				break
			}
		}
		m.applyFocusStyles()
	}
	m.rebuildPRTable(true)
}

// rebuildSnoozed lists the Snoozed pane's rows from both lists, indexes
// running over the authored list, then the review list: soonest wake first,
// activity snoozes last, ties in that order.
func (m *model) rebuildSnoozed() {
	pane := &m.panes[paneSnoozed]
	offset := 0
	for _, list := range listIDs {
		source := m.source(list)
		for index := range source {
			if m.shown(paneSnoozed, &source[index], m.snapshot.Preview) {
				pane.visible = append(pane.visible, offset+index)
			}
		}
		offset += len(source)
	}
	slices.SortStableFunc(pane.visible, func(a, b int) int {
		x, y := m.snoozeAt(a), m.snoozeAt(b)
		if x.Activity != y.Activity {
			if x.Activity {
				return 1
			}
			return -1
		}
		return x.Until.Compare(y.Until)
	})
	m.rebuildGone(paneSnoozed)
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
	var atTop [len(paneIDs)]bool
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
	quiet := m.fetchQuiet
	m.keepingSelection(func() {
		m.snapshot = snapshot
		m.err = nil
		if accountChanged {
			m.loadSnoozes(snapshot.Login)
			m.loadDismissals(snapshot.Login)
		}
		if !snapshot.Preview {
			m.pruneDismissals()
			reset := !strings.EqualFold(m.changesLogin, snapshot.Login) || m.settingsBaseline
			m.settingsBaseline = false
			m.changesLogin = snapshot.Login
			if reset {
				m.resetReadiness()
			} else {
				alerts = m.alerts()
			}
			if !quiet {
				alerts = append(alerts, m.reviewSnoozes(!reset)...)
			}
			for _, id := range listIDs {
				if reset {
					m.changes[id].reset(m.source(id))
				} else {
					m.changes[id].update(m.source(id))
				}
			}
			m.rechanged()
		}
		m.rebuildVisiblePRs()
	})
	if accountChanged {
		focus := paneMine
		for _, id := range m.drawnPanes() {
			if len(m.panes[id].visible) > 0 {
				focus = id
				break
			}
		}
		m.setFocus(focus)
	}
	var tick tea.Cmd
	if !snapshot.Preview && !quiet && !m.sleeping {
		tick = m.scheduleSnoozeTick()
	}
	if len(alerts) == 0 {
		return tick
	}
	return tea.Batch(m.notifyAlerts(alerts), m.startFlash(alertText(alerts)), tick)
}

// keepingSelection runs rebuild, keeping each pane's selection and following
// the focused pull request into another pane; see keepSelection.
func (m *model) keepingSelection(rebuild func()) {
	m.keepSelection(rebuild, true)
}

// keepSelection runs rebuild, then reselects each pane's pull request if it
// is still in the pane. With follow, when the focused pull request moved to
// another pane (between My PRs and the queue pane, or in or out of
// Snoozed), focus follows it, so the details screen, o, and y keep acting
// on it; panes partition each list, so it is in at most one. A preview's
// selection is not followed: a preview has no queue entries, and its first
// row stays first.
func (m *model) keepSelection(rebuild func(), follow bool) {
	type selection struct {
		repository string
		number     int
		ok         bool
	}
	var previous [len(paneIDs)]selection
	for _, id := range paneIDs {
		if pr, ok := m.paneSelectedPR(id); ok {
			previous[id] = selection{pr.Repository, pr.Number, true}
		}
	}
	focus := m.focus
	follow = follow && !m.snapshot.Preview
	rebuild()
	for _, id := range paneIDs {
		if previous[id].ok {
			m.selectPR(id, previous[id].repository, previous[id].number)
		}
	}
	selected := previous[focus]
	if !follow || !selected.ok || m.selectPR(focus, selected.repository, selected.number) {
		return
	}
	for _, to := range m.drawnPanes() {
		if to != focus && m.selectPR(to, selected.repository, selected.number) {
			m.setFocus(to)
			m.selectPR(to, selected.repository, selected.number)
			return
		}
	}
}

// sortMine orders authored pull requests ready to merge first, then oldest
// created first. Ties keep the fetched order.
func (m *model) sortMine(visible []int, source []github.PullRequest) {
	// Rules are evaluated once per row, not once per comparison.
	ready := make(map[int]bool, len(visible))
	for _, index := range visible {
		ready[index] = m.ready(&source[index])
	}
	slices.SortStableFunc(visible, func(a, b int) int {
		x, y := &source[a], &source[b]
		readyX, readyY := ready[a], ready[b]
		if readyX != readyY {
			if readyX {
				return -1
			}
			return 1
		}
		return x.CreatedAt.Compare(y.CreatedAt)
	})
}

// inScope reports whether a pull request is in the scope: its repository,
// and drafts only while they are shown. Counts, the title, and notifications
// follow it.
func (m *model) inScope(pr *github.PullRequest) bool {
	return (m.showDrafts || !pr.Draft) && m.inRepositoryScope(pr)
}

func (m *model) inRepositoryScope(pr *github.PullRequest) bool {
	if m.watchlist.Name != "" {
		return slices.ContainsFunc(m.watchlist.Repositories, func(repository string) bool {
			return strings.EqualFold(pr.Repository, repository)
		})
	}
	if m.owner != "" {
		owner, _, _ := strings.Cut(pr.Repository, "/")
		return strings.EqualFold(owner, m.owner)
	}
	return m.selectedRepository == "" || strings.EqualFold(pr.Repository, m.selectedRepository)
}

// setScope shows one repository, a watchlist of login's, every repository
// of an owner, or All repositories. A watchlist that no longer exists shows
// All.
func (m *model) setScope(login string, scope preferences.Scope) {
	m.selectedRepository = scope.Repository
	m.owner = scope.Owner
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

// ownerLabel names an owner scope: every repository of owner.
func ownerLabel(owner string) string { return singleLine(owner) + "/*" }

// scopeLabel names the scope in the title.
func (m *model) scopeLabel() string {
	switch {
	case m.watchlist.Name != "":
		return m.icons.star + " " + singleLine(m.watchlist.Name)
	case m.selectedRepository != "":
		return singleLine(m.selectedRepository)
	case m.owner != "":
		return ownerLabel(m.owner)
	default:
		return "All repositories"
	}
}

// rebuildGone lists a pane's gone pull requests in the current scope. The
// Snoozed pane's indexes run over the authored list's gone pull requests,
// then the review list's.
func (m *model) rebuildGone(id paneID) {
	pane := &m.panes[id]
	pane.gone = pane.gone[:0]
	if id == paneSnoozed {
		offset := 0
		for _, list := range listIDs {
			gone := m.changes[list].gone
			for index := range gone {
				if m.shown(paneSnoozed, &gone[index], false) {
					pane.gone = append(pane.gone, offset+index)
				}
			}
			offset += len(gone)
		}
		return
	}
	t := m.tracker(id)
	for index := range t.gone {
		if m.shown(id, &t.gone[index], false) {
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
	case m.scheduleEditor != nil && m.width >= minimumWidth && m.height >= minimumHeight:
		lines = m.scheduleLines()
	case m.snoozeEditor != nil && m.width >= minimumWidth && m.height >= minimumHeight:
		lines = m.snoozeLines()
	case m.rerequest != nil && m.width >= minimumWidth && m.height >= minimumHeight:
		lines = m.rerequestLines()
	case m.detailsShown():
		lines = m.detailsView()
	case m.helpShown():
		lines = m.helpView()
	case list:
		lines = m.listLines()
	case m.width < minimumWidth || m.height < minimumHeight:
		lines = wrapWords("Terminal too small; resize or press Ctrl+C to quit.", m.width)
	case m.sleeping && m.snapshot.Login == "" && m.err == nil:
		lines = m.sleepingLines()
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
	if list && m.mouse && !m.helpShown() {
		view.MouseMode = tea.MouseModeAllMotion
	}
	view.WindowTitle = m.windowTitle()
	view.ReportFocus = m.setTitle
	return view
}

// helpLines renders the help for a screen. Full help is used only when it is
// toggled on and the screen has a full view.
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
	if m.preferenceErr != nil {
		lines = append(lines, singleLine(m.preferenceErr.Error()))
	}
	if m.scheduleErr != nil && m.preferenceErr == nil {
		lines = append(lines, "Active-hours schedule needs repair.")
	}
	if status := m.scheduleStatus(); status != "" {
		lines = append(lines, status)
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
	for _, id := range m.drawnPanes() {
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
	if m.notify && m.icons.nerd {
		title += " · " + m.icons.bell
	} else if m.notify {
		title += " · notify"
	}
	// The legend is built once per frame; it takes the lines the layout leaves.
	legendLines := m.legendLines()
	if m.legendHidden(legendLines) {
		title += " · legend needs a taller terminal"
	}
	lines := []string{m.titleLine(title, m.countdownText())}
	if m.summaryShown() {
		lines = append(lines, m.summaryLine())
	}
	layout := m.layoutPanes()
	for _, id := range m.drawnPanes() {
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
	lines = append(lines, legendLines...)
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
	fixed := ""
	var status []string
	if pane := m.focused(); rowCount(pane) > 0 && pane.pages.TotalPages > 1 {
		fixed = m.pageIndicator()
	}
	if m.preferenceErr != nil {
		fixed = strings.TrimLeft(fixed+"  "+m.preferenceErr.Error(), " ")
	} else if m.notice != "" {
		fixed = strings.TrimLeft(fixed+"  "+m.notice, " ")
	} else if summary := m.changeStatus(m.changeStatusWidth(fixed)); summary != "" {
		fixed = strings.TrimLeft(fixed+"  "+summary, " ")
	} else {
		status = m.rowStatus()
	}
	if status := m.scheduleStatus(); status != "" {
		fixed = strings.TrimLeft(fixed+"  "+status, " ")
	}
	if m.sleeping && m.err != nil {
		fixed = strings.TrimLeft(fixed+"  Refresh failed: "+singleLine(m.err.Error()), " ")
	}
	if m.searching != nil {
		lines = append(lines, m.searching.View())
		return append(lines, m.helpLines(keyMap.searchHelp)...)
	}
	lines = append(lines, m.statusLine(fixed, status))
	return append(lines, m.helpLines(screen)...)
}

// titleLine puts a refresh indicator in the top-right corner while a fetch
// replaces the rows on screen, otherwise the countdown or activity status.
// The title is truncated to keep the corner visible.
func (m *model) titleLine(title, countdown string) string {
	indicator := countdown
	if status := m.scheduleStatus(); status != "" {
		indicator = status
		if m.sleeping && countdown != "" {
			indicator += " · " + countdown
		}
	}
	if m.refreshing() && !m.sleeping {
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
