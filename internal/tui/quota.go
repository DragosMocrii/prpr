package tui

import (
	"errors"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/DragosMocrii/prpr/internal/github"
)

// quotaPollInterval is the delay between quota reads. GitHub does not count
// rate-limit reads against the quota.
const quotaPollInterval = 10 * time.Second

type rateLimitMsg struct {
	limit github.RateLimit
	err   error
	// account is the accountGeneration the quota was read for.
	account uint64
}

type quotaTickMsg struct{}

// pollQuota reads the quota once. Only one poll chain runs: each result
// schedules the next tick.
func (m *model) pollQuota() tea.Cmd {
	client, ctx, account := m.client, m.ctx, m.accountGeneration
	return func() tea.Msg {
		limit, err := client.RateLimit(ctx)
		return rateLimitMsg{limit: limit, err: err, account: account}
	}
}

// quotaBlocked reports whether polling should pause: login owns the terminal,
// or the account needs a login before GitHub requests can succeed.
func (m *model) quotaBlocked() bool {
	var authErr *github.AuthError
	return m.loginActive || errors.As(m.err, &authErr)
}

// handleQuota advances the poll chain, pausing it while quotaBlocked.
func (m *model) handleQuota(msg tea.Msg) tea.Cmd {
	// A quota read as another account still continues the chain.
	if result, ok := msg.(rateLimitMsg); ok && result.account == m.accountGeneration {
		if result.err != nil {
			m.quotaStale = true
		} else {
			m.quota, m.quotaKnown, m.quotaStale = result.limit, true, false
		}
	}
	if m.quotaBlocked() {
		m.quotaPaused = true
		return nil
	}
	if _, ok := msg.(quotaTickMsg); ok {
		return m.pollQuota()
	}
	return tea.Tick(quotaPollInterval, func(time.Time) tea.Msg { return quotaTickMsg{} })
}

// resumeQuota restarts a paused poll chain.
func (m *model) resumeQuota() tea.Cmd {
	if !m.quotaPaused || m.quotaBlocked() {
		return nil
	}
	m.quotaPaused = false
	return m.pollQuota()
}

// quotaText renders the quota at a detail level: 0 with the reset time, 1
// without it, 2 as bare numbers. It is empty when the quota is unknown.
func (m *model) quotaText(level int) string {
	if !m.quotaKnown {
		return ""
	}
	stale := ""
	if m.quotaStale {
		stale = "?"
	}
	q := m.quota
	var text string
	switch level {
	case 0:
		text = "API " + groupThousands(q.Remaining) + "/" + groupThousands(q.Limit) + stale + " · resets " + q.Reset.Local().Format("15:04")
	case 1:
		text = "API " + groupThousands(q.Remaining) + "/" + groupThousands(q.Limit) + stale
	default:
		text = strconv.Itoa(q.Remaining) + "/" + strconv.Itoa(q.Limit) + stale
	}
	switch {
	case q.Remaining*20 < q.Limit:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(text)
	case q.Remaining*5 < q.Limit:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render(text)
	}
	return text
}

// changeStatusWidth is the room the status line leaves a change summary
// beside fixed and the shortest quota.
func (m *model) changeStatusWidth(fixed string) int {
	width := m.width
	if fixed != "" {
		width -= lipgloss.Width(fixed) + 2
	}
	if m.quotaKnown {
		width -= lipgloss.Width(m.quotaText(2)) + 2
	}
	return width
}

// statusLine joins the always-shown text and the legend, with the quota
// right-aligned. The legend is dropped first, then quota detail.
func (m *model) statusLine(fixed, legend string) string {
	join := func(parts ...string) string {
		kept := parts[:0:0]
		for _, part := range parts {
			if part != "" {
				kept = append(kept, part)
			}
		}
		return strings.Join(kept, "  ")
	}
	if !m.quotaKnown {
		return join(fixed, legend)
	}
	for _, option := range []struct {
		legend string
		level  int
	}{{legend, 0}, {"", 0}, {"", 1}, {"", 2}} {
		left, quota := join(fixed, option.legend), m.quotaText(option.level)
		minGap := 0
		if left != "" {
			minGap = 2
		}
		if gap := m.width - lipgloss.Width(left) - lipgloss.Width(quota); gap >= minGap {
			return left + strings.Repeat(" ", gap) + quota
		}
	}
	return fixed
}

func groupThousands(n int) string {
	digits := strconv.Itoa(n)
	if n < 0 {
		return digits
	}
	var b strings.Builder
	for i, digit := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(digit)
	}
	return b.String()
}
