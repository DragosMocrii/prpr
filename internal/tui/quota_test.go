package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

func quotaModel(t *testing.T, width int) *model {
	t.Helper()
	m := autoRefreshModel(t, 0)
	m.Update(windowSize(width, 24))
	m.startFetch()
	finishFetch(m, aliceSnapshot())
	return m
}

func statusText(m *model) string {
	lines := m.listLines()
	// The status line sits directly above the help line.
	return ansi.Strip(lines[len(lines)-2])
}
func updateQuota(m *model, msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case rateLimitMsg:
		msg.generation, msg.account = m.quotaGeneration, m.accountGeneration
		return m.Update(msg)
	case quotaTickMsg:
		msg.generation, msg.account = m.quotaGeneration, m.accountGeneration
		return m.Update(msg)
	}
	return m.Update(msg)
}

func TestQuotaPollUpdatesStatusAndSchedulesNextPoll(t *testing.T) {
	m := quotaModel(t, 100)
	reset := time.Date(2026, 10, 1, 6, 14, 0, 0, time.Local)
	_, cmd := updateQuota(m, rateLimitMsg{limit: github.RateLimit{Limit: 5000, Remaining: 4981, Reset: reset}})
	if cmd == nil {
		t.Fatal("quota result did not schedule the next poll")
	}
	status := statusText(m)
	if !strings.Contains(status, "4,981/5,000") || !strings.Contains(status, "06:14") || strings.Contains(status, "5,000?") {
		t.Fatalf("status = %q", status)
	}
	if !strings.HasSuffix(status, "06:14") {
		t.Fatalf("quota not right-aligned: %q", status)
	}
	if _, cmd := updateQuota(m, quotaTickMsg{}); cmd == nil {
		t.Fatal("quota tick did not poll")
	}
	_, cmd = updateQuota(m, rateLimitMsg{err: errors.New("offline")})
	if cmd == nil {
		t.Fatal("failed poll did not schedule a retry")
	}
	if status := statusText(m); !strings.Contains(status, "4,981/5,000?") {
		t.Fatalf("stale quota not marked: %q", status)
	}
	updateQuota(m, rateLimitMsg{limit: github.RateLimit{Limit: 5000, Remaining: 4000, Reset: reset}})
	if status := statusText(m); !strings.Contains(status, "4,000/5,000 ") {
		t.Fatalf("fresh quota still marked stale: %q", status)
	}
}

func TestQuotaPollingPausesForLoginAndResumesAfterFetch(t *testing.T) {
	m := quotaModel(t, 100)
	m.loginActive, m.loading = true, true
	if _, cmd := updateQuota(m, quotaTickMsg{}); cmd != nil || !m.quotaPaused {
		t.Fatal("quota polled while login had the terminal")
	}
	m.Update(loginFinishedMsg{})
	if cmd := finishFetch(m, aliceSnapshot()); cmd == nil || m.quotaPaused {
		t.Fatal("successful fetch did not resume quota polling")
	}
	if cmd := finishFetch(m, aliceSnapshot()); cmd != nil {
		t.Fatal("fetch started a second quota poll chain")
	}
	updateFetch(m, fetchFinishedMsg{err: &github.AuthError{Err: errors.New("logged out")}})
	if _, cmd := updateQuota(m, rateLimitMsg{limit: github.RateLimit{Limit: 5000, Remaining: 1}}); cmd != nil || !m.quotaPaused {
		t.Fatal("quota kept polling after an authentication failure")
	}
}

func TestQuotaDropsLegendThenResetThenShortensAsWidthShrinks(t *testing.T) {
	m := quotaModel(t, 100)
	updateQuota(m, rateLimitMsg{limit: github.RateLimit{Limit: 5000, Remaining: 4981, Reset: time.Date(2026, 10, 1, 6, 14, 0, 0, time.Local)}})
	stage := func() int {
		status := statusText(m)
		switch {
		case strings.Contains(status, "clean") && strings.Contains(status, "resets"):
			return 0
		case strings.Contains(status, "clean"):
			t.Fatalf("legend kept while quota shrank: %q", status)
		case strings.Contains(status, "resets"):
			return 1
		case strings.Contains(status, "API 4,981/5,000"):
			return 2
		case strings.Contains(status, "4981/5000"):
			return 3
		}
		return 4
	}
	previous := 0
	for width := 100; width >= minimumWidth; width-- {
		m.Update(windowSize(width, 24))
		got := stage()
		if got < previous {
			t.Fatalf("width %d restored a dropped part: stage %d after %d", width, got, previous)
		}
		if w := ansi.StringWidth(m.listLines()[len(m.listLines())-2]); w > width {
			t.Fatalf("status width %d at %d columns", w, width)
		}
		previous = got
	}
	if previous == 0 {
		t.Fatal("narrow terminal never dropped anything")
	}
}

func TestQuotaShownOnErrorScreen(t *testing.T) {
	m := quotaModel(t, 100)
	updateQuota(m, rateLimitMsg{limit: github.RateLimit{Limit: 5000, Remaining: 0, Reset: time.Date(2026, 10, 1, 6, 14, 0, 0, time.Local)}})
	updateFetch(m, fetchFinishedMsg{err: errors.New("API rate limit exceeded")})
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "API 0/5,000") {
		t.Fatalf("error screen lacks quota:\n%s", view)
	}
}

func windowSize(width, height int) tea.WindowSizeMsg {
	return tea.WindowSizeMsg{Width: width, Height: height}
}
