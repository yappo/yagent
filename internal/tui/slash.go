package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"yagent/internal/domain"
)

type slashCommand struct {
	name        string
	description string
}

var slashCommands = []slashCommand{
	{name: "/help", description: "ヘルプを表示"},
	{name: "/tools", description: "利用可能な tool 一覧を表示"},
	{name: "/tasks", description: "登録済み task 一覧を表示"},
	{name: "/mcp", description: "bind 済み MCP tool 一覧を表示"},
	{name: "/agents", description: "利用可能な agent 一覧を表示"},
	{name: "/graph", description: "Run Graph panel を表示"},
	{name: "/plan", description: "直近 run の plan を表示"},
	{name: "/verification", description: "直近 run の verification を表示"},
	{name: "/artifacts", description: "直近 run の artifacts を表示"},
	{name: "/memory", description: "repo memory を表示"},
	{name: "/failures", description: "Agent Status の失敗詳細を表示"},
	{name: "/status-filter", description: "Agent Status tree の filter を表示・変更 (/status-filter <text>|clear)"},
	{name: "/status-fold", description: "Agent Status の完了ノード折りたたみを切替 (/status-fold on|off|toggle)"},
	{name: "/status-search", description: "Agent Status の検索語を表示・変更 (/status-search <text>|clear)"},
	{name: "/model", description: "model override を表示・変更 (/model <name>|clear)"},
	{name: "/profile", description: "routing profile を表示・変更 (/profile <name>|clear)"},
	{name: "/stream", description: "streaming 応答を表示・切替 (/stream on|off)"},
	{name: "/theme", description: "TUI theme を表示・変更 (/theme <name>|clear)"},
	{name: "/continue", description: "保存済み会話を選択し、次の入力で継続 (/continue [conversation-id|latest])"},
	{name: "/recover", description: "停止した workflow を回復 (/recover <workflow-id>)"},
	{name: "/approvals", description: "approval 状態を表示"},
	{name: "/clear", description: "会話ログをクリア"},
	{name: "/exit", description: "yagent を終了"},
}

func handleSlashCommand(m model, input string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return m, m.resetComposerAndSync()
	}
	command, ok := findSlashCommand(parts[0])
	if !ok {
		m.appendChatBlock("不明なコマンドです。/help でヘルプを表示します")
		return m, m.resetComposerAndSync()
	}
	args := parts[1:]

	switch command.name {
	case "/exit":
		return m, tea.Quit
	case "/help":
		m.appendChatBlock("コマンド:")
		for _, slashCommand := range slashCommands {
			m.appendChatBlock(fmt.Sprintf("  %s - %s", slashCommand.name, slashCommand.description))
		}
		return m, m.resetComposerAndSync()
	case "/tools":
		m.appendChatBlock(formatSlashList("利用可能な tool", m.listTools())...)
		return m, m.resetComposerAndSync()
	case "/tasks":
		m.appendChatBlock(formatSlashList("登録済み task", m.listTasks())...)
		return m, m.resetComposerAndSync()
	case "/mcp":
		m.appendChatBlock(formatSlashList("bind 済み MCP tool", m.listMCPTools())...)
		return m, m.resetComposerAndSync()
	case "/agents":
		m.appendChatBlock(formatSlashList("利用可能な agent", m.listAgents())...)
		return m, m.resetComposerAndSync()
	case "/graph":
		m.setActivePanel(sidePanelRunGraph)
		m.appendChatBlock("Run Graph panel を表示します")
		return m, m.resetComposerAndSync()
	case "/plan":
		m.setActivePanel(sidePanelPlan)
		m.appendChatBlock(formatSlashList("Current plan", m.listPlan())...)
		return m, m.resetComposerAndSync()
	case "/verification":
		m.setActivePanel(sidePanelVerification)
		m.appendChatBlock(formatSlashList("Verification", m.listVerification())...)
		return m, m.resetComposerAndSync()
	case "/artifacts":
		m.appendChatBlock(formatSlashList("Run artifacts", m.listArtifacts())...)
		return m, m.resetComposerAndSync()
	case "/memory":
		m.setActivePanel(sidePanelMemory)
		m.appendChatBlock(formatSlashList("Repo memory", m.listMemory())...)
		return m, batchCmds(m.resetComposerAndSync(), m.loadMemoryCmd())
	case "/failures":
		m.setActivePanel(sidePanelRunGraph)
		if len(m.status.failures) == 0 {
			m.appendChatBlock("失敗詳細はまだありません")
		} else {
			m.status.showFailureDetail = true
			m.status.selectedFailure = minInt(m.status.selectedFailure, len(m.status.failures)-1)
			m.invalidatePanel(sidePanelRunGraph)
			m.mainPanelsCache.dirty = true
			m.appendChatBlock("Agent Status に失敗詳細を表示しました")
		}
		return m, m.resetComposerAndSync()
	case "/status-filter":
		lines := m.setStatusFilter(args)
		m.appendChatBlock(lines...)
		return m, m.resetComposerAndSync()
	case "/status-fold":
		lines := m.setStatusFold(args)
		m.appendChatBlock(lines...)
		return m, m.resetComposerAndSync()
	case "/status-search":
		lines := m.setStatusSearch(args)
		m.appendChatBlock(lines...)
		return m, m.resetComposerAndSync()
	case "/model":
		lines := m.setModelOverride(args)
		m.appendChatBlock(lines...)
		return m, m.resetComposerAndSync()
	case "/profile":
		lines := m.setRoutingProfile(args)
		m.appendChatBlock(lines...)
		return m, m.resetComposerAndSync()
	case "/stream":
		lines := m.setStreaming(args)
		m.appendChatBlock(lines...)
		return m, m.resetComposerAndSync()
	case "/theme":
		lines := m.setTheme(args)
		m.appendChatBlock(lines...)
		return m, m.resetComposerAndSync()
	case "/continue":
		lines := m.selectConversation(args)
		m.appendChatBlock(lines...)
		return m, m.resetComposerAndSync()
	case "/recover":
		return m.recoverWorkflow(args)
	case "/approvals":
		m.appendChatBlock(formatSlashList("Approvals", m.listApprovals())...)
		return m, m.resetComposerAndSync()
	case "/clear":
		m.messages = nil
		m.lastRun = nil
		m.conversationID = ""
		m.resetChatBlocks("チャット履歴をクリアしました")
		m.invalidateAllPanels()
		return m, batchCmds(m.resetComposerAndSync(), m.loadMemoryCmd())
	}

	return m, nil
}

func (m *model) setModelOverride(args []string) []string {
	if len(args) == 0 {
		return m.modelStatusLines()
	}
	value := strings.TrimSpace(strings.Join(args, " "))
	switch strings.ToLower(value) {
	case "", "clear", "default", "auto":
		m.modelOverride = ""
		m.invalidateModelHeader()
		return []string{"model override を解除しました", m.modelStatusLine()}
	default:
		m.modelOverride = value
		m.invalidateModelHeader()
		return []string{"model override を設定しました", m.modelStatusLine()}
	}
}

func (m *model) setRoutingProfile(args []string) []string {
	if len(args) == 0 {
		return m.profileStatusLines()
	}
	value := strings.TrimSpace(strings.Join(args, " "))
	switch strings.ToLower(value) {
	case "", "clear", "default", "auto":
		m.selectedProfile = ""
		m.invalidateModelHeader()
		return []string{"routing profile を解除しました", m.profileStatusLine()}
	default:
		if len(m.routingProfiles) > 0 && !containsString(m.routingProfiles, value) {
			lines := []string{"unknown routing profile: " + value}
			lines = append(lines, m.profileStatusLines()...)
			return lines
		}
		m.selectedProfile = value
		m.invalidateModelHeader()
		return []string{"routing profile を設定しました", m.profileStatusLine()}
	}
}

func (m *model) modelStatusLines() []string {
	lines := []string{m.modelStatusLine()}
	if m.selectedProfile != "" {
		lines = append(lines, "profile="+m.selectedProfile+" が優先され、model override は全 agent に明示 model として渡ります")
	}
	return lines
}

func (m model) modelStatusLine() string {
	if m.modelOverride != "" {
		return "model override: " + m.modelOverride
	}
	return "model override: (none; routing/default model)"
}

func (m model) modelDisplayLabel() string {
	if m.modelOverride != "" {
		return m.modelOverride
	}
	return "(routing)"
}

func (m model) profileStatusLines() []string {
	lines := []string{m.profileStatusLine()}
	if len(m.routingProfiles) > 0 {
		lines = append(lines, "available: "+strings.Join(m.routingProfiles, ", "))
	}
	return lines
}

func (m model) profileStatusLine() string {
	if m.selectedProfile != "" {
		return "routing profile: " + m.selectedProfile
	}
	return "routing profile: (default)"
}

func (m model) profileDisplayLabel() string {
	if m.selectedProfile != "" {
		return m.selectedProfile
	}
	return "(default)"
}

func (m *model) setStreaming(args []string) []string {
	if len(args) == 0 {
		return []string{m.streamingStatusLine()}
	}
	value := strings.ToLower(strings.TrimSpace(strings.Join(args, " ")))
	switch value {
	case "on", "true", "1", "enable", "enabled":
		m.streaming = true
		m.invalidateModelHeader()
		return []string{"streaming 応答を有効にしました", m.streamingStatusLine()}
	case "off", "false", "0", "disable", "disabled":
		m.streaming = false
		m.invalidateModelHeader()
		return []string{"streaming 応答を無効にしました", m.streamingStatusLine()}
	case "toggle":
		m.streaming = !m.streaming
		m.invalidateModelHeader()
		return []string{"streaming 応答を切り替えました", m.streamingStatusLine()}
	default:
		return []string{"usage: /stream on|off|toggle", m.streamingStatusLine()}
	}
}

func (m model) streamingStatusLine() string {
	if m.streaming {
		return "streaming: on"
	}
	return "streaming: off"
}

func (m model) streamingDisplayLabel() string {
	if m.streaming {
		return "on"
	}
	return "off"
}

func (m *model) invalidateModelHeader() {
	m.headerCache.dirty = true
	m.mainPanelsCache.dirty = true
}

func findSlashCommand(name string) (slashCommand, bool) {
	for _, command := range slashCommands {
		if command.name == name {
			return command, true
		}
	}
	return slashCommand{}, false
}

func (m model) listTools() []string {
	if m.tools == nil {
		return nil
	}

	definitions := m.tools.Definitions(domain.AgentSpec{})
	items := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		if strings.HasPrefix(definition.Name, "mcp__") {
			continue
		}
		description := strings.TrimSpace(definition.Description)
		if description == "" {
			description = "(説明なし)"
		}
		items = append(items, fmt.Sprintf("%s - %s", definition.Name, description))
	}
	sort.Strings(items)
	return items
}

func (m model) listTasks() []string {
	if m.taskCatalog == nil {
		return nil
	}

	tasks := m.taskCatalog.List(context.Background())
	items := make([]string, 0, len(tasks))
	for _, task := range tasks {
		description := strings.TrimSpace(task.Description)
		if description == "" {
			description = "(説明なし)"
		}
		items = append(items, fmt.Sprintf("%s - %s", task.ID, description))
	}
	sort.Strings(items)
	return items
}

func (m model) listMCPTools() []string {
	if m.mcpBindings == nil {
		return nil
	}

	bound := m.mcpBindings.BoundTools()
	items := make([]string, 0, len(bound))
	for _, tool := range bound {
		description := strings.TrimSpace(tool.Description)
		if description == "" {
			description = "(説明なし)"
		}
		items = append(items, fmt.Sprintf("%s - %s", tool.QualifiedName, description))
	}
	sort.Strings(items)
	return items
}

func (m model) listAgents() []string {
	if m.agentCatalog == nil {
		return nil
	}

	agents := m.agentCatalog.List()
	items := make([]string, 0, len(agents))
	for _, agent := range agents {
		description := strings.TrimSpace(agent.Description)
		if description == "" {
			description = "(説明なし)"
		}
		items = append(items, fmt.Sprintf("%s - %s", agent.ID, description))
	}
	sort.Strings(items)
	return items
}

func (m model) listPlan() []string {
	run := m.lastRun
	if run == nil {
		return []string{"(no run loaded)"}
	}
	if run.ExecutionPlan != nil {
		items := []string{
			fmt.Sprintf("mode=%s task=%s source=%s", fallbackString(run.ExecutionPlan.Mode, "-"), fallbackString(string(run.ExecutionPlan.TaskKind), "-"), fallbackString(run.ExecutionPlan.Source, "-")),
			"primary: " + describeAssignment(run.ExecutionPlan.Primary),
		}
		if run.ExecutionPlan.Plan != nil {
			items = append(items, "plan: "+describeAssignment(*run.ExecutionPlan.Plan))
		}
		if len(run.ExecutionPlan.Preparation) > 0 {
			items = append(items, "preparation: "+describeAssignments(run.ExecutionPlan.Preparation))
		}
		if len(run.ExecutionPlan.Verify) > 0 {
			items = append(items, "verify: "+describeAssignments(run.ExecutionPlan.Verify))
		}
		if run.ExecutionPlan.Recovery != nil {
			items = append(items, "recovery: "+describeAssignment(*run.ExecutionPlan.Recovery))
		}
		if run.ExecutionPlan.Finalize != nil {
			items = append(items, "finalize: "+describeAssignment(*run.ExecutionPlan.Finalize))
		}
		if reason := strings.TrimSpace(run.ExecutionPlan.FallbackReason); reason != "" {
			items = append(items, "fallback: "+reason)
		}
		for _, node := range run.Plan {
			items = append(items, fmt.Sprintf("step: %s [%s]", node.Title, node.Status))
		}
		return items
	}
	if len(run.Plan) == 0 {
		return []string{"(no plan available)"}
	}
	items := make([]string, 0, len(run.Plan))
	for _, node := range run.Plan {
		items = append(items, fmt.Sprintf("%s [%s]", node.Title, node.Status))
	}
	return items
}

func (m model) listArtifacts() []string {
	run := m.lastRun
	if run == nil {
		return []string{"(no run loaded)"}
	}
	if len(run.Artifacts) == 0 {
		return []string{"(no artifacts available)"}
	}
	items := make([]string, 0, len(run.Artifacts))
	for _, artifact := range run.Artifacts {
		items = append(items, fmt.Sprintf("%s - %s - %s", artifact.Phase, artifact.Name, fallbackString(artifact.Summary, "(no summary)")))
	}
	return items
}

func (m model) listVerification() []string {
	run := m.lastRun
	if run == nil {
		return []string{"(no run loaded)"}
	}
	if len(run.Verification) == 0 {
		return []string{"(no verification available)"}
	}
	items := make([]string, 0, len(run.Verification))
	for _, item := range run.Verification {
		line := fmt.Sprintf("try %d %s [%s]", item.Attempt, item.SourceAgent, item.Status)
		if summary := strings.TrimSpace(item.Summary); summary != "" {
			line += " - " + summary
		}
		items = append(items, line)
	}
	return items
}

func (m model) listMemory() []string {
	if m.memoryStore == nil {
		return []string{"(memory store unavailable)"}
	}
	if m.memory.loading {
		return []string{"(memory loading...)"}
	}
	if m.memory.err != nil {
		return []string{"failed to load memory: " + m.memory.err.Error()}
	}
	memory := m.memory.data
	if memory == nil {
		return []string{"(memory is empty)"}
	}
	items := []string{}
	for _, fact := range memory.StableFacts {
		items = append(items, "fact: "+fact.Summary)
	}
	for _, failure := range memory.KnownFailures {
		items = append(items, "failure: "+failure)
	}
	for _, observation := range memory.ReusableObservations {
		items = append(items, "observation: "+fallbackString(observation.Summary, observation.ToolName))
	}
	for _, artifact := range memory.RecentArtifacts {
		items = append(items, "artifact: "+fallbackString(artifact.Name, artifact.Kind))
	}
	if len(items) == 0 {
		return []string{"(memory is empty)"}
	}
	return items
}

func (m model) listApprovals() []string {
	items := []string{
		fmt.Sprintf("session approvals: %d", len(m.sessionApprovals)),
		fmt.Sprintf("pattern approvals: %d", len(m.patternApprovals)),
	}
	sessionKeys := make([]string, 0, len(m.sessionApprovals))
	for key := range m.sessionApprovals {
		sessionKeys = append(sessionKeys, key)
	}
	sort.Strings(sessionKeys)
	for _, key := range sessionKeys {
		items = append(items, "session approval: "+formatApprovalKeyForDisplay(key))
	}
	for _, approval := range m.patternApprovals {
		items = append(items, fmt.Sprintf(
			"pattern approval: tool=%s action=%s kind=%s risk=%s pattern=%s",
			fallbackString(approval.toolName, "-"),
			fallbackString(approval.action, "-"),
			fallbackString(approval.resourceKind, "-"),
			fallbackString(approval.risk, "-"),
			fallbackString(approval.pattern, "-"),
		))
	}
	if m.permission != nil {
		items = append(items, "pending approval: "+formatPermissionScopeForDisplay(m.permission.request)+formatPermissionBatchSuffix(*m.permission))
	}
	for index, state := range m.permissionQueue {
		items = append(items, fmt.Sprintf("queued approval %d: %s%s", index+1, formatPermissionScopeForDisplay(state.request), formatPermissionBatchSuffix(state)))
	}
	return items
}

func (m *model) selectConversation(args []string) []string {
	store, ok := m.runStore.(domain.ConversationStore)
	if !ok {
		return []string{"continue unavailable: conversation store is not configured"}
	}
	conversationID := strings.TrimSpace(strings.Join(args, " "))
	if conversationID == "" {
		conversationID = "latest"
	}
	turns, err := store.ListConversationTurns(context.Background(), 0)
	if err != nil {
		return []string{"continue failed: " + err.Error()}
	}
	var selected *domain.ConversationTurnRecord
	for index := range turns {
		turn := &turns[index]
		if turn.ConversationID == "" {
			continue
		}
		if conversationID == "latest" || string(turn.ConversationID) == conversationID {
			selected = turn
			break
		}
	}
	if selected == nil {
		if conversationID == "latest" {
			return []string{"continue failed: no saved conversation found"}
		}
		return []string{"continue failed: conversation not found: " + conversationID}
	}
	m.conversationID = selected.ConversationID
	m.messages = nil
	m.lastRun = nil
	if selected.Profile != "" {
		m.selectedProfile = selected.Profile
		m.invalidateModelHeader()
	}
	m.invalidateAllPanels()
	return []string{fmt.Sprintf("Selected conversation %s", selected.ConversationID), "The next message starts a new workflow."}
}

func (m model) recoverWorkflow(args []string) (tea.Model, tea.Cmd) {
	workflowID := strings.TrimSpace(strings.Join(args, " "))
	if workflowID == "" {
		m.appendChatBlock("recover failed: workflow id is required")
		return m, m.resetComposerAndSync()
	}
	m.resetStreamingBlock()
	m.loading = true
	m.loadingFrame = 0
	m.mainPanelsCache.dirty = true
	send := func() tea.Msg {
		result, err := m.runner.RecoverWorkflow(context.Background(), domain.WorkflowRecoveryRequest{WorkflowID: domain.WorkflowID(workflowID)})
		return chatMessage{content: result.Message.Content, run: result.Run, err: err, displayOnly: true}
	}
	return m, batchCmds(send, loadingTick(), m.resetComposerAndSync())
}

func formatSlashList(title string, items []string) []string {
	if len(items) == 0 {
		return []string{title + ":", "  (なし)"}
	}

	lines := make([]string, 0, len(items)+1)
	lines = append(lines, title+":")
	for _, item := range items {
		lines = append(lines, "  "+item)
	}
	return lines
}
