package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/readiness"
)

func pressL(m *model) {
	press(m, tea.Key{Code: 'L', Text: "L"})
}

func legendText(m *model) string {
	return ansi.Strip(strings.Join(m.legendLines(), "\n"))
}

func TestLegendDocksUnderTheListsAndIsSaved(t *testing.T) {
	m := newPaneModel(t, 120, 40, manyPRs(12), reviewPRs(8))
	before := m.panes[paneMine].table.Height() + m.panes[paneReview].table.Height()
	pressL(m)
	if !m.legend || !m.preferences.Legend() {
		t.Fatalf("L did not open and save the legend: open %t, saved %t", m.legend, m.preferences.Legend())
	}
	lines := assertBounded(t, m, 120, 40)
	view := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{"Legend", "Merge", "CI", "Review", "Marks", "✗ conflicts", "● running"} {
		if !strings.Contains(view, want) {
			t.Errorf("legend lacks %q:\n%s", want, view)
		}
	}
	after := m.panes[paneMine].table.Height() + m.panes[paneReview].table.Height()
	if after != before-len(m.legendLines()) {
		t.Fatalf("table rows %d → %d, want the legend's %d lines taken", before, after, len(m.legendLines()))
	}
	// It sits under the panes, above the selected URL.
	legendAt := slicesIndex(lines, "Legend")
	if url := slicesIndex(lines, "https://github.com/"); legendAt < 0 || url < legendAt {
		t.Fatalf("legend at line %d, URL at %d", legendAt, url)
	}
	// The status line's merge legend is left to the panel.
	if status := ansi.Strip(lines[len(lines)-2]); strings.Contains(status, "blocked") {
		t.Fatalf("status line repeats the legend: %q", status)
	}
	// Clicks on the panel hit no pane.
	if _, ok := m.hitTest(legendAt + 1); ok {
		t.Fatal("a legend line maps to a pane")
	}

	pressL(m)
	if m.legend || m.preferences.Legend() || len(m.legendLines()) != 0 {
		t.Fatal("L did not close and save the legend")
	}
	if got := m.panes[paneMine].table.Height() + m.panes[paneReview].table.Height(); got != before {
		t.Fatalf("table rows %d after closing, want %d", got, before)
	}
}

func slicesIndex(lines []string, text string) int {
	for i, line := range lines {
		if strings.Contains(ansi.Strip(line), text) {
			return i
		}
	}
	return -1
}

func TestLegendFollowsTheIconSetRulesAndBots(t *testing.T) {
	m := newPaneModel(t, 220, 50, manyPRs(3), reviewPRs(2))
	pressL(m)
	yellow := func() string { return mergeIcon(m.icons, false, "MERGEABLE", "CLEAN", false) }
	running := func() string { return botStateText(m.icons, github.BotReview{State: github.BotRunning}) }
	legend := func() string { return strings.Join(m.legendLines(), "\n") }
	if strings.Contains(legend(), yellow()) || strings.Contains(legend(), running()) {
		t.Fatal("default legend explains the yellow check or bots")
	}
	m.bots = true
	m.rules = readiness.Rules{Default: readiness.Rule{Approvals: 1}}
	m.rebuildPRTable(false)
	if !strings.Contains(legend(), yellow()) || !strings.Contains(legend(), running()) {
		t.Fatal("legend misses the yellow check or bots")
	}
	if strings.Contains(legend(), nerdIcons.header("CI")) {
		t.Fatal("unicode legend explains Nerd column headers")
	}
	pressI(m)
	if !strings.Contains(legend(), nerdIcons.header("CI")) || strings.Contains(ansi.Strip(legend()), unicodeIcons.check) {
		t.Fatal("nerd legend lacks column headers or draws Unicode icons")
	}
}

func TestLegendWrapsAndYieldsToTheTables(t *testing.T) {
	m := newPaneModel(t, 50, 40, manyPRs(12), reviewPRs(8))
	pressL(m)
	lines := assertBounded(t, m, 50, 40)
	if slicesIndex(lines, "Legend") < 0 {
		t.Fatal("no legend at 50 columns")
	}
	// Wrapped rows start under the first symbol.
	if text := legendText(m); !strings.Contains(text, "\n        ") {
		t.Fatalf("narrow legend did not wrap:\n%s", text)
	}

	// Shorter: only whole sections that leave both tables their minimum.
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 19})
	lines = assertBounded(t, m, 120, 19)
	if layout := m.layoutPanes(); layout.single {
		t.Fatal("the legend switched the panes to one at a time")
	}
	if text := legendText(m); !strings.Contains(text, "taller terminal") || strings.Contains(text, "Marks") {
		t.Fatalf("short legend:\n%s", text)
	}

	// No room at all: the panel hides and the title says why.
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 9})
	lines = assertBounded(t, m, 120, 9)
	if len(m.legendLines()) != 0 || !strings.Contains(ansi.Strip(lines[0]), "legend needs a taller terminal") {
		t.Fatalf("tiny terminal:\n%s", ansi.Strip(strings.Join(lines, "\n")))
	}
}

func TestSavedLegendOpensAtStart(t *testing.T) {
	store := testPreferences(t)
	if err := store.SaveLegend(true); err != nil {
		t.Fatal(err)
	}
	client, err := github.NewClient(nil)
	if err != nil {
		t.Fatal(err)
	}
	m := New(t.Context(), client, store, 0, false, "", false).(*model)
	if !m.legend {
		t.Fatal("a saved open legend started closed")
	}
}

func TestLegendDrawsWhatTheTablesDraw(t *testing.T) {
	m := newPaneModel(t, 220, 60, manyPRs(2), reviewPRs(1))
	m.bots = true
	m.rules = readiness.Rules{Default: readiness.Rule{Approvals: 1}}
	pressL(m)
	for _, nerd := range []bool{false, true} {
		if m.icons.nerd != nerd {
			pressI(m)
		}
		ic := m.icons
		drawn := []string{
			mergeIcon(ic, false, "MERGEABLE", "CLEAN", true), mergeIcon(ic, false, "MERGEABLE", "CLEAN", false),
			mergeIcon(ic, false, "MERGEABLE", "BLOCKED", false), mergeIcon(ic, false, "MERGEABLE", "BEHIND", false),
			mergeIcon(ic, false, "CONFLICTING", "DIRTY", false), mergeIcon(ic, false, "UNKNOWN", "UNKNOWN", false),
			checksIcon(ic, "SUCCESS"), checksIcon(ic, "FAILURE"), checksIcon(ic, "PENDING"),
			reviewText(ic, "APPROVED", 0), reviewText(ic, "CHANGES_REQUESTED", 0), reviewText(ic, "REVIEW_REQUIRED", 0),
			markText(markNew, false), markText(markChanged, false), markText(markActivity, false), markText(markNone, true),
			pendingText, ic.star,
		}
		for _, state := range []github.BotState{github.BotPassed, github.BotStale, github.BotRunning, github.BotFailed} {
			drawn = append(drawn, botStateText(ic, github.BotReview{State: state}))
		}
		if nerd {
			drawn = append(drawn, ic.stateText(false), ic.stateText(true), ic.pin, ic.bell, ic.header("CI"),
				reviewStatusTag(ic, github.ReviewNewCommits), reviewStatusTag(ic, github.ReviewWaitingOnAuthor))
		}
		legend := strings.Join(m.legendLines(), "\n")
		for _, symbol := range drawn {
			if !strings.Contains(legend, symbol) {
				t.Errorf("nerd %t: legend lacks %q", nerd, ansi.Strip(symbol))
			}
		}
	}
}
