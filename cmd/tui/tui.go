package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"yagent/cmd/llm"
)

type message struct {
	content string
	err     error
	role    string
}

type loadingTickMsg struct{}

type toolConfirmationMsg struct {
	request  llm.ToolConfirmationRequest
	response chan llm.ToolConfirmationDecision
}

type pendingConfirmation struct {
	request       llm.ToolConfirmationRequest
	response      chan llm.ToolConfirmationDecision
	selectedIndex int
}

type permissionOption struct {
	label    string
	decision llm.ToolConfirmationDecision
}

type model struct {
	messages         []llm.Message
	output           []string
	loading          bool
	loadingFrame     int
	width            int
	height           int
	confirming       *pendingConfirmation
	sessionApprovals map[string]bool
	client           *llm.LLMClient
	style            styles
	textarea         textarea.Model
	viewport         viewport.Model
	history          []string
	histIdx          int
}

var loadingFrames = []string{"◐", "◓", "◑", "◒"}

const userOutputLabel = "__USER__"
const assistantOutputLabel = "__ASSISTANT__"

var permissionOptions = []permissionOption{
	{label: "今回だけ許可", decision: llm.ConfirmAllowOnce},
	{label: "このセッションで許可", decision: llm.ConfirmAllowSession},
	{label: "拒否", decision: llm.ConfirmDeny},
}

const loadingTickInterval = 100 * time.Millisecond
const maxTextareaHeight = 6

type styles struct {
	prompt             lipgloss.Style
	user               lipgloss.Style
	assistant          lipgloss.Style
	tool               lipgloss.Style
	system             lipgloss.Style
	separator          lipgloss.Style
	error              lipgloss.Style
	hint               lipgloss.Style
	permissionCard     lipgloss.Style
	permissionTitle    lipgloss.Style
	permissionPath     lipgloss.Style
	permissionHelp     lipgloss.Style
	permissionOption   lipgloss.Style
	permissionSelected lipgloss.Style
}

func (m styles) init() {
	m.prompt = lipgloss.NewStyle().
		Foreground(lipgloss.Color("205")).
		Bold(true)

	m.user = lipgloss.NewStyle().
		Foreground(lipgloss.Color("206")).
		Bold(true)

	m.assistant = lipgloss.NewStyle().
		Foreground(lipgloss.Color("75"))

	m.tool = lipgloss.NewStyle().
		Foreground(lipgloss.Color("220")).
		Italic(true)

	m.system = lipgloss.NewStyle().
		Foreground(lipgloss.Color("99"))

	m.separator = lipgloss.NewStyle().
		Foreground(lipgloss.Color("240"))

	m.error = lipgloss.NewStyle().
		Foreground(lipgloss.Color("196"))

	m.hint = lipgloss.NewStyle().
		Foreground(lipgloss.Color("99")).
		Italic(true)

	m.permissionCard = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1)

	m.permissionTitle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("230")).
		Bold(true)

	m.permissionPath = lipgloss.NewStyle().
		Foreground(lipgloss.Color("252"))

	m.permissionHelp = lipgloss.NewStyle().
		Foreground(lipgloss.Color("244"))

	m.permissionOption = lipgloss.NewStyle().
		Foreground(lipgloss.Color("250")).
		Padding(0, 1)

	m.permissionSelected = lipgloss.NewStyle().
		Foreground(lipgloss.Color("232")).
		Background(lipgloss.Color("221")).
		Bold(true).
		Padding(0, 1)
}

func NewModel(client *llm.LLMClient) model {
	var s styles
	s.init()

	ta := textarea.New()
	ta.Placeholder = "質問を入力... (Ctrl+J で改行，Enter で送信，/exit または Ctrl+C で終了)"
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

	vp := viewport.New(0, 0)

	return model{
		history:          []string{},
		messages:         []llm.Message{},
		sessionApprovals: map[string]bool{},
		client:           client,
		style:            s,
		textarea:         ta,
		viewport:         vp,
	}
}

func (m model) Init() tea.Cmd {
	return textarea.Blink
}

func loadingTick() tea.Cmd {
	return tea.Tick(loadingTickInterval, func(time.Time) tea.Msg {
		return loadingTickMsg{}
	})
}

func (m *model) syncTextareaSize() {
	height := strings.Count(m.textarea.Value(), "\n") + 1
	if height < 1 {
		height = 1
	}
	if height > maxTextareaHeight {
		height = maxTextareaHeight
	}
	m.textarea.SetHeight(height)

	if m.width <= 0 {
		return
	}

	m.textarea.SetWidth(m.width)
}

func (m *model) syncLayout() {
	m.syncTextareaSize()

	if m.width <= 0 || m.height <= 0 {
		return
	}

	footerHeight := lipgloss.Height(m.textarea.View()) + 1
	if m.confirming != nil {
		footerHeight += m.permissionCardHeight()
	}

	headerHeight := 3
	viewportHeight := m.height - headerHeight - footerHeight
	if viewportHeight < 3 {
		viewportHeight = 3
	}

	m.viewport.Width = m.width
	m.viewport.Height = viewportHeight
	m.refreshViewportContent()
}

func (m model) nextPermissionIndex(step int) int {
	if m.confirming == nil {
		return 0
	}

	count := len(permissionOptions)
	return (m.confirming.selectedIndex + step + count) % count
}

func permissionDecisionLabel(decision llm.ToolConfirmationDecision) string {
	switch decision {
	case llm.ConfirmAllowOnce:
		return "今回だけ許可"
	case llm.ConfirmAllowSession:
		return "このセッションで許可"
	default:
		return "拒否"
	}
}

func renderPermissionOptions(selectedIndex int, selectedStyle lipgloss.Style, baseStyle lipgloss.Style) string {
	parts := make([]string, 0, len(permissionOptions))
	for i, option := range permissionOptions {
		label := fmt.Sprintf("%d. %s", i+1, option.label)
		if i == selectedIndex {
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

func (m model) resolveConfirmation(decision llm.ToolConfirmationDecision) (model, tea.Cmd) {
	if m.confirming == nil {
		return m, nil
	}

	request := m.confirming.request
	if decision == llm.ConfirmAllowSession {
		m.sessionApprovals[approvalKey(request)] = true
	}

	m.output = append(m.output, m.style.tool.Render(fmt.Sprintf("%s %s を%s", request.Operation, request.FilePath, permissionDecisionLabel(decision))))
	m.confirming.response <- decision
	close(m.confirming.response)
	m.confirming = nil
	m.syncLayout()

	return m, nil
}

func approvalKey(request llm.ToolConfirmationRequest) string {
	return request.ToolName + "\x00" + request.FilePath
}

func appendOutputBlock(output []string, label string, content string) []string {
	output = append(output, label)
	output = append(output, strings.Split(content, "\n")...)
	output = append(output, "───────────────────────────────────────────────────────────────────────")
	return output
}

func (m model) renderOutputContent() string {
	var sb strings.Builder

	for _, line := range m.output {
		if line == userOutputLabel {
			sb.WriteString(m.style.user.Render("You"))
		} else if line == assistantOutputLabel {
			sb.WriteString(m.style.assistant.Render("yagent"))
		} else if strings.HasPrefix(line, "────────") {
			sb.WriteString(m.style.separator.Render(line))
		} else {
			sb.WriteString(line)
		}
		sb.WriteString("\n")
	}

	if m.loading && m.confirming == nil {
		frame := loadingFrames[m.loadingFrame%len(loadingFrames)]
		sb.WriteString(m.style.tool.Render(frame + " 処理中..."))
		sb.WriteString("\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}

func (m *model) refreshViewportContent() {
	m.viewport.SetContent(m.renderOutputContent())
	m.viewport.GotoBottom()
}

func (m model) permissionCardHeight() int {
	if m.confirming == nil {
		return 0
	}

	cardWidth := m.width - 2
	if cardWidth < 32 {
		cardWidth = 32
	}

	path := trimPathFromEnd(m.confirming.request.FilePath, cardWidth-4)
	card := strings.Join([]string{
		m.style.permissionTitle.Render(m.confirming.request.Operation),
		m.style.permissionPath.Render(path),
		renderPermissionOptions(m.confirming.selectedIndex, m.style.permissionSelected, m.style.permissionOption),
		m.style.permissionHelp.Render("←/→ または Tab で選択 • Enter で確定 • Esc で拒否"),
	}, "\n")

	return lipgloss.Height(m.style.permissionCard.Width(cardWidth).Render(card))
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.confirming != nil {
			if msg.Type == tea.KeyCtrlC {
				m, _ = m.resolveConfirmation(llm.ConfirmDeny)
				fmt.Println("さようなら！")
				return m, tea.Quit
			}

			switch msg.Type {
			case tea.KeyLeft, tea.KeyShiftTab:
				m.confirming.selectedIndex = m.nextPermissionIndex(-1)
				m.syncLayout()
				return m, nil
			case tea.KeyRight, tea.KeyTab:
				m.confirming.selectedIndex = m.nextPermissionIndex(1)
				m.syncLayout()
				return m, nil
			case tea.KeyEnter:
				return m.resolveConfirmation(permissionOptions[m.confirming.selectedIndex].decision)
			case tea.KeyEsc:
				return m.resolveConfirmation(llm.ConfirmDeny)
			}

			switch strings.ToLower(msg.String()) {
			case "h":
				m.confirming.selectedIndex = m.nextPermissionIndex(-1)
				m.syncLayout()
				return m, nil
			case "l":
				m.confirming.selectedIndex = m.nextPermissionIndex(1)
				m.syncLayout()
				return m, nil
			case "1", "2", "3":
				idx := int(msg.String()[0] - '1')
				if idx >= 0 && idx < len(permissionOptions) {
					m.confirming.selectedIndex = idx
					return m.resolveConfirmation(permissionOptions[idx].decision)
				}
			case "y":
				m.confirming.selectedIndex = 0
				return m.resolveConfirmation(llm.ConfirmAllowOnce)
			case "n":
				return m.resolveConfirmation(llm.ConfirmDeny)
			}

			return m, nil
		}

		switch msg.Type {
		case tea.KeyCtrlC:
			fmt.Println("さようなら！")
			return m, tea.Quit
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
			if m.histIdx > 0 {
				m.histIdx--
				m.textarea.SetValue(m.history[m.histIdx])
				m.syncLayout()
			}
			return m, nil
		case tea.KeyDown:
			if msg.Alt {
				m.viewport.ScrollDown(1)
				return m, nil
			}
			if m.histIdx < len(m.history)-1 {
				m.histIdx++
				m.textarea.SetValue(m.history[m.histIdx])
				m.syncLayout()
			} else {
				m.histIdx = len(m.history)
				m.textarea.Reset()
				m.syncLayout()
			}
			return m, nil
		case tea.KeyCtrlJ:
			m.textarea.InsertString("\n")
			m.syncLayout()
			return m, nil
		case tea.KeyEnter:
			if m.textarea.Value() == "" {
				return m, nil
			}

			trimmed := strings.TrimSpace(m.textarea.Value())
			if strings.HasPrefix(trimmed, "/") {
				switch trimmed {
				case "/exit":
					fmt.Println("さようなら！")
					return m, tea.Quit
				case "/help":
					m.output = append(m.output, "コマンド:")
					m.output = append(m.output, "  /help - このヘルプを表示")
					m.output = append(m.output, "  /clear - チャット履歴をクリア")
					m.refreshViewportContent()
					m.textarea.Reset()
					m.syncLayout()
					return m, nil
				case "/clear":
					m.messages = m.messages[:1]
					m.output = append(m.output, "チャット履歴をクリアしました")
					m.refreshViewportContent()
					m.textarea.Reset()
					m.syncLayout()
					return m, nil
				default:
					m.output = append(m.output, "不明なコマンドです。/help でヘルプを表示します")
					m.refreshViewportContent()
					m.textarea.Reset()
					m.syncLayout()
					return m, nil
				}
			}

			m.history = append(m.history, m.textarea.Value())
			m.histIdx = len(m.history)
			m.messages = append(m.messages, llm.Message{
				Role:    "user",
				Content: trimmed,
			})
			m.output = appendOutputBlock(m.output, userOutputLabel, trimmed)
			m.textarea.Reset()
			m.syncLayout()
			m.loading = true
			m.loadingFrame = 0
			m.refreshViewportContent()

			toolDefinitions := m.client.GetToolHandler().GetRegistry().List()
			request := llm.ChatRequest{
				Messages: m.messages,
				Tools:    toolDefinitions,
			}

			sendCmd := func() tea.Msg {
				content, err := m.client.SendChatWithTools(request, 20)
				return message{content: content, err: err, role: "assistant"}
			}

			return m, tea.Batch(sendCmd, loadingTick())
		}

		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		m.syncLayout()
		return m, cmd

	case loadingTickMsg:
		if !m.loading {
			return m, nil
		}

		m.loadingFrame = (m.loadingFrame + 1) % len(loadingFrames)
		m.refreshViewportContent()
		return m, loadingTick()

	case message:
		m.loading = false
		if msg.err != nil {
			m.output = append(m.output, "LLM サーバーとの通信に失敗しました："+msg.err.Error())
			m.refreshViewportContent()
			m.messages = m.messages[:len(m.messages)-1]
			return m, nil
		}

		m.output = appendOutputBlock(m.output, assistantOutputLabel, msg.content)
		m.refreshViewportContent()
		m.messages = append(m.messages, llm.Message{
			Role:    "assistant",
			Content: msg.content,
		})

	case toolConfirmationMsg:
		if m.sessionApprovals[approvalKey(msg.request)] {
			msg.response <- llm.ConfirmAllowSession
			close(msg.response)
			return m, nil
		}

		m.confirming = &pendingConfirmation{
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
	}

	return m, nil
}

func (m model) View() string {
	var sb strings.Builder

	sb.WriteString(m.style.hint.Render("入力： コマンド：/help, /clear, /exit | Alt+↑/↓ と PgUp/PgDn でログ"))
	sb.WriteString("\n")
	sb.WriteString(m.style.separator.Render("───────────────────────────────────────────────────────────────────────"))
	sb.WriteString("\n\n")
	sb.WriteString(m.viewport.View())
	sb.WriteString("\n")

	if m.confirming != nil {
		cardWidth := m.width - 2
		if cardWidth < 32 {
			cardWidth = 32
		}

		path := trimPathFromEnd(m.confirming.request.FilePath, cardWidth-4)
		card := strings.Join([]string{
			m.style.permissionTitle.Render(m.confirming.request.Operation),
			m.style.permissionPath.Render(path),
			renderPermissionOptions(m.confirming.selectedIndex, m.style.permissionSelected, m.style.permissionOption),
			m.style.permissionHelp.Render("←/→ または Tab で選択 • Enter で確定 • Esc で拒否"),
		}, "\n")
		sb.WriteString(m.style.permissionCard.Width(cardWidth).Render(card))
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(m.textarea.View())

	return sb.String()
}

func Run(client *llm.LLMClient) {
	m := NewModel(client)
	p := tea.NewProgram(m, tea.WithAltScreen())

	confirmFunc := func(request llm.ToolConfirmationRequest) llm.ToolConfirmationDecision {
		response := make(chan llm.ToolConfirmationDecision, 1)
		p.Send(toolConfirmationMsg{
			request:  request,
			response: response,
		})

		return <-response
	}

	if handler := client.GetToolHandler(); handler != nil {
		if tool, ok := handler.GetRegistry().Get("file_reader").(*llm.FileReadTool); ok {
			tool.WithConfirmFunc(confirmFunc)
		}
		if tool, ok := handler.GetRegistry().Get("file_writer").(*llm.FileWriterTool); ok {
			tool.WithConfirmFunc(confirmFunc)
		}
	}

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "エラー：%v\n", err)
		os.Exit(1)
	}
}
