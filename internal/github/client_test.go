package github

import (
	"testing"
	"time"
)

func TestDecodePagesPreservesAllRowsAndOrder(t *testing.T) {
	data := []byte(`[
		{"data":{"viewer":{"login":"octocat","pullRequests":{"nodes":[
			{"number":12,"title":"Newest","url":"https://github.com/acme/one/pull/12","isDraft":true,"updatedAt":"2026-06-01T12:00:00Z","repository":{"nameWithOwner":"acme/one"}},
			null,
			{"number":8,"title":"Second","url":"https://github.com/acme/two/pull/8","isDraft":false,"updatedAt":"2026-05-31T12:00:00Z","repository":{"nameWithOwner":"acme/two"}}
		],"pageInfo":{"hasNextPage":true,"endCursor":"cursor"}}}}},
		{"data":{"viewer":{"login":"octocat","pullRequests":{"nodes":[
			{"number":4,"title":"Oldest","url":"https://github.com/acme/one/pull/4","isDraft":false,"updatedAt":"2026-05-30T12:00:00Z","repository":{"nameWithOwner":"acme/one"}}
		],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}}
	]`)

	snapshot, err := decodePages(data)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Login != "octocat" {
		t.Fatalf("login = %q, want octocat", snapshot.Login)
	}
	if len(snapshot.PullRequests) != 3 {
		t.Fatalf("got %d pull requests, want 3", len(snapshot.PullRequests))
	}
	want := []PullRequest{
		{Number: 12, Title: "Newest", Repository: "acme/one", Draft: true, UpdatedAt: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)},
		{Number: 8, Title: "Second", Repository: "acme/two", UpdatedAt: time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)},
		{Number: 4, Title: "Oldest", Repository: "acme/one", UpdatedAt: time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)},
	}
	for i, expected := range want {
		got := snapshot.PullRequests[i]
		if got.Number != expected.Number || got.Title != expected.Title || got.Repository != expected.Repository || got.Draft != expected.Draft || !got.UpdatedAt.Equal(expected.UpdatedAt) {
			t.Errorf("pull request %d = %+v, want %+v", i, got, expected)
		}
	}
}

func TestDecodePagesAcceptsEmptyConnection(t *testing.T) {
	snapshot, err := decodePages([]byte(`[{"data":{"viewer":{"login":"octocat","pullRequests":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}]`))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Login != "octocat" || len(snapshot.PullRequests) != 0 {
		t.Fatalf("snapshot = %+v, want authenticated user with no PRs", snapshot)
	}
}

func TestDecodePagesRejectsInvalidOrPartialResponses(t *testing.T) {
	cases := map[string]string{
		"malformed JSON":      `[`,
		"empty pages":         `[]`,
		"null viewer":         `[{"data":{"viewer":null}}]`,
		"missing viewer":      `[{"data":{}}]`,
		"null connection":     `[{"data":{"viewer":{"login":"octocat","pullRequests":null}}}]`,
		"missing connection":  `[{"data":{"viewer":{"login":"octocat"}}}]`,
		"later GraphQL error": `[{"data":{"viewer":{"login":"octocat","pullRequests":{"nodes":[{"number":1}]}}}},{"errors":[{"message":"rate limited"}]}]`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			snapshot, err := decodePages([]byte(input))
			if err == nil {
				t.Fatalf("decodePages() = %+v, want error", snapshot)
			}
			if snapshot.Login != "" || len(snapshot.PullRequests) != 0 {
				t.Fatalf("partial snapshot returned on error: %+v", snapshot)
			}
		})
	}
}

func TestDecodeRepositoryPagesSortsAndDeduplicatesNames(t *testing.T) {
	data := []byte(`[
		[{"full_name":"zeta/last"},{"full_name":"Acme/Repo"},{"full_name":"acme/repo"}],
		[{"full_name":"acme/empty"},{"full_name":"Beta/One"}]
	]`)
	got, err := decodeRepositoryPages(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"acme/empty", "Acme/Repo", "Beta/One", "zeta/last"}
	if len(got) != len(want) {
		t.Fatalf("repositories = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("repositories = %v, want %v", got, want)
		}
	}
}

func TestDecodeRepositoryPagesAcceptsEmptyListing(t *testing.T) {
	got, err := decodeRepositoryPages([]byte(`[[]]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("repositories = %v, want empty listing", got)
	}
}

func TestDecodeRepositoryPagesRejectsInvalidOrPartialResponses(t *testing.T) {
	for name, input := range map[string]string{
		"malformed":       `[`,
		"empty pages":     `[]`,
		"null outer":      `null`,
		"null page":       `[null]`,
		"object page":     `[{}]`,
		"null repository": `[[null]]`,
		"missing name":    `[[{}]]`,
		"null name":       `[[{"full_name":null}]]`,
		"empty name":      `[[{"full_name":""}]]`,
		"invalid name":    `[[{"full_name":"owner/repo/extra"}]]`,
		"later invalid":   `[[{"full_name":"valid/repo"}],[{"full_name":""}]]`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := decodeRepositoryPages([]byte(input))
			if err == nil {
				t.Fatalf("decodeRepositoryPages() = %v, want error", got)
			}
			if got != nil {
				t.Fatalf("partial repositories returned: %v", got)
			}
		})
	}
}

func TestDecodeRepositoryReturnsCanonicalName(t *testing.T) {
	got, err := decodeRepository([]byte(`{"id":42,"full_name":"Canonical/Name"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "Canonical/Name" {
		t.Fatalf("repository = %q, want Canonical/Name", got)
	}
	for _, input := range []string{`{`, `null`, `{}`, `{"full_name":null}`, `{"full_name":"bad/name/extra"}`} {
		if got, err := decodeRepository([]byte(input)); err == nil || got != "" {
			t.Errorf("decodeRepository(%s) = %q, %v; want error and empty result", input, got, err)
		}
	}
}

func TestValidRepositoryName(t *testing.T) {
	for _, name := range []string{"owner/repo", "Org_Name/repo.name-1", "a.b/_repo"} {
		if !ValidRepositoryName(name) {
			t.Errorf("ValidRepositoryName(%q) = false", name)
		}
	}
	for _, name := range []string{
		"", "owner", "/repo", "owner/", "owner/repo/extra", "../repo", "owner/..",
		"./repo", "owner/.", "https://github.com/owner/repo", "owner/repo?tab=x",
		"owner/repo#issues", "owner/repo\\name", "owner/re po", "owner/ repo", "owner/…",
	} {
		if ValidRepositoryName(name) {
			t.Errorf("ValidRepositoryName(%q) = true", name)
		}
	}
}
