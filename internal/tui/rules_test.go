package tui

import (
	"image/color"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/readiness"
)

// approvalRules ready authored pull requests with one approval.
func approvalRules() readiness.Rules {
	return readiness.Rules{Default: readiness.Rule{MergeButton: true, Approvals: 1}}
}

func cleanPR(number, approvals int) github.PullRequest {
	pr := changePR(number, "acme/a")
	pr.MergeState, pr.Approvals = "CLEAN", approvals
	return pr
}

func TestRulesDecideReadinessOfAuthoredPullRequests(t *testing.T) {
	// Unapproved first: only the rules can put the approved one ahead.
	unapproved, approved := cleanPR(1, 0), cleanPR(2, 1)
	review := changePR(9, "acme/b")
	review.MergeState = "CLEAN"
	m := changesModel(t)
	fetch(m, []github.PullRequest{unapproved, approved}, []github.PullRequest{review})
	if counts := categoryCounts(m); counts[0] != 2 {
		t.Fatalf("ready count with GitHub's rule = %d", counts[0])
	}
	m.applyRules(approvalRules())
	if counts := categoryCounts(m); counts[0] != 1 {
		t.Fatalf("ready count with an approval required = %d", counts[0])
	}
	if got := shownNumbers(m, paneMine); len(got) != 2 || got[0] != 2 {
		t.Fatalf("authored order = %v, want the approved one first", got)
	}
	if merge := cellOf(t, m, paneMine, 1, m.icons.header("Merge")); merge != coloredIcon(m.icons.check, "3") {
		t.Fatalf("GitHub-only merge cell = %q, want the yellow check", merge)
	}
	if merge := cellOf(t, m, paneMine, 0, m.icons.header("Merge")); merge != coloredIcon(m.icons.check, "2") {
		t.Fatalf("ready merge cell = %q, want the green check", merge)
	}
	// The ready filter follows the rules for authored rows; review rows keep
	// GitHub's answer, since their rule fields are not read.
	m.toggleQuick(quickReady)
	if mine, reviews := shownNumbers(m, paneMine), shownNumbers(m, paneReview); len(mine) != 1 || mine[0] != 2 || len(reviews) != 1 {
		t.Fatalf("ready filter: mine %v, reviews %v", mine, reviews)
	}
	m.toggleQuick(quickReady)
	// The details name the unmet condition.
	moveCursor(&m.panes[paneMine].table, 1)
	m.details = true
	if text := ansi.Strip(detailsText(m)); !strings.Contains(text, "Not ready by your default rules") || !strings.Contains(text, "At least 1 approval: has 0") {
		t.Fatalf("details = %q", text)
	}
}

func TestOwnerRulesReplaceTheDefault(t *testing.T) {
	m := changesModel(t)
	other := cleanPR(3, 0)
	other.Repository = "other/x"
	fetch(m, []github.PullRequest{cleanPR(1, 0), other}, nil)
	rules := readiness.DefaultRules()
	rules.Owners = map[string]readiness.Rule{"acme": {MergeButton: true, Approvals: 1}}
	m.applyRules(rules)
	if !m.ready(&m.snapshot.PullRequests[1]) || m.ready(&m.snapshot.PullRequests[0]) {
		t.Fatal("acme's rule did not apply to acme alone")
	}
}

func TestChangingRulesNeverAlerts(t *testing.T) {
	m := notifyModel(t, "")
	m.applyRules(approvalRules())
	fetch(m, []github.PullRequest{cleanPR(1, 0)}, nil)
	// Loosening the rule makes it ready now, without an alert.
	m.applyRules(readiness.DefaultRules())
	if sent := fetch(m, []github.PullRequest{cleanPR(1, 0)}, nil); len(sent) != 0 {
		t.Fatalf("a rule change alerted: %q", sent)
	}
	// A rule that reads new fields drops the baseline, so the fetch that
	// reads them does not alert either.
	rules := readiness.Rules{Default: readiness.Rule{MergeButton: true, CodeOwners: true}}
	m.applyRules(rules)
	if m.ready(&m.snapshot.PullRequests[0]) {
		t.Fatal("ready before code owners were read")
	}
	known := cleanPR(1, 0)
	known.CodeOwnersKnown = true
	if sent := fetch(m, []github.PullRequest{known}, nil); len(sent) != 0 {
		t.Fatalf("the first fetch with code owners alerted: %q", sent)
	}
	// Once known, a real change still alerts.
	pending := known
	pending.PendingCodeOwners = []string{"@acme/core"}
	fetch(m, []github.PullRequest{pending}, nil)
	if sent := fetch(m, []github.PullRequest{known}, nil); len(sent) != 1 || !strings.Contains(sent[0], "ready to merge") {
		t.Fatalf("code owners approving: %q", sent)
	}
}

// drive runs cmd and feeds the messages it sends back into m, as the
// program would. Commands that wait, such as cursor blinks, are dropped.
func drive(m *model, cmd tea.Cmd) {
	for depth := 0; cmd != nil && depth < 50; depth++ {
		var next []tea.Cmd
		for _, msg := range runQuick(cmd) {
			if _, size := msg.(tea.WindowSizeMsg); size {
				continue
			}
			_, more := m.Update(msg)
			next = append(next, more)
		}
		cmd = tea.Batch(next...)
	}
}

// runQuick runs cmd, unpacking batches and sequences, and keeps the
// messages that arrive within a moment.
func runQuick(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(20 * time.Millisecond):
		return nil
	}
	if msg == nil {
		return nil
	}
	if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeOf(tea.Cmd(nil)) {
		var msgs []tea.Msg
		for i := range v.Len() {
			msgs = append(msgs, runQuick(v.Index(i).Interface().(tea.Cmd))...)
		}
		return msgs
	}
	return []tea.Msg{msg}
}

func pressKey(m *model, k tea.Key) {
	_, cmd := m.Update(tea.KeyPressMsg(k))
	drive(m, cmd)
}

func TestRulesEditorSavesAnOwnersRuleAndEscCancels(t *testing.T) {
	m := changesModel(t)
	fetch(m, []github.PullRequest{cleanPR(1, 0)}, nil)
	pressKey(m, tea.Key{Code: ',', Text: ","})
	if m.rulesEditor == nil || !strings.Contains(ansi.Strip(m.View().Content), "Ready-to-merge rules") {
		t.Fatal(", did not open the rule editor")
	}
	if m.View().MouseMode != tea.MouseModeNone {
		t.Fatal("the editor asked for the mouse")
	}
	// List keys go to the form.
	pressKey(m, tea.Key{Code: 'q', Text: "q"})
	pressKey(m, tea.Key{Code: 'i', Text: "i"})
	if m.rulesEditor == nil || m.icons.nerd {
		t.Fatal("a list key acted in the editor")
	}
	pressKey(m, tea.Key{Code: tea.KeyEscape})
	if m.rulesEditor != nil || m.preferences.Rules().Customized() {
		t.Fatal("Esc did not cancel without saving")
	}

	// Choose acme, the owner of the listed pull request.
	pressKey(m, tea.Key{Code: ',', Text: ","})
	pressKey(m, tea.Key{Code: tea.KeyDown})
	pressKey(m, tea.Key{Code: tea.KeyEnter})
	if m.rulesEditor == nil || m.rulesEditor.stage != rulesEditing || m.rulesEditor.owner != "acme" {
		t.Fatalf("after choosing acme: %+v", m.rulesEditor)
	}
	// Keep the merge button, then require one approval and save.
	pressKey(m, tea.Key{Code: tea.KeyEnter})
	pressKey(m, tea.Key{Code: tea.KeyBackspace})
	pressKey(m, tea.Key{Code: '1', Text: "1"})
	// The note takes no focus: Enter moves to Save, and Enter saves.
	pressKey(m, tea.Key{Code: tea.KeyEnter})
	pressKey(m, tea.Key{Code: tea.KeyEnter})
	if m.rulesEditor != nil {
		t.Fatalf("the editor is still open at stage %d", m.rulesEditor.stage)
	}
	rule, own := m.preferences.Rules().For("acme/a")
	if !own || rule != (readiness.Rule{MergeButton: true, Approvals: 1}) {
		t.Fatalf("saved acme rule = %+v, %t", rule, own)
	}
	if m.ready(&m.snapshot.PullRequests[0]) {
		t.Fatal("the saved rule is not in use")
	}
}

func TestRulesEditorNamesAnotherOwnerAndRemovesItsRules(t *testing.T) {
	m := changesModel(t)
	fetch(m, []github.PullRequest{cleanPR(1, 0)}, nil)
	typeKeys := func(text string) {
		for _, r := range text {
			pressKey(m, tea.Key{Code: r, Text: string(r)})
		}
	}
	// Default, acme, Another owner….
	pressKey(m, tea.Key{Code: ',', Text: ","})
	pressKey(m, tea.Key{Code: tea.KeyDown})
	pressKey(m, tea.Key{Code: tea.KeyDown})
	pressKey(m, tea.Key{Code: tea.KeyEnter})
	typeKeys("bad owner")
	pressKey(m, tea.Key{Code: tea.KeyEnter})
	if m.rulesEditor == nil || m.rulesEditor.stage != rulesNaming {
		t.Fatal("an invalid owner name was accepted")
	}
	for range len("bad owner") {
		pressKey(m, tea.Key{Code: tea.KeyBackspace})
	}
	typeKeys("Globex")
	pressKey(m, tea.Key{Code: tea.KeyEnter})
	for range 3 {
		pressKey(m, tea.Key{Code: tea.KeyEnter})
	}
	if _, own := m.preferences.Rules().For("globex/x"); !own || m.rulesEditor != nil {
		t.Fatalf("globex was not saved: editor %+v", m.rulesEditor)
	}
	// globex is now listed after acme; remove its rules.
	pressKey(m, tea.Key{Code: ',', Text: ","})
	pressKey(m, tea.Key{Code: tea.KeyDown})
	pressKey(m, tea.Key{Code: tea.KeyDown})
	pressKey(m, tea.Key{Code: tea.KeyEnter})
	if m.rulesEditor == nil || m.rulesEditor.owner != "globex" {
		t.Fatalf("editing %+v, want globex", m.rulesEditor)
	}
	pressKey(m, tea.Key{Code: tea.KeyEnter})
	pressKey(m, tea.Key{Code: tea.KeyEnter})
	pressKey(m, tea.Key{Code: tea.KeyDown})
	pressKey(m, tea.Key{Code: tea.KeyEnter})
	if _, own := m.preferences.Rules().For("globex/x"); own || m.rulesEditor != nil {
		t.Fatal("globex's rules were not removed")
	}
}

func TestRuleFieldsTurningKnownMarkNothing(t *testing.T) {
	m := changesModel(t)
	fetch(m, []github.PullRequest{cleanPR(1, 0)}, nil)
	m.changes[paneMine].clear()
	m.applyRules(readiness.Rules{Default: readiness.Rule{MergeButton: true, CodeOwners: true, ResolvedThreads: true, RequiredChecks: true}})
	known := cleanPR(1, 0)
	known.CodeOwnersKnown, known.ThreadsKnown, known.RequiredChecks = true, true, "SUCCESS"
	fetch(m, []github.PullRequest{known}, nil)
	if got := markers(m, paneMine); got != " " {
		t.Fatalf("markers after the rule fields were first read = %q", got)
	}
	pending := known
	pending.PendingCodeOwners = []string{"@acme/core"}
	fetch(m, []github.PullRequest{pending}, nil)
	if got := markers(m, paneMine); got != "▼" {
		t.Fatalf("markers after a code owner was requested = %q", got)
	}
}

func TestRulesSavedDuringAFetchFetchAgainAfterIt(t *testing.T) {
	m := changesModel(t)
	fetch(m, []github.PullRequest{cleanPR(1, 0)}, nil)
	m.loading = true
	if cmd := m.applyRules(readiness.Rules{Default: readiness.Rule{CodeOwners: true}}); cmd != nil {
		t.Fatal("a second fetch started while one was running")
	}
	m.Update(fetchFinishedMsg{snapshot: github.Snapshot{Login: "alice", PullRequests: []github.PullRequest{cleanPR(1, 0)}}})
	if !m.loading || m.refetchForRules {
		t.Fatal("the fetch for the new rule's fields did not start after the running one")
	}
}

func TestRulesEditorFollowsResizes(t *testing.T) {
	m := changesModel(t)
	fetch(m, []github.PullRequest{cleanPR(1, 0)}, nil)
	pressKey(m, tea.Key{Code: ',', Text: ","})
	pressKey(m, tea.Key{Code: tea.KeyEnter})
	m.Update(tea.WindowSizeMsg{Width: 44, Height: 12})
	lines := strings.Split(m.View().Content, "\n")
	if len(lines) > 12 || !strings.Contains(ansi.Strip(lines[0]), "prpr") || !strings.Contains(ansi.Strip(lines[len(lines)-1]), "esc") {
		t.Fatalf("editor at 44x12:\n%s", ansi.Strip(m.View().Content))
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > 44 {
			t.Fatalf("line wider than 44: %q", ansi.Strip(line))
		}
	}
}

func TestRulesEditorTextContrastsWithTheBackground(t *testing.T) {
	bright := func(c color.Color) bool {
		r, g, b, _ := c.RGBA()
		return 0.2126*float64(r)+0.7152*float64(g)+0.0722*float64(b) > 0.5*0xffff
	}
	for _, dark := range []bool{true, false} {
		styles := rulesTheme(dark)
		for name, fg := range map[string]color.Color{
			"option":          styles.Focused.Option.GetForeground(),
			"unselected":      styles.Focused.UnselectedOption.GetForeground(),
			"placeholder":     styles.Focused.TextInput.Placeholder.GetForeground(),
			"blurred options": styles.Blurred.UnselectedOption.GetForeground(),
		} {
			if bright(fg) != dark {
				t.Errorf("dark background %t: %s text is bright %t", dark, name, bright(fg))
			}
		}
		if bg := styles.Focused.BlurredButton.GetBackground(); bright(bg) == dark {
			t.Errorf("dark background %t: blurred button background is bright %t", dark, bright(bg))
		}
	}
}
