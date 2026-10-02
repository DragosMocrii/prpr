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

// startPreviewedFetch starts a first fetch and returns its generation.
func startPreviewedFetch(t *testing.T, m *model) uint64 {
	t.Helper()
	m.startFetch()
	return m.refreshGeneration
}

func previewOf(prs ...github.PullRequest) github.Snapshot {
	preview := make([]github.PullRequest, len(prs))
	for i, pr := range prs {
		preview[i] = github.PullRequest{
			Number: pr.Number, Repository: pr.Repository, Title: pr.Title, URL: pr.URL,
			Draft: pr.Draft, Mergeable: pr.Mergeable, CreatedAt: pr.CreatedAt, UpdatedAt: pr.UpdatedAt,
			Additions: pr.Additions, Deletions: pr.Deletions,
		}
	}
	return github.Snapshot{Login: "alice", PullRequests: preview, Preview: true}
}

func previewModel(t *testing.T) *model {
	t.Helper()
	store := testPreferences(t)
	if err := store.Save("alice", ""); err != nil {
		t.Fatal(err)
	}
	m := testModel(store, 140, 30)
	m.now = func() time.Time { return changeTime }
	return m
}

func TestPreviewShowsRowsUntilTheFullFetchReplacesThem(t *testing.T) {
	m := previewModel(t)
	full := changePR(1, "acme/a")
	full.Checks, full.ReviewDecision, full.WaitingSince = "SUCCESS", "APPROVED", changeTime.Add(-48*time.Hour)
	conflicted := changePR(2, "acme/a")
	conflicted.Mergeable, conflicted.MergeState = "CONFLICTING", "DIRTY"
	generation := startPreviewedFetch(t, m)
	m.Update(previewMsg{generation: generation, snapshot: previewOf(full, conflicted)})

	view := ansi.Strip(strings.Join(assertBounded(t, m, 140, 30), "\n"))
	if !strings.Contains(view, "Loading details") || !strings.Contains(view, full.URL) {
		t.Fatalf("preview not shown:\n%s", view)
	}
	for _, title := range []string{"Merge", "Age", "CI", "Review"} {
		if cell := ansi.Strip(cellOf(t, m, paneMine, 0, title)); cell != "…" {
			t.Errorf("preview %s cell = %q, want …", title, cell)
		}
	}
	if cell := ansi.Strip(cellOf(t, m, paneMine, 1, "Merge")); cell != "✗" {
		t.Errorf("preview conflict = %q, want ✗", cell)
	}
	if m.keys.Refresh.Enabled() {
		t.Error("refresh enabled while details load")
	}

	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: []github.PullRequest{full, conflicted}}})
	view = ansi.Strip(strings.Join(assertBounded(t, m, 140, 30), "\n"))
	if strings.Contains(view, "Loading details") || strings.Contains(view, "…") {
		t.Fatalf("preview kept after the full fetch:\n%s", view)
	}
	if got := markers(m, paneMine); got != "  " {
		t.Fatalf("first full fetch after a preview marked rows: %q", got)
	}
}

func TestLateOrFailedPreviewIsIgnored(t *testing.T) {
	m := previewModel(t)
	one := changePR(1, "acme/a")
	generation := startPreviewedFetch(t, m)
	m.Update(previewMsg{generation: generation, err: errors.New("timeout")})
	if m.snapshot.Login != "" {
		t.Fatal("failed preview shown")
	}
	updateSnapshot(m, "alice", one)
	m.Update(previewMsg{generation: generation, snapshot: previewOf(one)})
	if m.snapshot.Preview {
		t.Fatal("preview arriving after the full fetch replaced it")
	}

	// A refresh does not start a preview; one from an older fetch is dropped.
	m.startFetch()
	m.Update(previewMsg{generation: generation, snapshot: previewOf(one)})
	m.Update(previewMsg{generation: m.refreshGeneration, snapshot: previewOf(one)})
	if m.snapshot.Preview {
		t.Fatal("preview replaced the rows shown during a refresh")
	}
}

func TestFailedFetchClearsPreviewRows(t *testing.T) {
	m := previewModel(t)
	one := changePR(1, "acme/a")
	generation := startPreviewedFetch(t, m)
	m.Update(previewMsg{generation: generation, snapshot: previewOf(one)})
	m.Update(fetchFinishedMsg{err: errors.New("network down")})
	if view := ansi.Strip(m.View().Content); strings.Contains(view, one.URL) {
		t.Fatalf("preview rows kept after a failed fetch:\n%s", view)
	}
}

func TestPreviewAfterAnErrorKeepsTheChangeBaseline(t *testing.T) {
	m := changesModel(t, changePR(1, "acme/a"), changePR(2, "acme/a"))
	m.Update(fetchFinishedMsg{err: errors.New("network down")})
	two := changePR(2, "acme/a")
	generation := startPreviewedFetch(t, m)
	m.Update(previewMsg{generation: generation, snapshot: previewOf(two)})
	updateSnapshot(m, "alice", two)
	if got := markers(m, paneMine); got != " −" {
		t.Fatalf("markers after an error, preview, and fetch = %q, want #1 gone", got)
	}
}

func TestScopeCanBeChosenFromAPreview(t *testing.T) {
	m := testModel(testPreferences(t), 140, 30)
	one := changePR(1, "acme/a")
	generation := startPreviewedFetch(t, m)
	m.Update(previewMsg{generation: generation, snapshot: previewOf(one)})
	press(m, tea.Key{Code: 'j', Text: "j"})
	press(m, tea.Key{Code: tea.KeyEnter})
	if !m.scopeChosen {
		t.Fatal("scope prompt ignored keys during the preview")
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, one.URL) {
		t.Fatalf("preview rows not shown after choosing a scope:\n%s", view)
	}

	// A picker opened from the preview closes when the fetch fails.
	m = testModel(testPreferences(t), 140, 30)
	generation = startPreviewedFetch(t, m)
	m.Update(previewMsg{generation: generation, snapshot: previewOf(one)})
	press(m, tea.Key{Code: tea.KeyEnter})
	if m.picker == nil {
		t.Fatal("picker did not open from the preview")
	}
	m.Update(fetchFinishedMsg{err: errors.New("network down")})
	if m.picker != nil {
		t.Fatal("picker left open over the error")
	}
}

func TestCursorOnTheFirstPreviewRowStaysOnTop(t *testing.T) {
	pr := func(number int, state string, created int) github.PullRequest {
		p := changePR(number, "acme/a")
		p.MergeState, p.CreatedAt = state, changeTime.Add(time.Duration(created)*time.Hour)
		return p
	}
	oldest, ready, other := pr(1, "BLOCKED", 0), pr(2, "CLEAN", 5), pr(3, "BLOCKED", 9)

	m := previewModel(t)
	generation := startPreviewedFetch(t, m)
	m.Update(previewMsg{generation: generation, snapshot: previewOf(other, ready, oldest)})
	if selected, _ := m.selectedPR(); selected.Number != 1 {
		t.Fatalf("preview selected #%d, want the oldest", selected.Number)
	}
	updateSnapshot(m, "alice", other, ready, oldest)
	if selected, _ := m.selectedPR(); selected.Number != 2 {
		t.Fatalf("after details selected #%d, want the ready #2 on top", selected.Number)
	}

	// A row picked during the preview stays selected.
	m = previewModel(t)
	generation = startPreviewedFetch(t, m)
	m.Update(previewMsg{generation: generation, snapshot: previewOf(other, ready, oldest)})
	press(m, tea.Key{Code: 'G', Text: "G"})
	updateSnapshot(m, "alice", other, ready, oldest)
	if selected, _ := m.selectedPR(); selected.Number != 3 {
		t.Fatalf("after details selected #%d, want #3 picked during the preview", selected.Number)
	}
}
