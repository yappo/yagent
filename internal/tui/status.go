package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"yagent/internal/domain"
)

type statusState struct {
	nodes             map[string]*agentStatusNode
	rootRunIDs        []string
	children          map[string][]string
	recent            []domain.ExecutionEvent
	failures          []domain.ExecutionEvent
	selectedFailure   int
	showFailureDetail bool
	counts            map[string]int
	filter            string
	foldCompleted     bool
	search            string
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

func handleStatusFailureKeys(m model, msg tea.KeyMsg) (model, bool) {
	if m.activePanel != sidePanelRunGraph || len(m.status.failures) == 0 {
		return m, false
	}
	key := msg.Key()
	switch {
	case key.Mod.Contains(tea.ModCtrl) && key.Code == tea.KeyUp:
		m.selectStatusFailure(1)
		return m, true
	case key.Mod.Contains(tea.ModCtrl) && key.Code == tea.KeyDown:
		m.selectStatusFailure(-1)
		return m, true
	case msg.String() == "esc" && m.status.showFailureDetail:
		m.status.showFailureDetail = false
		m.invalidatePanel(sidePanelRunGraph)
		m.mainPanelsCache.dirty = true
		return m, true
	case msg.String() == "enter" && !m.status.showFailureDetail && strings.TrimSpace(m.textarea.Value()) == "":
		m.status.showFailureDetail = true
		m.invalidatePanel(sidePanelRunGraph)
		m.mainPanelsCache.dirty = true
		return m, true
	}
	return m, false
}

func (m *model) selectStatusFailure(delta int) {
	if len(m.status.failures) == 0 {
		return
	}
	m.status.selectedFailure = wrapIndex(m.status.selectedFailure+delta, len(m.status.failures))
	m.status.showFailureDetail = true
	m.invalidatePanel(sidePanelRunGraph)
	m.mainPanelsCache.dirty = true
}

func (m *model) setStatusFilter(args []string) []string {
	value := strings.TrimSpace(strings.Join(args, " "))
	if value == "" {
		return []string{m.statusFilterLine()}
	}
	switch strings.ToLower(value) {
	case "clear", "off", "reset":
		m.status.filter = ""
		m.refreshStatusControls()
		return []string{"Agent Status filter を解除しました"}
	default:
		m.status.filter = value
		m.refreshStatusControls()
		return []string{"Agent Status filter: " + value}
	}
}

func (m *model) setStatusFold(args []string) []string {
	value := strings.ToLower(strings.TrimSpace(strings.Join(args, " ")))
	if value == "" {
		return []string{m.statusFoldLine()}
	}
	switch value {
	case "on", "true", "yes", "done", "completed":
		m.status.foldCompleted = true
	case "off", "false", "no", "clear", "reset":
		m.status.foldCompleted = false
	case "toggle":
		m.status.foldCompleted = !m.status.foldCompleted
	default:
		return []string{"usage: /status-fold on|off|toggle"}
	}
	m.refreshStatusControls()
	return []string{m.statusFoldLine()}
}

func (m *model) setStatusSearch(args []string) []string {
	value := strings.TrimSpace(strings.Join(args, " "))
	if value == "" {
		return []string{m.statusSearchLine()}
	}
	switch strings.ToLower(value) {
	case "clear", "off", "reset":
		m.status.search = ""
		m.refreshStatusControls()
		return []string{"Agent Status search を解除しました"}
	default:
		m.status.search = value
		m.refreshStatusControls()
		return []string{"Agent Status search: " + value}
	}
}

func (m *model) refreshStatusControls() {
	m.setActivePanel(sidePanelRunGraph)
	m.invalidatePanel(sidePanelRunGraph)
	m.mainPanelsCache.dirty = true
}

func (m model) statusFilterLine() string {
	if strings.TrimSpace(m.status.filter) == "" {
		return "Agent Status filter: off"
	}
	return "Agent Status filter: " + m.status.filter
}

func (m model) statusFoldLine() string {
	if m.status.foldCompleted {
		return "Agent Status fold: completed nodes hidden"
	}
	return "Agent Status fold: off"
}

func (m model) statusSearchLine() string {
	if strings.TrimSpace(m.status.search) == "" {
		return "Agent Status search: off"
	}
	return fmt.Sprintf("Agent Status search: %s (%d matches)", m.status.search, m.countStatusSearchMatches())
}

func (m *model) applyStatusEvent(event domain.ExecutionEvent) {
	rawEvent := cloneExecutionEvent(event)
	if strings.TrimSpace(event.Display) == "" {
		event.Display = summarizeStatusDetail(event.Detail)
	}
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
	m.linkStatusChild(node, event.ParentRunID)
	if node.AgentID == "" {
		node.AgentID = event.AgentID
	}
	if node.StartedAt.IsZero() {
		node.StartedAt = event.Timestamp
	}
	node.UpdatedAt = event.Timestamp
	node.Detail = statusEventDisplay(event)
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
	if isFailureStatusEvent(rawEvent) {
		m.status.failures = append([]domain.ExecutionEvent{rawEvent}, m.status.failures...)
		if len(m.status.failures) > 8 {
			m.status.failures = m.status.failures[:8]
		}
		m.status.selectedFailure = 0
		m.status.showFailureDetail = true
	}
	m.reorderStatusNodes(node)
	m.pruneStatusTree()
	m.rebuildStatusCounts()
}

func cloneExecutionEvent(event domain.ExecutionEvent) domain.ExecutionEvent {
	if event.Metrics != nil {
		metrics := make(map[string]any, len(event.Metrics))
		for key, value := range event.Metrics {
			metrics[key] = value
		}
		event.Metrics = metrics
	}
	return event
}

func statusEventDisplay(event domain.ExecutionEvent) string {
	if display := strings.TrimSpace(event.Display); display != "" {
		return display
	}
	return summarizeStatusDetail(event.Detail)
}

func isFailureStatusEvent(event domain.ExecutionEvent) bool {
	return event.Type == "agent_failed" || event.Type == "tool_failed" || event.Status == "failed"
}

func summarizeStatusDetail(detail string) string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return ""
	}

	if summary, ok := summarizeJSONDetail(detail); ok {
		return summary
	}

	lines := strings.Split(detail, "\n")
	trimmed := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		trimmed = append(trimmed, strings.Join(strings.Fields(line), " "))
	}
	if len(trimmed) == 0 {
		return ""
	}

	summary := trimmed[0]
	if len(trimmed) > 1 {
		summary += fmt.Sprintf(" (+%d lines)", len(trimmed)-1)
	}
	if len(summary) > 120 {
		summary = summary[:117] + "..."
	}
	return summary
}

func summarizeJSONDetail(detail string) (string, bool) {
	trimmed := strings.TrimSpace(detail)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return "", false
	}

	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return "", false
	}

	switch typed := decoded.(type) {
	case map[string]any:
		keys := preferredSummaryKeys(typed)
		parts := make([]string, 0, minInt(4, len(keys)))
		for _, key := range keys {
			value := summarizeJSONValue(typed[key])
			if value == "" {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s=%s", key, value))
			if len(parts) == 4 {
				break
			}
		}
		if len(parts) == 0 {
			return "json object", true
		}
		return strings.Join(parts, " "), true
	case []any:
		return fmt.Sprintf("json array (%d items)", len(typed)), true
	default:
		return summarizeJSONValue(decoded), true
	}
}

func preferredSummaryKeys(values map[string]any) []string {
	preferred := []string{"status", "message", "summary", "error", "path", "file", "command", "task", "result", "output"}
	keys := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, key := range preferred {
		if _, ok := values[key]; ok {
			keys = append(keys, key)
			seen[key] = struct{}{}
		}
	}
	rest := make([]string, 0, len(values))
	for key := range values {
		if _, ok := seen[key]; ok {
			continue
		}
		rest = append(rest, key)
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

func summarizeJSONValue(value any) string {
	switch typed := value.(type) {
	case string:
		return summarizeStatusDetail(typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		return fmt.Sprintf("%.0f", typed)
	case []any:
		return fmt.Sprintf("%d items", len(typed))
	case map[string]any:
		return fmt.Sprintf("%d keys", len(typed))
	default:
		return stringifyToolValue(value)
	}
}

func (m model) latestBlockedReason() string {
	for _, event := range m.status.recent {
		if event.Type == "agent_failed" || event.Type == "tool_failed" {
			return trimPathFromEnd(statusEventDisplay(event), maxInt(24, m.statusViewport.Width()-8))
		}
	}
	if m.lastRun != nil {
		for idx := len(m.lastRun.Verification) - 1; idx >= 0; idx-- {
			if strings.EqualFold(m.lastRun.Verification[idx].Status, "fail") {
				return trimPathFromEnd(summarizeStatusDetail(m.lastRun.Verification[idx].Summary), maxInt(24, m.statusViewport.Width()-8))
			}
		}
	}
	return ""
}

func (m model) renderRunGraphPanel() string {
	if len(m.status.nodes) == 0 {
		lines := m.renderPanelSummary()
		lines = append(lines, "", "まだサブエージェントは動いていません。")
		return strings.Join(lines, "\n")
	}

	lines := m.renderPanelSummary()
	lines = append(lines, "", fmt.Sprintf("running %d  done %d  failed %d", m.countStatuses("running", "working", "thinking"), m.countStatuses("done"), m.countStatuses("failed")))
	if controls := m.statusControlSummary(); controls != "" {
		lines = append(lines, m.styles.panelMeta.Render(controls))
	}
	lines = append(lines, "")

	renderedTree := false
	for _, runID := range m.status.rootRunIDs {
		treeLines := m.renderStatusTree(runID, "", true)
		if len(treeLines) == 0 {
			continue
		}
		renderedTree = true
		lines = append(lines, treeLines...)
	}
	if !renderedTree {
		lines = append(lines, "(no matching agent status)")
	}

	if detail := m.renderFailureDetailPanel(); detail != "" {
		lines = append(lines, "", detail)
	}

	if search := m.renderStatusSearchPanel(); len(search) > 0 {
		lines = append(lines, "")
		lines = append(lines, search...)
	}

	if len(m.status.recent) > 0 {
		lines = append(lines, "", "Recent")
		recentEvents := m.filteredRecentStatusEvents()
		if len(recentEvents) == 0 {
			lines = append(lines, "(no matching recent events)")
		}
		for _, event := range recentEvents {
			line := fmt.Sprintf("%s %s %s", event.Timestamp.Format("15:04:05"), shortType(event.Type), event.AgentID)
			if display := statusEventDisplay(event); display != "" {
				line += "  " + trimPathFromEnd(display, maxInt(20, m.statusViewport.Width()-12))
			}
			lines = append(lines, line)
		}
	}

	return strings.Join(lines, "\n")
}

func (m model) renderFailureDetailPanel() string {
	if !m.status.showFailureDetail || len(m.status.failures) == 0 {
		return ""
	}
	event, ok := m.selectedFailureEvent()
	if !ok {
		return ""
	}
	width := maxInt(24, m.statusViewport.Width()-6)
	lines := []string{
		fmt.Sprintf("Failure detail  %d/%d  Ctrl+Up/Down select  Esc close", m.status.selectedFailure+1, len(m.status.failures)),
		fmt.Sprintf("%s  %s  agent=%s  run=%s", event.Timestamp.Format("15:04:05"), fallbackString(event.Type, "-"), fallbackString(event.AgentID, "-"), fallbackString(event.RunID, "-")),
	}
	if event.ParentRunID != "" {
		lines = append(lines, "parent="+event.ParentRunID)
	}
	if event.Phase != "" || event.Attempt > 0 || event.Status != "" {
		lines = append(lines, fmt.Sprintf("phase=%s  attempt=%d  status=%s", fallbackString(string(event.Phase), "-"), event.Attempt, fallbackString(event.Status, "-")))
	}
	if event.ArtifactRef != "" {
		lines = append(lines, "artifact="+event.ArtifactRef)
	}
	if event.ContextCount > 0 {
		lines = append(lines, fmt.Sprintf("ctx=%d", event.ContextCount))
	}
	if strings.TrimSpace(event.Detail) != "" {
		lines = append(lines, "detail:")
		lines = append(lines, indentWrappedLines(event.Detail, width, "  ")...)
	}
	if len(event.Metrics) > 0 {
		lines = append(lines, "metrics:")
		for _, key := range sortedMetricKeys(event.Metrics) {
			lines = append(lines, indentWrappedLines(fmt.Sprintf("%s=%s", key, stringifyFailureMetric(event.Metrics[key])), width, "  ")...)
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) selectedFailureEvent() (domain.ExecutionEvent, bool) {
	if len(m.status.failures) == 0 {
		return domain.ExecutionEvent{}, false
	}
	idx := m.status.selectedFailure
	if idx < 0 {
		idx = 0
	}
	if idx >= len(m.status.failures) {
		idx = len(m.status.failures) - 1
	}
	return m.status.failures[idx], true
}

func indentWrappedLines(text string, width int, indent string) []string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		wrapped := wrapContent(strings.TrimSpace(line), maxInt(8, width-len(indent)))
		if wrapped == "" {
			out = append(out, indent)
			continue
		}
		for _, part := range strings.Split(wrapped, "\n") {
			out = append(out, indent+part)
		}
	}
	return out
}

func sortedMetricKeys(metrics map[string]any) []string {
	keys := make([]string, 0, len(metrics))
	for key := range metrics {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func stringifyFailureMetric(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", typed)
	case int64:
		return fmt.Sprintf("%d", typed)
	case float64:
		if typed == float64(int64(typed)) {
			return fmt.Sprintf("%.0f", typed)
		}
		return fmt.Sprintf("%.3f", typed)
	default:
		data, err := json.Marshal(typed)
		if err == nil {
			return string(data)
		}
		return fmt.Sprintf("%v", typed)
	}
}

func (m model) renderStatusTree(runID, prefix string, last bool) []string {
	node, ok := m.status.nodes[runID]
	if !ok {
		return nil
	}
	if !m.statusNodeVisible(runID) {
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

	line := fmt.Sprintf("%s%s  %s  %s", prefix+branch, titleCase(node.AgentID), statusLabel(node.Status), formatNodeMetricsAt(node, m.now))
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

	children := m.visibleStatusChildren(runID)
	for idx, childID := range children {
		lines = append(lines, m.renderStatusTree(childID, nextPrefix, idx == len(children)-1)...)
	}
	return lines
}

func (m model) visibleStatusChildren(runID string) []string {
	children := m.status.children[runID]
	if len(children) == 0 {
		return nil
	}
	visible := make([]string, 0, len(children))
	for _, childID := range children {
		if m.statusNodeVisible(childID) {
			visible = append(visible, childID)
		}
	}
	return visible
}

func (m model) statusNodeVisible(runID string) bool {
	node, ok := m.status.nodes[runID]
	if !ok {
		return false
	}
	query := normalizeStatusQuery(m.status.filter)
	nodeMatches := query != "" && statusNodeMatches(node, query)
	childVisible := false
	for _, childID := range m.status.children[runID] {
		if m.statusNodeVisible(childID) {
			childVisible = true
			break
		}
	}
	if query != "" && !nodeMatches && !childVisible {
		return false
	}
	if m.status.foldCompleted && node.Status == "done" && !nodeMatches && !childVisible {
		return false
	}
	return true
}

func (m model) countStatuses(statuses ...string) int {
	count := 0
	for _, status := range statuses {
		count += m.status.counts[status]
	}
	return count
}

func (m model) statusControlSummary() string {
	parts := []string{}
	if filter := strings.TrimSpace(m.status.filter); filter != "" {
		parts = append(parts, "filter="+filter)
	}
	if m.status.foldCompleted {
		parts = append(parts, "fold=completed")
	}
	if search := strings.TrimSpace(m.status.search); search != "" {
		parts = append(parts, fmt.Sprintf("search=%s matches=%d", search, m.countStatusSearchMatches()))
	}
	return strings.Join(parts, "  ")
}

func (m model) filteredRecentStatusEvents() []domain.ExecutionEvent {
	query := normalizeStatusQuery(m.status.search)
	if query == "" {
		return m.status.recent
	}
	events := make([]domain.ExecutionEvent, 0, len(m.status.recent))
	for _, event := range m.status.recent {
		if statusEventMatches(event, query) {
			events = append(events, event)
		}
	}
	return events
}

func (m model) renderStatusSearchPanel() []string {
	query := normalizeStatusQuery(m.status.search)
	if query == "" {
		return nil
	}
	lines := []string{"Search"}
	matches := m.statusSearchMatches(query, 8)
	if len(matches) == 0 {
		return append(lines, "(no matches)")
	}
	return append(lines, matches...)
}

func (m model) statusSearchMatches(query string, limit int) []string {
	if query == "" || limit <= 0 {
		return nil
	}
	matches := []string{}
	m.appendStatusNodeSearchMatches(&matches, query, limit)
	if len(matches) >= limit {
		return matches[:limit]
	}
	for _, event := range m.status.recent {
		if !statusEventMatches(event, query) {
			continue
		}
		matches = append(matches, "event "+m.formatStatusEventSearchLine(event))
		if len(matches) >= limit {
			return matches
		}
	}
	for idx, event := range m.status.failures {
		if !statusEventMatches(event, query) {
			continue
		}
		matches = append(matches, fmt.Sprintf("failure %d %s", idx+1, m.formatStatusEventSearchLine(event)))
		if len(matches) >= limit {
			return matches
		}
	}
	return matches
}

func (m model) appendStatusNodeSearchMatches(matches *[]string, query string, limit int) {
	seen := map[string]bool{}
	for _, runID := range m.status.rootRunIDs {
		m.appendStatusNodeSearchMatchesForRun(matches, query, limit, seen, runID)
		if len(*matches) >= limit {
			return
		}
	}
	for runID := range m.status.nodes {
		if seen[runID] {
			continue
		}
		m.appendStatusNodeSearchMatchesForRun(matches, query, limit, seen, runID)
		if len(*matches) >= limit {
			return
		}
	}
}

func (m model) appendStatusNodeSearchMatchesForRun(matches *[]string, query string, limit int, seen map[string]bool, runID string) {
	if len(*matches) >= limit || seen[runID] {
		return
	}
	seen[runID] = true
	node, ok := m.status.nodes[runID]
	if !ok {
		return
	}
	if statusNodeMatches(node, query) {
		*matches = append(*matches, "agent "+m.formatStatusNodeSearchLine(node))
		if len(*matches) >= limit {
			return
		}
	}
	for _, childID := range m.status.children[runID] {
		m.appendStatusNodeSearchMatchesForRun(matches, query, limit, seen, childID)
		if len(*matches) >= limit {
			return
		}
	}
}

func (m model) countStatusSearchMatches() int {
	query := normalizeStatusQuery(m.status.search)
	if query == "" {
		return 0
	}
	return len(m.statusSearchMatches(query, len(m.status.nodes)+len(m.status.recent)+len(m.status.failures)))
}

func (m model) formatStatusNodeSearchLine(node *agentStatusNode) string {
	line := fmt.Sprintf("%s %s run=%s", titleCase(node.AgentID), statusLabel(node.Status), node.RunID)
	if node.Phase != "" {
		line += " phase=" + string(node.Phase)
	}
	if node.Detail != "" {
		line += " " + trimPathFromEnd(strings.TrimSpace(node.Detail), maxInt(16, m.statusViewport.Width()-12))
	}
	return line
}

func (m model) formatStatusEventSearchLine(event domain.ExecutionEvent) string {
	line := fmt.Sprintf("%s %s agent=%s run=%s", event.Timestamp.Format("15:04:05"), shortType(event.Type), fallbackString(event.AgentID, "-"), fallbackString(event.RunID, "-"))
	if display := statusEventDisplay(event); display != "" {
		line += " " + trimPathFromEnd(display, maxInt(16, m.statusViewport.Width()-14))
	}
	return line
}

func normalizeStatusQuery(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func statusNodeMatches(node *agentStatusNode, query string) bool {
	if node == nil || query == "" {
		return false
	}
	return strings.Contains(strings.ToLower(strings.Join([]string{
		node.RunID,
		node.ParentRunID,
		node.AgentID,
		node.Status,
		string(node.Phase),
		node.Detail,
		node.ArtifactRef,
		fmt.Sprintf("try %d", node.Attempt),
	}, " ")), query)
}

func statusEventMatches(event domain.ExecutionEvent, query string) bool {
	if query == "" {
		return false
	}
	fields := []string{
		event.RunID,
		event.ParentRunID,
		event.AgentID,
		event.Type,
		string(event.Phase),
		event.Status,
		event.Display,
		event.Detail,
		event.ArtifactRef,
		fmt.Sprintf("attempt %d", event.Attempt),
	}
	for key, value := range event.Metrics {
		fields = append(fields, key, fmt.Sprintf("%v", value))
	}
	return strings.Contains(strings.ToLower(strings.Join(fields, " ")), query)
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

func (m model) currentRunMetrics() string {
	node := m.currentRootNode()
	if node == nil {
		return ""
	}
	return formatMetricsAt(node.StartedAt, node.UpdatedAt, node.Status, node.ContextCount, m.now)
}

func (m model) hasActiveStatusNodes() bool {
	for _, node := range m.status.nodes {
		if isActiveStatus(node.Status) {
			return true
		}
	}
	return false
}

func (m model) currentRootNode() *agentStatusNode {
	if len(m.status.rootRunIDs) == 0 {
		return nil
	}
	runID := m.status.rootRunIDs[len(m.status.rootRunIDs)-1]
	return m.status.nodes[runID]
}

func formatNodeMetricsAt(node *agentStatusNode, now time.Time) string {
	return formatMetricsAt(node.StartedAt, node.UpdatedAt, node.Status, node.ContextCount, now)
}

func formatMetricsAt(startedAt, updatedAt time.Time, status string, contextCount int, now time.Time) string {
	parts := []string{}
	if !startedAt.IsZero() {
		end := updatedAt
		if status != "done" && status != "failed" {
			end = now
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

func isActiveStatus(status string) bool {
	switch status {
	case "running", "working", "thinking":
		return true
	default:
		return false
	}
}

func isTerminalStatus(status string) bool {
	switch status {
	case "done", "failed":
		return true
	default:
		return false
	}
}

func (m *model) linkStatusChild(node *agentStatusNode, parentRunID string) {
	if parentRunID == "" {
		return
	}
	if node.ParentRunID != "" {
		if node.ParentRunID != parentRunID {
			children := m.status.children[node.ParentRunID]
			filtered := children[:0]
			for _, childID := range children {
				if childID != node.RunID {
					filtered = append(filtered, childID)
				}
			}
			m.status.children[node.ParentRunID] = filtered
		}
	}
	node.ParentRunID = parentRunID
	children := appendUnique(m.status.children[parentRunID], node.RunID)
	m.status.children[parentRunID] = children
}

func (m *model) rebuildStatusCounts() {
	counts := map[string]int{}
	for _, node := range m.status.nodes {
		counts[node.Status]++
	}
	m.status.counts = counts
}

func (m *model) pruneStatusTree() {
	m.status.rootRunIDs = m.pruneRunList(m.status.rootRunIDs, maxTerminalRootRuns)

	parentIDs := make([]string, 0, len(m.status.children))
	for parentID := range m.status.children {
		parentIDs = append(parentIDs, parentID)
	}
	for _, parentID := range parentIDs {
		children := m.status.children[parentID]
		if len(children) == 0 {
			continue
		}
		m.status.children[parentID] = m.pruneRunList(children, maxTerminalChildren)
	}
}

func (m *model) pruneRunList(runIDs []string, keepTerminal int) []string {
	if len(runIDs) == 0 {
		return nil
	}

	kept := make([]string, 0, len(runIDs))
	terminalCount := 0
	for _, runID := range runIDs {
		if !m.isTerminalSubtree(runID) {
			kept = append(kept, runID)
			continue
		}
		if terminalCount < keepTerminal {
			kept = append(kept, runID)
			terminalCount++
			continue
		}
		m.dropStatusSubtree(runID)
	}
	return kept
}

func (m *model) isTerminalSubtree(runID string) bool {
	node, ok := m.status.nodes[runID]
	if !ok || !isTerminalStatus(node.Status) {
		return false
	}
	for _, childID := range m.status.children[runID] {
		if !m.isTerminalSubtree(childID) {
			return false
		}
	}
	return true
}

func (m *model) dropStatusSubtree(runID string) {
	for _, childID := range m.status.children[runID] {
		m.dropStatusSubtree(childID)
	}
	delete(m.status.children, runID)
	delete(m.status.nodes, runID)
}

func (m *model) reorderStatusNodes(node *agentStatusNode) {
	if node == nil {
		return
	}
	if node.ParentRunID == "" {
		m.reorderRunIDs(m.status.rootRunIDs)
		return
	}
	m.reorderRunIDs(m.status.children[node.ParentRunID])
}

func (m *model) reorderRunIDs(ids []string) {
	sort.SliceStable(ids, func(i, j int) bool {
		left := m.status.nodes[ids[i]]
		right := m.status.nodes[ids[j]]
		if left == nil || right == nil {
			return ids[i] < ids[j]
		}
		leftRank := statusOrderRank(left.Status)
		rightRank := statusOrderRank(right.Status)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if !left.UpdatedAt.Equal(right.UpdatedAt) {
			return left.UpdatedAt.After(right.UpdatedAt)
		}
		if !left.StartedAt.Equal(right.StartedAt) {
			return left.StartedAt.After(right.StartedAt)
		}
		return ids[i] < ids[j]
	})
}

func statusOrderRank(status string) int {
	switch status {
	case "running", "working", "thinking":
		return 0
	case "failed":
		return 1
	case "done":
		return 2
	default:
		return 3
	}
}
