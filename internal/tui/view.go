package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type stringRenderCache struct {
	key     string
	content string
	dirty   bool
}

func (m model) View() tea.View {
	header := m.cachedHeaderView()
	mainView := m.cachedMainPanels()
	offsetY := lipgloss.Height(header) + 1 + lipgloss.Height(mainView) + 1

	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString("\n")
	sb.WriteString(mainView)
	sb.WriteString("\n")
	if m.hasToolLogs() {
		card := m.cachedToolLogCard()
		sb.WriteString(card)
		sb.WriteString("\n")
		offsetY += lipgloss.Height(card) + 1
	}
	if m.hasActiveTools() {
		card := m.cachedToolCard()
		sb.WriteString(card)
		sb.WriteString("\n")
		offsetY += lipgloss.Height(card) + 1
	}
	if m.permission != nil {
		card := m.cachedPermissionCard()
		sb.WriteString(card)
		sb.WriteString("\n")
		offsetY += lipgloss.Height(card) + 1
	}
	if m.hasCompletionCandidates() {
		suggestions := m.cachedCompletionSuggestions()
		sb.WriteString(suggestions)
		sb.WriteString("\n")
		offsetY += lipgloss.Height(suggestions) + 1
	}
	sb.WriteString("\n")
	offsetY++
	sb.WriteString(m.cachedComposerView())

	view := tea.NewView(sb.String())
	view.AltScreen = true
	if cursor := m.textarea.Cursor(); cursor != nil {
		cursor.Position.Y += offsetY
		view.Cursor = cursor
	}
	return view
}

func (m *model) cachedHeaderView() string {
	key := fmt.Sprintf("%d:%s:%s:%t:%s:%s", m.width, m.modelOverride, m.selectedProfile, m.streaming, m.selectedTheme, m.conversationID)
	if !m.headerCache.dirty && m.headerCache.key == key {
		return m.headerCache.content
	}
	m.headerCache.key = key
	modelLine := fmt.Sprintf("profile=%s  model=%s  stream=%s  theme=%s", m.profileDisplayLabel(), m.modelDisplayLabel(), m.streamingDisplayLabel(), m.themeDisplayLabel())
	if m.defaultModel != "" && m.modelOverride == "" {
		modelLine += "  default=" + m.defaultModel
	}
	if m.conversationID != "" {
		modelLine += "  conversation=" + string(m.conversationID)
	}
	m.headerCache.content = m.styles.hint.Render("入力： /help, /profile, /model, /stream, /theme, /graph, /plan, /verification, /memory, /continue, /recover, /clear, /exit | Ctrl+←/→ または Ctrl+H/L / Alt+[/] で panel 切替 | Alt+↑/↓ と PgUp/PgDn でログ") +
		"\n" + m.styles.hint.Render(modelLine) +
		"\n" + m.styles.separator.Render(strings.Repeat("─", maxInt(1, m.width))) + "\n"
	m.headerCache.dirty = false
	return m.headerCache.content
}

func (m *model) cachedMainPanels() string {
	key := fmt.Sprintf("%d:%d:%d:%d:%d:%s:%d:%t", m.viewport.Width(), m.viewport.Height(), m.viewport.YOffset(), m.statusViewport.Width(), m.statusViewport.Height(), m.activePanel, m.loadingFrame, m.loading)
	if !m.mainPanelsCache.dirty && m.mainPanelsCache.key == key {
		return m.mainPanelsCache.content
	}
	m.mainPanelsCache.key = key
	m.mainPanelsCache.content = m.renderMainPanels()
	m.mainPanelsCache.dirty = false
	return m.mainPanelsCache.content
}

func (m *model) cachedPermissionCard() string {
	key := fmt.Sprintf("%d:%t:%d:%s", m.width, m.permission != nil, m.pendingApprovalCount(), func() string {
		if m.permission == nil {
			return ""
		}
		return m.permission.patternInput
	}())
	if !m.permissionCache.dirty && m.permissionCache.key == key {
		return m.permissionCache.content
	}
	m.permissionCache.key = key
	m.permissionCache.content = m.renderPermissionCard()
	m.permissionCache.dirty = false
	return m.permissionCache.content
}

func (m *model) cachedCompletionSuggestions() string {
	return m.renderCompletionSuggestions()
}
