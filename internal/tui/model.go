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

type memoryLoadedMsg struct {
	memory *domain.RepoMemory
	err    error
}

type clockTickMsg struct {
	now time.Time
}

type panelRenderCache struct {
	width   int
	content string
	dirty   bool
}

type stringRenderCache struct {
	key     string
	content string
	dirty   bool
}

type memoryPanelState struct {
	data    *domain.RepoMemory
	err     error
	loading bool
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
	modelOverride    string
	selectedProfile  string
	routingProfiles  []string
	streaming        bool
	selectedTheme    string
	streamingContent string
	streamingBlock   int
	lastRun          *domain.RunState
	selectedRefs     map[string]string
	messages         []domain.Message
	chatBlocks       []chatBlock
	loading          bool
	loadingFrame     int
	now              time.Time
	width            int
	height           int
	viewport         viewport.Model
	statusViewport   viewport.Model
	toolLogViewport  viewport.Model
	textarea         textarea.Model
	composerWidth    int
	history          []string
	historyIndex     int
	activeTools      map[string]*toolCallState
	activeToolOrder  []string
	toolLogs         []toolLogEntry
	permission       *permissionState
	permissionQueue  []permissionState
	sessionApprovals map[string]bool
	patternApprovals []patternApproval
	activePanel      sidePanel
	status           statusState
	memory           memoryPanelState
	statusEvents     <-chan domain.ExecutionEvent
	cancelStatus     func()
	clockRunning     bool
	styles           styles
	logDirty         bool
	logRender        logRenderState
	toolLogDirty     bool
	panelCache       map[sidePanel]panelRenderCache
	headerCache      stringRenderCache
	mainPanelsCache  stringRenderCache
	toolCardCache    stringRenderCache
	toolLogCardCache stringRenderCache
	permissionCache  stringRenderCache
	completionCache  stringRenderCache
	composerCache    stringRenderCache
	completion       completionState
}

type styles struct {
	user               lipgloss.Style
	assistant          lipgloss.Style
	tool               lipgloss.Style
	separator          lipgloss.Style
	hint               lipgloss.Style
	markdownHeading    lipgloss.Style
	markdownList       lipgloss.Style
	markdownQuote      lipgloss.Style
	markdownCode       lipgloss.Style
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
	{name: "/resume", description: "run を会話に復元 (/resume [run-id|latest])"},
	{name: "/approvals", description: "approval 状態を表示"},
	{name: "/clear", description: "会話ログをクリア"},
	{name: "/exit", description: "yagent を終了"},
}

const (
	userOutputLabel      = "__USER__"
	assistantOutputLabel = "__ASSISTANT__"
	loadingInterval      = 100 * time.Millisecond
	maxComposerHeight    = 6
	maxToolLogEntries    = 8
	maxToolLogLines      = 40
	maxTerminalRootRuns  = 12
	maxTerminalChildren  = 6
	paneChromeHeight     = 3
	stackedPaneGap       = 1
	paneHorizontalFrame  = 4
)

func newModel(runner domain.Orchestrator, workingDir string, defaultModel string, tools domain.ToolExecutor, taskCatalog domain.TaskCatalog, mcpBindings domain.MCPConnectionManager, agentCatalog domain.AgentCatalog) model {
	return newModelWithStores(runner, workingDir, defaultModel, tools, taskCatalog, mcpBindings, agentCatalog, nil, nil)
}

func newModelWithStores(runner domain.Orchestrator, workingDir string, defaultModel string, tools domain.ToolExecutor, taskCatalog domain.TaskCatalog, mcpBindings domain.MCPConnectionManager, agentCatalog domain.AgentCatalog, runStore domain.RunStateStore, memoryStore domain.RepoMemoryStore) model {
	return newModelWithStoresAndProfiles(runner, workingDir, defaultModel, tools, taskCatalog, mcpBindings, agentCatalog, runStore, memoryStore, nil)
}

func newModelWithStoresAndProfiles(runner domain.Orchestrator, workingDir string, defaultModel string, tools domain.ToolExecutor, taskCatalog domain.TaskCatalog, mcpBindings domain.MCPConnectionManager, agentCatalog domain.AgentCatalog, runStore domain.RunStateStore, memoryStore domain.RepoMemoryStore, routingProfiles []string) model {
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
		routingProfiles:  uniqueSortedStrings(routingProfiles),
		selectedTheme:    defaultThemeName,
		streamingBlock:   -1,
		selectedRefs:     map[string]string{},
		now:              time.Now(),
		viewport:         viewport.New(),
		statusViewport:   viewport.New(),
		toolLogViewport:  viewport.New(),
		textarea:         ta,
		composerWidth:    0,
		history:          []string{},
		activeTools:      map[string]*toolCallState{},
		sessionApprovals: map[string]bool{},
		activePanel:      sidePanelRunGraph,
		memory: memoryPanelState{
			loading: memoryStore != nil,
		},
		status: statusState{
			nodes:    map[string]*agentStatusNode{},
			children: map[string][]string{},
			counts:   map[string]int{},
		},
		logDirty:     true,
		toolLogDirty: true,
		panelCache: map[sidePanel]panelRenderCache{
			sidePanelRunGraph:     {dirty: true},
			sidePanelPlan:         {dirty: true},
			sidePanelVerification: {dirty: true},
			sidePanelMemory:       {dirty: true},
		},
		headerCache:      stringRenderCache{dirty: true},
		mainPanelsCache:  stringRenderCache{dirty: true},
		toolCardCache:    stringRenderCache{dirty: true},
		toolLogCardCache: stringRenderCache{dirty: true},
		permissionCache:  stringRenderCache{dirty: true},
		completionCache:  stringRenderCache{dirty: true},
		composerCache:    stringRenderCache{dirty: true},
		completion: completionState{
			pathDirs: map[string]pathDirSnapshot{},
		},
		clockRunning: true,
		logRender:    logRenderState{dirty: true},
	}
	if stream, ok := runner.(domain.ExecutionEventStream); ok {
		m.statusEvents, m.cancelStatus = stream.SubscribeEvents()
	}
	m.styles = stylesForTheme(m.selectedTheme)
	return m
}

func defaultStyles() styles {
	return styles{
		user:      lipgloss.NewStyle().Foreground(lipgloss.Color("206")).Bold(true),
		assistant: lipgloss.NewStyle().Foreground(lipgloss.Color("75")),
		tool:      lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Italic(true),
		separator: lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		hint:      lipgloss.NewStyle().Foreground(lipgloss.Color("99")).Italic(true),
		markdownHeading: lipgloss.NewStyle().
			Foreground(lipgloss.Color("229")).
			Bold(true),
		markdownList:  lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		markdownQuote: lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Italic(true),
		markdownCode: lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("236")),
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
	return tea.Batch(textarea.Blink, listenStatusEvents(m.statusEvents), clockTick(), initialMemoryLoadCmd(m.memoryStore))
}

func (m *model) invalidateAllPanels() {
	for panel, cache := range m.panelCache {
		cache.dirty = true
		m.panelCache[panel] = cache
	}
	m.mainPanelsCache.dirty = true
}

func (m *model) invalidatePanel(panel sidePanel) {
	cache := m.panelCache[panel]
	cache.dirty = true
	m.panelCache[panel] = cache
	if panel == m.activePanel {
		m.mainPanelsCache.dirty = true
	}
}

func (m *model) invalidatePanelSummary() {
	m.invalidateAllPanels()
}

func (m *model) loadMemoryCmd() tea.Cmd {
	if m.memoryStore == nil || m.memory.loading {
		return nil
	}
	m.memory.loading = true
	m.invalidatePanel(sidePanelMemory)
	return func() tea.Msg {
		memory, err := m.memoryStore.LoadMemory(context.Background())
		return memoryLoadedMsg{memory: memory, err: err}
	}
}

func initialMemoryLoadCmd(store domain.RepoMemoryStore) tea.Cmd {
	if store == nil {
		return nil
	}
	return func() tea.Msg {
		memory, err := store.LoadMemory(context.Background())
		return memoryLoadedMsg{memory: memory, err: err}
	}
}

func (m *model) syncLayout() {
	m.syncComposer()
	if m.width <= 0 || m.height <= 0 {
		return
	}

	footerHeight := lipgloss.Height(m.cachedComposerView()) + 1
	if m.hasCompletionCandidates() {
		footerHeight += lipgloss.Height(m.cachedCompletionSuggestions())
	}
	if m.hasActiveTools() {
		footerHeight += m.toolCardHeight()
	}
	if m.hasToolLogs() {
		footerHeight += m.toolLogCardHeight()
	}
	if m.permission != nil {
		footerHeight += m.permissionCardHeight()
	}
	headerHeight := 4
	prevChatWidth := m.viewport.Width()
	prevChatHeight := m.viewport.Height()
	prevStatusWidth := m.statusViewport.Width()
	prevStatusHeight := m.statusViewport.Height()
	wasChatBottom := m.viewport.AtBottom()
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
	if prevChatWidth != m.viewport.Width() {
		m.logDirty = true
		m.logRender.dirty = true
		m.mainPanelsCache.dirty = true
	} else if prevChatHeight != m.viewport.Height() && wasChatBottom {
		m.viewport.GotoBottom()
	}
	if prevStatusWidth != m.statusViewport.Width() {
		m.invalidateAllPanels()
	} else if prevStatusHeight != m.statusViewport.Height() {
		m.mainPanelsCache.dirty = true
	}
	m.refreshViewport()
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
		m.enqueuePermissionState(state)
		m.invalidatePanelSummary()
		m.syncLayout()
		return m, nil

	case toolEventMsg:
		switch msg.event.Phase {
		case "start":
			m.startActiveTool(msg.event.Call)
		case "finish":
			state := m.finishActiveTool(msg.event.Call, msg.event.Result)
			m.appendToolLog(msg.event.Call, msg.event.Result)
			m.appendChatBlock(formatToolSummary(state), "───────────────────────────────────────────────────────────────────────")
			m.toolLogDirty = true
		}
		m.toolCardCache.dirty = true
		m.toolLogCardCache.dirty = true
		m.syncLayout()
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.headerCache.dirty = true
		m.syncLayout()
		return m, nil

	case loadingTickMsg:
		if !m.loading {
			return m, nil
		}
		m.loadingFrame = (m.loadingFrame + 1) % len(loadingFrames)
		m.mainPanelsCache.dirty = true
		return m, loadingTick()

	case clockTickMsg:
		if !m.hasActiveStatusNodes() {
			m.clockRunning = false
			return m, nil
		}
		m.now = msg.now
		m.invalidatePanel(sidePanelRunGraph)
		m.mainPanelsCache.dirty = true
		return m, clockTick()

	case chatMessage:
		m.loading = false
		m.mainPanelsCache.dirty = true
		if msg.err != nil {
			m.appendChatBlock("実行エラー: " + msg.err.Error())
			m.resetStreamingBlock()
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
		if msg.run != nil {
			m.lastRun = msg.run
			m.invalidateAllPanels()
		}
		if !m.finalizeStreamingBlock(msg.content) {
			m.appendOutputBlock(assistantOutputLabel, msg.content)
		}
		m.refreshViewport()
		return m, nil

	case statusEventMsg:
		if msg.event.Type == "llm_delta" {
			m.applyStreamDelta(msg.event.Detail)
			m.refreshViewport()
			return m, listenStatusEvents(m.statusEvents)
		}
		m.applyStatusEvent(msg.event)
		m.invalidatePanel(sidePanelRunGraph)
		m.mainPanelsCache.dirty = true
		m.refreshViewport()
		cmds := []tea.Cmd{listenStatusEvents(m.statusEvents)}
		if m.hasActiveStatusNodes() && !m.clockRunning {
			m.clockRunning = true
			cmds = append(cmds, clockTick())
		}
		return m, batchCmds(cmds...)

	case memoryLoadedMsg:
		m.memory.loading = false
		m.memory.data = msg.memory
		m.memory.err = msg.err
		m.invalidatePanel(sidePanelMemory)
		m.refreshViewport()
		return m, nil

	case tea.PasteMsg:
		if m.permission != nil {
			if m.permission.patternMode {
				m.permission.patternInput += msg.Content
				m.permissionCache.dirty = true
				m.syncLayout()
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		m.reconcileSelectedRefs()
		return m, batchCmds(cmd, m.syncAfterComposerChange(false))

	case pathCompletionDebounceMsg:
		if msg.seq != m.completion.pendingSeq {
			return m, nil
		}
		m.cancelPendingPathCompletion()
		return m, m.syncAfterComposerChange(true)

	case tea.KeyMsg:
		if m.permission != nil {
			return handlePermissionKeys(m, msg)
		}
		return handleComposerKeys(m, msg)
	}

	return m, nil
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
	case "/resume":
		lines := m.resumeRun(args)
		m.appendChatBlock(lines...)
		return m, m.resetComposerAndSync()
	case "/approvals":
		m.appendChatBlock(formatSlashList("Approvals", m.listApprovals())...)
		return m, m.resetComposerAndSync()
	case "/clear":
		m.messages = nil
		m.lastRun = nil
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

func loadingTick() tea.Cmd {
	return tea.Tick(loadingInterval, func(time.Time) tea.Msg {
		return loadingTickMsg{}
	})
}

func clockTick() tea.Cmd {
	return tea.Tick(time.Second, func(now time.Time) tea.Msg {
		return clockTickMsg{now: now}
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

func (m *model) resumeRun(args []string) []string {
	if m.runStore == nil {
		return []string{"resume unavailable: run store is not configured"}
	}
	runID := strings.TrimSpace(strings.Join(args, " "))
	if runID == "" {
		runID = "latest"
	}
	var run *domain.RunState
	var err error
	if runID == "latest" {
		run, err = m.runStore.LoadLatestRun(context.Background())
	} else {
		run, err = m.runStore.LoadRun(context.Background(), runID)
	}
	if err != nil {
		return []string{"resume failed: " + err.Error()}
	}
	if run == nil {
		if runID == "latest" {
			return []string{"resume failed: no previous run found"}
		}
		return []string{"resume failed: run not found: " + runID}
	}
	m.lastRun = run
	m.messages = cloneDomainMessages(run.Messages)
	if run.Profile != "" {
		m.selectedProfile = run.Profile
		m.invalidateModelHeader()
	}
	m.invalidateAllPanels()
	lines := []string{
		fmt.Sprintf("Resumed run %s", run.ID),
		fmt.Sprintf("phase=%s status=%s attempt=%d", run.CurrentPhase, run.Status, run.Attempt),
	}
	if len(run.WorkUnits) > 0 {
		lines = append(lines, fmt.Sprintf("work_units=%d", len(run.WorkUnits)))
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

func uniqueSortedStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func tailCheckpoints(items []domain.RunCheckpoint, limit int) []domain.RunCheckpoint {
	if limit <= 0 || len(items) <= limit {
		return append([]domain.RunCheckpoint(nil), items...)
	}
	return append([]domain.RunCheckpoint(nil), items[len(items)-limit:]...)
}

func (m model) loadingStatusView() string {
	if !m.loading || m.permission != nil {
		return ""
	}
	frame := loadingFrames[m.loadingFrame%len(loadingFrames)]
	return m.styles.tool.Render(frame + " 処理中...")
}

func (m *model) cachedHeaderView() string {
	key := fmt.Sprintf("%d:%s:%s:%t:%s", m.width, m.modelOverride, m.selectedProfile, m.streaming, m.selectedTheme)
	if !m.headerCache.dirty && m.headerCache.key == key {
		return m.headerCache.content
	}
	m.headerCache.key = key
	modelLine := fmt.Sprintf("profile=%s  model=%s  stream=%s  theme=%s", m.profileDisplayLabel(), m.modelDisplayLabel(), m.streamingDisplayLabel(), m.themeDisplayLabel())
	if m.defaultModel != "" && m.modelOverride == "" {
		modelLine += "  default=" + m.defaultModel
	}
	m.headerCache.content = m.styles.hint.Render("入力： /help, /profile, /model, /stream, /theme, /graph, /plan, /verification, /memory, /resume, /clear, /exit | Ctrl+←/→ または Ctrl+H/L / Alt+[/] で panel 切替 | Alt+↑/↓ と PgUp/PgDn でログ") +
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
