package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

func detailsText(m *model) string {
	return ansi.Strip(m.View().Content)
}

func enter(m *model) { press(m, tea.Key{Code: tea.KeyEnter}) }

func TestMergeDetailNamesOnlyProvenBlockers(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		draft                              bool
		mergeable, state, decision, checks string
		want, wantNot                      []string
	}{
		{name: "conflicts win", draft: true, mergeable: "CONFLICTING", state: "BLOCKED", want: []string{"conflicts"}, wantNot: []string{"Draft", "Blocked"}},
		{name: "draft", draft: true, state: "BLOCKED", want: []string{"Draft"}},
		{name: "clean", state: "CLEAN", want: []string{"Ready to merge"}},
		{name: "unstable", state: "UNSTABLE", want: []string{"Ready to merge", "not required"}},
		{name: "behind", state: "BEHIND", want: []string{"Behind"}},
		{name: "review required", state: "BLOCKED", decision: "REVIEW_REQUIRED", want: []string{"approving review is required"}},
		{name: "changes requested", state: "BLOCKED", decision: "CHANGES_REQUESTED", want: []string{"changes were requested"}},
		{name: "unnamed rule", state: "BLOCKED", decision: "APPROVED", checks: "SUCCESS", want: []string{"does not name"}, wantNot: []string{"review is required", "failing"}},
		{name: "failing checks are not proven blockers", state: "BLOCKED", decision: "APPROVED", checks: "FAILURE", want: []string{"does not name", "does not say whether they are required"}, wantNot: []string{"Blocked: checks", "Blocked by failing"}},
		{name: "unknown", state: "", want: []string{"not computed"}, wantNot: []string{"Ready"}},
		{name: "unknown with null decision", state: "UNKNOWN", want: []string{"not computed"}},
	} {
		got := mergeDetail(tc.draft, tc.mergeable, tc.state, tc.decision, tc.checks)
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s: %q lacks %q", tc.name, got, want)
			}
		}
		for _, not := range tc.wantNot {
			if strings.Contains(got, not) {
				t.Errorf("%s: %q contains %q", tc.name, got, not)
			}
		}
	}
	if got := reviewDetail("", 0); strings.Contains(got, "Approved") || strings.Contains(got, "required") {
		t.Errorf("null review decision reads as known: %q", got)
	}
	if got := checksDetail(""); strings.Contains(got, "passed") {
		t.Errorf("null check rollup reads as passing: %q", got)
	}
}

func TestDetailsOpenFollowTheCursorAndClose(t *testing.T) {
	long := strings.Repeat("very long title words ", 12) + "END"
	prs := manyPRs(3)
	prs[1].Title = long
	m := newPaneModel(t, 60, 24, prs, reviewPRs(1))
	enter(m)
	view := detailsText(m)
	if !m.detailsShown() || !strings.Contains(view, "#1 acme/a") || !strings.Contains(view, prs[0].URL) {
		t.Fatalf("enter did not open details for #1:\n%s", view)
	}
	press(m, tea.Key{Code: 'j', Text: "j"})
	view = detailsText(m)
	if !strings.Contains(view, "#2 acme/a") || !strings.Contains(view, "END") {
		t.Fatalf("j did not show #2 with its full title:\n%s", view)
	}
	// Keys that belong to the list are off here.
	press(m, tea.Key{Code: tea.KeyTab})
	press(m, tea.Key{Code: 'p', Text: "p"})
	if m.focus != paneMine || m.picker != nil {
		t.Fatalf("list keys acted on the details screen: focus %v picker %v", m.focus, m.picker)
	}
	press(m, tea.Key{Code: tea.KeyEsc})
	if m.detailsShown() || !strings.Contains(detailsText(m), "My PRs") {
		t.Fatalf("esc did not return to the list:\n%s", detailsText(m))
	}
	if pr, _ := m.selectedPR(); pr.Number != 2 {
		t.Fatalf("selection after details = #%d, want #2", pr.Number)
	}
	enter(m)
	enter(m)
	if m.detailsShown() {
		t.Fatal("enter did not close details")
	}
}

func TestDetailsFollowThePRAcrossRefreshesAndGoneRows(t *testing.T) {
	m := newPaneModel(t, 100, 24, manyPRs(3), nil)
	press(m, tea.Key{Code: 'j', Text: "j"})
	enter(m)
	// #1 leaves; the cursor stays on #2.
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: manyPRs(3)[1:]}})
	if view := detailsText(m); !strings.Contains(view, "#2 acme/a") || strings.Contains(view, "left the list") {
		t.Fatalf("details did not follow #2:\n%s", view)
	}
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: manyPRs(3)[2:]}})
	if view := detailsText(m); !strings.Contains(view, "#2 acme/a") || !strings.Contains(view, "left the list") {
		t.Fatalf("gone #2 not described as gone:\n%s", view)
	}
	m.Update(fetchFinishedMsg{err: errors.New("boom")})
	if m.detailsShown() || m.details {
		t.Fatal("details survived a failed fetch")
	}
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: manyPRs(3)}})
	if m.detailsShown() {
		t.Fatal("details reappeared after the error")
	}
}

func TestDetailsDescribeStatusesAndPreviewRows(t *testing.T) {
	pr := github.PullRequest{Number: 9, Repository: "acme/a", URL: "https://github.com/acme/a/pull/9", Title: "t",
		MergeState: "BLOCKED", Mergeable: "MERGEABLE", ReviewDecision: "REVIEW_REQUIRED", Checks: "FAILURE", Approvals: 1,
		Bots: []github.BotReview{{Name: "Copilot", State: github.BotConcerns, Concerns: 2}, {Name: "Codex"}}}
	m := newPaneModel(t, 100, 30, []github.PullRequest{pr}, nil)
	m.bots = true
	enter(m)
	view := detailsText(m)
	for _, want := range []string{"approving review is required", "Some checks failed", "Review required · 1 approval", "Copilot: 2 unresolved threads", "Codex: has not acted"} {
		if !strings.Contains(view, want) {
			t.Errorf("details lack %q:\n%s", want, view)
		}
	}

	preview := newPaneModel(t, 100, 30, nil, nil)
	preview.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", Preview: true, PullRequests: []github.PullRequest{pr}}})
	enter(preview)
	view = detailsText(preview)
	if !strings.Contains(view, loadingDetails) || strings.Contains(view, "approving review") || strings.Contains(view, "Some checks failed") {
		t.Fatalf("preview details claim unknown statuses:\n%s", view)
	}
}

func TestDetailsStayBoundedAndKeepTheFooter(t *testing.T) {
	prs := manyPRs(1)
	prs[0].Title = strings.Repeat("word ", 80)
	m := newPaneModel(t, 120, 30, prs, nil)
	enter(m)
	for _, size := range [][2]int{{40, 8}, {50, 12}, {120, 30}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := assertBounded(t, m, size[0], size[1])
		if len(lines) != size[1] || !strings.Contains(strings.Join(lines, "\n"), prs[0].URL) {
			t.Fatalf("details at %dx%d lost the URL or did not fill the screen:\n%s", size[0], size[1], strings.Join(lines, "\n"))
		}
		if !strings.Contains(ansi.Strip(lines[len(lines)-1]), "back") {
			t.Fatalf("details at %dx%d lost the help: %q", size[0], size[1], ansi.Strip(lines[len(lines)-1]))
		}
	}
}

func TestDetailsSurviveATooSmallTerminal(t *testing.T) {
	m := newPaneModel(t, 100, 24, manyPRs(2), nil)
	enter(m)
	m.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	if !m.detailsShown() {
		t.Fatal("shrinking the terminal closed the details")
	}
}

func TestWideTerminalsShowDetailsOverTheList(t *testing.T) {
	m := newPaneModel(t, 120, 30, manyPRs(3), reviewPRs(2))
	press(m, tea.Key{Code: 'j', Text: "j"})
	enter(m)
	lines := assertBounded(t, m, 120, 30)
	view := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.Contains(view, "prpr — @alice — All repositories") || !strings.Contains(view, "#2 acme/a") ||
		!strings.Contains(view, "Merge") || !strings.Contains(ansi.Strip(lines[len(lines)-1]), "esc back") {
		t.Fatalf("modal details:\n%s", view)
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	view = ansi.Strip(m.View().Content)
	if !strings.HasPrefix(view, "prpr — @alice — #2 acme/a") {
		t.Fatalf("narrow details are not full screen:\n%s", view)
	}
}
