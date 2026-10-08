package github

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFormatNudgeRoundTrips(t *testing.T) {
	body, err := FormatNudge(NudgeUrgent, []string{"alice", "Bob-2"}, "release\tis\x1b[31m today")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(body, "@alice @Bob-2 ") {
		t.Fatalf("body does not open with the mentions: %q", body)
	}
	urgency, to, note, ok := parseNudge(body)
	if !ok || urgency != NudgeUrgent || strings.Join(to, ",") != "alice,Bob-2" {
		t.Fatalf("parse = %v %v %v", urgency, to, ok)
	}
	if strings.ContainsAny(note, "\t\x1b") || !strings.Contains(note, "today") {
		t.Fatalf("note not cleaned: %q", note)
	}
}

func TestFormatNudgeRefuses(t *testing.T) {
	for name, call := range map[string]func() (string, error){
		"no one":        func() (string, error) { return FormatNudge(NudgeLow, nil, "") },
		"bad login":     func() (string, error) { return FormatNudge(NudgeLow, []string{"a b"}, "") },
		"no urgency":    func() (string, error) { return FormatNudge(0, []string{"alice"}, "") },
		"injected flag": func() (string, error) { return FormatNudge(NudgeLow, []string{"-x"}, "") },
	} {
		if _, err := call(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestFormatNudgeCapsTheNote(t *testing.T) {
	body, _ := FormatNudge(NudgeLow, []string{"alice"}, strings.Repeat("x", 500))
	_, _, note, _ := parseNudge(body)
	if n := len([]rune(note)); n > nudgeNoteMax {
		t.Fatalf("note has %d runes, cap %d", n, nudgeNoteMax)
	}
}

func TestParseNudge(t *testing.T) {
	cases := map[string]struct {
		body string
		ok   bool
	}{
		"plain":           {"@a hi\n\n<!-- prpr:nudge v1 urgency=normal to=a -->", true},
		"crlf and spaces": {"@a hi\r\n\r\n<!-- prpr:nudge v1 urgency=low to=a -->  \r\n\r\n", true},
		"other version":   {"<!-- prpr:nudge v2 urgency=low to=a -->", false},
		"unknown urgency": {"<!-- prpr:nudge v1 urgency=meh to=a -->", false},
		"bad login":       {"<!-- prpr:nudge v1 urgency=low to=a,-b -->", false},
		"not last line":   {"<!-- prpr:nudge v1 urgency=low to=a -->\nthanks", false},
		"inline":          {"see <!-- prpr:nudge v1 urgency=low to=a -->", false},
		"no marker":       {"@a please look", false},
		"empty":           {"", false},
	}
	for name, c := range cases {
		if _, _, _, ok := parseNudge(c.body); ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", name, ok, c.ok)
		}
	}
}

// items decodes timeline items from literal JSON.
func items(t *testing.T, nodes ...string) []*activityNode {
	t.Helper()
	var out []*activityNode
	if err := json.Unmarshal([]byte("["+strings.Join(nodes, ",")+"]"), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func nudgeComment(login, at, urgency, to string) string {
	body, _ := json.Marshal("@" + to + " hi\n\n> ship it\n\n<!-- prpr:nudge v1 urgency=" + urgency + " to=" + to + " -->")
	return `{"__typename":"IssueComment","author":{"login":"` + login + `"},"createdAt":"` + at + `","body":` + string(body) + `}`
}

func TestActiveNudge(t *testing.T) {
	cases := []struct {
		name  string
		nodes []string
		want  NudgeUrgency // 0 is none
	}{
		{"author nudges viewer", []string{nudgeComment("bob", at(5), "urgent", "octocat")}, NudgeUrgent},
		{"login case ignored", []string{nudgeComment("BOB", at(5), "low", "OctoCat")}, NudgeLow},
		{"someone else copied the marker", []string{nudgeComment("mallory", at(5), "urgent", "octocat")}, 0},
		{"names someone else", []string{nudgeComment("bob", at(5), "urgent", "carol")}, 0},
		{"viewer commented after", []string{nudgeComment("bob", at(5), "urgent", "octocat"), comment("octocat", at(6))}, 0},
		{"viewer reviewed after", []string{nudgeComment("bob", at(5), "urgent", "octocat"), review("octocat", "APPROVED", at(6), "h2")}, 0},
		{"viewer comment at the same time ends it", []string{nudgeComment("bob", at(5), "urgent", "octocat"), comment("octocat", at(5))}, 0},
		{"viewer acted before", []string{comment("octocat", at(4)), nudgeComment("bob", at(5), "normal", "octocat")}, NudgeNormal},
		{"newer nudge wins", []string{nudgeComment("bob", at(5), "urgent", "octocat"), nudgeComment("bob", at(6), "low", "octocat")}, NudgeLow},
		{"pending review does not end it", []string{nudgeComment("bob", at(5), "urgent", "octocat"),
			`{"__typename":"PullRequestReview","author":{"login":"octocat"},"state":"PENDING","submittedAt":null}`}, NudgeUrgent},
	}
	for _, c := range cases {
		got := activeNudge(items(t, c.nodes...), "bob", "octocat")
		switch {
		case c.want == 0 && got != nil:
			t.Errorf("%s: got %+v, want none", c.name, got)
		case c.want != 0 && (got == nil || got.Urgency != c.want):
			t.Errorf("%s: got %+v, want %v", c.name, got, c.want)
		}
	}
	got := activeNudge(items(t, nudgeComment("bob", at(5), "normal", "octocat")), "bob", "octocat")
	if got == nil || !got.At.Equal(day(5)) || got.Note != "ship it" {
		t.Fatalf("nudge = %+v, want at day 5 with its note", got)
	}
}
