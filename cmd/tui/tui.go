package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"yagent/cmd/llm"
)

type message struct {
	content string
	err     error
	role    string
}

type model struct {
	messages []llm.Message
	output   []string
	loading  bool
	client   *llm.LLMClient
	style    styles
	textarea textarea.Model
	history  []string
	histIdx  int
}

type styles struct {
	prompt    lipgloss.Style
	user      lipgloss.Style
	assistant lipgloss.Style
	tool      lipgloss.Style
	system    lipgloss.Style
	separator lipgloss.Style
	error     lipgloss.Style
	hint      lipgloss.Style
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
}

func NewModel(client *llm.LLMClient) model {
	var s styles
	s.init()

	ta := textarea.New()
	ta.Placeholder = "質問を入力... (Ctrl+J で改行，Enter で送信，/exit または Ctrl+C で終了)"
	ta.CharLimit = 50000
	ta.ShowLineNumbers = false
	ta.Focus()

	return model{
		history:  []string{},
		messages: []llm.Message{},
		client:   client,
		style:    s,
		textarea: ta,
	}
}

func (m model) Init() tea.Cmd {
	return textarea.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			fmt.Println("さようなら！")
			return m, tea.Quit
		case tea.KeyCtrlJ:
			// Ctrl+J で改行
			m.textarea.InsertString("\n")
			return m, nil
		case tea.KeyEnter:
			// Enter で送信

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
					m.textarea.Reset()
					return m, nil
				case "/clear":
					m.messages = m.messages[:1]
					m.output = append(m.output, "チャット履歴をクリアしました")
					m.textarea.Reset()
					return m, nil
				default:
					m.output = append(m.output, "不明なコマンドです。/help でヘルプを表示します")
					m.textarea.Reset()
					return m, nil
				}
			}

			m.history = append(m.history, m.textarea.Value())
			m.histIdx = len(m.history)
			m.messages = append(m.messages, llm.Message{
				Role:    "user",
				Content: trimmed,
			})
			m.textarea.Reset()
			m.loading = true

			toolDefinitions := m.client.GetToolHandler().GetRegistry().List()
			request := llm.ChatRequest{
				Messages: m.messages,
				Tools:    toolDefinitions,
			}

			return m, func() tea.Msg {
				content, err := m.client.SendChatWithTools(request, 20)
				return message{content: content, err: err, role: "assistant"}
			}

		case tea.KeyUp:
			if m.histIdx > 0 {
				m.histIdx--
				m.textarea.SetValue(m.history[m.histIdx])
			}
			return m, nil
		case tea.KeyDown:
			if m.histIdx < len(m.history)-1 {
				m.histIdx++
				m.textarea.SetValue(m.history[m.histIdx])
			} else {
				m.histIdx = len(m.history)
				m.textarea.Reset()
			}
			return m, nil
		}

		// textarea の更新
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd

	case message:
		m.loading = false
		if msg.err != nil {
			m.output = append(m.output, "LLM サーバーとの通信に失敗しました："+msg.err.Error())
			m.messages = m.messages[:len(m.messages)-1]
			return m, nil
		}

		m.output = append(m.output, "yagent")
		m.output = append(m.output, msg.content)
		m.output = append(m.output, "───────────────────────────────────────────────────────────────────────")
		m.messages = append(m.messages, llm.Message{
			Role:    "assistant",
			Content: msg.content,
		})

	case tea.WindowSizeMsg:
		return m, nil
	}

	return m, nil
}

func (m model) View() string {
	var sb strings.Builder

	sb.WriteString(m.style.hint.Render("入力：'quit' で終了 | コマンド：/help, /clear | 矢印キーで履歴"))
	sb.WriteString("\n")
	sb.WriteString(m.style.separator.Render("───────────────────────────────────────────────────────────────────────"))
	sb.WriteString("\n\n")

	for _, line := range m.output {
		if line == "yagent" {
			sb.WriteString(m.style.assistant.Render(line))
		} else if strings.HasPrefix(line, "────────") {
			sb.WriteString(m.style.separator.Render(line))
		} else {
			sb.WriteString(line)
		}
		sb.WriteString("\n")
	}

	if m.loading {
		sb.WriteString(m.style.tool.Render("処理中..."))
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(m.style.prompt.Render("❯"))
	sb.WriteString(" ")
	sb.WriteString(m.textarea.View())

	return sb.String()
}

func Run(client *llm.LLMClient) {
	m := NewModel(client)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "エラー：%v\n", err)
		os.Exit(1)
	}
}
