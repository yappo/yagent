package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"yagent/internal/domain"
)

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

func (m model) toolCardHeight() int {
	if !m.hasActiveTools() {
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

func (m model) renderToolCard() string {
	if !m.hasActiveTools() {
		return ""
	}

	cardWidth := maxInt(32, m.width-2)
	active := m.orderedActiveTools()
	lines := []string{m.styles.toolTitle.Render(fmt.Sprintf("Tool Use (%d active)", len(active)))}
	display := active
	const maxVisibleActiveTools = 4
	if len(display) > maxVisibleActiveTools {
		display = display[:maxVisibleActiveTools]
	}
	for _, state := range display {
		statusStyle := m.styles.toolMeta
		if state.success != nil {
			if *state.success {
				statusStyle = m.styles.toolSuccess
			} else {
				statusStyle = m.styles.toolFailure
			}
		}
		lines = append(lines, m.styles.toolMeta.Render(state.name+"  "+statusStyle.Render(state.status)))
		if state.target != "" {
			lines = append(lines, m.styles.permissionPath.Render(trimPathFromEnd(state.target, cardWidth-4)))
		}
		for _, option := range state.options {
			lines = append(lines, m.styles.toolOption.Render("• "+option))
		}
	}
	if omitted := len(active) - len(display); omitted > 0 {
		lines = append(lines, m.styles.toolMeta.Render(fmt.Sprintf("+%d more active tools", omitted)))
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
	if len(m.toolLogs) > maxToolLogEntries {
		m.toolLogs = m.toolLogs[len(m.toolLogs)-maxToolLogEntries:]
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

func (m model) hasActiveTools() bool {
	return len(m.activeTools) > 0
}

func (m model) orderedActiveTools() []*toolCallState {
	items := make([]*toolCallState, 0, len(m.activeTools))
	seen := map[string]bool{}
	for _, key := range m.activeToolOrder {
		state := m.activeTools[key]
		if state == nil {
			continue
		}
		items = append(items, state)
		seen[key] = true
	}
	keys := make([]string, 0, len(m.activeTools))
	for key := range m.activeTools {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		items = append(items, m.activeTools[key])
	}
	return items
}

func (m *model) startActiveTool(call domain.ToolCall) {
	if m.activeTools == nil {
		m.activeTools = map[string]*toolCallState{}
	}
	key := m.newActiveToolKey(call)
	m.activeTools[key] = buildToolCallState(call)
	m.activeToolOrder = append(m.activeToolOrder, key)
}

func (m *model) finishActiveTool(call domain.ToolCall, result domain.ToolResult) *toolCallState {
	key, state := m.findActiveTool(call)
	if state == nil {
		state = buildToolCallState(call)
	}
	state = finalizeToolCallState(call, result, state)
	if key != "" {
		delete(m.activeTools, key)
		m.activeToolOrder = removeString(m.activeToolOrder, key)
	}
	return state
}

func (m model) newActiveToolKey(call domain.ToolCall) string {
	base := activeToolBaseKey(call)
	key := base
	for idx := 2; ; idx++ {
		if _, exists := m.activeTools[key]; !exists {
			return key
		}
		key = fmt.Sprintf("%s#%d", base, idx)
	}
}

func (m model) findActiveTool(call domain.ToolCall) (string, *toolCallState) {
	if len(m.activeTools) == 0 {
		return "", nil
	}
	if call.ID != "" {
		for key, state := range m.activeTools {
			if strings.HasPrefix(key, "id:"+call.ID) {
				return key, state
			}
		}
	}
	target, options := summarizeToolCall(call)
	for _, key := range m.activeToolOrder {
		state := m.activeTools[key]
		if sameToolCallState(state, call.Name, target, options) {
			return key, state
		}
	}
	for key, state := range m.activeTools {
		if sameToolCallState(state, call.Name, target, options) {
			return key, state
		}
	}
	return "", nil
}

func activeToolBaseKey(call domain.ToolCall) string {
	if call.ID != "" {
		return "id:" + call.ID
	}
	target, options := summarizeToolCall(call)
	return strings.Join(append([]string{"semantic", call.Name, target}, options...), "\x00")
}

func sameToolCallState(state *toolCallState, name string, target string, options []string) bool {
	if state == nil || state.name != name || state.target != target || len(state.options) != len(options) {
		return false
	}
	for idx := range options {
		if state.options[idx] != options[idx] {
			return false
		}
	}
	return true
}

func removeString(items []string, target string) []string {
	if len(items) == 0 {
		return items
	}
	out := items[:0]
	for _, item := range items {
		if item != target {
			out = append(out, item)
		}
	}
	return out
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
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
	if len(trimmed) > maxToolLogLines {
		trimmed = append(trimmed[:maxToolLogLines], "... (truncated)")
	}
	return strings.Join(trimmed, "\n")
}

func (m *model) cachedToolCard() string {
	key := fmt.Sprintf("%d:%s", m.width, m.activeToolCacheKey())
	if !m.toolCardCache.dirty && m.toolCardCache.key == key {
		return m.toolCardCache.content
	}
	m.toolCardCache.key = key
	m.toolCardCache.content = m.renderToolCard()
	m.toolCardCache.dirty = false
	return m.toolCardCache.content
}

func (m model) activeToolCacheKey() string {
	if len(m.activeTools) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(m.activeTools))
	for _, key := range m.activeToolOrder {
		if state := m.activeTools[key]; state != nil {
			parts = append(parts, key+":"+state.name+":"+state.status)
		}
	}
	for key, state := range m.activeTools {
		if !containsString(m.activeToolOrder, key) {
			parts = append(parts, key+":"+state.name+":"+state.status)
		}
	}
	return strings.Join(parts, "|")
}

func (m *model) cachedToolLogCard() string {
	key := fmt.Sprintf("%d:%d", m.width, len(m.toolLogs))
	if !m.toolLogCardCache.dirty && m.toolLogCardCache.key == key {
		return m.toolLogCardCache.content
	}
	m.toolLogCardCache.key = key
	m.toolLogCardCache.content = m.renderToolLogCard()
	m.toolLogCardCache.dirty = false
	return m.toolLogCardCache.content
}
