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
)

type chatMessage struct {
	content string
	run     *domain.RunState
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
	patternMode   bool
	patternInput  string
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
	label           string
	decision        domain.PermissionDecision
	requiresPattern bool
}

type patternApproval struct {
	toolName     string
	action       string
	resourceKind string
	risk         string
	pattern      string
}

type slashCommand struct {
	name        string
	description string
}

type sidePanel string

const (
	sidePanelRunGraph     sidePanel = "graph"
	sidePanelPlan         sidePanel = "plan"
	sidePanelVerification sidePanel = "verification"
	sidePanelMemory       sidePanel = "memory"
)

type model struct {
	runner           domain.Orchestrator
	tools            domain.ToolExecutor
	taskCatalog      domain.TaskCatalog
	mcpBindings      domain.MCPConnectionManager
	agentCatalog     domain.AgentCatalog
	runStore         domain.RunStateStore
	memoryStore      domain.RepoMemoryStore
	workingDir       string
	defaultModel     string
	lastRun          *domain.RunState
	selectedRefs     map[string]string
	messages         []domain.Message
	output           []string
	loading          bool
	loadingFrame     int
	width            int
	height           int
	viewport         viewport.Model
	statusViewport   viewport.Model
	toolLogViewport  viewport.Model
	textarea         textarea.Model
	history          []string
	historyIndex     int
	activeTool       *toolCallState
	toolLogs         []toolLogEntry
	permission       *permissionState
	permissionQueue  []permissionState
	sessionApprovals map[string]bool
	patternApprovals []patternApproval
	activePanel      sidePanel
	status           statusState
	statusEvents     <-chan domain.ExecutionEvent
	cancelStatus     func()
	styles           styles
	logDirty         bool
	statusDirty      bool
	toolLogDirty     bool
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
	Phase        domain.RunPhase
	Attempt      int
	Detail       string
	ArtifactRef  string
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
	panelTab           lipgloss.Style
	panelTabActive     lipgloss.Style
	panelMeta          lipgloss.Style
}

var loadingFrames = []string{"◐", "◓", "◑", "◒"}

var sidePanels = []sidePanel{
	sidePanelRunGraph,
	sidePanelPlan,
	sidePanelVerification,
	sidePanelMemory,
}

var defaultPermissionOptions = []permissionOption{
	{label: "今回だけ許可", decision: domain.PermissionAllowOnce},
	{label: "同じ操作を以後許可", decision: domain.PermissionAllowSession},
	{label: "拒否", decision: domain.PermissionDeny},
}

var filePermissionOptions = []permissionOption{
	{label: "今回だけ許可", decision: domain.PermissionAllowOnce},
	{label: "同じ操作を以後許可", decision: domain.PermissionAllowSession},
	{label: "ファイルパターン指定で以後許可", decision: domain.PermissionAllowSession, requiresPattern: true},
	{label: "拒否", decision: domain.PermissionDeny},
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
	{name: "/resume", description: "最新 run を会話に復元"},
	{name: "/approvals", description: "approval 状態を表示"},
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

func newModel(runner domain.Orchestrator, workingDir string, defaultModel string, tools domain.ToolExecutor, taskCatalog domain.TaskCatalog, mcpBindings domain.MCPConnectionManager, agentCatalog domain.AgentCatalog) model {
	return newModelWithStores(runner, workingDir, defaultModel, tools, taskCatalog, mcpBindings, agentCatalog, nil, nil)
}

func newModelWithStores(runner domain.Orchestrator, workingDir string, defaultModel string, tools domain.ToolExecutor, taskCatalog domain.TaskCatalog, mcpBindings domain.MCPConnectionManager, agentCatalog domain.AgentCatalog, runStore domain.RunStateStore, memoryStore domain.RepoMemoryStore) model {
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
		tools:            tools,
		taskCatalog:      taskCatalog,
		mcpBindings:      mcpBindings,
		agentCatalog:     agentCatalog,
		runStore:         runStore,
		memoryStore:      memoryStore,
		workingDir:       workingDir,
		defaultModel:     defaultModel,
		selectedRefs:     map[string]string{},
		viewport:         viewport.New(),
		statusViewport:   viewport.New(),
		toolLogViewport:  viewport.New(),
		textarea:         ta,
		history:          []string{},
		sessionApprovals: map[string]bool{},
		activePanel:      sidePanelRunGraph,
		status: statusState{
			nodes: map[string]*agentStatusNode{},
		},
		logDirty:     true,
		statusDirty:  true,
		toolLogDirty: true,
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
		panelTab: lipgloss.NewStyle().
			Foreground(lipgloss.Color("248")).
			Padding(0, 1),
		panelTabActive: lipgloss.NewStyle().
			Foreground(lipgloss.Color("232")).
			Background(lipgloss.Color("180")).
			Bold(true).
			Padding(0, 1),
		panelMeta: lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true),
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
	prevChatWidth := m.viewport.Width()
	prevChatHeight := m.viewport.Height()
	prevStatusWidth := m.statusViewport.Width()
	prevStatusHeight := m.statusViewport.Height()
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
	if prevChatWidth != m.viewport.Width() || prevChatHeight != m.viewport.Height() {
		m.logDirty = true
	}
	if prevStatusWidth != m.statusViewport.Width() || prevStatusHeight != m.statusViewport.Height() {
		m.statusDirty = true
	}
	m.refreshViewport()
}

func (m *model) refreshViewport() {
	if m.logDirty {
		m.viewport.SetContent(m.renderLog())
		m.viewport.GotoBottom()
		m.logDirty = false
	}
	if m.statusDirty {
		m.statusViewport.SetContent(m.renderStatus())
		m.statusViewport.GotoTop()
		m.statusDirty = false
	}
	m.syncToolLogViewport()
}

func (m *model) syncToolLogViewport() {
	if !m.hasToolLogs() || m.width <= 0 {
		return
	}

	contentWidth := maxInt(1, m.width-6)
	prevWidth := m.toolLogViewport.Width()
	prevHeight := m.toolLogViewport.Height()
	m.toolLogViewport.SetWidth(contentWidth)
	m.toolLogViewport.SetHeight(m.toolLogViewportHeight())
	if prevWidth != contentWidth || prevHeight != m.toolLogViewport.Height() {
		m.toolLogDirty = true
	}
	if m.toolLogDirty {
		m.toolLogViewport.SetContent(m.renderToolLogs(contentWidth))
		m.toolLogViewport.GotoBottom()
		m.toolLogDirty = false
	}
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
		if m.hasPatternApproval(msg.request) {
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
			m.logDirty = true
			m.toolLogDirty = true
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
		m.logDirty = true
		m.refreshViewport()
		return m, loadingTick()

	case chatMessage:
		m.loading = false
		if msg.err != nil {
			m.output = append(m.output, "実行エラー: "+msg.err.Error())
			if len(m.messages) > 0 {
				m.messages = m.messages[:len(m.messages)-1]
			}
			m.logDirty = true
			m.refreshViewport()
			return m, nil
		}
		m.messages = append(m.messages, domain.Message{
			Role:    domain.RoleAssistant,
			Content: msg.content,
		})
		if msg.run != nil {
			m.lastRun = msg.run
			m.statusDirty = true
		}
		m.output = appendOutputBlock(m.output, assistantOutputLabel, msg.content)
		m.logDirty = true
		m.refreshViewport()
		return m, nil

	case statusEventMsg:
		m.applyStatusEvent(msg.event)
		m.statusDirty = true
		m.refreshViewport()
		return m, listenStatusEvents(m.statusEvents)

	case tea.PasteMsg:
		if m.permission != nil {
			if m.permission.patternMode {
				m.permission.patternInput += msg.Content
				m.syncLayout()
			}
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

	if m.permission.patternMode {
		return handlePatternPermissionKeys(m, msg)
	}

	options := permissionOptionsForRequest(m.permission.request)
	switch msg.String() {
	case "left", "shift+tab":
		m.permission.selectedIndex = wrapIndex(m.permission.selectedIndex-1, len(options))
		m.syncLayout()
		return m, nil
	case "right", "tab":
		m.permission.selectedIndex = wrapIndex(m.permission.selectedIndex+1, len(options))
		m.syncLayout()
		return m, nil
	case "enter":
		option := options[m.permission.selectedIndex]
		if option.requiresPattern {
			m.permission.patternMode = true
			m.permission.patternInput = ""
			m.syncLayout()
			return m, nil
		}
		m.resolvePermission(option.decision)
		return m, nil
	case "esc":
		m.resolvePermission(domain.PermissionDeny)
		return m, nil
	}

	switch strings.ToLower(msg.String()) {
	case "1", "2", "3", "4":
		idx := int(msg.String()[0] - '1')
		if idx >= 0 && idx < len(options) {
			m.permission.selectedIndex = idx
			if options[idx].requiresPattern {
				m.permission.patternMode = true
				m.permission.patternInput = ""
				m.syncLayout()
				return m, nil
			}
			m.resolvePermission(options[idx].decision)
		}
	case "y":
		m.resolvePermission(domain.PermissionAllowOnce)
	case "n":
		m.resolvePermission(domain.PermissionDeny)
	}

	return m, nil
}

func handlePatternPermissionKeys(m model, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.permission.patternMode = false
		m.permission.patternInput = ""
		m.syncLayout()
		return m, nil
	case "enter":
		patternValue := strings.TrimSpace(m.permission.patternInput)
		if patternValue == "" {
			return m, nil
		}
		m.patternApprovals = append(m.patternApprovals, newPatternApproval(m.permission.request, patternValue))
		m.permission.patternMode = false
		m.permission.patternInput = ""
		m.resolvePermissionWithLabel(domain.PermissionAllowSession, "パターン許可 ("+patternValue+")")
		return m, nil
	case "backspace", "ctrl+h":
		if len(m.permission.patternInput) > 0 {
			runes := []rune(m.permission.patternInput)
			m.permission.patternInput = string(runes[:len(runes)-1])
			m.syncLayout()
		}
		return m, nil
	}

	if typed := msg.String(); len([]rune(typed)) == 1 {
		m.permission.patternInput += typed
		m.syncLayout()
	}
	return m, nil
}

func handleComposerKeys(m model, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if delta, ok := panelNavigationDelta(msg); ok {
		m.cyclePanel(delta)
		m.syncLayout()
		return m, nil
	}

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
		m.logDirty = true
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
		m.logDirty = true
		m.textarea.Reset()
		m.syncLayout()
		return m, nil
	case "/tools":
		m.output = append(m.output, formatSlashList("利用可能な tool", m.listTools())...)
		m.logDirty = true
		m.textarea.Reset()
		m.syncLayout()
		return m, nil
	case "/tasks":
		m.output = append(m.output, formatSlashList("登録済み task", m.listTasks())...)
		m.logDirty = true
		m.textarea.Reset()
		m.syncLayout()
		return m, nil
	case "/mcp":
		m.output = append(m.output, formatSlashList("bind 済み MCP tool", m.listMCPTools())...)
		m.logDirty = true
		m.textarea.Reset()
		m.syncLayout()
		return m, nil
	case "/agents":
		m.output = append(m.output, formatSlashList("利用可能な agent", m.listAgents())...)
		m.logDirty = true
		m.textarea.Reset()
		m.syncLayout()
		return m, nil
	case "/graph":
		m.setActivePanel(sidePanelRunGraph)
		m.output = append(m.output, "Run Graph panel を表示します")
		m.logDirty = true
		m.textarea.Reset()
		m.syncLayout()
		return m, nil
	case "/plan":
		m.setActivePanel(sidePanelPlan)
		m.output = append(m.output, formatSlashList("Current plan", m.listPlan())...)
		m.logDirty = true
		m.textarea.Reset()
		m.syncLayout()
		return m, nil
	case "/verification":
		m.setActivePanel(sidePanelVerification)
		m.output = append(m.output, formatSlashList("Verification", m.listVerification())...)
		m.logDirty = true
		m.textarea.Reset()
		m.syncLayout()
		return m, nil
	case "/artifacts":
		m.output = append(m.output, formatSlashList("Run artifacts", m.listArtifacts())...)
		m.logDirty = true
		m.textarea.Reset()
		m.syncLayout()
		return m, nil
	case "/memory":
		m.setActivePanel(sidePanelMemory)
		m.output = append(m.output, formatSlashList("Repo memory", m.listMemory())...)
		m.logDirty = true
		m.textarea.Reset()
		m.syncLayout()
		return m, nil
	case "/resume":
		lines := m.resumeLatest()
		m.output = append(m.output, lines...)
		m.statusDirty = true
		m.logDirty = true
		m.textarea.Reset()
		m.syncLayout()
		return m, nil
	case "/approvals":
		m.output = append(m.output, formatSlashList("Approvals", m.listApprovals())...)
		m.logDirty = true
		m.textarea.Reset()
		m.syncLayout()
		return m, nil
	case "/clear":
		m.messages = nil
		m.lastRun = nil
		m.output = []string{"チャット履歴をクリアしました"}
		m.logDirty = true
		m.statusDirty = true
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
	m.logDirty = true
	m.syncLayout()

	send := func() tea.Msg {
		result, err := m.runner.RunTurn(context.Background(), domain.TurnRequest{
			Messages: m.messages,
			Model:    m.defaultModel,
		})
		return chatMessage{content: result.Message.Content, run: result.Run, err: err}
	}

	return m, tea.Batch(send, loadingTick())
}

func (m *model) resolvePermission(decision domain.PermissionDecision) {
	m.resolvePermissionWithLabel(decision, "")
}

func (m *model) resolvePermissionWithLabel(decision domain.PermissionDecision, label string) {
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
		fallbackString(label, permissionDecisionLabel(decision))+" ("+m.permission.request.Resource+")",
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
	m.logDirty = true
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
	node.Phase = event.Phase
	if event.Attempt > 0 {
		node.Attempt = event.Attempt
	}
	node.ArtifactRef = event.ArtifactRef
	if event.ContextCount > 0 {
		node.ContextCount = event.ContextCount
	}
	if event.Status != "" {
		node.Status = event.Status
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
	options := permissionOptionsForRequest(m.permission.request)
	lines := []string{
		m.styles.permissionTitle.Render(m.permission.request.Operation),
		m.styles.permissionPath.Render(resource),
		m.styles.permissionHelp.Render("requester: " + permissionRequesterDisplay(m.permission.request)),
	}
	if summary := strings.TrimSpace(m.permission.request.Summary); summary != "" {
		lines = append(lines, m.styles.permissionHelp.Render(summary))
	}
	if m.permission.request.Purpose != "" {
		lines = append(lines, m.styles.permissionHelp.Render("purpose: "+m.permission.request.Purpose))
	}
	meta := strings.TrimSpace(strings.Join([]string{
		"risk: " + fallbackString(m.permission.request.Risk, "-"),
		"scope: " + fallbackString(m.permission.request.Scope, "-"),
	}, " • "))
	if meta != "" {
		lines = append(lines, m.styles.permissionHelp.Render(meta))
	}
	if len(m.permission.request.SideEffects) > 0 {
		lines = append(lines, m.styles.permissionHelp.Render("effects: "+strings.Join(m.permission.request.SideEffects, ", ")))
	}
	if m.permission.patternMode {
		patternValue := m.permission.patternInput
		if patternValue == "" {
			patternValue = "例: *.go / internal/*"
		}
		lines = append(lines,
			m.styles.permissionHelp.Render("パターン許可: このセッション中、glob に一致するパスを自動許可"),
			m.styles.permissionSelected.Render("pattern> "+patternValue),
			m.styles.permissionHelp.Render("Enter で確定 • Esc で戻る • basename または path glob を指定"),
		)
	} else {
		lines = append(lines,
			renderPermissionOptions(options, m.permission.selectedIndex, m.styles.permissionSelected, m.styles.permissionOption),
			m.styles.permissionHelp.Render("←/→ または Tab で選択 • Enter で確定 • Esc で拒否"),
		)
	}
	card := strings.Join(lines, "\n")
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

func renderPermissionOptions(options []permissionOption, selected int, selectedStyle, baseStyle lipgloss.Style) string {
	parts := make([]string, 0, len(options))
	for i, option := range options {
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

func cloneDomainMessages(messages []domain.Message) []domain.Message {
	out := make([]domain.Message, 0, len(messages))
	for _, message := range messages {
		item := message
		item.ToolCalls = append([]domain.ToolCall(nil), message.ToolCalls...)
		if message.Metadata != nil {
			item.Metadata = map[string]string{}
			for key, value := range message.Metadata {
				item.Metadata[key] = value
			}
		}
		out = append(out, item)
	}
	return out
}

func permissionOptionsForRequest(request domain.PermissionRequest) []permissionOption {
	if domain.PermissionRequestSupportsPatternApproval(request) {
		return filePermissionOptions
	}
	return defaultPermissionOptions
}

func newPatternApproval(request domain.PermissionRequest, patternValue string) patternApproval {
	return patternApproval{
		toolName:     request.ToolName,
		action:       request.Action,
		resourceKind: request.ResourceKind,
		risk:         request.Risk,
		pattern:      strings.TrimSpace(patternValue),
	}
}

func (m model) hasPatternApproval(request domain.PermissionRequest) bool {
	for _, approval := range m.patternApprovals {
		if approval.toolName != request.ToolName || approval.action != request.Action || approval.resourceKind != request.ResourceKind || approval.risk != request.Risk {
			continue
		}
		if domain.PermissionRequestMatchesPattern(request, approval.pattern) {
			return true
		}
	}
	return false
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
		return 5
	}
	height := (m.height*30)/100 - 4
	if height < 4 {
		height = 4
	}
	if height > 10 {
		height = 10
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
	memory, err := m.memoryStore.LoadMemory(context.Background())
	if err != nil {
		return []string{"failed to load memory: " + err.Error()}
	}
	items := []string{}
	for _, constraint := range memory.Constraints {
		items = append(items, "constraint: "+constraint)
	}
	for _, command := range memory.SuccessfulCommands {
		items = append(items, "command: "+fallbackString(command.Summary, command.Command))
	}
	for _, artifact := range memory.RecentArtifacts {
		items = append(items, "artifact: "+artifact)
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
	if m.permission != nil {
		items = append(items, "pending approval: "+m.permission.request.Operation+" ("+m.permission.request.Resource+")")
	}
	return items
}

func (m *model) resumeLatest() []string {
	if m.runStore == nil {
		return []string{"resume unavailable: run store is not configured"}
	}
	run, err := m.runStore.LoadLatestRun(context.Background())
	if err != nil {
		return []string{"resume failed: " + err.Error()}
	}
	if run == nil {
		return []string{"resume failed: no previous run found"}
	}
	m.lastRun = run
	m.messages = cloneDomainMessages(run.Messages)
	lines := []string{
		fmt.Sprintf("Resumed run %s", run.ID),
		fmt.Sprintf("phase=%s status=%s attempt=%d", run.CurrentPhase, run.Status, run.Attempt),
	}
	if summary := strings.TrimSpace(run.ConversationSummary); summary != "" {
		lines = append(lines, "summary: "+summary)
	}
	return lines
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
	count := len(m.permissionQueue)
	if m.permission != nil {
		count++
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

func (m model) latestBlockedReason() string {
	for _, event := range m.status.recent {
		if event.Type == "agent_failed" || event.Type == "tool_failed" {
			return trimPathFromEnd(strings.TrimSpace(event.Detail), maxInt(24, m.statusViewport.Width()-8))
		}
	}
	if m.lastRun != nil {
		for idx := len(m.lastRun.Verification) - 1; idx >= 0; idx-- {
			if strings.EqualFold(m.lastRun.Verification[idx].Status, "fail") {
				return trimPathFromEnd(strings.TrimSpace(m.lastRun.Verification[idx].Summary), maxInt(24, m.statusViewport.Width()-8))
			}
		}
	}
	return ""
}

func (m model) View() tea.View {
	var sb strings.Builder
	offsetY := 0
	sb.WriteString(m.styles.hint.Render("入力： /help, /graph, /plan, /verification, /memory, /resume, /clear, /exit | Ctrl+←/→ または Ctrl+H/L / Alt+[/] で panel 切替 | Alt+↑/↓ と PgUp/PgDn でログ"))
	sb.WriteString("\n")
	sb.WriteString(m.styles.separator.Render(strings.Repeat("─", maxInt(1, m.width))))
	sb.WriteString("\n\n")
	offsetY += 3
	mainView := m.renderMainPanels()
	sb.WriteString(mainView)
	offsetY += lipgloss.Height(mainView)
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

func (m model) renderStatus() string {
	switch m.activePanel {
	case sidePanelPlan:
		return m.renderPlanPanel()
	case sidePanelVerification:
		return m.renderVerificationPanel()
	case sidePanelMemory:
		return m.renderMemoryPanel()
	default:
		return m.renderRunGraphPanel()
	}
}

func (m model) renderRunGraphPanel() string {
	if len(m.status.nodes) == 0 {
		lines := m.renderPanelSummary()
		lines = append(lines, "", "まだサブエージェントは動いていません。")
		return strings.Join(lines, "\n")
	}

	lines := m.renderPanelSummary()
	lines = append(lines, "", fmt.Sprintf("running %d  done %d  failed %d", m.countStatus("running", "working", "thinking"), m.countStatus("done"), m.countStatus("failed")))
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

func (m *model) setActivePanel(panel sidePanel) {
	if m.activePanel == panel {
		return
	}
	m.activePanel = panel
	m.statusDirty = true
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
	memory, err := m.memoryStore.LoadMemory(context.Background())
	if err != nil {
		lines = append(lines, "", "failed to load memory: "+err.Error())
		return strings.Join(lines, "\n")
	}
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
	appendSection("Constraints", memory.Constraints)
	appendSection("Failure patterns", memory.FailurePatterns)
	commands := make([]string, 0, len(memory.SuccessfulCommands))
	for _, item := range memory.SuccessfulCommands {
		commands = append(commands, fallbackString(item.Summary, item.Command))
	}
	appendSection("Successful commands", commands)
	appendSection("Recent artifacts", memory.RecentArtifacts)
	if len(lines) == len(m.renderPanelSummary()) {
		lines = append(lines, "", "(memory is empty)")
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
	if node.Phase != "" {
		line += "  " + string(node.Phase)
	}
	if node.Attempt > 0 {
		line += fmt.Sprintf("  try %d", node.Attempt)
	}
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
	statusWidth := minInt(58, maxInt(36, (totalWidth*3)/7))
	chatWidth := maxInt(44, totalWidth-statusWidth-1)
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

func tailCheckpoints(items []domain.RunCheckpoint, limit int) []domain.RunCheckpoint {
	if limit <= 0 || len(items) <= limit {
		return append([]domain.RunCheckpoint(nil), items...)
	}
	return append([]domain.RunCheckpoint(nil), items[len(items)-limit:]...)
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
