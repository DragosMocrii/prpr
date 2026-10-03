package tui

import (
	"errors"
	"strconv"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

// flashInterval is how often a flashing title alternates its marker.
const flashInterval = 500 * time.Millisecond

// flashDuration ends a flash that nothing else stopped.
const flashDuration = 5 * time.Minute

// terminalFocus is what the terminal last reported about its focus. Many
// terminals never report it, so it starts unknown.
type terminalFocus int

const (
	focusUnknown terminalFocus = iota
	focusIn
	focusOut
)

type flashTickMsg struct{ generation uint64 }

// windowTitle is the terminal title: a flashing alert, a fetch in progress,
// an error, or how many pull requests need the viewer. It is empty when
// titles are off.
func (m *model) windowTitle() string {
	if !m.setTitle {
		return ""
	}
	var title string
	var authErr *github.AuthError
	switch {
	case m.flashText != "":
		marker := "○"
		if m.flashOn {
			marker = "●"
		}
		title = marker + " prpr: " + m.flashText
	case m.sleeping:
		title = "prpr · sleeping"
		if !m.lastSuccessAt.IsZero() {
			title += " · updated " + m.lastSuccessAt.In(time.Local).Format("15:04")
		}
		if m.err != nil && m.keptWhileAsleep() {
			title += " · stale"
		}
	case m.loading && !m.loginActive && m.refreshing() && !m.snapshot.Preview:
		title = m.titleSpinner() + " prpr · refreshing"
	case m.loading && !m.loginActive:
		title = m.titleSpinner() + " prpr · loading"
	case errors.As(m.err, &authErr):
		title = "prpr · sign-in needed"
	case m.err != nil:
		title = "prpr · error"
	case m.needYouCount() > 0:
		title = "prpr · " + strconv.Itoa(m.needYouCount()) + " need you"
	default:
		title = "prpr"
	}
	return singleLine(ansi.Strip(title))
}

// titleSpinnerInterval paces the title's spinner below the list's, since
// terminals redraw their tabs and window titles slowly.
const titleSpinnerInterval = 250 * time.Millisecond

// titleSpinner is the spinner frame for the current time. The title changes
// only when the frame does, though the list's spinner ticks faster.
func (m *model) titleSpinner() string {
	frames := spinner.MiniDot.Frames
	return frames[int(m.now().UnixMilli()/titleSpinnerInterval.Milliseconds())%len(frames)]
}

// needYouCategories are the attention categories that ask the viewer to act:
// ready to merge, changes requested, failing CI, and awaiting review.
var needYouCategories = []int{1, 2, 3, 6}

// needYouCount counts the distinct pull requests in the repository scope in
// any of needYouCategories; search and filters do not change it.
func (m *model) needYouCount() int {
	count := 0
	for _, id := range paneIDs {
		source := m.source(id)
		for i := range source {
			pr := &source[i]
			if !m.inScope(pr) {
				continue
			}
			for _, number := range needYouCategories {
				if m.categoryMatches(number, id, pr, m.snapshot.Preview) {
					count++
					break
				}
			}
		}
	}
	return count
}

// startFlash flashes the title with an alert's text, unless titles are off
// or the terminal reports that it has focus. A new flash replaces the one
// shown.
func (m *model) startFlash(text string) tea.Cmd {
	if !m.setTitle || m.terminalFocus == focusIn || !m.notificationsAllowed() {
		return nil
	}
	m.flashGeneration++
	m.flashText, m.flashOn, m.flashUntil = text, true, m.now().Add(flashDuration)
	return m.flashTick()
}

func (m *model) flashTick() tea.Cmd {
	generation := m.flashGeneration
	return tea.Tick(flashInterval, func(time.Time) tea.Msg { return flashTickMsg{generation} })
}

func (m *model) handleFlashTick(msg flashTickMsg) tea.Cmd {
	if msg.generation != m.flashGeneration || m.flashText == "" {
		return nil
	}
	if !m.now().Before(m.flashUntil) {
		m.stopFlash()
		return nil
	}
	m.flashOn = !m.flashOn
	return m.flashTick()
}

// stopFlash ends a flash; its ticks are dropped.
func (m *model) stopFlash() {
	if m.flashText != "" {
		m.flashText = ""
		m.flashGeneration++
	}
}

func (m *model) handleFocus(focus terminalFocus) {
	m.terminalFocus = focus
	if focus == focusIn {
		m.stopFlash()
	}
}
