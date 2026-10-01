package github

import (
	"fmt"
	"strings"
	"time"
)

// Bot is a review bot to report on. Check, when set, is a substring of the
// bot's check-run names on the head commit.
type Bot struct {
	Name  string
	Login string
	Check string
}

// DefaultBots configures GitHub Copilot code review, OpenAI Codex, and Claude.
const DefaultBots = "Copilot=copilot-pull-request-reviewer:copilot-pull-request-reviewer," +
	"Codex=chatgpt-codex-connector," +
	"Claude=claude:Claude Code Review"

// ParseBots reads comma-separated Name=login[:check] entries. An empty value
// configures no bots.
func ParseBots(value string) ([]Bot, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var bots []Bot
	seen := make(map[string]struct{})
	for _, entry := range strings.Split(value, ",") {
		name, rest, ok := strings.Cut(entry, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("bot %q: want Name=login or Name=login:check", strings.TrimSpace(entry))
		}
		login, check, _ := strings.Cut(rest, ":")
		login = botLogin(strings.TrimSpace(login))
		if !validLogin(login) {
			return nil, fmt.Errorf("bot %q: invalid GitHub login %q", name, login)
		}
		key := strings.ToLower(login)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("bot %q: login %q is listed twice", name, login)
		}
		seen[key] = struct{}{}
		bots = append(bots, Bot{Name: name, Login: login, Check: strings.TrimSpace(check)})
	}
	return bots, nil
}

// botLogin drops the [bot] suffix that reactions show but reviews omit.
func botLogin(login string) string {
	return strings.TrimSuffix(login, "[bot]")
}

func validLogin(login string) bool {
	if login == "" {
		return false
	}
	for _, r := range login {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}
	return true
}

// BotState is ordered from least to most attention-worthy.
type BotState int

const (
	// BotNotRun means the bot has not acted on the pull request.
	BotNotRun BotState = iota
	// BotPassed means the bot acted since the head commit and left no open
	// threads.
	BotPassed
	// BotStale means the bot left no open threads but last acted before the
	// head commit.
	BotStale
	// BotRunning means the bot's check run on the head commit has not finished.
	BotRunning
	// BotFailed means the bot's check run on the head commit failed.
	BotFailed
	// BotConcerns means threads the bot started are unresolved and current.
	BotConcerns
)

type BotReview struct {
	Name     string
	State    BotState
	Concerns int
}

// botFields are selected for both lists when bots are configured. Threads
// are the costly part: each one's first comment is a nested request.
const botFields = `
        reviews(last: 30) { nodes { author { login } submittedAt } }
        comments(last: 30) { nodes { author { login } createdAt lastEditedAt } }
        reactions(last: 20) { nodes { content createdAt user { login } } }
        reviewThreads(last: 20) {
          nodes { isResolved isOutdated comments(first: 1) { nodes { author { login } } } }
        }`

// headCheckFields extend the head commit selection when bots are configured.
const headCheckFields = `
            committedDate
            statusCheckRollup { state contexts(first: 50) { nodes { ... on CheckRun { name status conclusion } } } }`

type actor struct {
	Login string `json:"login"`
}

type botNodes struct {
	Reviews struct {
		Nodes []*struct {
			Author      *actor    `json:"author"`
			SubmittedAt time.Time `json:"submittedAt"`
		} `json:"nodes"`
	} `json:"reviews"`
	Comments struct {
		Nodes []*struct {
			Author       *actor     `json:"author"`
			CreatedAt    time.Time  `json:"createdAt"`
			LastEditedAt *time.Time `json:"lastEditedAt"`
		} `json:"nodes"`
	} `json:"comments"`
	Reactions struct {
		Nodes []*struct {
			Content   string    `json:"content"`
			CreatedAt time.Time `json:"createdAt"`
			User      *actor    `json:"user"`
		} `json:"nodes"`
	} `json:"reactions"`
	ReviewThreads struct {
		Nodes []*struct {
			IsResolved bool `json:"isResolved"`
			IsOutdated bool `json:"isOutdated"`
			Comments   struct {
				Nodes []*struct {
					Author *actor `json:"author"`
				} `json:"nodes"`
			} `json:"comments"`
		} `json:"nodes"`
	} `json:"reviewThreads"`
}

type checkRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

func isActor(a *actor, bot Bot) bool {
	return a != nil && strings.EqualFold(botLogin(a.Login), bot.Login)
}

// botReview applies one rule to every bot. Open threads come first, then the
// head commit's check run, then whether the bot acted since the head commit.
func botReview(bot Bot, nodes *botNodes, headDate time.Time, checks []*checkRun) BotReview {
	review := BotReview{Name: bot.Name}
	for _, thread := range nodes.ReviewThreads.Nodes {
		if thread == nil || thread.IsResolved || thread.IsOutdated || len(thread.Comments.Nodes) == 0 {
			continue
		}
		if first := thread.Comments.Nodes[0]; first != nil && isActor(first.Author, bot) {
			review.Concerns++
		}
	}
	if review.Concerns > 0 {
		review.State = BotConcerns
		return review
	}
	if bot.Check != "" {
		running, failed := false, false
		for _, check := range checks {
			if check == nil || !strings.Contains(check.Name, bot.Check) {
				continue
			}
			switch {
			case check.Status != "" && check.Status != "COMPLETED":
				running = true
			case check.Conclusion == "FAILURE" || check.Conclusion == "TIMED_OUT" || check.Conclusion == "STARTUP_FAILURE":
				failed = true
			}
		}
		if running {
			review.State = BotRunning
			return review
		}
		if failed {
			review.State = BotFailed
			return review
		}
	}
	var last time.Time
	seen := func(at time.Time) {
		if at.After(last) {
			last = at
		}
	}
	for _, r := range nodes.Reviews.Nodes {
		if r != nil && isActor(r.Author, bot) {
			seen(r.SubmittedAt)
		}
	}
	for _, c := range nodes.Comments.Nodes {
		if c != nil && isActor(c.Author, bot) {
			seen(c.CreatedAt)
			if c.LastEditedAt != nil {
				seen(*c.LastEditedAt)
			}
		}
	}
	for _, r := range nodes.Reactions.Nodes {
		// Eyes mark work in progress, not a finished review.
		if r != nil && r.Content != "EYES" && isActor(r.User, bot) {
			seen(r.CreatedAt)
		}
	}
	switch {
	case last.IsZero():
		review.State = BotNotRun
	case last.Before(headDate):
		review.State = BotStale
	default:
		review.State = BotPassed
	}
	return review
}
