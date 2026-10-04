package tui

import (
	"charm.land/lipgloss/v2"
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
	// draft tags a draft's name instead of the word when set.
	draft string
	// Reviewed pull requests: needing the viewer again, then waiting.
	newCommits, replied, dismissed, activity, waiting, approved, backInDraft string
	// Merge queue states, then the tag of a pull request the queue removed;
	// empty in the Unicode set, which writes words.
	queueSubmitted, queueQueued, queueTesting, queueFailing, queuePassed, queueRemoved string
	star, pin, bell                                                                    string
	// woke is the tag of a pull request that woke from a snooze; empty in the
	// Unicode set, which writes words.
	woke string
	// plead marks a review request that asks the viewer again: pleadIcon in
	// both sets, two cells wide.
	plead string
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
	plead:      pleadIcon,
	categories: [7]string{"✓", "✗", "✗", "✗", "✗", "●", "?"},
}

// nerdIcons are octicons in Nerd Fonts 3.
var nerdIcons = iconSet{
	nerd:  true,
	plead: pleadIcon,
	check: "", cross: "", pending: "", behind: "", unknown: "", none: "",
	passed: "", failed: "", botRunning: "", botFailed: "",
	draft:      "",
	newCommits: "", replied: "", dismissed: "", activity: "",
	waiting: "", approved: "", backInDraft: "",
	queueSubmitted: "\uf4fa", // paper_airplane
	queueQueued:    "\uf43a", // clock
	queueTesting:   "\uf499", // beaker
	queueFailing:   "\uf421", // alert
	queuePassed:    "\uf49e", // check_circle
	queueRemoved:   "\uf468", // circle_slash
	woke:           "\uf522", // sun
	star:           "", pin: "", bell: "", gap: " ",
	// Ready to merge, changes requested, failing CI, conflicts, bot threads,
	// awaiting your review, and status unknown.
	categories: [7]string{"\uf419", "\uf52f", "\uf45e", "\uf47f", "\uf477", "\uf4af", "\uf420"},
	headers: map[string]string{
		"Merge": "", "Age": "", "Bots": "",
		"CI": "", "Review": "", "Comments": "", "Size": "",
		"Queue": "\uf4db", // git_merge_queue
		"Wakes": "\uf4ee", // moon
	},
}

// header is a column's title in this set.
func (ic *iconSet) header(title string) string {
	if icon, ok := ic.headers[title]; ok {
		return icon
	}
	return title
}

// Icon set names, as preferences spell them.
const (
	IconsUnicode = "unicode"
	IconsNerd    = "nerd"
)

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
	m.settingSaved(m.preferences.SaveIcons(m.icons.name()))
	m.rebuildPRTable(false)
}

// draftTag leads a draft's name: the word, or an icon in the Nerd set.
func (ic *iconSet) draftTag() string {
	if ic.draft == "" {
		return lipgloss.NewStyle().Faint(true).Render("draft")
	}
	return lipgloss.NewStyle().Faint(true).Render(ic.draft)
}
