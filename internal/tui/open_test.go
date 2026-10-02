package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/DragosMocrii/prpr/internal/github"
)

// recordOpens makes the model record the URLs it opens, failing with err.
func recordOpens(m *model, err error) *[]string {
	var opened []string
	m.openBrowser = func(_ context.Context, url string) error {
		opened = append(opened, url)
		return err
	}
	return &opened
}

func TestOpenAndCopyTargetTheFocusedPaneSelection(t *testing.T) {
	m := newPaneModel(t, 120, 30, manyPRs(3), reviewPRs(2))
	opened := recordOpens(m, nil)
	press(m, tea.Key{Code: 'j', Text: "j"})
	cmd := m.handleKey(tea.KeyPressMsg{Code: 'o', Text: "o"})
	if cmd == nil || !strings.Contains(statusText(m), "Opening #2") {
		t.Fatalf("o: cmd %v, status %q", cmd, statusText(m))
	}
	m.Update(cmd())
	if want := manyPRs(3)[1].URL; len(*opened) != 1 || (*opened)[0] != want {
		t.Fatalf("opened %v, want %s", *opened, want)
	}
	if strings.Contains(statusText(m), "Opening") {
		t.Fatalf("notice kept after a successful open: %q", statusText(m))
	}

	press(m, tea.Key{Code: tea.KeyTab})
	press(m, tea.Key{Code: 'j', Text: "j"})
	cmd = m.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil || fmt.Sprint(cmd()) != reviewPRs(2)[1].URL {
		t.Fatalf("y copied %v, want %s", cmd, reviewPRs(2)[1].URL)
	}
	if !strings.Contains(statusText(m), "Copied the URL of #101") {
		t.Fatalf("copy status %q", statusText(m))
	}
	press(m, tea.Key{Code: 'k', Text: "k"})
	if strings.Contains(statusText(m), "Copied") {
		t.Fatalf("notice kept after the next key: %q", statusText(m))
	}
}

func TestOpenFailureIsReportedAndOlderResultsKeepNewerNotices(t *testing.T) {
	m := newPaneModel(t, 120, 30, manyPRs(2), nil)
	recordOpens(m, errors.New("Could not open the browser: exit status 1: no browser\nfound"))
	failing := m.handleKey(tea.KeyPressMsg{Code: 'o', Text: "o"})
	m.openBrowser = func(context.Context, string) error { return nil }
	succeeding := m.handleKey(tea.KeyPressMsg{Code: 'o', Text: "o"})
	ok := succeeding()
	m.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.Update(ok)
	if !strings.Contains(statusText(m), "Copied") {
		t.Fatalf("an older open cleared a newer notice: %q", statusText(m))
	}
	m.Update(failing())
	if got := statusText(m); !strings.Contains(got, "Could not open the browser") || !strings.Contains(got, "no browser found") {
		t.Fatalf("failure status %q", got)
	}
}

func TestOpenAndCopyRefuseUnsafeURLsAndNeedARow(t *testing.T) {
	m := newPaneModel(t, 120, 30, []github.PullRequest{{Number: 7, Repository: "acme/a", URL: "https://evil.test/pull/7"}}, nil)
	opened := recordOpens(m, nil)
	for _, key := range []tea.Key{{Code: 'o', Text: "o"}, {Code: 'y', Text: "y"}} {
		if cmd := m.handleKey(tea.KeyPressMsg(key)); cmd != nil {
			t.Fatalf("%s ran a command for an unsafe URL", key.Text)
		}
		if !strings.Contains(statusText(m), "#7 has no GitHub link") {
			t.Fatalf("%s status %q", key.Text, statusText(m))
		}
	}
	if len(*opened) != 0 {
		t.Fatalf("opened %v", *opened)
	}

	empty := newPaneModel(t, 120, 30, nil, nil)
	recordOpens(empty, nil)
	empty.syncKeys()
	if empty.keys.Open.Enabled() || empty.keys.CopyURL.Enabled() {
		t.Fatal("open and copy enabled without rows")
	}
	if cmd := empty.handleKey(tea.KeyPressMsg{Code: 'o', Text: "o"}); cmd != nil {
		t.Fatal("o ran a command without rows")
	}
}

func TestOpenWorksOnGoneRows(t *testing.T) {
	m := newPaneModel(t, 120, 30, manyPRs(2), nil)
	opened := recordOpens(m, nil)
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: manyPRs(1)}})
	press(m, tea.Key{Code: 'G', Text: "G"})
	if pr, ok := m.selectedPR(); !ok || pr.Number != manyPRs(2)[1].Number {
		t.Fatalf("selected %+v, %v; want the gone row", pr, ok)
	}
	m.Update(m.handleKey(tea.KeyPressMsg{Code: 'o', Text: "o"})())
	if len(*opened) != 1 || (*opened)[0] != manyPRs(2)[1].URL {
		t.Fatalf("opened %v", *opened)
	}
}
