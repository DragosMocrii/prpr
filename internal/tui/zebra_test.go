package tui

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DragosMocrii/prpr/internal/github"
)

func zebraModel(t *testing.T) *model {
	t.Helper()
	var prs []github.PullRequest
	for n := 1; n <= 4; n++ {
		prs = append(prs, github.PullRequest{
			Number: n, Repository: "acme/a", Title: "change", URL: fmt.Sprintf("https://github.com/acme/a/pull/%d", n),
			Checks: "FAILURE", ReviewDecision: "APPROVED",
		})
	}
	return newTableModel(t, 120, 24, prs)
}

func background(index int) string { return fmt.Sprintf("\x1b[48;5;%dm", index) }

// spansBackground reports whether bg is set at the start of the line and
// turned back on after every reset before the line ends.
func spansBackground(line, bg string) bool {
	body := strings.TrimSuffix(line, "\x1b[m")
	if !strings.Contains(body, bg) {
		return false
	}
	for i := strings.Index(body, "\x1b[m"); i >= 0; i = strings.Index(body, "\x1b[m") {
		rest := body[i+len("\x1b[m"):]
		if !strings.HasPrefix(rest, bg) {
			return false
		}
		body = rest
	}
	return true
}

func TestTableRowsAreStripedAndSelectionSpansTheRow(t *testing.T) {
	for _, dark := range []bool{true, false} {
		m := zebraModel(t)
		bgColor := color.Color(color.White)
		stripe, selected := lightStripe, lightSelected
		if dark {
			bgColor, stripe, selected = color.Black, darkStripe, darkSelected
		}
		m.Update(tea.BackgroundColorMsg{Color: bgColor})
		plain := strings.Split(m.panes[paneMine].table.View(), "\n")
		lines := m.tableLines(paneMine, -1)
		if len(lines) != len(plain) {
			t.Fatalf("dark %t: %d lines, want %d", dark, len(lines), len(plain))
		}
		for i, line := range lines {
			if ansi.StringWidth(line) != ansi.StringWidth(plain[i]) {
				t.Errorf("dark %t line %d width changed", dark, i)
			}
			row := i - tableHeaderLen
			switch {
			case row == 0:
				if !spansBackground(line, fmt.Sprintf("\x1b[1;48;5;%dm", selected)) {
					t.Errorf("dark %t: selected row background does not span the row: %q", dark, line)
				}
			case row%2 == 1 && row < 4:
				if !spansBackground(line, background(stripe)) {
					t.Errorf("dark %t: row %d is not striped across: %q", dark, row, line)
				}
			default:
				if strings.Contains(line, "48;") {
					t.Errorf("dark %t: line %d has a background: %q", dark, i, line)
				}
			}
		}
	}
}

func TestUnfocusedPaneIsStripedWithoutSelection(t *testing.T) {
	m := zebraModel(t)
	m.Update(tea.BackgroundColorMsg{Color: color.Black})
	m.snapshot.ReviewRequests = m.snapshot.PullRequests
	m.rebuildVisiblePRs()
	lines := m.tableLines(paneReview, -1)
	for i, line := range lines[tableHeaderLen : tableHeaderLen+4] {
		striped := strings.Contains(line, background(darkStripe))
		if striped != (i%2 == 1) || strings.Contains(line, background(darkSelected)) {
			t.Errorf("unfocused row %d: %q", i, line)
		}
	}
}
