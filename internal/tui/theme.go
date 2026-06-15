package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

const defaultThemeName = "default"

var themeNames = []string{defaultThemeName, "contrast", "mono"}

func stylesForTheme(name string) styles {
	switch normalizeThemeName(name) {
	case "contrast":
		return contrastStyles()
	case "mono":
		return monoStyles()
	default:
		return defaultStyles()
	}
}

func normalizeThemeName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func themeExists(value string) bool {
	value = normalizeThemeName(value)
	for _, name := range themeNames {
		if value == name {
			return true
		}
	}
	return false
}

func (m *model) setTheme(args []string) []string {
	if len(args) == 0 {
		return m.themeStatusLines()
	}
	value := normalizeThemeName(strings.Join(args, " "))
	switch value {
	case "", "clear", "default", "auto", "reset":
		m.applyTheme(defaultThemeName)
		return []string{"TUI theme を既定に戻しました", m.themeStatusLine()}
	default:
		if !themeExists(value) {
			lines := []string{"unknown TUI theme: " + value}
			lines = append(lines, m.themeStatusLines()...)
			return lines
		}
		m.applyTheme(value)
		return []string{"TUI theme を設定しました", m.themeStatusLine()}
	}
}

func (m model) themeStatusLines() []string {
	return []string{
		m.themeStatusLine(),
		"available: " + strings.Join(themeNames, ", "),
	}
}

func (m model) themeStatusLine() string {
	return "TUI theme: " + m.themeDisplayLabel()
}

func (m model) themeDisplayLabel() string {
	if m.selectedTheme == "" {
		return defaultThemeName
	}
	return m.selectedTheme
}

func (m *model) applyTheme(name string) {
	name = normalizeThemeName(name)
	if name == "" {
		name = defaultThemeName
	}
	m.selectedTheme = name
	m.styles = stylesForTheme(name)
	m.invalidateThemeCaches()
}

func (m *model) invalidateThemeCaches() {
	m.headerCache.dirty = true
	m.toolCardCache.dirty = true
	m.toolLogCardCache.dirty = true
	m.permissionCache.dirty = true
	m.completionCache.dirty = true
	m.composerCache.dirty = true
	m.toolLogDirty = true
	m.logDirty = true
	m.logRender = logRenderState{dirty: true}
	for idx := range m.chatBlocks {
		m.chatBlocks[idx].cachedWidth = 0
		m.chatBlocks[idx].rendered = ""
	}
	if m.completion.ctx != nil && len(m.completion.ctx.candidates) > 0 {
		m.completion.rendered = renderCompletionSuggestionsForContext(m.styles, m.completion.ctx)
		m.completion.height = lipgloss.Height(m.completion.rendered)
	}
	m.invalidateAllPanels()
}

func contrastStyles() styles {
	return styles{
		user:      lipgloss.NewStyle().Foreground(lipgloss.Color("201")).Bold(true),
		assistant: lipgloss.NewStyle().Foreground(lipgloss.Color("45")).Bold(true),
		tool:      lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Bold(true),
		separator: lipgloss.NewStyle().Foreground(lipgloss.Color("250")),
		hint:      lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true),
		markdownHeading: lipgloss.NewStyle().
			Foreground(lipgloss.Color("226")).
			Bold(true),
		markdownList:  lipgloss.NewStyle().Foreground(lipgloss.Color("255")),
		markdownQuote: lipgloss.NewStyle().Foreground(lipgloss.Color("159")).Italic(true),
		markdownCode: lipgloss.NewStyle().
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("238")),
		toolCard: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("250")).
			Padding(0, 1),
		toolTitle:   lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Bold(true),
		toolMeta:    lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true),
		toolOption:  lipgloss.NewStyle().Foreground(lipgloss.Color("255")),
		toolSuccess: lipgloss.NewStyle().Foreground(lipgloss.Color("120")).Bold(true),
		toolFailure: lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true),
		toolLogCard: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("250")).
			Padding(0, 1),
		toolLogTitle: lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Bold(true),
		toolLogHint:  lipgloss.NewStyle().Foreground(lipgloss.Color("255")),
		permissionCard: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("250")).
			Padding(0, 1),
		permissionTitle:  lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Bold(true),
		permissionPath:   lipgloss.NewStyle().Foreground(lipgloss.Color("255")),
		permissionHelp:   lipgloss.NewStyle().Foreground(lipgloss.Color("255")),
		permissionOption: lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Padding(0, 1),
		permissionSelected: lipgloss.NewStyle().
			Foreground(lipgloss.Color("16")).
			Background(lipgloss.Color("226")).
			Bold(true).
			Padding(0, 1),
		commandHint:      lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true),
		commandCandidate: lipgloss.NewStyle().Foreground(lipgloss.Color("255")),
		commandSelected: lipgloss.NewStyle().
			Foreground(lipgloss.Color("16")).
			Background(lipgloss.Color("45")).
			Bold(true).
			Padding(0, 1),
		panelTab: lipgloss.NewStyle().
			Foreground(lipgloss.Color("255")).
			Padding(0, 1),
		panelTabActive: lipgloss.NewStyle().
			Foreground(lipgloss.Color("16")).
			Background(lipgloss.Color("226")).
			Bold(true).
			Padding(0, 1),
		panelMeta: lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true),
	}
}

func monoStyles() styles {
	return styles{
		user:            lipgloss.NewStyle().Bold(true),
		assistant:       lipgloss.NewStyle(),
		tool:            lipgloss.NewStyle().Italic(true),
		separator:       lipgloss.NewStyle(),
		hint:            lipgloss.NewStyle().Italic(true),
		markdownHeading: lipgloss.NewStyle().Bold(true),
		markdownList:    lipgloss.NewStyle(),
		markdownQuote:   lipgloss.NewStyle().Italic(true),
		markdownCode:    lipgloss.NewStyle(),
		toolCard: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1),
		toolTitle:      lipgloss.NewStyle().Bold(true),
		toolMeta:       lipgloss.NewStyle(),
		toolOption:     lipgloss.NewStyle(),
		toolSuccess:    lipgloss.NewStyle().Bold(true),
		toolFailure:    lipgloss.NewStyle().Bold(true),
		toolLogCard:    lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1),
		toolLogTitle:   lipgloss.NewStyle().Bold(true),
		toolLogHint:    lipgloss.NewStyle(),
		permissionCard: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1),
		permissionTitle: lipgloss.NewStyle().
			Bold(true),
		permissionPath:   lipgloss.NewStyle(),
		permissionHelp:   lipgloss.NewStyle(),
		permissionOption: lipgloss.NewStyle().Padding(0, 1),
		permissionSelected: lipgloss.NewStyle().
			Reverse(true).
			Bold(true).
			Padding(0, 1),
		commandHint:      lipgloss.NewStyle(),
		commandCandidate: lipgloss.NewStyle(),
		commandSelected: lipgloss.NewStyle().
			Reverse(true).
			Bold(true).
			Padding(0, 1),
		panelTab: lipgloss.NewStyle().
			Padding(0, 1),
		panelTabActive: lipgloss.NewStyle().
			Reverse(true).
			Bold(true).
			Padding(0, 1),
		panelMeta: lipgloss.NewStyle().Italic(true),
	}
}
