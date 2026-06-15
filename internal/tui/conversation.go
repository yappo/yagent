package tui

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
)

var orderedListPattern = regexp.MustCompile(`^(\d+)[.)]\s+(.+)$`)

type chatBlock struct {
	rawLines    []string
	cachedWidth int
	rendered    string
}

type logRenderState struct {
	width      int
	blockCount int
	content    string
	dirty      bool
}

func (m *model) appendChatBlock(rawLines ...string) {
	if len(rawLines) == 0 {
		return
	}
	m.chatBlocks = append(m.chatBlocks, chatBlock{rawLines: append([]string(nil), rawLines...)})
	m.logDirty = true
	m.mainPanelsCache.dirty = true
}

func (m *model) appendOutputBlock(label, content string) {
	lines := []string{label}
	lines = append(lines, strings.Split(content, "\n")...)
	lines = append(lines, "───────────────────────────────────────────────────────────────────────")
	m.appendChatBlock(lines...)
}

func (m *model) resetStreamingBlock() {
	m.streamingContent = ""
	m.streamingBlock = -1
}

func (m *model) applyStreamDelta(delta string) {
	if delta == "" {
		return
	}
	m.streamingContent += delta
	m.setStreamingOutputBlock(m.streamingContent)
}

func (m *model) finalizeStreamingBlock(content string) bool {
	if m.streamingBlock < 0 || m.streamingBlock >= len(m.chatBlocks) {
		m.resetStreamingBlock()
		return false
	}
	m.setStreamingOutputBlock(content)
	m.resetStreamingBlock()
	return true
}

func (m *model) setStreamingOutputBlock(content string) {
	lines := []string{assistantOutputLabel}
	lines = append(lines, strings.Split(content, "\n")...)
	lines = append(lines, "───────────────────────────────────────────────────────────────────────")
	block := chatBlock{rawLines: append([]string(nil), lines...)}
	if m.streamingBlock < 0 || m.streamingBlock >= len(m.chatBlocks) {
		m.chatBlocks = append(m.chatBlocks, block)
		m.streamingBlock = len(m.chatBlocks) - 1
	} else {
		m.chatBlocks[m.streamingBlock] = block
	}
	m.logDirty = true
	m.mainPanelsCache.dirty = true
}

func (m *model) resetChatBlocks(rawLines ...string) {
	m.chatBlocks = nil
	m.resetStreamingBlock()
	if len(rawLines) > 0 {
		m.chatBlocks = append(m.chatBlocks, chatBlock{rawLines: append([]string(nil), rawLines...)})
	}
	m.logDirty = true
	m.logRender = logRenderState{dirty: true}
	m.mainPanelsCache.dirty = true
}

func (m *model) refreshViewport() {
	if m.logDirty {
		m.viewport.SetContent(m.renderLog())
		m.viewport.GotoBottom()
		m.logDirty = false
	}
	if m.width > 0 && m.statusViewport.Width() > 0 {
		cache := m.panelCache[m.activePanel]
		needsRefresh := cache.dirty || cache.width != m.statusViewport.Width()
		content := m.renderStatus()
		if needsRefresh {
			m.statusViewport.SetContent(content)
			m.statusViewport.GotoTop()
		}
	}
	m.syncToolLogViewport()
}

func (m *model) renderLog() string {
	contentWidth := maxInt(1, m.viewport.Width())
	if m.logRender.dirty || m.logRender.width != contentWidth || m.logRender.blockCount > len(m.chatBlocks) {
		var sb strings.Builder
		for idx := range m.chatBlocks {
			block := m.chatBlocks[idx]
			if block.cachedWidth != contentWidth || block.rendered == "" {
				block.rendered = renderChatBlock(block.rawLines, contentWidth, m.styles)
				block.cachedWidth = contentWidth
				m.chatBlocks[idx] = block
			}
			if block.rendered == "" {
				continue
			}
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(block.rendered)
		}
		m.logRender = logRenderState{
			width:      contentWidth,
			blockCount: len(m.chatBlocks),
			content:    sb.String(),
		}
		return m.logRender.content
	}

	if m.logRender.blockCount == len(m.chatBlocks) {
		return m.logRender.content
	}

	var sb strings.Builder
	sb.WriteString(m.logRender.content)
	for idx := m.logRender.blockCount; idx < len(m.chatBlocks); idx++ {
		block := m.chatBlocks[idx]
		if block.cachedWidth != contentWidth || block.rendered == "" {
			block.rendered = renderChatBlock(block.rawLines, contentWidth, m.styles)
			block.cachedWidth = contentWidth
			m.chatBlocks[idx] = block
		}
		if block.rendered == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(block.rendered)
	}
	m.logRender.width = contentWidth
	m.logRender.blockCount = len(m.chatBlocks)
	m.logRender.content = sb.String()
	m.logRender.dirty = false
	return m.logRender.content
}

func wrapContent(content string, width int) string {
	if width <= 0 {
		return content
	}

	return runewidth.Wrap(content, width)
}

func renderChatBlock(lines []string, width int, styles styles) string {
	if len(lines) == 0 {
		return ""
	}
	rendered := make([]string, 0, len(lines))
	inCodeFence := false
	for _, line := range lines {
		switch {
		case line == userOutputLabel:
			rendered = append(rendered, styles.user.Render("You"))
		case line == assistantOutputLabel:
			rendered = append(rendered, styles.assistant.Render("yagent"))
		case strings.HasPrefix(line, "────────"):
			rendered = append(rendered, styles.separator.Render(strings.Repeat("─", width)))
		default:
			rendered = append(rendered, renderMarkdownLine(line, width, styles, &inCodeFence)...)
		}
	}
	return strings.Join(rendered, "\n")
}

func renderMarkdownLine(line string, width int, styles styles, inCodeFence *bool) []string {
	trimmed := strings.TrimSpace(line)
	if isMarkdownFence(trimmed) {
		*inCodeFence = !*inCodeFence
		label := "code"
		if info := strings.TrimSpace(strings.TrimLeft(trimmed, "`~")); info != "" {
			label = "code: " + info
		}
		return styledWrappedLines(label, width, styles.markdownCode)
	}
	if *inCodeFence {
		if line == "" {
			return []string{styles.markdownCode.Render(" ")}
		}
		return styledWrappedLines(line, width, styles.markdownCode)
	}
	if trimmed == "" {
		return []string{""}
	}
	if heading, ok := markdownHeading(trimmed); ok {
		return styledWrappedLines(heading, width, styles.markdownHeading)
	}
	if quote, ok := markdownQuote(trimmed); ok {
		return styledWrappedLines("│ "+quote, width, styles.markdownQuote)
	}
	if item, ok := markdownListItem(trimmed); ok {
		return styledWrappedLines(item, width, styles.markdownList)
	}
	return splitWrappedLines(wrapContent(cleanInlineMarkdown(line), width))
}

func isMarkdownFence(trimmed string) bool {
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

func markdownHeading(trimmed string) (string, bool) {
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(trimmed) || trimmed[level] != ' ' {
		return "", false
	}
	text := strings.TrimSpace(trimmed[level:])
	if text == "" {
		return "", false
	}
	return text, true
}

func markdownQuote(trimmed string) (string, bool) {
	if !strings.HasPrefix(trimmed, ">") {
		return "", false
	}
	return cleanInlineMarkdown(strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))), true
}

func markdownListItem(trimmed string) (string, bool) {
	if len(trimmed) >= 2 && (trimmed[0] == '-' || trimmed[0] == '*' || trimmed[0] == '+') && unicode.IsSpace(rune(trimmed[1])) {
		return "• " + cleanInlineMarkdown(strings.TrimSpace(trimmed[1:])), true
	}
	if matches := orderedListPattern.FindStringSubmatch(trimmed); len(matches) == 3 {
		return fmt.Sprintf("%s. %s", matches[1], cleanInlineMarkdown(matches[2])), true
	}
	return "", false
}

func cleanInlineMarkdown(text string) string {
	text = stripMarkdownPairs(text, "**")
	text = stripMarkdownPairs(text, "__")
	text = stripMarkdownPairs(text, "`")
	return text
}

func stripMarkdownPairs(text string, marker string) string {
	for {
		start := strings.Index(text, marker)
		if start < 0 {
			return text
		}
		end := strings.Index(text[start+len(marker):], marker)
		if end < 0 {
			return text
		}
		end += start + len(marker)
		text = text[:start] + text[start+len(marker):end] + text[end+len(marker):]
	}
}

func styledWrappedLines(text string, width int, style interface{ Render(...string) string }) []string {
	lines := splitWrappedLines(wrapContent(text, width))
	for index, line := range lines {
		if line == "" {
			continue
		}
		lines[index] = style.Render(line)
	}
	return lines
}

func splitWrappedLines(text string) []string {
	return strings.Split(text, "\n")
}
