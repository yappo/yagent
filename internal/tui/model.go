package tui

import (
	"context"
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
	content     string
	run         *domain.RunState
	err         error
	displayOnly bool
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
	conversationID   domain.ConversationID
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

var loadingFrames = []string{"◐", "◓", "◑", "◒"}

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
			if !msg.displayOnly && len(m.messages) > 0 {
				m.messages = m.messages[:len(m.messages)-1]
			}
			m.refreshViewport()
			return m, nil
		}
		if !msg.displayOnly {
			m.messages = append(m.messages, domain.Message{
				Role:    domain.RoleAssistant,
				Content: msg.content,
			})
		}
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
