package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
	"github.com/DragosMocrii/prpr/internal/preferences"
)

func repoPR(repository string, number int) github.PullRequest {
	return github.PullRequest{Number: number, Repository: repository, Title: "change",
		URL: fmt.Sprintf("https://github.com/%s/pull/%d", repository, number), Author: "bob"}
}

// listTitle is the list's title line, without styles.
func listTitle(m *model) string {
	return ansi.Strip(strings.Split(m.View().Content, "\n")[0])
}

// repositoryCells are a pane's Repository cells, nil without the column.
func repositoryCells(m *model, id paneID) []string {
	column := columnIndex(m.panes[id].table.Columns(), "Repository")
	if column < 0 {
		return nil
	}
	var cells []string
	for _, row := range m.panes[id].table.Rows() {
		cells = append(cells, ansi.Strip(row[column]))
	}
	return cells
}

func TestOneRepositoryMovesFromTheTablesToTheTitle(t *testing.T) {
	m := newPaneModel(t, 140, 30, []github.PullRequest{repoPR("acme/api", 1)}, []github.PullRequest{repoPR("acme/api", 2)})
	for _, id := range []paneID{paneMine, paneReview} {
		if cells := repositoryCells(m, id); cells != nil {
			t.Errorf("pane %d still has a Repository column: %q", id, cells)
		}
	}
	if title := listTitle(m); !strings.Contains(title, "acme/api") {
		t.Fatalf("title %q does not name the repository", title)
	}
}

func TestOneOwnerLeavesTheTablesShortNames(t *testing.T) {
	m := newPaneModel(t, 140, 30, []github.PullRequest{repoPR("acme/api", 1), repoPR("acme/web", 2)}, nil)
	if cells := repositoryCells(m, paneMine); len(cells) != 2 || cells[0] == cells[1] ||
		strings.Contains(cells[0], "acme") || strings.Contains(cells[1], "acme") {
		t.Fatalf("Repository cells = %q, want names without the owner", cells)
	}
	if title := listTitle(m); !strings.Contains(title, "acme/*") {
		t.Fatalf("title %q does not name the owner", title)
	}
	// An owner scope names the owner already.
	m.applyScope(preferences.Scope{Owner: "acme"})
	if title := listTitle(m); strings.Count(title, "acme/*") != 1 {
		t.Fatalf("title %q names the owner other than once", title)
	}
}

func TestMixedOwnersKeepFullRepositoryNames(t *testing.T) {
	m := newPaneModel(t, 140, 30, []github.PullRequest{repoPR("acme/api", 1), repoPR("other/web", 2)}, nil)
	cells := repositoryCells(m, paneMine)
	if len(cells) != 2 || !strings.Contains(strings.Join(cells, " "), "acme/api") || !strings.Contains(strings.Join(cells, " "), "other/web") {
		t.Fatalf("Repository cells = %q, want full names", cells)
	}
	if title := listTitle(m); strings.Contains(title, "only") {
		t.Fatalf("title %q names something shared", title)
	}
}

func TestAGoneRowOfAnotherRepositoryKeepsTheColumn(t *testing.T) {
	m := newPaneModel(t, 140, 30, []github.PullRequest{repoPR("acme/api", 1), repoPR("other/web", 2)}, nil)
	updateSnapshot(m, "alice", repoPR("acme/api", 1))
	if cells := repositoryCells(m, paneMine); len(cells) != 2 {
		t.Fatalf("Repository cells = %q, want both rows while one is gone", cells)
	}
	// x clears the gone row, and with it the other repository.
	pressMsg(m, letter("x"))
	if cells := repositoryCells(m, paneMine); cells != nil {
		t.Fatalf("Repository cells = %q after the gone row cleared", cells)
	}
}

func TestSearchDoesNotHideTheRepositoryColumn(t *testing.T) {
	m := newPaneModel(t, 140, 30, []github.PullRequest{repoPR("acme/api", 1), repoPR("other/web", 2)}, nil)
	m.search = "web"
	m.rebuildVisiblePRs()
	if cells := repositoryCells(m, paneMine); len(cells) != 1 || cells[0] != "other/web" {
		t.Fatalf("Repository cells = %q under a search, want the column kept", cells)
	}
}
