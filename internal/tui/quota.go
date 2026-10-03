package tui

import (
	"context"
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
	limit      github.RateLimit
	err        error
	account    uint64
	generation uint64
}

type quotaTickMsg struct {
	account    uint64
	generation uint64
}

// pollQuota starts one cancellable quota chain. Each request schedules the
// next generation-tagged tick only after its result arrives.
func (m *model) pollQuota() tea.Cmd {
	if !m.pollingAllowed() || m.quotaBlocked() {
		m.quotaPaused = true
		return nil
	}
	if m.quotaRunning {
		return nil
	}
	m.quotaGeneration++
	m.quotaRunning, m.quotaPaused = true, false
	base := m.activityCtx
	if base == nil {
		base = m.ctx
	}
	ctx, cancel := context.WithCancel(base)
	m.quotaCancel = cancel
	return m.readQuota(ctx, m.quotaGeneration, m.accountGeneration)
}

func (m *model) readQuota(ctx context.Context, generation, account uint64) tea.Cmd {
	client, deadline, now := m.client, m.activityDeadline, m.now
	return func() tea.Msg {
		if ctx.Err() != nil || !deadline.IsZero() && !now().Before(deadline) {
			return rateLimitMsg{err: context.Canceled, account: account, generation: generation}
		}
		limit, err := client.RateLimit(ctx)
		return rateLimitMsg{limit: limit, err: err, account: account, generation: generation}
	}
}

// quotaBlocked reports whether polling should pause for sleep, login, or auth.
func (m *model) quotaBlocked() bool {
	var authErr *github.AuthError
	return !m.pollingAllowed() || m.loginActive || errors.As(m.err, &authErr)
}

func (m *model) pauseQuota() {
	m.invalidateQuota()
}

// handleQuota advances the single poll chain and rejects every obsolete result.
func (m *model) handleQuota(msg tea.Msg) tea.Cmd {
	if m.quotaBlocked() {
		m.pauseQuota()
		return nil
	}
	switch msg := msg.(type) {
	case rateLimitMsg:
		if msg.generation != m.quotaGeneration || msg.account != m.accountGeneration {
			return nil
		}
		if msg.err != nil {
			if !errors.Is(msg.err, context.Canceled) && !errors.Is(msg.err, context.DeadlineExceeded) {
				m.quotaStale = true
			}
		} else {
			m.quota, m.quotaKnown, m.quotaStale, m.quotaFetchedAt = msg.limit, true, false, m.now()
		}
	case quotaTickMsg:
		if msg.generation != m.quotaGeneration || msg.account != m.accountGeneration {
			return nil
		}
		base := m.activityCtx
		if base == nil {
			base = m.ctx
		}
		if m.quotaCancel != nil {
			m.quotaCancel()
		}
		ctx, cancel := context.WithCancel(base)
		m.quotaCancel = cancel
		return m.readQuota(ctx, msg.generation, msg.account)
	default:
		return nil
	}
	generation, account := m.quotaGeneration, m.accountGeneration
	return tea.Tick(quotaPollInterval, func(time.Time) tea.Msg {
		return quotaTickMsg{generation: generation, account: account}
	})
}

func (m *model) resumeQuota() tea.Cmd {
	if !m.quotaPaused || m.quotaBlocked() || m.quotaRunning {
		return nil
	}
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
	if m.sleeping {
		text += " · paused"
		if !m.quotaFetchedAt.IsZero() {
			text += " as of " + m.quotaFetchedAt.In(time.Local).Format("15:04")
		}
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
