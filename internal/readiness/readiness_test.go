package readiness

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/DragosMocrii/prpr/internal/github"
)

// clean is an authored pull request GitHub would merge, with every rule
// field read and nothing outstanding.
func clean() github.PullRequest {
	return github.PullRequest{
		Repository: "acme/api", Mergeable: "MERGEABLE", MergeState: "CLEAN", Checks: "SUCCESS",
		CodeOwnersKnown: true, ThreadsKnown: true, RequiredChecks: "SUCCESS",
		Bots: []github.BotReview{{Name: "Copilot", State: github.BotPassed}},
	}
}

func TestDefaultRuleIsGitHubsMergeButton(t *testing.T) {
	for _, state := range []string{"CLEAN", "HAS_HOOKS", "UNSTABLE", "BLOCKED", "BEHIND", "DIRTY", "UNKNOWN", ""} {
		pr := clean()
		pr.MergeState = state
		want := state == "CLEAN" || state == "HAS_HOOKS" || state == "UNSTABLE"
		if got := Default.Evaluate(&pr, false); got.Ready != want {
			t.Errorf("%q: ready = %t, want %t", state, got.Ready, want)
		}
	}
	for name, edit := range map[string]func(*github.PullRequest){
		"draft":       func(pr *github.PullRequest) { pr.Draft = true },
		"conflicting": func(pr *github.PullRequest) { pr.Mergeable = "CONFLICTING" },
	} {
		pr := clean()
		edit(&pr)
		// Fixed blockers hold even with every condition off.
		if got := (Rule{}).Evaluate(&pr, false); got.Ready || !got.Known {
			t.Errorf("%s: %+v, want known and not ready", name, got)
		}
	}
}

func TestEachConditionBlocksUntilItsDataSaysOtherwise(t *testing.T) {
	every := Rule{MergeButton: true, RequiredChecks: true, AllChecks: true, Approvals: 1,
		NoChangesRequested: true, CodeOwners: true, ResolvedThreads: true, BotsClear: true}
	ready := clean()
	ready.Approvals = 1
	if got := every.Evaluate(&ready, true); !got.Ready || !got.Known || len(got.Checks) != 8 {
		t.Fatalf("every condition met: %+v", got)
	}
	cases := []struct {
		name   string
		edit   func(*github.PullRequest)
		status Status
		detail string
	}{
		{"merge button blocked", func(pr *github.PullRequest) { pr.MergeState = "BLOCKED" }, Unmet, "blocked"},
		{"required check failed", func(pr *github.PullRequest) {
			pr.RequiredChecks, pr.RequiredNotPassed = "FAILURE", []string{"build"}
		}, Unmet, "build"},
		{"required checks not read", func(pr *github.PullRequest) { pr.RequiredChecks = "" }, Unknown, ""},
		{"no checks", func(pr *github.PullRequest) { pr.Checks = "" }, Unmet, "no checks"},
		{"too few approvals", func(pr *github.PullRequest) { pr.Approvals = 0 }, Unmet, "has 0"},
		{"changes requested", func(pr *github.PullRequest) { pr.ChangesRequested = 1 }, Unmet, ""},
		{"code owner pending", func(pr *github.PullRequest) {
			pr.PendingCodeOwners = []string{"@acme/core", ""}
		}, Unmet, "@acme/core, an owner GitHub does not name"},
		{"code owners not read", func(pr *github.PullRequest) { pr.CodeOwnersKnown = false }, Unknown, ""},
		{"unresolved thread", func(pr *github.PullRequest) { pr.UnresolvedThreads = 2 }, Unmet, "2 unresolved"},
		{"threads not read", func(pr *github.PullRequest) { pr.ThreadsKnown = false }, Unknown, ""},
		{"bot concerns", func(pr *github.PullRequest) {
			pr.Bots = []github.BotReview{{Name: "Copilot", State: github.BotConcerns}}
		}, Unmet, "Copilot"},
	}
	for _, c := range cases {
		pr := ready
		c.edit(&pr)
		got := every.Evaluate(&pr, true)
		if got.Ready {
			t.Errorf("%s: ready", c.name)
		}
		var found *Check
		for i := range got.Checks {
			if got.Checks[i].Status != Met {
				found = &got.Checks[i]
			}
		}
		if found == nil || found.Status != c.status || !strings.Contains(found.Detail, c.detail) {
			t.Errorf("%s: checks %+v, want one %v with %q", c.name, got.Checks, c.status, c.detail)
		}
		// Only an undecided condition leaves readiness unknown.
		if got.Known != (c.status == Unmet) {
			t.Errorf("%s: known = %t", c.name, got.Known)
		}
	}
	// Without bots configured the bot condition is left out.
	if got := every.Evaluate(&ready, false); len(got.Checks) != 7 {
		t.Errorf("checks without bots = %+v", got.Checks)
	}
}

func TestReadyWithoutTheMergeButtonStillNeedsAKnownMergeState(t *testing.T) {
	rule := Rule{Approvals: 1}
	pr := clean()
	pr.MergeState, pr.Approvals = "BLOCKED", 1
	if got := rule.Evaluate(&pr, false); !got.Ready {
		t.Fatalf("a bypassable block with the approval: %+v", got)
	}
	pr.MergeState = "UNKNOWN"
	if got := rule.Evaluate(&pr, false); got.Ready || got.Known {
		t.Fatalf("an uncomputed merge state: %+v", got)
	}
}

func TestRulesPickTheOwnersRuleIgnoringCase(t *testing.T) {
	strict := Rule{MergeButton: true, Approvals: 2}
	rules := Rules{Default: Default, Owners: map[string]Rule{"acme": strict}}
	if rule, own := rules.For("ACME/api"); !own || rule != strict {
		t.Fatalf("acme rule = %+v, %t", rule, own)
	}
	if rule, own := rules.For("other/api"); own || rule != Default {
		t.Fatalf("other rule = %+v, %t", rule, own)
	}
	if !rules.Customized() || DefaultRules().Customized() {
		t.Fatal("Customized does not follow the rules")
	}
	if needs := rules.Needs(); needs != (github.Needs{}) {
		t.Fatalf("needs = %+v, want none", needs)
	}
	rules.Owners["acme"] = Rule{CodeOwners: true, RequiredChecks: true}
	if needs := rules.Needs(); !needs.CodeOwners || !needs.RequiredChecks || needs.Threads {
		t.Fatalf("needs = %+v", needs)
	}
}

func TestRulesJSONKeepsDefaultsForLeftOutConditionsAndRejectsUnknownOnes(t *testing.T) {
	var rules Rules
	if err := json.Unmarshal([]byte(`{"default":{"approvals":1},"owners":{"Acme":{"merge_button":false,"code_owners":true}}}`), &rules); err != nil {
		t.Fatal(err)
	}
	if rules.Default != (Rule{MergeButton: true, Approvals: 1}) {
		t.Fatalf("default = %+v, want the merge button kept", rules.Default)
	}
	if rule, own := rules.For("acme/api"); !own || rule != (Rule{CodeOwners: true}) {
		t.Fatalf("acme = %+v, %t", rule, own)
	}
	data, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	var again Rules
	if err := json.Unmarshal(data, &again); err != nil || again.Default != rules.Default || again.Owners["acme"] != rules.Owners["acme"] {
		t.Fatalf("round trip %s: %+v, %v", data, again, err)
	}
	for _, bad := range []string{
		`{"default":{"merge_buton":true}}`,
		`{"default":{"approvals":11}}`,
		`{"owners":{"bad owner":{}}}`,
		`{"owners":{"acme":{},"ACME":{}}}`,
		`{"defaults":{}}`,
		`[]`,
	} {
		if err := json.Unmarshal([]byte(bad), &rules); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
}
