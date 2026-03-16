package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"

	"yagent/internal/domain"
)

type chatMessage struct {
	content string
	err     error
}

type loadingTickMsg struct{}

type statusEventMsg struct {
	event domain.ExecutionEvent
}

type permissionState struct {
	request       domain.PermissionRequest
	response      chan domain.PermissionDecision
	selectedIndex int
}

type permissionOption struct {
	label    string
	decision domain.PermissionDecision
}

type slashCommand struct {
	name        string
	description string
}

type model struct {
	runner           domain.Orchestrator
	workingDir       string
	selectedRefs     map[string]string
	messages         []domain.Message
	output           []string
	loading          bool
	loadingFrame     int
	width            int
	height           int
	viewport         viewport.Model
	statusViewport   viewport.Model
	textarea         textarea.Model
	history          []string
	historyIndex     int
	permission       *permissionState
	permissionQueue  []permissionState
	sessionApprovals map[string]bool
	status           statusState
	statusEvents     <-chan domain.ExecutionEvent
	cancelStatus     func()
	styles           styles
}

type statusState struct {
	nodes      map[string]*agentStatusNode
	rootRunIDs []string
	recent     []domain.ExecutionEvent
}

type agentStatusNode struct {
	RunID        string
	ParentRunID  string
	AgentID      string
	Status       string
	Detail       string
	StartedAt    time.Time
	UpdatedAt    time.Time
	ContextCount int
}

type styles struct {
	user               lipgloss.Style
	assistant          lipgloss.Style
	tool               lipgloss.Style
	separator          lipgloss.Style
	hint               lipgloss.Style
	permissionCard     lipgloss.Style
	permissionTitle    lipgloss.Style
	permissionPath     lipgloss.Style
	permissionHelp     lipgloss.Style
	permissionOption   lipgloss.Style
	permissionSelected lipgloss.Style
	commandHint        lipgloss.Style
	commandCandidate   lipgloss.Style
	commandSelected    lipgloss.Style
}

var loadingFrames = []string{"◐", "◓", "◑", "◒"}

var permissionOptions = []permissionOption{
	{label: "今回だけ許可", decision: domain.PermissionAllowOnce},
	{label: "このセッションで許可", decision: domain.PermissionAllowSession},
	{label: "拒否", decision: domain.PermissionDeny},
}

var slashCommands = []slashCommand{
	{name: "/help", description: "ヘルプを表示"},
	{name: "/clear", description: "会話ログをクリア"},
	{name: "/exit", description: "yagent を終了"},
}

const (
	userOutputLabel      = "__USER__"
	assistantOutputLabel = "__ASSISTANT__"
	loadingInterval      = 100 * time.Millisecond
	maxComposerHeight    = 6
	paneChromeHeight     = 3
	stackedPaneGap       = 1
	paneHorizontalFrame  = 4
)

func newModel(runner domain.Orchestrator, workingDir string) model {
	ta := textarea.New()
	ta.Placeholder = "質問を入力... (Ctrl+J で改行, Enter で送信, /exit または Ctrl+C で終了)"
	ta.CharLimit = 50000
	ta.ShowLineNumbers = false
	ta.SetPromptFunc(2, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			return "❯ "
		}
		return "  "
	})
	styles := ta.Styles()
	styles.Focused.CursorLine = lipgloss.NewStyle()
	styles.Blurred.CursorLine = lipgloss.NewStyle()
	ta.SetStyles(styles)
	ta.Focus()
	ta.SetVirtualCursor(false)
	ta.SetHeight(1)

	m := model{
		runner:           runner,
		workingDir:       workingDir,
		selectedRefs:     map[string]string{},
		viewport:         viewport.New(),
		statusViewport:   viewport.New(),
		textarea:         ta,
		history:          []string{},
		sessionApprovals: map[string]bool{},
		status: statusState{
			nodes: map[string]*agentStatusNode{},
		},
	}
	if stream, ok := runner.(domain.ExecutionEventStream); ok {
		m.statusEvents, m.cancelStatus = stream.SubscribeEvents()
	}
	m.styles = defaultStyles()
	return m
}

func defaultStyles() styles {
	return styles{
		user:      lipgloss.NewStyle().Foreground(lipgloss.Color("206")).Bold(true),
		assistant: lipgloss.NewStyle().Foreground(lipgloss.Color("75")),
		tool:      lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Italic(true),
		separator: lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		hint:      lipgloss.NewStyle().Foreground(lipgloss.Color("99")).Italic(true),
		permissionCard: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1),
		permissionTitle:  lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Bold(true),
		permissionPath:   lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		permissionHelp:   lipgloss.NewStyle().Foreground(lipgloss.Color("244")),
		permissionOption: lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Padding(0, 1),
		permissionSelected: lipgloss.NewStyle().
			Foreground(lipgloss.Color("232")).
			Background(lipgloss.Color("221")).
			Bold(true).
			Padding(0, 1),
		commandHint:      lipgloss.NewStyle().Foreground(lipgloss.Color("244")),
		commandCandidate: lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		commandSelected: lipgloss.NewStyle().
			Foreground(lipgloss.Color("232")).
			Background(lipgloss.Color("110")).
			Bold(true).
			Padding(0, 1),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, listenStatusEvents(m.statusEvents))
}

func (m *model) syncComposer() {
	height := strings.Count(m.textarea.Value(), "\n") + 1
	if height < 1 {
		height = 1
	}
	if height > maxComposerHeight {
		height = maxComposerHeight
	}
	m.textarea.SetHeight(height)
	if m.width > 0 {
		m.textarea.SetWidth(m.width)
	}
}

func (m *model) syncLayout() {
	m.syncComposer()
	if m.width <= 0 || m.height <= 0 {
		return
	}

	footerHeight := lipgloss.Height(m.textarea.View()) + 1
	if m.hasCompletionCandidates() {
		footerHeight += m.completionSuggestionsHeight()
	}
	if m.permission != nil {
		footerHeight += m.permissionCardHeight()
	}
	headerHeight := 3
	chatWidth, statusWidth, stacked := layoutWidths(m.width)
	mainHeight := maxInt(3, m.height-headerHeight-footerHeight)
	if stacked {
		contentHeight := maxInt(3, mainHeight-(paneChromeHeight*2)-stackedPaneGap)
		statusHeight := maxInt(3, minInt(8, contentHeight/3))
		chatHeight := maxInt(3, contentHeight-statusHeight)
		m.statusViewport.SetWidth(chatWidth)
		m.statusViewport.SetHeight(statusHeight)
		m.viewport.SetWidth(chatWidth)
		m.viewport.SetHeight(chatHeight)
	} else {
		m.viewport.SetWidth(chatWidth)
		m.viewport.SetHeight(maxInt(3, mainHeight-paneChromeHeight))
		m.statusViewport.SetWidth(statusWidth)
		m.statusViewport.SetHeight(maxInt(3, mainHeight-paneChromeHeight))
	}
	m.refreshViewport()
}

func (m *model) refreshViewport() {
	m.viewport.SetContent(m.renderLog())
	m.statusViewport.SetContent(m.renderStatus())
	m.viewport.GotoBottom()
	m.statusViewport.GotoTop()
}

func (m model) renderLog() string {
	var sb strings.Builder
	contentWidth := maxInt(1, m.viewport.Width())

	for _, line := range m.output {
		switch {
		case line == userOutputLabel:
			sb.WriteString(m.styles.user.Render("You"))
		case line == assistantOutputLabel:
			sb.WriteString(m.styles.assistant.Render("yagent"))
		case strings.HasPrefix(line, "────────"):
			sb.WriteString(m.styles.separator.Render(strings.Repeat("─", contentWidth)))
		default:
			sb.WriteString(wrapContent(line, contentWidth))
		}
		sb.WriteString("\n")
	}

	if m.loading && m.permission == nil {
		frame := loadingFrames[m.loadingFrame%len(loadingFrames)]
		sb.WriteString(m.styles.tool.Render(wrapContent(frame+" 処理中...", contentWidth)))
		sb.WriteString("\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}

func wrapContent(content string, width int) string {
	if width <= 0 {
		return content
	}

	return runewidth.Wrap(content, width)
}

func appendOutputBlock(output []string, label, content string) []string {
	output = append(output, label)
	output = append(output, strings.Split(content, "\n")...)
	output = append(output, "───────────────────────────────────────────────────────────────────────")
	return output
}

func approvalKey(request domain.PermissionRequest) string {
	return request.ToolName + "\x00" + request.Resource
}

func (m model) currentInput() string {
	return strings.TrimSpace(m.textarea.Value())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case permissionRequestMsg:
		if m.sessionApprovals[approvalKey(msg.request)] {
			msg.response <- domain.PermissionAllowSession
			close(msg.response)
			return m, nil
		}
		state := permissionState{
			request:       msg.request,
			response:      msg.response,
			selectedIndex: 0,
		}
		if m.permission == nil {
			m.permission = &state
		} else {
			m.permissionQueue = append(m.permissionQueue, state)
		}
		m.syncLayout()
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.syncLayout()
		return m, nil

	case loadingTickMsg:
		if !m.loading {
			return m, nil
		}
		m.loadingFrame = (m.loadingFrame + 1) % len(loadingFrames)
		m.refreshViewport()
		return m, loadingTick()

	case chatMessage:
		m.loading = false
		if msg.err != nil {
			m.output = append(m.output, "実行エラー: "+msg.err.Error())
			if len(m.messages) > 0 {
				m.messages = m.messages[:len(m.messages)-1]
			}
			m.refreshViewport()
			return m, nil
		}
		m.messages = append(m.messages, domain.Message{
			Role:    domain.RoleAssistant,
			Content: msg.content,
		})
		m.output = appendOutputBlock(m.output, assistantOutputLabel, msg.content)
		m.refreshViewport()
		return m, nil

	case statusEventMsg:
		m.applyStatusEvent(msg.event)
		m.refreshViewport()
		return m, listenStatusEvents(m.statusEvents)

	case tea.PasteMsg:
		if m.permission != nil {
			return m, nil
		}
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		m.reconcileSelectedRefs()
		m.syncLayout()
		return m, cmd

	case tea.KeyMsg:
		if m.permission != nil {
			return handlePermissionKeys(m, msg)
		}
		return handleComposerKeys(m, msg)
	}

	return m, nil
}

func handlePermissionKeys(m model, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.resolvePermission(domain.PermissionDeny)
		return m, tea.Quit
	}

	switch msg.String() {
	case "left", "shift+tab":
		m.permission.selectedIndex = wrapIndex(m.permission.selectedIndex-1, len(permissionOptions))
		m.syncLayout()
		return m, nil
	case "right", "tab":
		m.permission.selectedIndex = wrapIndex(m.permission.selectedIndex+1, len(permissionOptions))
		m.syncLayout()
		return m, nil
	case "enter":
		m.resolvePermission(permissionOptions[m.permission.selectedIndex].decision)
		return m, nil
	case "esc":
		m.resolvePermission(domain.PermissionDeny)
		return m, nil
	}

	switch strings.ToLower(msg.String()) {
	case "1", "2", "3":
		idx := int(msg.String()[0] - '1')
		if idx >= 0 && idx < len(permissionOptions) {
			m.permission.selectedIndex = idx
			m.resolvePermission(permissionOptions[idx].decision)
		}
	case "y":
		m.resolvePermission(domain.PermissionAllowOnce)
	case "n":
		m.resolvePermission(domain.PermissionDeny)
	}

	return m, nil
}

func handleComposerKeys(m model, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "tab":
		if m.hasCompletionCandidates() {
			m.applyCompletion()
			return m, nil
		}
		return m, nil
	case "pgup":
		m.viewport.PageUp()
		return m, nil
	case "pgdown":
		m.viewport.PageDown()
		return m, nil
	case "up", "alt+up":
		if msg.String() == "alt+up" {
			m.viewport.ScrollUp(1)
			return m, nil
		}
		if m.textarea.Line() > 0 {
			m.textarea.CursorUp()
			m.syncLayout()
			return m, nil
		}
		if m.historyIndex > 0 {
			m.historyIndex--
			m.textarea.SetValue(m.history[m.historyIndex])
			m.reconcileSelectedRefs()
			m.syncLayout()
		}
		return m, nil
	case "down", "alt+down":
		if msg.String() == "alt+down" {
			m.viewport.ScrollDown(1)
			return m, nil
		}
		if m.textarea.Line() < m.textarea.LineCount()-1 {
			m.textarea.CursorDown()
			m.syncLayout()
			return m, nil
		}
		if m.historyIndex < len(m.history)-1 {
			m.historyIndex++
			m.textarea.SetValue(m.history[m.historyIndex])
		} else {
			m.historyIndex = len(m.history)
			m.textarea.Reset()
		}
		m.reconcileSelectedRefs()
		m.syncLayout()
		return m, nil
	case "ctrl+j":
		m.textarea.InsertString("\n")
		m.reconcileSelectedRefs()
		m.syncLayout()
		return m, nil
	case "enter":
		input := strings.TrimSpace(m.textarea.Value())
		if input == "" {
			return m, nil
		}
		if strings.HasPrefix(input, "/") {
			return handleSlashCommand(m, input)
		}
		return submitPrompt(m, input)
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.reconcileSelectedRefs()
	m.syncLayout()
	return m, cmd
}

func handleSlashCommand(m model, input string) (tea.Model, tea.Cmd) {
	command, ok := findSlashCommand(input)
	if !ok {
		m.output = append(m.output, "不明なコマンドです。/help でヘルプを表示します")
		m.textarea.Reset()
		m.syncLayout()
		return m, nil
	}

	switch command.name {
	case "/exit":
		return m, tea.Quit
	case "/help":
		m.output = append(m.output, "コマンド:")
		for _, slashCommand := range slashCommands {
			m.output = append(m.output, fmt.Sprintf("  %s - %s", slashCommand.name, slashCommand.description))
		}
		m.textarea.Reset()
		m.syncLayout()
		return m, nil
	case "/clear":
		m.messages = nil
		m.output = []string{"チャット履歴をクリアしました"}
		m.textarea.Reset()
		m.syncLayout()
		return m, nil
	}

	return m, nil
}

func submitPrompt(m model, input string) (tea.Model, tea.Cmd) {
	m.history = append(m.history, input)
	m.historyIndex = len(m.history)
	normalized := normalizePromptReferences(input, m.selectedRefs)
	m.messages = append(m.messages, domain.Message{
		Role:    domain.RoleUser,
		Content: normalized,
	})
	m.output = appendOutputBlock(m.output, userOutputLabel, input)
	m.textarea.Reset()
	m.selectedRefs = map[string]string{}
	m.loading = true
	m.loadingFrame = 0
	m.syncLayout()
	m.refreshViewport()

	send := func() tea.Msg {
		result, err := m.runner.RunTurn(context.Background(), domain.TurnRequest{
			Messages: m.messages,
		})
		return chatMessage{content: result.Message.Content, err: err}
	}

	return m, tea.Batch(send, loadingTick())
}

func (m *model) resolvePermission(decision domain.PermissionDecision) {
	if m.permission == nil {
		return
	}

	if decision == domain.PermissionAllowSession {
		m.sessionApprovals[approvalKey(m.permission.request)] = true
	}

	m.output = append(m.output, fmt.Sprintf("%s [%s] %s を%s",
		permissionRequesterLabel(m.permission.request),
		permissionRequesterType(m.permission.request),
		m.permission.request.Operation,
		permissionDecisionLabel(decision)+" ("+m.permission.request.Resource+")",
	))
	m.permission.response <- decision
	close(m.permission.response)
	if len(m.permissionQueue) > 0 {
		next := m.permissionQueue[0]
		m.permissionQueue = m.permissionQueue[1:]
		m.permission = &next
	} else {
		m.permission = nil
	}
	m.syncLayout()
}

func (m *model) applyStatusEvent(event domain.ExecutionEvent) {
	node, ok := m.status.nodes[event.RunID]
	if !ok {
		node = &agentStatusNode{
			RunID:       event.RunID,
			ParentRunID: event.ParentRunID,
			AgentID:     event.AgentID,
		}
		m.status.nodes[event.RunID] = node
		if event.ParentRunID == "" {
			m.status.rootRunIDs = appendUnique(m.status.rootRunIDs, event.RunID)
		}
	}
	if node.AgentID == "" {
		node.AgentID = event.AgentID
	}
	if node.ParentRunID == "" {
		node.ParentRunID = event.ParentRunID
	}
	if node.StartedAt.IsZero() {
		node.StartedAt = event.Timestamp
	}
	node.UpdatedAt = event.Timestamp
	node.Detail = event.Detail
	if event.ContextCount > 0 {
		node.ContextCount = event.ContextCount
	}
	switch event.Type {
	case "agent_started", "delegate_started", "handoff_started":
		node.Status = "running"
	case "llm_called":
		if node.Status == "" {
			node.Status = "thinking"
		}
	case "tool_called":
		node.Status = "working"
	case "tool_failed", "agent_failed":
		node.Status = "failed"
	case "agent_completed":
		node.Status = "done"
	}
	m.status.recent = append([]domain.ExecutionEvent{event}, m.status.recent...)
	if len(m.status.recent) > 8 {
		m.status.recent = m.status.recent[:8]
	}
}

func permissionDecisionLabel(decision domain.PermissionDecision) string {
	switch decision {
	case domain.PermissionAllowOnce:
		return "今回だけ許可"
	case domain.PermissionAllowSession:
		return "このセッションで許可"
	default:
		return "拒否"
	}
}

func (m model) permissionCardHeight() int {
	if m.permission == nil {
		return 0
	}
	return lipgloss.Height(m.renderPermissionCard())
}

func (m model) renderPermissionCard() string {
	if m.permission == nil {
		return ""
	}

	cardWidth := maxInt(32, m.width-2)
	resource := trimPathFromEnd(m.permission.request.Resource, cardWidth-4)
	lines := []string{
		m.styles.permissionTitle.Render(m.permission.request.Operation),
		m.styles.permissionPath.Render(resource),
		m.styles.permissionHelp.Render("requester: " + permissionRequesterDisplay(m.permission.request)),
	}
	if m.permission.request.Purpose != "" {
		lines = append(lines, m.styles.permissionHelp.Render("purpose: "+m.permission.request.Purpose))
	}
	lines = append(lines,
		renderPermissionOptions(m.permission.selectedIndex, m.styles.permissionSelected, m.styles.permissionOption),
		m.styles.permissionHelp.Render("←/→ または Tab で選択 • Enter で確定 • Esc で拒否"),
	)
	card := strings.Join(lines, "\n")
	return m.styles.permissionCard.Width(cardWidth).Render(card)
}

func renderPermissionOptions(selected int, selectedStyle, baseStyle lipgloss.Style) string {
	parts := make([]string, 0, len(permissionOptions))
	for i, option := range permissionOptions {
		label := fmt.Sprintf("%d. %s", i+1, option.label)
		if i == selected {
			parts = append(parts, selectedStyle.Render("[ "+label+" ]"))
			continue
		}
		parts = append(parts, baseStyle.Render("  "+label+"  "))
	}
	return strings.Join(parts, "  ")
}

func trimPathFromEnd(path string, width int) string {
	if width <= 0 || lipgloss.Width(path) <= width {
		return path
	}

	runes := []rune(path)
	for len(runes) > 0 && lipgloss.Width("..."+string(runes)) > width {
		runes = runes[1:]
	}
	return "..." + string(runes)
}

func wrapIndex(index, count int) int {
	return (index + count) % count
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func loadingTick() tea.Cmd {
	return tea.Tick(loadingInterval, func(time.Time) tea.Msg {
		return loadingTickMsg{}
	})
}

func findSlashCommand(name string) (slashCommand, bool) {
	for _, command := range slashCommands {
		if command.name == name {
			return command, true
		}
	}
	return slashCommand{}, false
}

func (m model) View() tea.View {
	var sb strings.Builder
	offsetY := 0
	sb.WriteString(m.styles.hint.Render("入力： コマンド：/help, /clear, /exit | Alt+↑/↓ と PgUp/PgDn でログ"))
	sb.WriteString("\n")
	sb.WriteString(m.styles.separator.Render(strings.Repeat("─", maxInt(1, m.width))))
	sb.WriteString("\n\n")
	offsetY += 3
	mainView := m.renderMainPanels()
	sb.WriteString(mainView)
	offsetY += lipgloss.Height(mainView)
	sb.WriteString("\n")
	offsetY++
	if m.permission != nil {
		card := m.renderPermissionCard()
		sb.WriteString(card)
		sb.WriteString("\n")
		offsetY += lipgloss.Height(card) + 1
	}
	if m.hasCompletionCandidates() {
		suggestions := m.renderCompletionSuggestions()
		sb.WriteString(suggestions)
		sb.WriteString("\n")
		offsetY += lipgloss.Height(suggestions) + 1
	}
	sb.WriteString("\n")
	offsetY++
	sb.WriteString(m.textarea.View())

	view := tea.NewView(sb.String())
	view.AltScreen = true
	if cursor := m.textarea.Cursor(); cursor != nil {
		cursor.Position.Y += offsetY
		view.Cursor = cursor
	}
	return view
}

func (m model) renderMainPanels() string {
	chatTitle := "Chat"
	if metrics := m.currentRunMetrics(); metrics != "" {
		chatTitle += "  " + metrics
	}
	chatPane := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Width(maxInt(1, m.viewport.Width()+paneHorizontalFrame)).
		Render(chatTitle + "\n" + m.viewport.View())
	statusTitle := "Agent Status"
	if metrics := m.currentRunMetrics(); metrics != "" {
		statusTitle += "  " + metrics
	}
	statusPane := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Width(maxInt(1, m.statusViewport.Width()+paneHorizontalFrame)).
		Render(statusTitle + "\n" + m.statusViewport.View())

	_, _, stacked := layoutWidths(m.width)
	if stacked {
		return chatPane + "\n" + statusPane
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, chatPane, statusPane)
}

func (m model) renderStatus() string {
	if len(m.status.nodes) == 0 {
		return "まだサブエージェントは動いていません。"
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("running %d  done %d  failed %d", m.countStatus("running", "working", "thinking"), m.countStatus("done"), m.countStatus("failed")))
	lines = append(lines, "")

	rootIDs := append([]string(nil), m.status.rootRunIDs...)
	sort.Strings(rootIDs)
	for _, runID := range rootIDs {
		lines = append(lines, m.renderStatusTree(runID, "", true)...)
	}

	if len(m.status.recent) > 0 {
		lines = append(lines, "", "Recent")
		for _, event := range m.status.recent {
			line := fmt.Sprintf("%s %s %s", event.Timestamp.Format("15:04:05"), shortType(event.Type), event.AgentID)
			if event.Detail != "" {
				line += "  " + trimPathFromEnd(event.Detail, maxInt(20, m.statusViewport.Width()-12))
			}
			lines = append(lines, line)
		}
	}

	return strings.Join(lines, "\n")
}

func (m model) renderStatusTree(runID, prefix string, last bool) []string {
	node, ok := m.status.nodes[runID]
	if !ok {
		return nil
	}
	branch := "├─ "
	nextPrefix := prefix + "│  "
	if last {
		branch = "└─ "
		nextPrefix = prefix + "   "
	}
	if prefix == "" {
		branch = ""
		nextPrefix = ""
	}

	line := fmt.Sprintf("%s%s  %s  %s", prefix+branch, titleCase(node.AgentID), statusLabel(node.Status), formatNodeMetrics(node))
	if node.Detail != "" {
		line += "  " + trimPathFromEnd(strings.TrimSpace(node.Detail), maxInt(16, m.statusViewport.Width()-10))
	}
	lines := []string{line}

	children := m.childRunIDs(runID)
	for idx, childID := range children {
		lines = append(lines, m.renderStatusTree(childID, nextPrefix, idx == len(children)-1)...)
	}
	return lines
}

func (m model) childRunIDs(parentRunID string) []string {
	var ids []string
	for runID, node := range m.status.nodes {
		if node.ParentRunID == parentRunID {
			ids = append(ids, runID)
		}
	}
	sort.Strings(ids)
	return ids
}

func (m model) countStatus(statuses ...string) int {
	allowed := map[string]bool{}
	for _, status := range statuses {
		allowed[status] = true
	}
	count := 0
	for _, node := range m.status.nodes {
		if allowed[node.Status] {
			count++
		}
	}
	return count
}

func listenStatusEvents(ch <-chan domain.ExecutionEvent) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return nil
		}
		return statusEventMsg{event: event}
	}
}

func layoutWidths(totalWidth int) (int, int, bool) {
	if totalWidth < 110 {
		return maxInt(1, totalWidth), maxInt(1, totalWidth), true
	}
	statusWidth := minInt(42, maxInt(30, totalWidth/3))
	chatWidth := maxInt(40, totalWidth-statusWidth-1)
	return chatWidth, statusWidth, false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func appendUnique(values []string, value string) []string {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func statusLabel(status string) string {
	switch status {
	case "running", "working":
		return "[running]"
	case "thinking":
		return "[thinking]"
	case "done":
		return "[done]"
	case "failed":
		return "[failed]"
	default:
		return "[queued]"
	}
}

func shortType(value string) string {
	switch value {
	case "agent_started":
		return "start"
	case "agent_completed":
		return "done "
	case "delegate_started":
		return "deleg"
	case "handoff_started":
		return "hand "
	case "tool_called":
		return "tool "
	case "tool_failed":
		return "fail "
	case "agent_failed":
		return "afail"
	default:
		return value
	}
}

func titleCase(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func permissionRequesterDisplay(request domain.PermissionRequest) string {
	label := permissionRequesterLabel(request)
	if permissionRequesterType(request) == "main" {
		return label + " (main)"
	}
	return label + " (subagent)"
}

func permissionRequesterLabel(request domain.PermissionRequest) string {
	switch request.AgentID {
	case "", "manager":
		return "manager"
	default:
		return request.AgentID
	}
}

func permissionRequesterType(request domain.PermissionRequest) string {
	switch request.AgentID {
	case "", "manager":
		return "main"
	default:
		return "subagent"
	}
}

func (m model) currentRunMetrics() string {
	node := m.currentRootNode()
	if node == nil {
		return ""
	}
	return formatMetrics(node.StartedAt, node.UpdatedAt, node.Status, node.ContextCount)
}

func (m model) currentRootNode() *agentStatusNode {
	if len(m.status.rootRunIDs) == 0 {
		return nil
	}
	runID := m.status.rootRunIDs[len(m.status.rootRunIDs)-1]
	return m.status.nodes[runID]
}

func formatNodeMetrics(node *agentStatusNode) string {
	return formatMetrics(node.StartedAt, node.UpdatedAt, node.Status, node.ContextCount)
}

func formatMetrics(startedAt, updatedAt time.Time, status string, contextCount int) string {
	parts := []string{}
	if !startedAt.IsZero() {
		end := updatedAt
		if status != "done" && status != "failed" {
			end = time.Now()
		}
		if end.Before(startedAt) {
			end = startedAt
		}
		parts = append(parts, "elapsed "+formatDuration(end.Sub(startedAt)))
	}
	if contextCount > 0 {
		parts = append(parts, fmt.Sprintf("ctx %d", contextCount))
	}
	return strings.Join(parts, "  ")
}

func formatDuration(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	if duration < time.Minute {
		seconds := int(duration / time.Second)
		if duration%time.Second != 0 {
			seconds++
		}
		if seconds < 1 {
			seconds = 1
		}
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := int(duration / time.Minute)
	seconds := int((duration % time.Minute) / time.Second)
	return fmt.Sprintf("%dm%02ds", minutes, seconds)
}
