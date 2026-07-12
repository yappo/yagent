package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"yagent/internal/domain"
)

func (m *model) syncComposer() {
	height := strings.Count(m.textarea.Value(), "\n") + 1
	if height < 1 {
		height = 1
	}
	if height > maxComposerHeight {
		height = maxComposerHeight
	}
	if m.textarea.Height() != height {
		m.textarea.SetHeight(height)
		m.composerCache.dirty = true
	}
	if m.width > 0 && m.composerWidth != m.width {
		m.textarea.SetWidth(m.width)
		m.composerWidth = m.width
		m.composerCache.dirty = true
	}
}

func (m model) currentInput() string {
	return strings.TrimSpace(m.textarea.Value())
}

func handleComposerKeys(m model, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if delta, ok := panelNavigationDelta(msg); ok {
		m.cyclePanel(delta)
		m.syncLayout()
		if m.activePanel == sidePanelMemory {
			return m, m.loadMemoryCmd()
		}
		return m, nil
	}
	if next, handled := handleStatusFailureKeys(m, msg); handled {
		next.syncLayout()
		return next, nil
	}

	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "tab":
		return m, m.applyCompletion()
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
			return m, m.syncAfterComposerChange(false)
		}
		if m.historyIndex > 0 {
			m.historyIndex--
			m.textarea.SetValue(m.history[m.historyIndex])
			m.reconcileSelectedRefs()
			return m, m.syncAfterComposerChange(false)
		}
		return m, nil
	case "down", "alt+down":
		if msg.String() == "alt+down" {
			m.viewport.ScrollDown(1)
			return m, nil
		}
		if m.textarea.Line() < m.textarea.LineCount()-1 {
			m.textarea.CursorDown()
			return m, m.syncAfterComposerChange(false)
		}
		if m.historyIndex < len(m.history)-1 {
			m.historyIndex++
			m.textarea.SetValue(m.history[m.historyIndex])
		} else {
			m.historyIndex = len(m.history)
			m.textarea.Reset()
		}
		m.reconcileSelectedRefs()
		return m, m.syncAfterComposerChange(false)
	case "ctrl+j":
		m.textarea.InsertString("\n")
		m.reconcileSelectedRefs()
		return m, m.syncAfterComposerChange(false)
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
	return m, batchCmds(cmd, m.syncAfterComposerChange(false))
}

func submitPrompt(m model, input string) (tea.Model, tea.Cmd) {
	m.history = append(m.history, input)
	m.historyIndex = len(m.history)
	normalized := normalizePromptReferences(input, m.selectedRefs)
	m.messages = append(m.messages, domain.Message{
		Role:    domain.RoleUser,
		Content: normalized,
	})
	m.appendOutputBlock(userOutputLabel, input)
	m.textarea.Reset()
	m.selectedRefs = map[string]string{}
	m.resetStreamingBlock()
	m.loading = true
	m.loadingFrame = 0
	m.mainPanelsCache.dirty = true
	layoutCmd := m.syncAfterComposerChange(false)

	send := func() tea.Msg {
		var result domain.TurnResult
		var err error
		if m.conversationID != "" {
			result, err = m.runner.ContinueConversation(context.Background(), domain.ConversationTurnRequest{
				ConversationID: m.conversationID,
				Messages:       []domain.Message{{Role: domain.RoleUser, Content: normalized}},
				Model:          m.modelOverride,
				Profile:        m.selectedProfile,
				Stream:         m.streaming,
			})
		} else {
			result, err = m.runner.RunTurn(context.Background(), domain.TurnRequest{
				Messages: m.messages,
				Model:    m.modelOverride,
				Profile:  m.selectedProfile,
				Stream:   m.streaming,
			})
		}
		return chatMessage{content: result.Message.Content, run: result.Run, err: err}
	}

	return m, batchCmds(send, loadingTick(), layoutCmd)
}

func (m *model) cachedComposerView() string {
	lineInfo := m.textarea.LineInfo()
	key := fmt.Sprintf("%d:%s:%d:%d", m.width, m.textarea.Value(), m.textarea.Line(), lineInfo.CharOffset)
	if !m.composerCache.dirty && m.composerCache.key == key {
		return m.composerCache.content
	}
	m.composerCache.key = key
	m.composerCache.content = m.textarea.View()
	m.composerCache.dirty = false
	return m.composerCache.content
}
