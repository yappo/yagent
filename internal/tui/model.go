package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	messages         []domain.Message
	output           []string
	loading          bool
	loadingFrame     int
	width            int
	height           int
	viewport         viewport.Model
	textarea         textarea.Model
	history          []string
	historyIndex     int
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

func newModel(runner *chatusecase.Service) model {
	ta := textarea.New()
	ta.Placeholder = "質問を入力... (Ctrl+J で改行, Enter で送信, /exit または Ctrl+C で終了)"
	ta.CharLimit = 50000
	ta.ShowLineNumbers = false
	ta.SetPromptFunc(2, func(line int) string {
		if line == 0 {
			return "❯ "
		}
		return "  "
	})
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	ta.Focus()
	ta.SetHeight(1)

	m := model{
		runner:           runner,
		viewport:         viewport.New(0, 0),
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
	if m.hasCommandCandidates() {
		footerHeight += m.commandSuggestionsHeight()
	}
	if m.permission != nil {
		footerHeight += m.permissionCardHeight()
	}
	headerHeight := 3
	m.viewport.Width = m.width
	m.viewport.Height = maxInt(3, m.height-headerHeight-footerHeight)
	m.refreshViewport()
}

func (m *model) refreshViewport() {
	m.viewport.SetContent(m.renderLog())
	m.viewport.GotoBottom()
}

func (m model) renderLog() string {
	var sb strings.Builder
	contentWidth := maxInt(1, m.viewport.Width)

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

func (m model) commandCandidates() []slashCommand {
	input := m.currentInput()
	if !strings.HasPrefix(input, "/") {
		return nil
	}

	candidates := make([]slashCommand, 0, len(slashCommands))
	for _, command := range slashCommands {
		if strings.HasPrefix(command.name, input) {
			candidates = append(candidates, command)
		}
	}

	return candidates
}

func (m model) hasCommandCandidates() bool {
	return len(m.commandCandidates()) > 0
}

func (m *model) applyCommandCompletion() {
	candidates := m.commandCandidates()
	if len(candidates) == 0 {
		return
	}

	m.textarea.SetValue(candidates[0].name)
	m.syncLayout()
}

func (m model) renderCommandSuggestions() string {
	candidates := m.commandCandidates()
	if len(candidates) == 0 {
		return ""
	}

	lines := make([]string, 0, len(candidates)+1)
	lines = append(lines, m.styles.commandHint.Render("候補コマンド: Tab で補完"))
	for index, candidate := range candidates {
		label := fmt.Sprintf("%s  %s", candidate.name, candidate.description)
		if index == 0 {
			lines = append(lines, m.styles.commandSelected.Render(label))
			continue
		}
		lines = append(lines, m.styles.commandCandidate.Render(label))
	}

	return strings.Join(lines, "\n")
}

func (m model) commandSuggestionsHeight() int {
	suggestions := m.renderCommandSuggestions()
	if suggestions == "" {
		return 0
	}
	return lipgloss.Height(suggestions)
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
	if msg.Type == tea.KeyCtrlC {
		m.resolvePermission(domain.PermissionDeny)
		return m, tea.Quit
	}

	switch msg.Type {
	case tea.KeyLeft, tea.KeyShiftTab:
		m.permission.selectedIndex = wrapIndex(m.permission.selectedIndex-1, len(permissionOptions))
		m.syncLayout()
		return m, nil
	case tea.KeyRight, tea.KeyTab:
		m.permission.selectedIndex = wrapIndex(m.permission.selectedIndex+1, len(permissionOptions))
		m.syncLayout()
		return m, nil
	case tea.KeyEnter:
		m.resolvePermission(permissionOptions[m.permission.selectedIndex].decision)
		return m, nil
	case tea.KeyEsc:
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
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyTab:
		if m.hasCommandCandidates() {
			m.applyCommandCompletion()
			return m, nil
		}
		return m, nil
	case tea.KeyPgUp:
		m.viewport.PageUp()
		return m, nil
	case tea.KeyPgDown:
		m.viewport.PageDown()
		return m, nil
	case tea.KeyUp:
		if msg.Alt {
			m.viewport.ScrollUp(1)
			return m, nil
		}
		if m.historyIndex > 0 {
			m.historyIndex--
			m.textarea.SetValue(m.history[m.historyIndex])
			m.syncLayout()
		}
		return m, nil
	case tea.KeyDown:
		if msg.Alt {
			m.viewport.ScrollDown(1)
			return m, nil
		}
		if m.historyIndex < len(m.history)-1 {
			m.historyIndex++
			m.textarea.SetValue(m.history[m.historyIndex])
		} else {
			m.historyIndex = len(m.history)
			m.textarea.Reset()
		}
		m.syncLayout()
		return m, nil
	case tea.KeyCtrlJ:
		m.textarea.InsertString("\n")
		m.syncLayout()
		return m, nil
	case tea.KeyEnter:
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
	m.messages = append(m.messages, domain.Message{
		Role:    domain.RoleUser,
		Content: input,
	})
	m.output = appendOutputBlock(m.output, userOutputLabel, input)
	m.textarea.Reset()
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

func (m model) renderPermissionCard() string {
	if m.permission == nil {
		return ""
	}

	cardWidth := maxInt(32, m.width-2)
	resource := trimPathFromEnd(m.permission.request.Resource, cardWidth-4)
	card := strings.Join([]string{
		m.styles.permissionTitle.Render(m.permission.request.Operation),
		m.styles.permissionPath.Render(resource),
		renderPermissionOptions(m.permission.selectedIndex, m.styles.permissionSelected, m.styles.permissionOption),
		m.styles.permissionHelp.Render("←/→ または Tab で選択 • Enter で確定 • Esc で拒否"),
	}, "\n")
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

func (m model) View() string {
	var sb strings.Builder
	sb.WriteString(m.styles.hint.Render("入力： コマンド：/help, /clear, /exit | Alt+↑/↓ と PgUp/PgDn でログ"))
	sb.WriteString("\n")
	sb.WriteString(m.styles.separator.Render("───────────────────────────────────────────────────────────────────────"))
	sb.WriteString("\n\n")
	sb.WriteString(m.viewport.View())
	sb.WriteString("\n")
	if m.permission != nil {
		sb.WriteString(m.renderPermissionCard())
		sb.WriteString("\n")
	}
	if m.hasCommandCandidates() {
		sb.WriteString(m.renderCommandSuggestions())
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	sb.WriteString(m.textarea.View())
	return sb.String()
}
