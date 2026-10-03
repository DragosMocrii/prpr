package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/preferences"
)

// letter is a key press of one printable character. (A package-level
// "key" would clash with the bubbles key import.)
func letter(text string) tea.KeyPressMsg {
	r := []rune(text)[0]
	return tea.KeyPressMsg{Code: r, Text: text}
}

// pressSnooze opens the form on the focused row and picks the option at
// index (0 activity, 1 hour, 2 tomorrow, 3 Monday, 4 custom).
func pressSnooze(t *testing.T, m *model, index int) {
	t.Helper()
	pressMsg(m, letter("z"))
	if m.snoozeEditor == nil {
		t.Fatal("z did not open the snooze form")
	}
	for range index {
		pressMsg(m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEnter})
}

func TestSnoozeMovesTheRowAndSaves(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	m.now = func() time.Time { return snoozeNow }
	m.setFocus(paneMine)
	m.selectPR(paneMine, "acme/api", 1)
	pressSnooze(t, m, 2)
	if m.snoozeEditor != nil {
		t.Fatal("form still open after choosing")
	}
	if got := paneNumbers(m, paneSnoozed); len(got) != 1 || got[0] != 1 {
		t.Fatalf("Snoozed = %v, want [1]", got)
	}
	if m.focus != paneMine {
		t.Fatalf("focus = %v; snoozing keeps focus in its pane", m.focus)
	}
	if pr, ok := m.selectedPR(); !ok || pr.Number != 2 {
		t.Fatalf("cursor on %v, want the next row, #2", pr)
	}
	saved := m.preferences.Snoozes("alice")
	if len(saved) != 1 || !saved[0].Until.Equal(time.Date(2026, 10, 4, 9, 0, 0, 0, snoozeZone)) || saved[0].List != preferences.SnoozeMine {
		t.Fatalf("saved = %+v", saved)
	}
	if !strings.Contains(m.notice, "U undo") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestCustomSnoozeValidatesTypedTimes(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	m.now = func() time.Time { return snoozeNow }
	m.setFocus(paneMine)
	pressSnooze(t, m, 4)
	for _, r := range "soon" {
		pressMsg(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.snoozeEditor == nil || len(m.snoozes) != 0 {
		t.Fatal("an unreadable time snoozed or closed the form")
	}
	for range 4 {
		pressMsg(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	for _, r := range "3h" {
		pressMsg(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.snoozeEditor != nil || len(m.snoozes) != 1 {
		t.Fatal("a valid typed time did not snooze")
	}
}

func TestEscCancelsTheSnoozeForm(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	pressMsg(m, letter("z"))
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.snoozeEditor != nil || len(m.snoozes) != 0 {
		t.Fatal("esc snoozed or kept the form")
	}
}

func TestPreviewAndGoneRowsCannotBeSnoozed(t *testing.T) {
	mine, review := snoozePRs()
	store := testPreferences(t)
	if err := store.Save("alice", ""); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 140, 40)
	m.loading = true
	m.Update(previewMsg{snapshot: github.Snapshot{Login: "alice", Preview: true, PullRequests: mine, ReviewRequests: review}})
	pressMsg(m, letter("z"))
	if m.snoozeEditor != nil {
		t.Fatal("a preview row opened the snooze form")
	}
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine, ReviewRequests: review}})
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: mine[1:], ReviewRequests: review}})
	m.setFocus(paneMine)
	m.selectPR(paneMine, "acme/api", 1) // gone row
	pressMsg(m, letter("z"))
	if m.snoozeEditor != nil {
		t.Fatal("a gone row opened the snooze form")
	}
}

func TestUndoRestoresTheSnoozedPullRequest(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	m.now = func() time.Time { return snoozeNow }
	m.setFocus(paneReview)
	pressSnooze(t, m, 1)
	if len(m.snoozes) != 1 {
		t.Fatal("not snoozed")
	}
	pressMsg(m, letter("U"))
	if len(m.snoozes) != 0 || len(m.preferences.Snoozes("alice")) != 0 {
		t.Fatal("undo kept the snooze")
	}
	if pr, ok := m.selectedPR(); !ok || m.focus != paneReview || pr.Number != 7 {
		t.Fatalf("after undo: focus %v on %v", m.focus, pr)
	}
	pressMsg(m, letter("U"))
	if !strings.Contains(m.notice, "Nothing to undo") {
		t.Fatalf("second undo notice = %q", m.notice)
	}
}

func TestZInTheSnoozedPaneWakesWithoutAlerting(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	m.notify = true
	m.setTitle = true
	snoozeFor(m, preferences.Snooze{Repository: "acme/api", Number: 2, List: preferences.SnoozeMine,
		Until: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)})
	m.setFocus(paneSnoozed)
	pressMsg(m, letter("z"))
	if len(m.snoozes) != 0 {
		t.Fatal("z did not wake")
	}
	if m.flashText != "" || m.notice != "Woke acme/api#2" {
		t.Fatalf("flash %q, notice %q", m.flashText, m.notice)
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "woke: woken") {
		t.Fatal("woken row has no tag")
	}
}

// blockPreferences makes the model's preference saves fail by swapping in a
// store whose directory is read-only.
func blockPreferences(t *testing.T, m *model) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root can write to a read-only directory")
	}
	dir := t.TempDir()
	store, err := preferences.Open(filepath.Join(dir, "preferences.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	m.preferences = store
}

func TestSnoozeSaveFailureKeepsTheSnooze(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	m.now = func() time.Time { return snoozeNow }
	blockPreferences(t, m)
	m.setFocus(paneMine)
	pressSnooze(t, m, 1)
	if len(m.snoozes) != 1 || m.preferenceErr == nil {
		t.Fatalf("snoozes %d, preferenceErr %v", len(m.snoozes), m.preferenceErr)
	}
}

// pressMsg sends a key and runs the commands the form answers with, which is
// how huh steps between fields.
func pressMsg(m *model, msg tea.KeyPressMsg) {
	_, cmd := m.Update(msg)
	drive(m, cmd)
}

func TestHelpListsUndoFromTheStart(t *testing.T) {
	mine, review := snoozePRs()
	m := newPaneModel(t, 140, 40, mine, review)
	m.syncKeys()
	if !m.keys.Undo.Enabled() || !m.keys.Snooze.Enabled() {
		t.Fatal("snooze and undo are not enabled before anything is snoozed")
	}
	m.help.ShowAll = true
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "undo snooze") {
		t.Fatal("full help does not list U")
	}
}
