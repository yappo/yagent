package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"yagent/internal/domain"
)

type panelRenderCache struct {
	width   int
	content string
	dirty   bool
}

type memoryPanelState struct {
	data    *domain.RepoMemory
	err     error
	loading bool
}

type sidePanel string

const (
	sidePanelRunGraph     sidePanel = "graph"
	sidePanelPlan         sidePanel = "plan"
	sidePanelVerification sidePanel = "verification"
	sidePanelMemory       sidePanel = "memory"
)

var sidePanels = []sidePanel{
	sidePanelRunGraph,
	sidePanelPlan,
	sidePanelVerification,
	sidePanelMemory,
}

func panelNavigationDelta(msg tea.KeyMsg) (int, bool) {
	key := msg.Key()
	if key.Mod.Contains(tea.ModCtrl) {
		switch key.Code {
		case tea.KeyLeft:
			return -1, true
		case tea.KeyRight:
			return 1, true
		case 'h':
			return -1, true
		case 'l':
			return 1, true
		}
	}
	if key.Mod.Contains(tea.ModAlt) {
		switch key.Code {
		case '[':
			return -1, true
		case ']':
			return 1, true
		}
	}
	return 0, false
}

func (m model) pendingApprovalCount() int {
	count := m.queuedPermissionCount()
	if m.permission != nil {
		count += m.permission.batchSize()
	}
	return count
}

func (m model) queuedPermissionCount() int {
	count := 0
	for _, state := range m.permissionQueue {
		count += state.batchSize()
	}
	return count
}

func (m model) latestArtifactSummary() string {
	if m.lastRun == nil || len(m.lastRun.Artifacts) == 0 {
		return ""
	}
	artifact := m.lastRun.Artifacts[len(m.lastRun.Artifacts)-1]
	return artifact.Name + " (" + fallbackString(artifact.Summary, artifact.Kind) + ")"
}

func (m model) renderMainPanels() string {
	chatTitle := "Chat"
	if metrics := m.currentRunMetrics(); metrics != "" {
		chatTitle += "  " + metrics
	}
	chatBody := m.viewport.View()
	if loading := m.loadingStatusView(); loading != "" {
		if chatBody != "" {
			chatBody += "\n"
		}
		chatBody += loading
	}
	chatPane := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Width(maxInt(1, m.viewport.Width()+paneHorizontalFrame)).
		Render(chatTitle + "\n" + chatBody)
	sideTitle := m.renderPanelTabs()
	statusPane := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Width(maxInt(1, m.statusViewport.Width()+paneHorizontalFrame)).
		Render(sideTitle + "\n" + m.statusViewport.View())

	_, _, stacked := layoutWidths(m.width)
	if stacked {
		return chatPane + "\n" + statusPane
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, chatPane, statusPane)
}

func (m *model) renderStatus() string {
	cache := m.panelCache[m.activePanel]
	width := m.statusViewport.Width()
	if !cache.dirty && cache.width == width {
		return cache.content
	}
	switch m.activePanel {
	case sidePanelPlan:
		cache.content = m.renderPlanPanel()
	case sidePanelVerification:
		cache.content = m.renderVerificationPanel()
	case sidePanelMemory:
		cache.content = m.renderMemoryPanel()
	default:
		cache.content = m.renderRunGraphPanel()
	}
	cache.width = width
	cache.dirty = false
	m.panelCache[m.activePanel] = cache
	return cache.content
}

func (m *model) setActivePanel(panel sidePanel) {
	if m.activePanel == panel {
		return
	}
	m.activePanel = panel
	m.mainPanelsCache.dirty = true
	if panel == sidePanelMemory {
		m.invalidatePanel(sidePanelMemory)
	}
}

func (m *model) cyclePanel(delta int) {
	if len(sidePanels) == 0 {
		return
	}
	index := 0
	for idx, panel := range sidePanels {
		if panel == m.activePanel {
			index = idx
			break
		}
	}
	next := wrapIndex(index+delta, len(sidePanels))
	m.setActivePanel(sidePanels[next])
}

func (m model) renderPanelTabs() string {
	parts := make([]string, 0, len(sidePanels))
	for _, panel := range sidePanels {
		label := m.panelTitle(panel)
		if panel == m.activePanel {
			parts = append(parts, m.styles.panelTabActive.Render(label))
			continue
		}
		parts = append(parts, m.styles.panelTab.Render(label))
	}
	return strings.Join(parts, " ")
}

func (m model) panelTitle(panel sidePanel) string {
	switch panel {
	case sidePanelPlan:
		return "Plan"
	case sidePanelVerification:
		return "Verification"
	case sidePanelMemory:
		return "Memory"
	default:
		return "Run Graph"
	}
}

func (m model) renderPanelSummary() []string {
	lines := []string{}
	if m.lastRun != nil {
		lines = append(lines, m.styles.panelMeta.Render(fmt.Sprintf(
			"run %s  phase=%s  status=%s  attempt=%d",
			m.lastRun.ID,
			fallbackString(string(m.lastRun.CurrentPhase), "-"),
			fallbackString(string(m.lastRun.Status), "-"),
			maxInt(1, m.lastRun.Attempt),
		)))
	} else if metrics := m.currentRunMetrics(); metrics != "" {
		lines = append(lines, m.styles.panelMeta.Render(metrics))
	} else {
		lines = append(lines, m.styles.panelMeta.Render("run (not loaded)"))
	}
	lines = append(lines, m.styles.panelMeta.Render(fmt.Sprintf("pending approvals %d", m.pendingApprovalCount())))
	if artifact := m.latestArtifactSummary(); artifact != "" {
		lines = append(lines, m.styles.panelMeta.Render("latest artifact: "+artifact))
	}
	if reason := m.latestBlockedReason(); reason != "" {
		lines = append(lines, m.styles.panelMeta.Render("blocked: "+reason))
	}
	return lines
}

func (m model) renderPlanPanel() string {
	lines := m.renderPanelSummary()
	if m.lastRun == nil {
		lines = append(lines, "", "(no run loaded)")
		return strings.Join(lines, "\n")
	}
	if m.lastRun.ExecutionPlan != nil {
		plan := m.lastRun.ExecutionPlan
		width := maxInt(24, m.statusViewport.Width()-6)
		lines = append(lines, "", "Execution plan")
		lines = append(lines, wrapContent(fmt.Sprintf("mode=%s  task=%s  source=%s", fallbackString(plan.Mode, "-"), fallbackString(string(plan.TaskKind), "-"), fallbackString(plan.Source, "-")), width))
		if summary := strings.TrimSpace(plan.Summary); summary != "" {
			lines = append(lines, wrapContent(summary, width))
		}
		if reason := strings.TrimSpace(plan.FallbackReason); reason != "" {
			lines = append(lines, wrapContent("fallback: "+reason, width))
		}
		if plan.Plan != nil {
			lines = append(lines, "", "Plan owner")
			lines = append(lines, wrapContent("- "+describeAssignment(*plan.Plan), width))
		}
		if len(plan.Preparation) > 0 {
			lines = append(lines, "", "Preparation")
			for _, item := range plan.Preparation {
				lines = append(lines, wrapContent("- "+describeAssignment(item), width))
			}
		}
		lines = append(lines, "", "Primary")
		lines = append(lines, wrapContent("- "+describeAssignment(plan.Primary), width))
		if len(plan.Verify) > 0 {
			lines = append(lines, "", "Verification")
			for _, item := range plan.Verify {
				lines = append(lines, wrapContent("- "+describeAssignment(item), width))
			}
		}
		if plan.Recovery != nil {
			lines = append(lines, "", "Recovery")
			lines = append(lines, wrapContent("- "+describeAssignment(*plan.Recovery), width))
		}
		if plan.Finalize != nil {
			lines = append(lines, "", "Finalize")
			lines = append(lines, wrapContent("- "+describeAssignment(*plan.Finalize), width))
		}
		if len(m.lastRun.Plan) > 0 {
			lines = append(lines, "", "Steps")
			for idx, node := range m.lastRun.Plan {
				line := fmt.Sprintf("%d. [%s] %s", idx+1, fallbackString(node.Status, "pending"), node.Title)
				lines = append(lines, wrapContent(trimPathFromEnd(line, width), width))
				if description := strings.TrimSpace(node.Description); description != "" {
					lines = append(lines, wrapContent("   agent: "+trimPathFromEnd(description, width), width))
				}
			}
		}
		return strings.Join(lines, "\n")
	}
	if len(m.lastRun.Plan) == 0 {
		lines = append(lines, "", "(no plan available)")
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "", "Plan")
	width := maxInt(24, m.statusViewport.Width()-6)
	for idx, node := range m.lastRun.Plan {
		line := fmt.Sprintf("%d. [%s] %s", idx+1, fallbackString(node.Status, "pending"), node.Title)
		lines = append(lines, wrapContent(trimPathFromEnd(line, width), width))
		if description := strings.TrimSpace(node.Description); description != "" {
			lines = append(lines, "   "+wrapContent(trimPathFromEnd(description, width), width))
		}
	}
	if checkpoints := tailCheckpoints(m.lastRun.Checkpoints, 4); len(checkpoints) > 0 {
		lines = append(lines, "", "Recent checkpoints")
		for _, checkpoint := range checkpoints {
			lines = append(lines, wrapContent(fmt.Sprintf("%s  %s", checkpoint.Phase, fallbackString(checkpoint.Summary, "(no summary)")), width))
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) renderVerificationPanel() string {
	lines := m.renderPanelSummary()
	if m.lastRun == nil {
		lines = append(lines, "", "(no run loaded)")
		return strings.Join(lines, "\n")
	}
	if len(m.lastRun.Verification) == 0 {
		lines = append(lines, "", "(no verification available)")
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "", "Verification")
	width := maxInt(24, m.statusViewport.Width()-6)
	for _, item := range m.lastRun.Verification {
		header := fmt.Sprintf("try %d  %s  %s", item.Attempt, item.SourceAgent, strings.ToUpper(fallbackString(item.Status, "unknown")))
		lines = append(lines, trimPathFromEnd(header, width))
		if summary := strings.TrimSpace(item.Summary); summary != "" {
			lines = append(lines, wrapContent("  "+trimPathFromEnd(summary, width), width))
		}
		if brief := strings.TrimSpace(item.RepairBrief); brief != "" {
			lines = append(lines, wrapContent("  repair: "+trimPathFromEnd(brief, width), width))
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) renderMemoryPanel() string {
	lines := m.renderPanelSummary()
	if m.memoryStore == nil {
		lines = append(lines, "", "(memory store unavailable)")
		return strings.Join(lines, "\n")
	}
	if m.memory.loading {
		lines = append(lines, "", "(memory loading...)")
		return strings.Join(lines, "\n")
	}
	if m.memory.err != nil {
		lines = append(lines, "", "failed to load memory: "+m.memory.err.Error())
		return strings.Join(lines, "\n")
	}
	memory := m.memory.data
	if memory == nil {
		lines = append(lines, "", "(memory is empty)")
		return strings.Join(lines, "\n")
	}

	width := maxInt(24, m.statusViewport.Width()-6)
	appendSection := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		lines = append(lines, "", title)
		for _, item := range items {
			lines = append(lines, wrapContent("- "+trimPathFromEnd(strings.TrimSpace(item), width), width))
		}
	}
	facts := make([]string, 0, len(memory.StableFacts))
	for _, item := range memory.StableFacts {
		facts = append(facts, item.Summary)
	}
	appendSection("Stable facts", facts)
	appendSection("Known failures", memory.KnownFailures)
	observations := make([]string, 0, len(memory.ReusableObservations))
	for _, item := range memory.ReusableObservations {
		observations = append(observations, fallbackString(item.Summary, item.ToolName))
	}
	appendSection("Reusable observations", observations)
	artifacts := make([]string, 0, len(memory.RecentArtifacts))
	for _, item := range memory.RecentArtifacts {
		artifacts = append(artifacts, fallbackString(item.Name, item.Kind))
	}
	appendSection("Recent artifacts", artifacts)
	if len(lines) == len(m.renderPanelSummary()) {
		lines = append(lines, "", "(memory is empty)")
	}
	return strings.Join(lines, "\n")
}

func describeAssignment(item domain.PlannedAgentAssignment) string {
	text := fallbackString(item.AgentID, "-")
	if reason := strings.TrimSpace(item.Reason); reason != "" {
		text += " - " + reason
	}
	return text
}

func describeAssignments(items []domain.PlannedAgentAssignment) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, describeAssignment(item))
	}
	return strings.Join(parts, " | ")
}

func layoutWidths(totalWidth int) (int, int, bool) {
	if totalWidth < 110 {
		return maxInt(1, totalWidth), maxInt(1, totalWidth), true
	}
	statusWidth := minInt(58, maxInt(36, (totalWidth*3)/7))
	chatWidth := maxInt(44, totalWidth-statusWidth-1)
	return chatWidth, statusWidth, false
}

func (m model) loadingStatusView() string {
	if !m.loading || m.permission != nil {
		return ""
	}
	frame := loadingFrames[m.loadingFrame%len(loadingFrames)]
	return m.styles.tool.Render(frame + " 処理中...")
}
