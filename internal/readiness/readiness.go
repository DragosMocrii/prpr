// Package readiness decides whether an authored pull request is ready to
// merge by the user's rules: a default rule, and rules of their own for
// repository owners.
package readiness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/DragosMocrii/prpr/internal/github"
)

// Rule is the conditions a pull request must all meet to be ready to merge.
// Drafts, conflicts, and a merge state GitHub has not computed always block.
type Rule struct {
	// MergeButton requires GitHub to allow merging now (CLEAN, HAS_HOOKS,
	// or UNSTABLE), which follows branch protection for the viewer.
	MergeButton bool `json:"merge_button"`
	// RequiredChecks requires the head commit's required checks to pass.
	RequiredChecks bool `json:"required_checks"`
	// AllChecks requires every head-commit check to pass.
	AllChecks bool `json:"all_checks"`
	// Approvals is the fewest approving reviews; 0 requires none.
	Approvals int `json:"approvals"`
	// NoChangesRequested requires no latest review to request changes.
	NoChangesRequested bool `json:"no_changes_requested"`
	// CodeOwners requires no code-owner review request to be pending.
	CodeOwners bool `json:"code_owners"`
	// ResolvedThreads requires every review thread to be resolved.
	ResolvedThreads bool `json:"resolved_threads"`
	// BotsClear requires no configured bot to have open concerns or a
	// running or failed check.
	BotsClear bool `json:"bots_clear"`
}

// Default is GitHub's own answer: the merge button alone.
var Default = Rule{MergeButton: true}

// MaxApprovals is the most approvals a rule can require.
const MaxApprovals = 10

// Rules is the default rule and the rules of repository owners that have
// their own, which replace the default whole.
type Rules struct {
	Default Rule
	// Owners maps a lower-case repository owner to its rule.
	Owners map[string]Rule
}

// DefaultRules is the default rule alone.
func DefaultRules() Rules { return Rules{Default: Default} }

// Clone copies the rules, so an edit leaves the original alone.
func (r Rules) Clone() Rules {
	return Rules{Default: r.Default, Owners: maps.Clone(r.Owners)}
}

// Owner is a repository's owner, lower-cased.
func Owner(repository string) string {
	owner, _, _ := strings.Cut(repository, "/")
	return strings.ToLower(owner)
}

// For is the rule of a repository, and whether its owner has its own.
func (r Rules) For(repository string) (Rule, bool) {
	rule, own := r.Owners[Owner(repository)]
	if !own {
		return r.Default, false
	}
	return rule, true
}

// Customized reports whether any rule differs from Default.
func (r Rules) Customized() bool {
	if r.Default != Default {
		return true
	}
	for _, rule := range r.Owners {
		if rule != Default {
			return true
		}
	}
	return false
}

// Needs are the fields some rule reads that fetches leave out otherwise.
func (r Rules) Needs() github.Needs {
	var needs github.Needs
	add := func(rule Rule) {
		needs.CodeOwners = needs.CodeOwners || rule.CodeOwners
		needs.Threads = needs.Threads || rule.ResolvedThreads
		needs.RequiredChecks = needs.RequiredChecks || rule.RequiredChecks
	}
	add(r.Default)
	for _, rule := range r.Owners {
		add(rule)
	}
	return needs
}

// ValidOwner reports whether name can be a GitHub user or organization.
func ValidOwner(name string) bool {
	if name == "" || len(name) > 39 || strings.HasPrefix(name, "-") {
		return false
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}
	return true
}

type rulesJSON struct {
	Default json.RawMessage            `json:"default,omitempty"`
	Owners  map[string]json.RawMessage `json:"owners,omitempty"`
}

// MarshalJSON writes every condition of every rule.
func (r Rules) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Default Rule            `json:"default"`
		Owners  map[string]Rule `json:"owners,omitempty"`
	}{r.Default, r.Owners})
}

// UnmarshalJSON reads rules. A condition a rule leaves out keeps its
// default; an unknown condition is an error, since ignoring it would make
// the rule looser than written.
func (r *Rules) UnmarshalJSON(data []byte) error {
	var raw rulesJSON
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return fmt.Errorf("rules must be an object with default and owners: %w", err)
	}
	rules := DefaultRules()
	if raw.Default != nil {
		rule, err := decodeRule(raw.Default)
		if err != nil {
			return fmt.Errorf("default rule: %w", err)
		}
		rules.Default = rule
	}
	for owner, value := range raw.Owners {
		if !ValidOwner(owner) {
			return fmt.Errorf("invalid owner %q", owner)
		}
		key := strings.ToLower(owner)
		if _, exists := rules.Owners[key]; exists {
			return fmt.Errorf("owner %q is listed twice", owner)
		}
		rule, err := decodeRule(value)
		if err != nil {
			return fmt.Errorf("rule of %q: %w", owner, err)
		}
		if rules.Owners == nil {
			rules.Owners = make(map[string]Rule)
		}
		rules.Owners[key] = rule
	}
	*r = rules
	return nil
}

func decodeRule(data []byte) (Rule, error) {
	rule := Default
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&rule); err != nil {
		return Rule{}, err
	}
	if rule.Approvals < 0 || rule.Approvals > MaxApprovals {
		return Rule{}, fmt.Errorf("approvals must be 0 to %d", MaxApprovals)
	}
	return rule, nil
}

// Status is whether a condition holds.
type Status int

const (
	Met Status = iota
	Unmet
	// Unknown means the data to decide was not fetched or was cut short.
	Unknown
)

// Check is one condition of a rule, as a pull request meets it.
type Check struct {
	Condition string
	Status    Status
	// Detail says why a condition is unmet or unknown.
	Detail string
}

// Result is a pull request against a rule.
type Result struct {
	Ready bool
	// Known is false when nothing has failed but something could not be
	// decided, or GitHub has not computed the merge state, so a change of
	// readiness cannot be told.
	Known bool
	// Checks are the rule's conditions, in the order the settings list them.
	Checks []Check
}

// blocked reports whether a pull request cannot be ready whatever the rule.
func blocked(pr *github.PullRequest) bool {
	return pr.Draft || pr.Mergeable == "CONFLICTING" || pr.MergeState == "DIRTY"
}

func unknownMergeState(pr *github.PullRequest) bool {
	return pr.Mergeable != "CONFLICTING" && (pr.MergeState == "" || pr.MergeState == "UNKNOWN")
}

// Evaluate checks a pull request against the rule. bots is false when no
// bots are configured, which leaves BotsClear out.
func (rule Rule) Evaluate(pr *github.PullRequest, bots bool) Result {
	var checks []Check
	add := func(condition string, status Status, detail string) {
		checks = append(checks, Check{condition, status, detail})
	}
	if rule.MergeButton {
		switch pr.MergeState {
		case "CLEAN", "HAS_HOOKS", "UNSTABLE":
			add(MergeButtonLabel, Met, "")
		case "", "UNKNOWN":
			add(MergeButtonLabel, Unknown, "GitHub is still computing the merge state")
		default:
			add(MergeButtonLabel, Unmet, "GitHub says "+strings.ToLower(strings.ReplaceAll(pr.MergeState, "_", " ")))
		}
	}
	if rule.RequiredChecks {
		switch pr.RequiredChecks {
		case "SUCCESS":
			add(RequiredChecksLabel, Met, "")
		case "PENDING":
			add(RequiredChecksLabel, Unmet, "running: "+names(pr.RequiredNotPassed))
		case "FAILURE":
			add(RequiredChecksLabel, Unmet, "not passed: "+names(pr.RequiredNotPassed))
		default:
			detail := "not loaded"
			if blocked(pr) {
				// Fetches skip pull requests that cannot be ready.
				detail = "not read for drafts and conflicts"
			}
			add(RequiredChecksLabel, Unknown, detail)
		}
	}
	if rule.AllChecks {
		switch pr.Checks {
		case "SUCCESS":
			add(AllChecksLabel, Met, "")
		case "":
			add(AllChecksLabel, Unmet, "no checks reported")
		case "PENDING", "EXPECTED":
			add(AllChecksLabel, Unmet, "checks are running")
		default:
			add(AllChecksLabel, Unmet, "checks are failing")
		}
	}
	if rule.Approvals > 0 {
		label := ApprovalsLabel(rule.Approvals)
		if pr.Approvals >= rule.Approvals {
			add(label, Met, "")
		} else {
			add(label, Unmet, "has "+strconv.Itoa(pr.Approvals))
		}
	}
	if rule.NoChangesRequested {
		if pr.ChangesRequested == 0 && pr.ReviewDecision != "CHANGES_REQUESTED" {
			add(NoChangesRequestedLabel, Met, "")
		} else {
			add(NoChangesRequestedLabel, Unmet, "changes were requested")
		}
	}
	if rule.CodeOwners {
		switch {
		case !pr.CodeOwnersKnown:
			add(CodeOwnersLabel, Unknown, "not loaded")
		case len(pr.PendingCodeOwners) == 0:
			add(CodeOwnersLabel, Met, "")
		default:
			add(CodeOwnersLabel, Unmet, "waiting on "+names(pr.PendingCodeOwners))
		}
	}
	if rule.ResolvedThreads {
		switch {
		case !pr.ThreadsKnown:
			add(ResolvedThreadsLabel, Unknown, "not loaded")
		case pr.UnresolvedThreads == 0:
			add(ResolvedThreadsLabel, Met, "")
		default:
			add(ResolvedThreadsLabel, Unmet, strconv.Itoa(pr.UnresolvedThreads)+" unresolved")
		}
	}
	if rule.BotsClear && bots {
		var busy []string
		for _, bot := range pr.Bots {
			switch bot.State {
			case github.BotConcerns:
				busy = append(busy, bot.Name+" has concerns")
			case github.BotRunning:
				busy = append(busy, bot.Name+" is running")
			case github.BotFailed:
				busy = append(busy, bot.Name+" failed")
			}
		}
		switch {
		case pr.Bots == nil:
			add(BotsClearLabel, Unknown, "not loaded")
		case len(busy) == 0:
			add(BotsClearLabel, Met, "")
		default:
			add(BotsClearLabel, Unmet, strings.Join(busy, ", "))
		}
	}

	result := Result{Checks: checks}
	unmet, unknown := false, false
	for _, check := range checks {
		unmet = unmet || check.Status == Unmet
		unknown = unknown || check.Status == Unknown
	}
	fixed := blocked(pr)
	result.Ready = !fixed && !unmet && !unknown && !unknownMergeState(pr)
	result.Known = !unknownMergeState(pr) && (fixed || unmet || !unknown)
	return result
}

// names lists names, calling one GitHub does not name an unnamed owner.
func names(list []string) string {
	shown := make([]string, len(list))
	for i, name := range list {
		shown[i] = name
		if name == "" {
			shown[i] = "an owner GitHub does not name"
		}
	}
	if len(shown) == 0 {
		return "unnamed checks"
	}
	return strings.Join(shown, ", ")
}

// Condition labels, as the settings and the details screen show them.
const (
	MergeButtonLabel        = "GitHub's merge button is green"
	RequiredChecksLabel     = "Required checks passed"
	AllChecksLabel          = "All checks passed"
	NoChangesRequestedLabel = "No changes requested"
	CodeOwnersLabel         = "Every code owner approved"
	ResolvedThreadsLabel    = "No unresolved threads"
	BotsClearLabel          = "Bots clear"
)

// ApprovalsLabel names the approvals condition.
func ApprovalsLabel(n int) string {
	if n == 1 {
		return "At least 1 approval"
	}
	return "At least " + strconv.Itoa(n) + " approvals"
}
