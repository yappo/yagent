package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"yagent/internal/domain"
)

type permissionState struct {
	request       domain.PermissionRequest
	response      chan domain.PermissionDecision
	selectedIndex int
	patternMode   bool
	patternInput  string
	batch         []permissionState
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

func approvalKey(request domain.PermissionRequest) string {
	return strings.Join([]string{
		request.ToolName,
		request.Action,
		request.ResourceKind,
		request.Scope,
		request.Risk,
	}, "\x00")
}

func handlePermissionKeys(m model, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.resolvePermission(domain.PermissionDeny)
		return m, tea.Quit
	}

	if m.permission.patternMode {
		return handlePatternPermissionKeys(m, msg)
	}

	switch msg.String() {
	case "ctrl+a":
		m.resolveAllPermissions(domain.PermissionAllowOnce)
		return m, nil
	case "ctrl+d":
		m.resolveAllPermissions(domain.PermissionDeny)
		return m, nil
	}

	options := permissionOptionsForRequest(m.permission.request)
	switch msg.String() {
	case "left", "shift+tab":
		m.permission.selectedIndex = wrapIndex(m.permission.selectedIndex-1, len(options))
		m.permissionCache.dirty = true
		m.syncLayout()
		return m, nil
	case "right", "tab":
		m.permission.selectedIndex = wrapIndex(m.permission.selectedIndex+1, len(options))
		m.permissionCache.dirty = true
		m.syncLayout()
		return m, nil
	case "enter":
		option := options[m.permission.selectedIndex]
		if option.requiresPattern {
			m.permission.patternMode = true
			m.permission.patternInput = ""
			m.permissionCache.dirty = true
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
				m.permissionCache.dirty = true
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
		m.permissionCache.dirty = true
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
		m.permissionCache.dirty = true
		m.resolvePermissionWithLabel(domain.PermissionAllowSession, "パターン許可 ("+patternValue+")")
		return m, nil
	case "backspace", "ctrl+h":
		if len(m.permission.patternInput) > 0 {
			runes := []rune(m.permission.patternInput)
			m.permission.patternInput = string(runes[:len(runes)-1])
			m.permissionCache.dirty = true
			m.syncLayout()
		}
		return m, nil
	}

	if typed := msg.String(); len([]rune(typed)) == 1 {
		m.permission.patternInput += typed
		m.permissionCache.dirty = true
		m.syncLayout()
	}
	return m, nil
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

	state := *m.permission
	if label == "" && state.batchSize() > 1 {
		label = permissionGroupedDecisionLabel(decision, state.batchSize())
	}
	m.resolvePermissionState(state, decision, fallbackString(label, permissionDecisionLabel(decision)), true)
	m.permission = nil
	m.advancePermissionQueue()
	m.permissionCache.dirty = true
	m.invalidatePanelSummary()
	m.syncLayout()
}

func (m *model) resolveAllPermissions(decision domain.PermissionDecision) {
	if m.permission == nil && len(m.permissionQueue) == 0 {
		return
	}
	states := make([]permissionState, 0, len(m.permissionQueue)+1)
	if m.permission != nil {
		states = append(states, *m.permission)
	}
	states = append(states, m.permissionQueue...)
	pending := flattenPermissionStates(states)
	label := permissionBatchDecisionLabel(decision, len(pending))
	for _, state := range pending {
		m.appendPermissionResolution(state.request, label)
		respondPermissionState(state, decision)
	}
	m.permission = nil
	m.permissionQueue = nil
	m.permissionCache.dirty = true
	m.invalidatePanelSummary()
	m.syncLayout()
}

func (m *model) resolvePermissionState(state permissionState, decision domain.PermissionDecision, label string, summarizeGroup bool) {
	if summarizeGroup {
		m.appendPermissionStateResolution(state, label)
	} else {
		m.appendPermissionResolution(state.request, label)
	}
	for _, item := range state.flatten() {
		respondPermissionState(item, decision)
	}
}

func respondPermissionState(state permissionState, decision domain.PermissionDecision) {
	state.response <- decision
	close(state.response)
}

func (m *model) appendPermissionResolution(request domain.PermissionRequest, label string) {
	m.appendChatBlock(fmt.Sprintf("%s [%s] %s を%s",
		permissionRequesterLabel(request),
		permissionRequesterType(request),
		request.Operation,
		label+" ("+request.Resource+")",
	))
}

func (m *model) appendPermissionStateResolution(state permissionState, label string) {
	lines := []string{
		fmt.Sprintf("%s [%s] %s を%s",
			permissionRequesterLabel(state.request),
			permissionRequesterType(state.request),
			state.request.Operation,
			label+" ("+state.request.Resource+")",
		),
	}
	if state.batchSize() > 1 {
		if resources := permissionStateResourceSummary(state, 4); resources != "" {
			lines = append(lines, fmt.Sprintf("同種 request %d件: %s", state.batchSize(), resources))
		} else {
			lines = append(lines, fmt.Sprintf("同種 request %d件", state.batchSize()))
		}
		if requesters := permissionStateRequesterSummary(state, 4); requesters != "" {
			lines = append(lines, "requesters: "+requesters)
		}
	}
	m.appendChatBlock(lines...)
}

func (m *model) advancePermissionQueue() {
	if len(m.permissionQueue) > 0 {
		next := m.permissionQueue[0]
		m.permissionQueue = m.permissionQueue[1:]
		if m.resolveQueuedPermissionIfApproved(next) {
			m.advancePermissionQueue()
			return
		}
		m.permission = &next
		return
	}
	m.permission = nil
}

func (m *model) resolveQueuedPermissionIfApproved(state permissionState) bool {
	if !m.sessionApprovals[approvalKey(state.request)] && !m.hasPatternApproval(state.request) {
		return false
	}
	label := "自動許可"
	if state.batchSize() > 1 {
		label = fmt.Sprintf("自動許可 (%d件)", state.batchSize())
	}
	m.resolvePermissionState(state, domain.PermissionAllowSession, label, true)
	return true
}

func (m *model) enqueuePermissionState(state permissionState) {
	if m.permission == nil {
		m.permission = &state
		return
	}
	if appendPermissionBatch(m.permission, state) {
		m.permissionCache.dirty = true
		return
	}
	for index := range m.permissionQueue {
		if appendPermissionBatch(&m.permissionQueue[index], state) {
			m.permissionCache.dirty = true
			return
		}
	}
	m.permissionQueue = append(m.permissionQueue, state)
	m.permissionCache.dirty = true
}

func appendPermissionBatch(target *permissionState, state permissionState) bool {
	if target == nil || !permissionRequestsCanBatch(target.request, state.request) {
		return false
	}
	state.batch = nil
	target.batch = append(target.batch, state)
	return true
}

func permissionRequestsCanBatch(left, right domain.PermissionRequest) bool {
	if approvalKey(left) != approvalKey(right) {
		return false
	}
	if left.Operation != right.Operation {
		return false
	}
	if strings.TrimSpace(left.Scope) == "" && left.Resource != right.Resource {
		return false
	}
	if left.PreviewKind != right.PreviewKind || left.Preview != right.Preview {
		return false
	}
	if left.ChangeFiles != right.ChangeFiles || left.Additions != right.Additions || left.Deletions != right.Deletions {
		return false
	}
	return equalStringSlices(left.SideEffects, right.SideEffects)
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (state permissionState) batchSize() int {
	return 1 + len(state.batch)
}

func (state permissionState) flatten() []permissionState {
	items := make([]permissionState, 0, state.batchSize())
	children := append([]permissionState(nil), state.batch...)
	state.batch = nil
	items = append(items, state)
	for _, child := range children {
		child.batch = nil
		items = append(items, child)
	}
	return items
}

func flattenPermissionStates(states []permissionState) []permissionState {
	items := []permissionState{}
	for _, state := range states {
		items = append(items, state.flatten()...)
	}
	return items
}

func permissionStateResourceSummary(state permissionState, limit int) string {
	return summarizePermissionValues(state, limit, func(request domain.PermissionRequest) string {
		return request.Resource
	})
}

func permissionStateRequesterSummary(state permissionState, limit int) string {
	return summarizePermissionValues(state, limit, permissionRequesterDisplay)
}

func summarizePermissionValues(state permissionState, limit int, value func(domain.PermissionRequest) string) string {
	values := []string{}
	seen := map[string]bool{}
	for _, item := range state.flatten() {
		text := strings.TrimSpace(value(item.request))
		if text == "" || seen[text] {
			continue
		}
		seen[text] = true
		values = append(values, text)
	}
	if len(values) == 0 {
		return ""
	}
	sort.Strings(values)
	if limit > 0 && len(values) > limit {
		omitted := len(values) - limit
		values = values[:limit]
		values = append(values, fmt.Sprintf("+%d more", omitted))
	}
	return strings.Join(values, ", ")
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

func permissionBatchDecisionLabel(decision domain.PermissionDecision, count int) string {
	switch decision {
	case domain.PermissionAllowOnce:
		return fmt.Sprintf("一括許可 (%d件)", count)
	case domain.PermissionAllowSession:
		return fmt.Sprintf("一括セッション許可 (%d件)", count)
	case domain.PermissionDeny:
		return fmt.Sprintf("一括拒否 (%d件)", count)
	default:
		return permissionDecisionLabel(decision)
	}
}

func permissionGroupedDecisionLabel(decision domain.PermissionDecision, count int) string {
	switch decision {
	case domain.PermissionAllowOnce:
		return fmt.Sprintf("同種許可 (%d件)", count)
	case domain.PermissionAllowSession:
		return fmt.Sprintf("同種セッション許可 (%d件)", count)
	case domain.PermissionDeny:
		return fmt.Sprintf("同種拒否 (%d件)", count)
	default:
		return permissionDecisionLabel(decision)
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
	options := permissionOptionsForRequest(m.permission.request)
	lines := []string{
		m.styles.permissionTitle.Render(m.permission.request.Operation),
		m.styles.permissionPath.Render(resource),
		m.styles.permissionHelp.Render("requester: " + permissionRequesterDisplay(m.permission.request)),
	}
	if m.permission.batchSize() > 1 {
		lines = append(lines, m.styles.permissionHelp.Render(fmt.Sprintf("same-kind requests: %d", m.permission.batchSize())))
		if resources := permissionStateResourceSummary(*m.permission, 4); resources != "" {
			lines = append(lines, m.styles.permissionHelp.Render("resources: "+resources))
		}
		if requesters := permissionStateRequesterSummary(*m.permission, 4); requesters != "" {
			lines = append(lines, m.styles.permissionHelp.Render("requesters: "+requesters))
		}
	}
	if summary := strings.TrimSpace(m.permission.request.Summary); summary != "" {
		lines = append(lines, m.styles.permissionHelp.Render(summary))
	}
	if changes := permissionChangeSummary(m.permission.request); changes != "" {
		lines = append(lines, m.styles.permissionHelp.Render("changes: "+changes))
	}
	if preview := strings.TrimSpace(m.permission.request.Preview); preview != "" {
		label := strings.TrimSpace(m.permission.request.PreviewKind)
		if label == "" {
			label = "preview"
		}
		lines = append(lines, m.styles.permissionHelp.Render(label+":"))
		for _, line := range strings.Split(preview, "\n") {
			lines = append(lines, m.styles.permissionHelp.Render(wrapContent(line, cardWidth-4)))
		}
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
	if queueCount := m.queuedPermissionCount(); queueCount > 0 {
		lines = append(lines, m.styles.permissionHelp.Render(fmt.Sprintf("queue: %d waiting", queueCount)))
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
			m.styles.permissionHelp.Render("←/→ または Tab で選択 • Enter で確定 • Esc で拒否 • Ctrl+A で全て許可 • Ctrl+D で全て拒否"),
		)
	}
	card := strings.Join(lines, "\n")
	return m.styles.permissionCard.Width(cardWidth).Render(card)
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

func permissionChangeSummary(request domain.PermissionRequest) string {
	parts := make([]string, 0, 3)
	if request.ChangeFiles > 0 {
		parts = append(parts, fmt.Sprintf("files=%d", request.ChangeFiles))
	}
	if request.Additions > 0 || request.Deletions > 0 {
		parts = append(parts, fmt.Sprintf("+%d", request.Additions), fmt.Sprintf("-%d", request.Deletions))
	}
	return strings.Join(parts, " ")
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

func formatPermissionBatchSuffix(state permissionState) string {
	if state.batchSize() <= 1 {
		return ""
	}
	parts := []string{fmt.Sprintf("same_kind=%d", state.batchSize())}
	if resources := permissionStateResourceSummary(state, 3); resources != "" {
		parts = append(parts, "resources="+resources)
	}
	return " " + strings.Join(parts, " ")
}

func formatApprovalKeyForDisplay(key string) string {
	parts := strings.Split(key, "\x00")
	for len(parts) < 5 {
		parts = append(parts, "")
	}
	return fmt.Sprintf(
		"tool=%s action=%s kind=%s scope=%s risk=%s",
		fallbackString(parts[0], "-"),
		fallbackString(parts[1], "-"),
		fallbackString(parts[2], "-"),
		fallbackString(parts[3], "-"),
		fallbackString(parts[4], "-"),
	)
}

func formatPermissionScopeForDisplay(request domain.PermissionRequest) string {
	parts := []string{
		request.Operation + " (" + fallbackString(request.Resource, "-") + ")",
		"tool=" + fallbackString(request.ToolName, "-"),
		"action=" + fallbackString(request.Action, "-"),
		"scope=" + fallbackString(request.Scope, "-"),
		"risk=" + fallbackString(request.Risk, "-"),
		"requester=" + permissionRequesterDisplay(request),
	}
	if len(request.SideEffects) > 0 {
		parts = append(parts, "effects="+strings.Join(request.SideEffects, ","))
	}
	return strings.Join(parts, " ")
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
