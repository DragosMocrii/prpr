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

func settingsView(m *model) string { return ansi.Strip(strings.Join(m.settingsLines(), "\n")) }

// moveTo puts the Settings cursor on the row labeled label.
func moveTo(t *testing.T, m *model, label string) {
	t.Helper()
	for range 20 {
		if m.settings.rows[m.settings.cursor].label == label {
			return
		}
		down := tea.KeyDown
		for i, row := range m.settings.rows {
			if row.label == label && i < m.settings.cursor {
				down = tea.KeyUp
			}
		}
		press(m, tea.Key{Code: down})
	}
	t.Fatalf("no row %q", label)
}

func openSettingsModel(t *testing.T) *model {
	t.Helper()
	m := newPaneModel(t, 120, 30, []github.PullRequest{botPR(1)}, nil)
	// Test models start without the saved settings New applies.
	m.fullRefresh = m.preferences.FullRefresh()
	m.refreshInterval, m.setTitle, m.notify, m.mouse = m.preferences.Refresh(), m.preferences.Title(), m.preferences.Notify(), m.preferences.Mouse()
	pressMsg(m, letter(","))
	if m.settings == nil {
		t.Fatal(", did not open Settings")
	}
	return m
}

func TestSettingsListsEverySettingWithItsValue(t *testing.T) {
	m := openSettingsModel(t)
	view := settingsView(m)
	for _, want := range []string{"Icons", "Unicode", "Show drafts", "Legend", "Mouse", "Terminal title",
		"Refresh every", "Full refresh every", "5m", "Desktop notifications", "Review bots", "Copilot, Codex, Claude",
		"Merge queues", "Trunk, GitHub", "Ready-to-merge rules", "Active hours"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Settings lacks %q:\n%s", want, view)
		}
	}
	if m.View().MouseMode != tea.MouseModeNone {
		t.Fatal("Settings requested the mouse")
	}
}

func TestSettingsTogglesAndCyclesAndSaves(t *testing.T) {
	m := openSettingsModel(t)
	for _, label := range []string{"Icons", "Show drafts", "Legend", "Mouse", "Terminal title", "Desktop notifications"} {
		moveTo(t, m, label)
		press(m, tea.Key{Code: tea.KeySpace, Text: " "})
	}
	moveTo(t, m, "Refresh every")
	press(m, tea.Key{Code: tea.KeyEnter})
	store := m.preferences
	if store.Icons() != IconsNerd || !store.ShowDrafts() || !store.Legend() || !store.Mouse() || store.Title() || !store.Notify() ||
		store.Refresh() != 10*time.Minute {
		t.Fatalf("saved icons %q drafts %v legend %v mouse %v title %v notify %v refresh %v", store.Icons(), store.ShowDrafts(),
			store.Legend(), store.Mouse(), store.Title(), store.Notify(), store.Refresh())
	}
	if !strings.Contains(settingsView(m), "Saved.") {
		t.Fatal("no Saved. notice")
	}
	press(m, tea.Key{Code: tea.KeyEsc})
	if m.settings != nil {
		t.Fatal("esc did not close Settings")
	}
}

func TestRefreshShowsAnUnlistedValueAndCyclesOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := os.WriteFile(path, []byte(`{"github.com/alice":"","app":{"refresh":"90s"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := preferences.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 120, 30)
	m.refreshInterval, m.setTitle = store.Refresh(), store.Title()
	updateSnapshot(m, "alice", botPR(1))
	pressMsg(m, letter(","))
	if !strings.Contains(settingsView(m), "1m30s") {
		t.Fatalf("unlisted refresh not shown:\n%s", settingsView(m))
	}
	moveTo(t, m, "Refresh every")
	press(m, tea.Key{Code: tea.KeySpace, Text: " "})
	if store.Refresh() != 2*time.Minute {
		t.Fatalf("90s cycled to %v, want 2m", store.Refresh())
	}
}

func TestBotsFieldAppliesRejectsAndReverts(t *testing.T) {
	m := openSettingsModel(t)
	moveTo(t, m, "Review bots")
	press(m, tea.Key{Code: tea.KeyEnter})
	if m.settings.editing == nil {
		t.Fatal("enter did not open the bots field")
	}
	m.settings.editing.SetValue("no-equals")
	press(m, tea.Key{Code: tea.KeyEnter})
	if m.settings.editing == nil || m.settings.problem == "" || m.preferences.BotsText() != github.DefaultBots {
		t.Fatalf("invalid bots: editing %v problem %q saved %q", m.settings.editing != nil, m.settings.problem, m.preferences.BotsText())
	}
	press(m, tea.Key{Code: tea.KeyEsc})
	if m.settings == nil || m.settings.editing != nil || m.preferences.BotsText() != github.DefaultBots {
		t.Fatal("esc did not revert the bots field and stay in Settings")
	}
	press(m, tea.Key{Code: tea.KeyEnter})
	m.settings.editing.SetValue("")
	press(m, tea.Key{Code: tea.KeyEnter})
	if m.settings.editing != nil || m.preferences.BotsText() != "" || m.bots || !strings.Contains(settingsView(m), "none") {
		t.Fatalf("empty bots: saved %q bots %v\n%s", m.preferences.BotsText(), m.bots, settingsView(m))
	}
}

func TestQueuesChecklist(t *testing.T) {
	m := openSettingsModel(t)
	moveTo(t, m, "Merge queues")
	press(m, tea.Key{Code: tea.KeyEnter})
	if m.settings.queues == nil {
		t.Fatal("enter did not open the queue checklist")
	}
	press(m, tea.Key{Code: tea.KeySpace, Text: " "}) // Trunk off
	press(m, tea.Key{Code: tea.KeyEnter})
	if got := m.preferences.Queues(); len(got) != 1 || got[0] != github.QueueGitHub {
		t.Fatalf("queues = %v, want github", got)
	}
}

func TestEditorRowsReturnToSettings(t *testing.T) {
	m := openSettingsModel(t)
	moveTo(t, m, "Ready-to-merge rules")
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.rulesEditor == nil {
		t.Fatal("the rules row did not open the rules editor")
	}
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.rulesEditor != nil || m.settings == nil || !strings.Contains(ansi.Strip(m.View().Content), "Refresh every") {
		t.Fatal("closing the rules editor did not return to Settings")
	}
	moveTo(t, m, "Active hours")
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.scheduleEditor == nil {
		t.Fatal("the active hours row did not open its editor")
	}
	pressMsg(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.scheduleEditor != nil || m.settings == nil {
		t.Fatal("closing the active-hours editor did not return to Settings")
	}
}

func TestSettingsFitsANarrowShortTerminal(t *testing.T) {
	m := openSettingsModel(t)
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 14})
	assertBounded(t, m, 60, 14)
	for range 15 {
		press(m, tea.Key{Code: tea.KeyDown})
	}
	if !strings.Contains(settingsView(m), "> Active hours") {
		t.Fatalf("the cursor row scrolled out of view:\n%s", settingsView(m))
	}
}

func TestSettingsShowsAFailedSave(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "preferences.json")
	if err := os.WriteFile(path, []byte(`{"github.com/alice":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := preferences.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 120, 30)
	updateSnapshot(m, "alice", botPR(1))
	pressMsg(m, letter(","))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	moveTo(t, m, "Show drafts")
	press(m, tea.Key{Code: tea.KeySpace, Text: " "})
	if !strings.Contains(settingsView(m), "Setting not saved") {
		t.Fatalf("no failure shown:\n%s", settingsView(m))
	}
}

func TestBotsFieldScrollsWithinTheTerminalWidth(t *testing.T) {
	m := openSettingsModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	moveTo(t, m, "Review bots")
	press(m, tea.Key{Code: tea.KeyEnter})
	for _, line := range m.settingsLines() {
		if ansi.StringWidth(line) > 80 {
			t.Fatalf("line wider than 80: %q", ansi.Strip(line))
		}
	}
	view := settingsView(m)
	if !strings.Contains(view, "Claude Code Review") && !strings.Contains(view, github.DefaultBots[len(github.DefaultBots)-8:]) {
		t.Fatalf("the end of the value is hidden:\n%s", view)
	}
	m.settings.editing.SetValue("bad")
	press(m, tea.Key{Code: tea.KeyEnter})
	if !strings.Contains(settingsView(m), m.settings.problem) || strings.Contains(ansi.Strip(m.settingsLines()[len(m.settingsLines())-2]), m.settings.problem) {
		t.Fatalf("problem not on the row:\n%s", settingsView(m))
	}
}

func TestSettingsShowsTheSessionValueAfterAFailedSave(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "preferences.json")
	if err := os.WriteFile(path, []byte(`{"github.com/alice":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := preferences.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 120, 30)
	updateSnapshot(m, "alice", botPR(1))
	pressMsg(m, letter(","))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	moveTo(t, m, "Review bots")
	press(m, tea.Key{Code: tea.KeyEnter})
	m.settings.editing.SetValue("")
	press(m, tea.Key{Code: tea.KeyEnter})
	if !strings.Contains(settingsView(m), "Setting not saved") {
		t.Fatalf("the save did not fail:\n%s", settingsView(m))
	}
	moveTo(t, m, "Review bots")
	if row := rowLine(t, m, "Review bots"); !strings.Contains(row, "none") {
		t.Fatalf("the row shows the stored bots, not the session's: %q", row)
	}
}

func rowLine(t *testing.T, m *model, label string) string {
	t.Helper()
	for _, line := range m.settingsLines() {
		if l := ansi.Strip(line); strings.Contains(l, label) {
			return l
		}
	}
	t.Fatalf("no %q row:\n%s", label, settingsView(m))
	return ""
}

func TestFullRefreshCyclesAndSaves(t *testing.T) {
	m := openSettingsModel(t)
	if !strings.Contains(settingsView(m), "Full refresh every") || !strings.Contains(settingsView(m), "15m") {
		t.Fatalf("Settings lacks the full refresh row:\n%s", settingsView(m))
	}
	moveTo(t, m, "Full refresh every")
	for _, want := range []string{"30m", "1h", "never", "5m", "15m"} {
		press(m, tea.Key{Code: tea.KeySpace, Text: " "})
		line := ""
		for _, l := range strings.Split(settingsView(m), "\n") {
			if strings.Contains(l, "Full refresh every") {
				line = l
			}
		}
		if !strings.Contains(line, want) {
			t.Fatalf("row %q, want %s", line, want)
		}
	}
	press(m, tea.Key{Code: tea.KeySpace, Text: " "})
	if m.preferences.FullRefresh() != 30*time.Minute {
		t.Fatalf("saved %v", m.preferences.FullRefresh())
	}
}

func TestRefreshKeyForcesACompleteFetch(t *testing.T) {
	m := newPaneModel(t, 120, 30, manyPRs(2), nil)
	forced := 0
	m.forceFull = func() { forced++ }
	m.handleKey(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if forced != 1 {
		t.Fatalf("r forced %d complete fetches, want 1", forced)
	}
}
