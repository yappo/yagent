package tui

import (
	"context"
	"encoding/json"
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
	chatusecase "yagent/internal/usecase/chat"
)

type chatMessage struct {
	content string
	err     error
}

type loadingTickMsg struct{}

type permissionState struct {
	request       domain.PermissionRequest
	response      chan domain.PermissionDecision
	selectedIndex int
}

type toolCallState struct {
	name    string
	target  string
	options []string
	status  string
	success *bool
}

type toolLogEntry struct {
	title   string
	content string
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
	runner           *chatusecase.Service
	workingDir       string
	selectedRefs     map[string]string
	messages         []domain.Message
	output           []string
	loading          bool
	loadingFrame     int
	width            int
	height           int
	viewport         viewport.Model
	toolLogViewport  viewport.Model
	textarea         textarea.Model
	history          []string
	historyIndex     int
	activeTool       *toolCallState
	toolLogs         []toolLogEntry
	permission       *permissionState
	sessionApprovals map[string]bool
	styles           styles
}

type styles struct {
	user               lipgloss.Style
	assistant          lipgloss.Style
	tool               lipgloss.Style
	separator          lipgloss.Style
	hint               lipgloss.Style
	toolCard           lipgloss.Style
	toolTitle          lipgloss.Style
	toolMeta           lipgloss.Style
	toolOption         lipgloss.Style
	toolSuccess        lipgloss.Style
	toolFailure        lipgloss.Style
	toolLogCard        lipgloss.Style
	toolLogTitle       lipgloss.Style
	toolLogHint        lipgloss.Style
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
)

func newModel(runner *chatusecase.Service, workingDir string) model {
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
		toolLogViewport:  viewport.New(),
		textarea:         ta,
		history:          []string{},
		sessionApprovals: map[string]bool{},
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
		toolCard: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("242")).
			Padding(0, 1),
		toolTitle:   lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true),
		toolMeta:    lipgloss.NewStyle().Foreground(lipgloss.Color("250")),
		toolOption:  lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		toolSuccess: lipgloss.NewStyle().Foreground(lipgloss.Color("114")).Bold(true),
		toolFailure: lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true),
		toolLogCard: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("238")).
			Padding(0, 1),
		toolLogTitle: lipgloss.NewStyle().Foreground(lipgloss.Color("223")).Bold(true),
		toolLogHint:  lipgloss.NewStyle().Foreground(lipgloss.Color("244")),
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
	return textarea.Blink
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
	if m.activeTool != nil {
		footerHeight += m.toolCardHeight()
	}
	if m.hasToolLogs() {
		footerHeight += m.toolLogCardHeight()
	}
	if m.permission != nil {
		footerHeight += m.permissionCardHeight()
	}
	headerHeight := 3
	m.viewport.SetWidth(m.width)
	m.viewport.SetHeight(maxInt(3, m.height-headerHeight-footerHeight))
	m.syncToolLogViewport()
	m.refreshViewport()
}

func (m *model) refreshViewport() {
	m.viewport.SetContent(m.renderLog())
	m.viewport.GotoBottom()
}

func (m *model) syncToolLogViewport() {
	if !m.hasToolLogs() || m.width <= 0 {
		return
	}

	contentWidth := maxInt(1, m.width-6)
	m.toolLogViewport.SetWidth(contentWidth)
	m.toolLogViewport.SetHeight(m.toolLogViewportHeight())
	m.toolLogViewport.SetContent(m.renderToolLogs(contentWidth))
	m.toolLogViewport.GotoBottom()
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
	return strings.Join([]string{
		request.ToolName,
		request.Action,
		request.ResourceKind,
		request.Scope,
		request.Risk,
	}, "\x00")
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
		m.permission = &permissionState{
			request:       msg.request,
			response:      msg.response,
			selectedIndex: 0,
		}
		m.syncLayout()
		return m, nil

	case toolEventMsg:
		switch msg.event.Phase {
		case "start":
			m.activeTool = buildToolCallState(msg.event.Call)
		case "finish":
			m.activeTool = finalizeToolCallState(msg.event.Call, msg.event.Result, m.activeTool)
			m.appendToolLog(msg.event.Call, msg.event.Result)
			m.output = append(m.output, formatToolSummary(m.activeTool))
			m.output = append(m.output, "───────────────────────────────────────────────────────────────────────")
			m.activeTool = nil
		}
		m.syncLayout()
		m.refreshViewport()
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
			m.output = append(m.output, "LLM サーバーとの通信に失敗しました: "+msg.err.Error())
			m.messages = m.messages[:len(m.messages)-1]
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
		result, err := m.runner.Run(context.Background(), chatusecase.Input{
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

	m.output = append(m.output, fmt.Sprintf("%s %s を%s",
		m.permission.request.Operation,
		m.permission.request.Resource,
		permissionDecisionLabel(decision),
	))
	m.permission.response <- decision
	close(m.permission.response)
	m.permission = nil
	m.syncLayout()
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

func (m model) toolCardHeight() int {
	if m.activeTool == nil {
		return 0
	}
	return lipgloss.Height(m.renderToolCard())
}

func (m model) toolLogCardHeight() int {
	if !m.hasToolLogs() {
		return 0
	}
	return m.toolLogViewportHeight() + 4
}

func (m model) renderPermissionCard() string {
	if m.permission == nil {
		return ""
	}

	cardWidth := maxInt(32, m.width-2)
	resource := trimPathFromEnd(m.permission.request.Resource, cardWidth-4)
	meta := strings.TrimSpace(strings.Join([]string{
		"risk: " + fallbackString(m.permission.request.Risk, "-"),
		"scope: " + fallbackString(m.permission.request.Scope, "-"),
	}, " • "))
	sideEffects := ""
	if len(m.permission.request.SideEffects) > 0 {
		sideEffects = "effects: " + strings.Join(m.permission.request.SideEffects, ", ")
	}
	card := strings.Join([]string{
		m.styles.permissionTitle.Render(m.permission.request.Operation),
		m.styles.permissionPath.Render(resource),
		m.styles.permissionHelp.Render(strings.TrimSpace(m.permission.request.Summary)),
		m.styles.permissionHelp.Render(meta),
		m.styles.permissionHelp.Render(sideEffects),
		renderPermissionOptions(m.permission.selectedIndex, m.styles.permissionSelected, m.styles.permissionOption),
		m.styles.permissionHelp.Render("←/→ または Tab で選択 • Enter で確定 • Esc で拒否"),
	}, "\n")
	return m.styles.permissionCard.Width(cardWidth).Render(card)
}

func (m model) renderToolCard() string {
	if m.activeTool == nil {
		return ""
	}

	cardWidth := maxInt(32, m.width-2)
	statusStyle := m.styles.toolMeta
	if m.activeTool.success != nil {
		if *m.activeTool.success {
			statusStyle = m.styles.toolSuccess
		} else {
			statusStyle = m.styles.toolFailure
		}
	}

	lines := []string{
		m.styles.toolTitle.Render("Tool Use"),
		m.styles.toolMeta.Render(m.activeTool.name + "  " + statusStyle.Render(m.activeTool.status)),
	}
	if m.activeTool.target != "" {
		lines = append(lines, m.styles.permissionPath.Render(trimPathFromEnd(m.activeTool.target, cardWidth-4)))
	}
	for _, option := range m.activeTool.options {
		lines = append(lines, m.styles.toolOption.Render("• "+option))
	}

	return m.styles.toolCard.Width(cardWidth).Render(strings.Join(lines, "\n"))
}

func (m model) renderToolLogCard() string {
	if !m.hasToolLogs() {
		return ""
	}

	cardWidth := maxInt(32, m.width-2)
	lines := []string{
		m.styles.toolLogTitle.Render("Tool Logs"),
		m.styles.toolLogHint.Render("stdout/stderr や一覧結果など、ユーザに意味のある tool 出力を表示"),
		m.toolLogViewport.View(),
	}
	return m.styles.toolLogCard.Width(cardWidth).Render(strings.Join(lines, "\n"))
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

func fallbackString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func (m *model) appendToolLog(call domain.ToolCall, result domain.ToolResult) {
	content := strings.TrimSpace(result.Output)
	if content == "" {
		return
	}
	content = compactToolLogContent(content)
	if content == "" {
		return
	}

	entry := toolLogEntry{
		title:   formatToolLogTitle(call, result.Success),
		content: content,
	}
	m.toolLogs = append(m.toolLogs, entry)
	if len(m.toolLogs) > 20 {
		m.toolLogs = m.toolLogs[len(m.toolLogs)-20:]
	}
}

func (m model) hasToolLogs() bool {
	return len(m.toolLogs) > 0
}

func (m model) toolLogViewportHeight() int {
	if m.height <= 0 {
		return 8
	}
	height := (m.height - 8) / 3
	if height < 6 {
		height = 6
	}
	if height > 14 {
		height = 14
	}
	return height
}

func (m model) renderToolLogs(width int) string {
	if len(m.toolLogs) == 0 {
		return ""
	}

	blocks := make([]string, 0, len(m.toolLogs))
	for _, entry := range m.toolLogs {
		blocks = append(blocks, m.styles.toolMeta.Render(entry.title)+"\n"+wrapContent(entry.content, width))
	}
	return strings.Join(blocks, "\n\n")
}

func buildToolCallState(call domain.ToolCall) *toolCallState {
	target, options := summarizeToolCall(call)
	return &toolCallState{
		name:    call.Name,
		target:  target,
		options: options,
		status:  "requesting",
	}
}

func finalizeToolCallState(call domain.ToolCall, result domain.ToolResult, current *toolCallState) *toolCallState {
	state := current
	if state == nil {
		state = buildToolCallState(call)
	}
	success := result.Success
	state.success = &success
	if result.Success {
		state.status = "completed"
	} else {
		state.status = "failed"
	}
	return state
}

func summarizeToolCall(call domain.ToolCall) (string, []string) {
	targetKeys := []string{"path", "repo_path", "root", "task_id", "source_path", "destination_path", "target"}
	target := ""
	for _, key := range targetKeys {
		if value, ok := call.Arguments[key]; ok {
			target = stringifyToolValue(value)
			if target != "" {
				break
			}
		}
	}

	keys := make([]string, 0, len(call.Arguments))
	for key := range call.Arguments {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	options := make([]string, 0, len(keys))
	for _, key := range keys {
		if isPrimaryTargetKey(key) {
			continue
		}
		options = append(options, fmt.Sprintf("%s=%s", key, stringifyToolValue(call.Arguments[key])))
	}
	if len(options) == 0 {
		options = append(options, "no extra options")
	}

	return target, options
}

func formatToolSummary(state *toolCallState) string {
	if state == nil {
		return ""
	}
	status := "done"
	if state.success != nil && !*state.success {
		status = "failed"
	}
	line := "tool " + state.name + " " + status
	if state.target != "" {
		line += " • target=" + state.target
	}
	if len(state.options) > 0 {
		line += " • " + strings.Join(state.options, " • ")
	}
	return line
}

func isPrimaryTargetKey(key string) bool {
	switch key {
	case "path", "repo_path", "root", "task_id", "target":
		return true
	default:
		return false
	}
}

func stringifyToolValue(value any) string {
	switch typed := value.(type) {
	case string:
		if len(typed) > 80 {
			return typed[:77] + "..."
		}
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		return fmt.Sprintf("%.0f", typed)
	case int:
		return fmt.Sprintf("%d", typed)
	case []any:
		return fmt.Sprintf("%d items", len(typed))
	case map[string]any:
		if data, err := json.Marshal(typed); err == nil {
			return string(data)
		}
	}
	if data, err := json.Marshal(value); err == nil {
		text := string(data)
		if len(text) > 80 {
			return text[:77] + "..."
		}
		return text
	}
	return fmt.Sprintf("%v", value)
}

func formatToolLogTitle(call domain.ToolCall, success bool) string {
	status := "done"
	if !success {
		status = "failed"
	}
	target, _ := summarizeToolCall(call)
	if target == "" {
		return call.Name + " • " + status
	}
	return call.Name + " • " + status + " • " + target
}

func compactToolLogContent(content string) string {
	lines := strings.Split(content, "\n")
	trimmed := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			trimmed = append(trimmed, "")
			continue
		}
		trimmed = append(trimmed, line)
	}

	for len(trimmed) > 0 && trimmed[0] == "" {
		trimmed = trimmed[1:]
	}
	for len(trimmed) > 0 && trimmed[len(trimmed)-1] == "" {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if len(trimmed) > 120 {
		trimmed = append(trimmed[:120], "... (truncated)")
	}
	return strings.Join(trimmed, "\n")
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
	sb.WriteString(m.viewport.View())
	offsetY += lipgloss.Height(m.viewport.View())
	sb.WriteString("\n")
	offsetY++
	if m.hasToolLogs() {
		card := m.renderToolLogCard()
		sb.WriteString(card)
		sb.WriteString("\n")
		offsetY += lipgloss.Height(card) + 1
	}
	if m.activeTool != nil {
		card := m.renderToolCard()
		sb.WriteString(card)
		sb.WriteString("\n")
		offsetY += lipgloss.Height(card) + 1
	}
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
