package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"prpr/internal/github"
)

func TestNavigationKeepsEmptyListAtZero(t *testing.T) {
	m := &model{ctx: context.Background(), snapshot: github.Snapshot{Login: "octocat"}, width: 80, height: 24}
	m.Update(tea.KeyPressMsg(tea.Key{Code: 'j', Text: "j"}))
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if m.cursor != 0 || m.offset != 0 {
		t.Fatalf("empty-list selection = (%d, %d), want (0, 0)", m.cursor, m.offset)
	}
}

func TestSuccessfulRefreshReplacesAccountAndClampsSelection(t *testing.T) {
	m := &model{
		ctx: context.Background(),
		snapshot: github.Snapshot{Login: "old", PullRequests: []github.PullRequest{
			{Number: 1}, {Number: 2}, {Number: 3}, {Number: 4},
		}},
		cursor: 3, offset: 2, width: 80, height: 8,
	}
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "new", PullRequests: []github.PullRequest{{Number: 9}}}})
	if m.snapshot.Login != "new" || len(m.snapshot.PullRequests) != 1 || m.snapshot.PullRequests[0].Number != 9 {
		t.Fatalf("snapshot after account replacement = %+v", m.snapshot)
	}
	if m.cursor != 0 || m.offset != 0 {
		t.Fatalf("selection after shorter refresh = (%d, %d), want (0, 0)", m.cursor, m.offset)
	}
}

func TestFailedRefreshDoesNotExposeStaleRows(t *testing.T) {
	m := &model{
		ctx:      context.Background(),
		snapshot: github.Snapshot{Login: "old", PullRequests: []github.PullRequest{{Number: 7}}},
		width:    80, height: 24,
	}
	m.startFetch()
	if len(m.snapshot.PullRequests) != 0 || !m.loading {
		t.Fatalf("refresh did not clear old list while loading: %+v", m)
	}
	m.Update(fetchFinishedMsg{err: errors.New("offline")})
	if len(m.snapshot.PullRequests) != 0 || m.snapshot.Login != "" || m.loading || m.err == nil {
		t.Fatalf("failed refresh retained or hid stale state: %+v", m)
	}
}

func TestSuccessfulRefreshToEmptyListResetsSelection(t *testing.T) {
	m := &model{
		ctx:      context.Background(),
		snapshot: github.Snapshot{Login: "old", PullRequests: []github.PullRequest{{Number: 1}, {Number: 2}}},
		cursor:   1, offset: 1, width: 80, height: 24,
	}
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "new"}})
	if m.snapshot.Login != "new" || len(m.snapshot.PullRequests) != 0 || m.cursor != 0 || m.offset != 0 {
		t.Fatalf("empty refresh left stale selection/account state: %+v", m)
	}
}

func TestFailedLoginReturnsToRetryableErrorState(t *testing.T) {
	m := &model{ctx: context.Background(), loading: true, loginActive: true}
	m.Update(loginFinishedMsg{err: errors.New("cancelled")})
	if m.loading || m.loginActive || m.err == nil {
		t.Fatalf("failed login state = loading:%t active:%t error:%v", m.loading, m.loginActive, m.err)
	}
}

func TestRepositoryFilterKeepsInterleavedOrderAndSelectedIdentity(t *testing.T) {
	m := &model{ctx: context.Background(), width: 80, height: 12}
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: []github.PullRequest{
		{Number: 1, Repository: "Acme/A", URL: "https://example.test/a/1", Draft: true},
		{Number: 1, Repository: "Acme/B", URL: "https://example.test/b/1"},
		{Number: 2, Repository: "acme/a", URL: "https://example.test/a/2"},
	}}})
	m.applyRepository("acme/A")
	if len(m.visiblePRs) != 2 {
		t.Fatalf("visible PR indices = %v, want two", m.visiblePRs)
	}
	first := m.snapshot.PullRequests[m.visiblePRs[0]]
	second := m.snapshot.PullRequests[m.visiblePRs[1]]
	if first.Repository != "Acme/A" || first.Number != 1 || !first.Draft ||
		second.Repository != "acme/a" || second.Number != 2 {
		t.Fatalf("filtered PR order/identity = %+v, %+v", first, second)
	}
	m.Update(tea.KeyPressMsg(tea.Key{Code: 'j', Text: "j"}))
	selected := m.snapshot.PullRequests[m.visiblePRs[m.cursor]]
	if selected.URL != "https://example.test/a/2" {
		t.Fatalf("selected URL = %q, want second filtered PR", selected.URL)
	}
	m.applyRepository("")
	if len(m.visiblePRs) != 3 || m.cursor != 0 || m.offset != 0 {
		t.Fatalf("all-repository state = indices %v, cursor %d offset %d", m.visiblePRs, m.cursor, m.offset)
	}
}

func TestRefreshRetainsFilterWhenRepositoryHasNoPullRequests(t *testing.T) {
	m := &model{ctx: context.Background(), width: 80, height: 24}
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: []github.PullRequest{
		{Number: 4, Repository: "acme/empty"},
		{Number: 5, Repository: "acme/other"},
	}}})
	m.applyRepository("acme/empty")
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: []github.PullRequest{
		{Number: 6, Repository: "acme/other"},
	}}})
	if m.selectedRepository != "acme/empty" || len(m.visiblePRs) != 0 || m.cursor != 0 || m.offset != 0 {
		t.Fatalf("same-account refresh lost empty repository filter: filter %q visible %v", m.selectedRepository, m.visiblePRs)
	}
}

func TestEmptyRepositoryFilterKeepsNavigationSafeAndExplainsScope(t *testing.T) {
	m := &model{ctx: context.Background(), width: 80, height: 12}
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: []github.PullRequest{
		{Number: 1, Repository: "acme/other"},
	}}})
	m.applyRepository("acme/empty")
	press(m, tea.Key{Code: tea.KeyDown})
	if m.cursor != 0 || m.offset != 0 || len(m.visiblePRs) != 0 {
		t.Fatalf("empty filter navigation state: cursor %d offset %d visible %v", m.cursor, m.offset, m.visiblePRs)
	}
	lines := strings.Join(m.listLines(), "\n")
	if !strings.Contains(lines, "No open pull requests in acme/empty.") {
		t.Fatalf("empty repository scope missing its empty-state explanation: %q", lines)
	}
}

func TestAccountChangeResetsRepositoryFilter(t *testing.T) {
	m := &model{ctx: context.Background(), width: 80, height: 24}
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: []github.PullRequest{
		{Number: 1, Repository: "acme/repo"},
	}}})
	m.applyRepository("acme/repo")
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "bob", PullRequests: []github.PullRequest{
		{Number: 2, Repository: "other/repo"},
	}}})
	if m.selectedRepository != "" || len(m.visiblePRs) != 1 ||
		m.snapshot.PullRequests[m.visiblePRs[0]].Repository != "other/repo" {
		t.Fatalf("account switch retained previous filter: filter %q visible %v", m.selectedRepository, m.visiblePRs)
	}
}
