package tui

import (
	"strings"

	"github.com/DragosMocrii/prpr/internal/github"
)

// sharedRepositories is what every pull request the lists hold in scope has
// in common: one repository, or else one owner; empty when they differ or
// there are none. Search and filters do not change it, so the Repository
// column does not come and go while typing.
type sharedRepositories struct {
	repository, owner string
}

// shared finds it afresh; rebuildPRTable keeps it in sharedRows, which the
// tables and the title read.
func (m *model) shared() sharedRepositories {
	var shared sharedRepositories
	first, mixedOwners, mixedRepositories := true, false, false
	note := func(pr *github.PullRequest) {
		if !m.inScope(pr) {
			return
		}
		owner, _, _ := strings.Cut(pr.Repository, "/")
		if first {
			shared, first = sharedRepositories{repository: pr.Repository, owner: owner}, false
			return
		}
		mixedRepositories = mixedRepositories || !strings.EqualFold(pr.Repository, shared.repository)
		mixedOwners = mixedOwners || !strings.EqualFold(owner, shared.owner)
	}
	for _, id := range trackedIDs {
		list := m.source(id)
		for i := range list {
			note(&list[i])
		}
		for i := range m.changes[id].gone {
			note(&m.changes[id].gone[i])
		}
	}
	if mixedRepositories {
		shared.repository = ""
	}
	if mixedOwners {
		shared.owner = ""
	}
	return shared
}

// repositoryText is a row's repository as the tables show it: without its
// owner when every row shares that owner.
func (s sharedRepositories) repositoryText(pr *github.PullRequest) string {
	if owner, name, ok := strings.Cut(pr.Repository, "/"); ok && s.owner != "" && strings.EqualFold(owner, s.owner) {
		return singleLine(name)
	}
	return singleLine(pr.Repository)
}

// sharedLabel names in the title what the tables leave out: the one
// repository, or the one owner, of every row; "" when the scope already
// says it.
func (m *model) sharedLabel() string {
	if m.selectedRepository != "" {
		return ""
	}
	shared := m.sharedRows
	switch {
	case shared.repository != "":
		return "only " + singleLine(shared.repository)
	case shared.owner != "" && !strings.EqualFold(shared.owner, m.owner):
		return "only " + ownerLabel(shared.owner)
	}
	return ""
}
