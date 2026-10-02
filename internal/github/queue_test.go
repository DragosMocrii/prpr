package github

import "testing"

func trunkComment(line string) []queueComment {
	return []queueComment{
		{Author: "acme-ci", Body: "Coverage report <!-- Trunk Merge -->"},
		{Author: "trunk-io", Body: "<!-- Trunk Merge -->\n" + line},
	}
}

func TestTrunkCommentStates(t *testing.T) {
	for _, tc := range []struct {
		line   string
		state  QueueState
		detail string
	}{
		{"✨ Submitted to Merge by Pat Example (@pat). It will be added to the merge queue once all branch protection rules pass. See more details [here](https://app.trunk.io/acme/merge-queue/x/12).", QueueSubmitted, ""},
		{"✨ Stack submitted to Merge by Pat Example (@pat). See more details [here](https://app.trunk.io/acme/merge-queue/x/12).", QueueSubmitted, ""},
		{"⏳ Waiting to start tests on this pull request - [details](https://app.trunk.io/acme/merge-queue/x/12).", QueueQueued, ""},
		{"⏳ Waiting to start tests on this pull request because a pull request ([#40](https://www.github.com/acme/api/pull/40)) ahead of it failed tests - [details](https://app.trunk.io/acme/merge-queue/x/12).", QueueQueued, "a PR ahead failed"},
		{"⏳ Stack waiting to start tests on this stack - [details](https://app.trunk.io/acme/merge-queue/x/12).", QueueQueued, ""},
		{"🧪 Running tests on this pull request (testing on PR [#77](https://www.github.com/acme/api/pull/77)) - [details](https://app.trunk.io/acme/merge-queue/x/12).", QueueTesting, "testing on #77"},
		{"🧪 Running tests on this stack (testing on PR [#77](https://www.github.com/acme/api/pull/77)) - [details](https://app.trunk.io/acme/merge-queue/x/12).", QueueTesting, "testing on #77"},
		{"⚠️ The required check [`build`](https://github.com/acme/api/actions/runs/1) (Failure) has failed. Pull request failed tests and is waiting for other pull requests to finish testing. PR [#77](https://www.github.com/acme/api/pull/77) was used for testing.", QueueFailing, "build failed"},
		{"👍 Pull request will be merged soon because it has passed required tests (tested on PR [#77](https://www.github.com/acme/api/pull/77)) - [details](https://app.trunk.io/acme/merge-queue/x/12).", QueuePassed, "tested on #77"},
		{"👍 Stack will be merged soon because it has passed required tests (tested on PR [#77](https://www.github.com/acme/api/pull/77)) - [details](https://app.trunk.io/acme/merge-queue/x/12).", QueuePassed, "tested on #77"},
		{"❌ This pull request was removed from the merge queue because it failed tests. PR [#77](https://www.github.com/acme/api/pull/77) was used for testing. See more details [here](https://app.trunk.io/acme/merge-queue/x/12).", QueueRemovedFailed, "tested on #77"},
		{"❌ This stack was removed from the merge queue because it failed tests. PR [#77](https://www.github.com/acme/api/pull/77) was used for testing.", QueueRemovedFailed, "tested on #77"},
		{"🚫 This pull request was removed from the merge queue because it was canceled by Pat Example (a GitHub user). See more details [here](https://app.trunk.io/acme/merge-queue/x/12).", QueueRemovedCanceled, ""},
		{"🦄 Something Trunk says one day", QueueUnknown, ""},
	} {
		entry := trunkEntry(trunkComment(tc.line))
		if entry == nil || entry.Provider != "Trunk" || entry.State != tc.state || entry.Detail != tc.detail {
			t.Errorf("%.30q: entry %+v, want state %d detail %q", tc.line, entry, tc.state, tc.detail)
		}
	}
}

func TestTrunkCommentsThatAreNotQueueEntries(t *testing.T) {
	template := "<!-- Trunk Merge -->\nMerging to `main` in this repository is managed by Trunk.\n\n<!-- Start PR Submit Checkbox -->\n- [ ] <!-- End PR Submit Checkbox -->To merge this pull request, check the box to the left or comment `/trunk merge` below."
	for name, comments := range map[string][]queueComment{
		"none":      nil,
		"template":  {{Author: "trunk-io", Body: template}},
		"merged":    {{Author: "trunk-io", Body: "<!-- Trunk Merge -->\n😎 Merged successfully - [details](https://app.trunk.io/acme/merge-queue/x/12)."}},
		"no marker": {{Author: "trunk-io", Body: "🧪 Running tests on this pull request"}},
		"impostor":  {{Author: "pat", Body: "<!-- Trunk Merge -->\n🧪 Running tests on this pull request"}},
	} {
		if entry := trunkEntry(comments); entry != nil {
			t.Errorf("%s: entry %+v, want none", name, entry)
		}
	}
}

func TestTrunkEntryKeepsOnlySafeSingleLineText(t *testing.T) {
	entry := trunkEntry(trunkComment("🧪 Running tests on this pull request (testing on PR [#77](https://www.github.com/acme/api/pull/77)) - [details](https://app.trunk.io/acme/merge-queue/x/12)."))
	if entry.URL != "https://app.trunk.io/acme/merge-queue/x/12" {
		t.Fatalf("URL = %q", entry.URL)
	}
	unsafe := trunkEntry(trunkComment("🧪 Running tests (testing on PR [#77](https://evil.test/77)) - [details](https://app.trunk.io.evil.test/x)."))
	if unsafe.URL != "" {
		t.Fatalf("unsafe URL kept: %q", unsafe.URL)
	}
	long := trunkEntry(trunkComment("⚠️ The required check [`a\x1b[31mvery-long-check-name-that-goes-on-and-on-and-on`](https://x) (Failure) has failed."))
	if len([]rune(long.Detail)) > maxQueueDetail || containsControl(long.Detail) {
		t.Fatalf("detail not single-line and capped: %q", long.Detail)
	}
	for _, raw := range []string{"http://app.trunk.io/x", "https://app.trunk.io.evil.test/x", "https://user@app.trunk.io/x", "https://app.trunk.io/x\n", ""} {
		if safeTrunkURL(raw) {
			t.Errorf("safeTrunkURL(%q) = true", raw)
		}
	}
}

func TestGitHubQueueStates(t *testing.T) {
	for state, want := range map[string]QueueState{
		"QUEUED": QueueQueued, "AWAITING_CHECKS": QueueTesting, "MERGEABLE": QueuePassed,
		"UNMERGEABLE": QueueFailing, "LOCKED": QueueQueued, "NEW_STATE": QueueUnknown,
	} {
		entry := githubQueueEntry(state, 3)
		if entry == nil || entry.Provider != "GitHub" || entry.State != want || entry.Detail != "position 3" {
			t.Errorf("%s: %+v, want %d", state, entry, want)
		}
	}
	if entry := githubQueueEntry("", 0); entry != nil {
		t.Fatalf("no state: %+v", entry)
	}
}

func TestQueueStatesInQueue(t *testing.T) {
	for _, s := range []QueueState{QueueSubmitted, QueueQueued, QueueTesting, QueueFailing, QueuePassed, QueueUnknown} {
		if !s.InQueue() {
			t.Errorf("%d not in queue", s)
		}
	}
	for _, s := range []QueueState{0, QueueRemovedFailed, QueueRemovedCanceled} {
		if s.InQueue() {
			t.Errorf("%d in queue", s)
		}
	}
}
