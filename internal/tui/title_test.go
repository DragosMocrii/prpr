package tui

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/DragosMocrii/prpr/internal/github"
)

func titleModel(t *testing.T) *model {
	t.Helper()
	m := notifyModel(t, "")
	m.notify = false
	m.setTitle = true
	return m
}

// flashTicks returns the flash ticks cmd schedules.
func flashTicks(cmd tea.Cmd) []flashTickMsg {
	var ticks []flashTickMsg
	// Tick commands block for their interval; flash ticks are the only ones
	// these tests expect from an alert, so each is run.
	_, msgs := run(cmd)
	for _, msg := range msgs {
		if tick, ok := msg.(flashTickMsg); ok {
			ticks = append(ticks, tick)
		}
	}
	return ticks
}

func TestTitleShowsFetchesErrorsAndWhatNeedsYou(t *testing.T) {
	m := titleModel(t)
	m.loading = true
	if got := m.View().WindowTitle; !strings.Contains(got, "prpr · loading") {
		t.Fatalf("first fetch title = %q", got)
	}
	ready, blocked := changePR(1, "acme/a"), changePR(2, "acme/a")
	ready.MergeState = "CLEAN"
	fetch(m, []github.PullRequest{ready, blocked}, []github.PullRequest{reviewedPR(9, github.ReviewRequested), reviewedPR(8, github.ReviewWaitingOnAuthor)})
	if got := m.View().WindowTitle; got != "prpr · 2 need you" {
		t.Fatalf("idle title = %q, want the ready PR and the review request", got)
	}
	// Search does not change the count.
	m.search = "nothing matches this"
	m.applyFilters()
	if got := m.View().WindowTitle; got != "prpr · 2 need you" {
		t.Fatalf("title while searching = %q", got)
	}
	m.loading = true
	if got := m.View().WindowTitle; !strings.Contains(got, "prpr · refreshing") {
		t.Fatalf("refresh title = %q", got)
	}
	m.Update(fetchFinishedMsg{err: &github.AuthError{Err: errors.New("bad credentials")}})
	if got := m.View().WindowTitle; got != "prpr · sign-in needed" {
		t.Fatalf("auth error title = %q", got)
	}
	m.loading = true
	m.Update(fetchFinishedMsg{err: errors.New("boom")})
	if got := m.View().WindowTitle; got != "prpr · error" {
		t.Fatalf("error title = %q", got)
	}
	if !m.View().ReportFocus {
		t.Fatal("titles on did not ask for focus reports")
	}

	m.setTitle = false
	if view := m.View(); view.WindowTitle != "" || view.ReportFocus {
		t.Fatalf("titles off: title %q, focus reports %t", view.WindowTitle, view.ReportFocus)
	}
}

func TestAlertsFlashTheTitleUntilFocusOrAKey(t *testing.T) {
	m := titleModel(t)
	if _, cmd := m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice"}}); len(flashTicks(cmd)) != 0 || m.flashText != "" {
		t.Fatal("the first fetch flashed")
	}
	_, cmd := m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: []github.PullRequest{reviewedPR(9, github.ReviewRequested)}}})
	ticks := flashTicks(cmd)
	if len(ticks) != 1 || !strings.HasPrefix(m.View().WindowTitle, "● prpr: acme/b#9 review requested") {
		t.Fatalf("alert with notify off: ticks %v, title %q", ticks, m.View().WindowTitle)
	}
	_, next := m.Update(ticks[0])
	if got := m.View().WindowTitle; !strings.HasPrefix(got, "○ prpr:") || next == nil {
		t.Fatalf("after a tick: title %q, next %v", got, next)
	}
	m.Update(tea.FocusMsg{})
	if m.flashText != "" {
		t.Fatal("focus did not stop the flash")
	}
	if _, cmd := m.Update(ticks[0]); cmd != nil {
		t.Fatal("a tick from the stopped flash continued")
	}

	// Focused terminals do not flash; a blur makes them flash again.
	alert := func(number int) {
		m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: []github.PullRequest{reviewedPR(number, github.ReviewRequested)}}})
	}
	alert(7)
	if m.flashText != "" {
		t.Fatal("a focused terminal flashed")
	}
	m.Update(tea.BlurMsg{})
	alert(6)
	if m.flashText == "" {
		t.Fatal("a blurred terminal did not flash")
	}
	press(m, tea.Key{Code: tea.KeyDown})
	if m.flashText != "" {
		t.Fatal("a key did not stop the flash")
	}
}

func TestFlashEndsAfterItsDurationAndANewOneReplacesIt(t *testing.T) {
	m := titleModel(t)
	now := changeTime
	m.now = func() time.Time { return now }
	m.startFlash("one")
	m.startFlash("two")
	if _, cmd := m.Update(flashTickMsg{generation: m.flashGeneration - 1}); cmd != nil || m.flashText != "two" {
		t.Fatal("the replaced flash's tick ran")
	}
	now = now.Add(flashDuration)
	if _, cmd := m.Update(flashTickMsg{generation: m.flashGeneration}); cmd != nil || m.flashText != "" {
		t.Fatal("the flash outlived its duration")
	}
	if got := m.View().WindowTitle; strings.Contains(got, "two") {
		t.Fatalf("title after the flash = %q", got)
	}
}

func TestFlashingTitleCarriesNoControlCharacters(t *testing.T) {
	m := titleModel(t)
	fetch(m, nil, nil)
	evil := reviewedPR(9, github.ReviewRequested)
	evil.Title = "x\x07\x1b]0;evil\x07\u009c\u009d y"
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", ReviewRequests: []github.PullRequest{evil}}})
	title := m.View().WindowTitle
	if !strings.Contains(title, "acme/b#9") || strings.ContainsFunc(title, unicode.IsControl) {
		t.Fatalf("flashing title = %q", title)
	}
}
