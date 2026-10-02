package tui

import (
	"fmt"

	"charm.land/lipgloss/v2"

	"github.com/DragosMocrii/prpr/internal/preferences"
)

// iconSet holds the symbols prpr draws. unicodeIcons uses characters that
// common fonts include; nerdIcons uses Nerd Font octicons, which need a
// patched font that prpr cannot detect, so they are opt-in. Every icon is
// one cell wide. Colors, the details screen, notifications, and help are the
// same in both sets.
type iconSet struct {
	nerd bool
	// Merge, CI, review, and bot states.
	check, cross, pending, behind, unknown, none string
	// Bigger variants for CI and review, and the bot states that have no
	// merge counterpart.
	passed, failed, botRunning, botFailed string
	// open and draft replace the State column's words when set.
	open, draft string
	// Reviewed pull requests: needing the viewer again, then waiting.
	newCommits, replied, dismissed, activity, waiting, approved, backInDraft string
	star, pin, bell                                                          string
	// gap separates an icon from a number or mark after it. Nerd Font icons
	// are often drawn wider than the one cell the terminal gives them, so
	// whatever follows directly would be drawn over them.
	gap string
	// categories are the attention categories' icons, numbered from 1.
	categories [7]string
	// Column headers, by column title; missing ones keep the title.
	headers map[string]string
}

var unicodeIcons = iconSet{
	check: "✓", cross: "✗", pending: "●", behind: "↓", unknown: "?", none: "–",
	passed: "✓", failed: "✗", botRunning: "◌", botFailed: "!",
	star:       "★",
	categories: [7]string{"✓", "✗", "✗", "✗", "✗", "●", "?"},
}

// nerdIcons are octicons in Nerd Fonts 3.
var nerdIcons = iconSet{
	nerd:  true,
	check: "", cross: "", pending: "", behind: "", unknown: "", none: "",
	passed: "", failed: "", botRunning: "", botFailed: "",
	open: "", draft: "",
	newCommits: "", replied: "", dismissed: "", activity: "",
	waiting: "", approved: "", backInDraft: "",
	star: "", pin: "", bell: "", gap: " ",
	// Ready to merge, changes requested, failing CI, conflicts, bot threads,
	// awaiting your review, and status unknown.
	categories: [7]string{"\uf419", "\uf52f", "\uf45e", "\uf47f", "\uf477", "\uf4af", "\uf420"},
	headers: map[string]string{
		"State": "", "Merge": "", "Age": "", "Bots": "",
		"CI": "", "Review": "", "Comments": "", "Size": "",
	},
}

// header is a column's title in this set.
func (ic *iconSet) header(title string) string {
	if icon, ok := ic.headers[title]; ok {
		return icon
	}
	return title
}

// Icon set names, as --icons, PRPR_ICONS, and preferences spell them.
const (
	IconsUnicode = "unicode"
	IconsNerd    = "nerd"
)

// ParseIcons checks an icon set name; empty means no choice.
func ParseIcons(name string) (string, error) {
	switch name {
	case "", IconsUnicode, IconsNerd:
		return name, nil
	}
	return "", fmt.Errorf("unknown icon set %q; use %s or %s", name, IconsNerd, IconsUnicode)
}

// startIcons is the icon set a run starts with: the one named for the run,
// else the saved one, else Unicode.
func startIcons(name string, store *preferences.Store) *iconSet {
	if name == "" {
		name = store.Icons()
	}
	return iconsNamed(name)
}

func iconsNamed(name string) *iconSet {
	if name == IconsNerd {
		return &nerdIcons
	}
	return &unicodeIcons
}

func (ic *iconSet) name() string {
	if ic.nerd {
		return IconsNerd
	}
	return IconsUnicode
}

// toggleIcons switches between the icon sets and saves the choice. A failed
// save keeps the new set for the session and shows the warning.
func (m *model) toggleIcons() {
	if m.icons.nerd {
		m.icons = &unicodeIcons
		m.setNotice("Unicode icons")
	} else {
		m.icons = &nerdIcons
		m.setNotice("Nerd Font icons on — press i again if you see boxes")
	}
	if err := m.preferences.SaveIcons(m.icons.name()); err != nil {
		m.preferenceErr = fmt.Errorf("Icon choice not saved: %w", err)
	}
	m.rebuildPRTable(false)
}

// legend explains the Merge column's symbols. With rules of the user's own,
// a yellow check means only GitHub would merge.
func (ic *iconSet) legend(rules bool) string {
	ready := ic.check + " ready  "
	if rules {
		ready = ic.check + " ready (yellow: GitHub only)  "
	}
	return ready + ic.pending + " blocked  " + ic.behind + " behind  " + ic.cross + " conflicts  " + ic.unknown + " unknown"
}

// stateText is the State column: words, or an icon in the Nerd set.
func (ic *iconSet) stateText(draft bool) string {
	switch {
	case ic.open == "" && draft:
		return "draft"
	case ic.open == "":
		return "open"
	case draft:
		return lipgloss.NewStyle().Faint(true).Render(ic.draft)
	default:
		return coloredIcon(ic.open, "2")
	}
}
