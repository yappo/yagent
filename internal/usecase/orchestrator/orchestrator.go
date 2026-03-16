package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"yagent/internal/domain"
)

type Config struct {
	MaxParallelAgents int
	MaxHandoffDepth   int
	DefaultTimeout    time.Duration
	TraceSink         domain.TraceSink
	Approver          domain.Approver
}

type Service struct {
	model      domain.ModelClient
	tools      domain.ToolExecutor
	catalog    domain.AgentCatalog
	config     Config
	runCounter atomic.Uint64
	mu         sync.Mutex
	listeners  map[chan domain.ExecutionEvent]struct{}
}

func New(model domain.ModelClient, tools domain.ToolExecutor, catalog domain.AgentCatalog, config Config) *Service {
	if config.MaxParallelAgents < 1 {
		config.MaxParallelAgents = 1
	}
	if config.MaxHandoffDepth < 1 {
		config.MaxHandoffDepth = 1
	}
	if config.DefaultTimeout <= 0 {
		config.DefaultTimeout = 120 * time.Second
	}
	return &Service{
		model:     model,
		tools:     tools,
		catalog:   catalog,
		config:    config,
		listeners: map[chan domain.ExecutionEvent]struct{}{},
	}
}

func (s *Service) SubscribeEvents() (<-chan domain.ExecutionEvent, func()) {
	ch := make(chan domain.ExecutionEvent, 64)
	s.mu.Lock()
	s.listeners[ch] = struct{}{}
	s.mu.Unlock()

	cancel := func() {
		s.mu.Lock()
		if _, ok := s.listeners[ch]; ok {
			delete(s.listeners, ch)
			close(ch)
		}
		s.mu.Unlock()
	}

	return ch, cancel
}

func (s *Service) RunTurn(ctx context.Context, request domain.TurnRequest) (domain.TurnResult, error) {
	manager, ok := s.catalog.Resolve("manager")
	if !ok {
		return domain.TurnResult{}, fmt.Errorf("manager agent が見つかりません")
	}
	manager = s.withManagerDelegationBias(manager, request.Messages)

	result, err := s.runAgent(ctx, domain.AgentInvocation{
		RunID:    s.nextRunID("run"),
		Agent:    s.resolveModel(manager, request.Model),
		Messages: cloneMessages(request.Messages),
		Model:    request.Model,
		Stream:   request.Stream,
		Context:  buildContext(request.Messages, "", s.tools.Definitions(manager)),
	}, 0)
	if err != nil {
		return domain.TurnResult{}, err
	}

	return domain.TurnResult{Message: result.Message, Events: result.Events}, nil
}

func (s *Service) withManagerDelegationBias(manager domain.AgentSpec, messages []domain.Message) domain.AgentSpec {
	if !shouldBiasManagerTowardDelegation(messages) {
		return manager
	}
	extra := "You must actively use subagents for this request. Start by delegating planning or research to planner and/or researcher before doing broad repository inspection yourself. Manager should coordinate, merge findings, and produce the final answer rather than personally traversing many directories or files."
	if strings.Contains(manager.Instruction, extra) {
		return manager
	}
	if manager.Instruction == "" {
		manager.Instruction = extra
		return manager
	}
	manager.Instruction += "\n\n" + extra
	return manager
}

func (s *Service) runAgent(ctx context.Context, invocation domain.AgentInvocation, depth int) (domain.AgentResult, error) {
	if depth > s.config.MaxHandoffDepth {
		err := fmt.Errorf("最大 handoff 深度 (%d) に達しました", s.config.MaxHandoffDepth)
		failed := s.newEvent(
			invocation.RunID,
			invocation.ParentRunID,
			invocation.Agent.ID,
			"agent_failed",
			err.Error(),
			countContextItems(invocation.Messages, invocation.Context),
		)
		return domain.AgentResult{Status: "failed", Events: []domain.ExecutionEvent{failed}}, err
	}

	events := []domain.ExecutionEvent{s.newEvent(
		invocation.RunID,
		invocation.ParentRunID,
		invocation.Agent.ID,
		"agent_started",
		invocation.Context.TaskBrief,
		countContextItems(invocation.Messages, invocation.Context),
	)}

	messages := cloneMessages(invocation.Messages)
	maxTurns := invocation.Agent.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 200
	}

	for {
		for turn := 0; turn < maxTurns; turn++ {
			tools := append(s.tools.Definitions(invocation.Agent), s.agentToolDefinitions(invocation.Agent)...)
			llmCtx, cancel := context.WithTimeout(ctx, s.timeoutFor(invocation.Agent))
			response, err := s.model.Generate(llmCtx, domain.ModelRequest{
				Agent:        invocation.Agent,
				Instructions: invocation.Agent.Instruction,
				Messages:     messages,
				Model:        modelName(invocation),
				Stream:       invocation.Stream,
				Tools:        tools,
			})
			cancel()
			if err != nil {
				events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "agent_failed", err.Error(), countContextItems(messages, invocation.Context)))
				return domain.AgentResult{}, err
			}
			events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "llm_called", response.FinishReason, countContextItems(messages, invocation.Context)))

			if len(response.Message.ToolCalls) == 0 {
				response.Message.AgentID = invocation.Agent.ID
				events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "agent_completed", response.Message.Content, countContextItems(messages, invocation.Context)))
				return domain.AgentResult{
					Status:    "completed",
					Message:   response.Message,
					Summary:   response.Message.Content,
					Artifacts: map[string]any{},
					Events:    events,
				}, nil
			}

			assistantMessage := response.Message
			assistantMessage.AgentID = invocation.Agent.ID
			for idx := range assistantMessage.ToolCalls {
				assistantMessage.ToolCalls[idx].RequestedByAgentID = invocation.Agent.ID
				if assistantMessage.ToolCalls[idx].Purpose == "" {
					assistantMessage.ToolCalls[idx].Purpose = defaultPurpose(assistantMessage.ToolCalls[idx])
				}
			}
			messages = append(messages, assistantMessage)

			results, direct, childEvents, err := s.executeCalls(ctx, invocation, assistantMessage.ToolCalls, depth)
			events = append(events, childEvents...)
			if err != nil {
				events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "agent_failed", err.Error(), countContextItems(messages, invocation.Context)))
				return domain.AgentResult{}, err
			}
			if direct != nil {
				direct.Events = append(events, direct.Events...)
				return *direct, nil
			}
			messages = append(messages, results...)
		}

		if !s.approveContinue(ctx, invocation, maxTurns) {
			err := fmt.Errorf("agent %q が最大ターン数 (%d) に達しました", invocation.Agent.ID, maxTurns)
			events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "agent_failed", err.Error(), countContextItems(messages, invocation.Context)))
			return domain.AgentResult{}, err
		}

		events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "agent_continued", fmt.Sprintf("最大ターン数 %d 到達後に継続が承認されました", maxTurns), countContextItems(messages, invocation.Context)))
	}
}

func (s *Service) executeCalls(ctx context.Context, invocation domain.AgentInvocation, calls []domain.ToolCall, depth int) ([]domain.Message, *domain.AgentResult, []domain.ExecutionEvent, error) {
	if len(calls) == 0 {
		return nil, nil, nil, nil
	}

	executable := s.prepareExecutableCalls(invocation.Agent, calls)
	if s.shouldRunSequentially(executable) {
		return s.executeSequential(ctx, invocation, executable, depth)
	}
	return s.executeParallel(ctx, invocation, executable, depth)
}

type executableCall struct {
	call        domain.ToolCall
	definition  domain.ToolDefinition
	targetAgent *domain.AgentSpec
	handoff     bool
	ephemeral   *domain.AgentSpec
	blockedBy   string
}

func (s *Service) prepareExecutableCalls(agent domain.AgentSpec, calls []domain.ToolCall) []executableCall {
	executable := make([]executableCall, 0, len(calls))
	definitions := definitionMap(s.tools.Definitions(agent))
	for _, spec := range s.catalog.List() {
		definitions[delegateToolName(spec.ID)] = agentToolDefinition(spec, false)
		if spec.Mode == domain.AgentModeHandoff {
			definitions[handoffToolName(spec.ID)] = agentToolDefinition(spec, true)
		}
	}
	definitions["run_ephemeral_agent"] = ephemeralToolDefinition()

	for _, call := range calls {
		item := executableCall{call: call, definition: definitions[call.Name]}
		switch {
		case strings.HasPrefix(call.Name, "delegate_to_"):
			id := strings.TrimPrefix(call.Name, "delegate_to_")
			if spec, ok := s.catalog.Resolve(id); ok {
				if reason := blockedDelegationReason(agent, spec, false); reason != "" {
					item.blockedBy = reason
				} else {
					item.targetAgent = &spec
				}
			}
		case strings.HasPrefix(call.Name, "handoff_to_"):
			id := strings.TrimPrefix(call.Name, "handoff_to_")
			if spec, ok := s.catalog.Resolve(id); ok {
				if reason := blockedDelegationReason(agent, spec, true); reason != "" {
					item.blockedBy = reason
				} else {
					item.targetAgent = &spec
					item.handoff = true
				}
			}
		case call.Name == "run_ephemeral_agent":
			spec := ephemeralSpecFromCall(call)
			item.ephemeral = &spec
			item.definition = ephemeralToolDefinition()
		}
		executable = append(executable, item)
	}
	return executable
}

func (s *Service) executeSequential(ctx context.Context, invocation domain.AgentInvocation, executable []executableCall, depth int) ([]domain.Message, *domain.AgentResult, []domain.ExecutionEvent, error) {
	results := make([]domain.Message, 0, len(executable))
	events := []domain.ExecutionEvent{}
	for _, item := range executable {
		message, direct, callEvents, err := s.executeOne(ctx, invocation, item, depth)
		events = append(events, callEvents...)
		if err != nil {
			return nil, nil, events, err
		}
		if direct != nil {
			return nil, direct, events, nil
		}
		results = append(results, message)
	}
	return results, nil, events, nil
}

func (s *Service) executeParallel(ctx context.Context, invocation domain.AgentInvocation, executable []executableCall, depth int) ([]domain.Message, *domain.AgentResult, []domain.ExecutionEvent, error) {
	type result struct {
		index  int
		msg    domain.Message
		direct *domain.AgentResult
		events []domain.ExecutionEvent
	}

	events := make([][]domain.ExecutionEvent, len(executable))
	results := make([]domain.Message, len(executable))
	var directMu sync.Mutex
	var direct *domain.AgentResult

	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(s.config.MaxParallelAgents)

	for idx := range executable {
		idx := idx
		group.Go(func() error {
			msg, directResult, callEvents, err := s.executeOne(groupCtx, invocation, executable[idx], depth)
			events[idx] = callEvents
			if err != nil {
				return err
			}
			if directResult != nil {
				directMu.Lock()
				if direct == nil {
					direct = directResult
				}
				directMu.Unlock()
				return nil
			}
			results[idx] = msg
			return nil
		})
	}

	if err := group.Wait(); err != nil {
		return nil, nil, flattenEvents(events), err
	}
	if direct != nil {
		return nil, direct, flattenEvents(events), nil
	}
	return results, nil, flattenEvents(events), nil
}

func (s *Service) executeOne(ctx context.Context, invocation domain.AgentInvocation, item executableCall, depth int) (domain.Message, *domain.AgentResult, []domain.ExecutionEvent, error) {
	switch {
	case item.blockedBy != "":
		detail := item.call.Name + ": " + item.blockedBy
		events := []domain.ExecutionEvent{s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "tool_failed", detail, countContextItems(invocation.Messages, invocation.Context))}
		return toolMessage(item.call, "エラー: "+item.blockedBy), nil, events, nil
	case item.targetAgent != nil:
		return s.executeDelegation(ctx, invocation, *item.targetAgent, item.call, item.handoff, depth)
	case item.ephemeral != nil:
		result, err := s.runAgent(ctx, domain.AgentInvocation{
			RunID:       s.nextRunID("ephemeral"),
			ParentRunID: invocation.RunID,
			Agent:       s.resolveModel(*item.ephemeral, invocation.Model),
			Messages:    childMessages(item.call),
			Context:     buildContext(invocation.Messages, stringArg(item.call.Arguments, "task"), s.tools.Definitions(*item.ephemeral)),
			Model:       invocation.Model,
			Stream:      false,
		}, depth+1)
		if err != nil {
			events := []domain.ExecutionEvent{s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "agent_failed", err.Error(), countContextItems(invocation.Messages, invocation.Context))}
			return toolMessage(item.call, "エラー: "+err.Error()), nil, events, nil
		}
		return toolMessage(item.call, result.Message.Content), nil, result.Events, nil
	default:
		result := s.tools.Execute(ctx, invocation.Agent, item.call)
		eventType := "tool_called"
		detail := item.call.Name
		if !result.Success {
			eventType = "tool_failed"
			detail = item.call.Name + ": " + result.Output
		}
		events := []domain.ExecutionEvent{s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, eventType, detail, countContextItems(invocation.Messages, invocation.Context))}
		return toolMessage(item.call, result.Output), nil, events, nil
	}
}

func (s *Service) executeDelegation(ctx context.Context, invocation domain.AgentInvocation, target domain.AgentSpec, call domain.ToolCall, handoff bool, depth int) (domain.Message, *domain.AgentResult, []domain.ExecutionEvent, error) {
	child := domain.AgentInvocation{
		RunID:       s.nextRunID(target.ID),
		ParentRunID: invocation.RunID,
		Agent:       s.resolveModel(target, invocation.Model),
		Messages:    childMessages(call),
		Context:     buildContext(invocation.Messages, stringArg(call.Arguments, "task"), s.tools.Definitions(target)),
		Model:       invocation.Model,
		Stream:      false,
	}
	startType := "delegate_started"
	if handoff {
		startType = "handoff_started"
	}
	startEvent := s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, startType, target.ID+": "+stringArg(call.Arguments, "task"), countContextItems(invocation.Messages, invocation.Context))
	result, err := s.runAgent(ctx, child, depth+1)
	if err != nil {
		events := []domain.ExecutionEvent{s.newEvent(child.RunID, invocation.RunID, target.ID, "agent_failed", err.Error(), countContextItems(child.Messages, child.Context))}
		return toolMessage(call, "エラー: "+err.Error()), nil, events, nil
	}
	if handoff {
		return domain.Message{}, &result, append([]domain.ExecutionEvent{startEvent}, result.Events...), nil
	}
	return toolMessage(call, result.Message.Content), nil, append([]domain.ExecutionEvent{startEvent}, result.Events...), nil
}

func (s *Service) shouldRunSequentially(executable []executableCall) bool {
	if s.config.MaxParallelAgents <= 1 || len(executable) <= 1 {
		return true
	}
	for _, item := range executable {
		if item.handoff || item.definition.MutatesWorkspace || !item.definition.ParallelSafe {
			return true
		}
	}
	return false
}

func (s *Service) agentToolDefinitions(current domain.AgentSpec) []domain.ToolDefinition {
	tools := make([]domain.ToolDefinition, 0, len(s.catalog.List())+1)
	for _, spec := range s.catalog.List() {
		if spec.ID == current.ID || spec.Disabled || spec.Mode == domain.AgentModeManager {
			continue
		}
		tools = append(tools, agentToolDefinition(spec, false))
		if spec.Mode == domain.AgentModeHandoff {
			tools = append(tools, agentToolDefinition(spec, true))
		}
	}
	tools = append(tools, ephemeralToolDefinition())
	return tools
}

func agentToolDefinition(spec domain.AgentSpec, handoff bool) domain.ToolDefinition {
	name := delegateToolName(spec.ID)
	description := fmt.Sprintf("%s にタスクを委譲します。", spec.Name)
	if handoff {
		name = handoffToolName(spec.ID)
		description = fmt.Sprintf("%s に現在のターンを handoff します。", spec.Name)
	}
	return domain.ToolDefinition{
		Name:         name,
		Description:  description,
		ReadOnly:     spec.ReadOnly,
		ParallelSafe: spec.ReadOnly,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{
					"type":        "string",
					"description": "サブエージェントに依頼するタスク",
				},
			},
			"required": []string{"task"},
		},
	}
}

func ephemeralToolDefinition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:         "run_ephemeral_agent",
		Description:  "一時的なサブエージェントを作成して実行します。",
		ReadOnly:     false,
		ParallelSafe: false,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task":        map[string]any{"type": "string"},
				"instruction": map[string]any{"type": "string"},
				"allowed_tools": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
				"read_only": map[string]any{"type": "boolean"},
				"mode":      map[string]any{"type": "string"},
			},
			"required": []string{"task", "instruction"},
		},
	}
}

func ephemeralSpecFromCall(call domain.ToolCall) domain.AgentSpec {
	allowed := []string{}
	if raw, ok := call.Arguments["allowed_tools"].([]any); ok {
		for _, item := range raw {
			if value, ok := item.(string); ok {
				allowed = append(allowed, value)
			}
		}
	}
	mode := domain.AgentModeTool
	if value, ok := call.Arguments["mode"].(string); ok {
		mode = domain.AgentMode(value)
	}
	readOnly, _ := call.Arguments["read_only"].(bool)
	return domain.AgentSpec{
		ID:           "ephemeral",
		Name:         "Ephemeral Agent",
		Instruction:  stringArg(call.Arguments, "instruction"),
		Mode:         mode,
		AllowedTools: allowed,
		ReadOnly:     readOnly,
		MaxTurns:     4,
	}
}

func buildContext(messages []domain.Message, task string, tools []domain.ToolDefinition) domain.ContextPack {
	return domain.ContextPack{
		UserGoal:           latestUserMessage(messages),
		TaskBrief:          task,
		RelevantFiles:      extractRelevantFiles(messages),
		RecentSummary:      latestUserMessage(messages),
		AvailableToolNames: toolNames(tools),
	}
}

func latestUserMessage(messages []domain.Message) string {
	for idx := len(messages) - 1; idx >= 0; idx-- {
		if messages[idx].Role == domain.RoleUser {
			return messages[idx].Content
		}
	}
	return ""
}

func shouldBiasManagerTowardDelegation(messages []domain.Message) bool {
	latest := strings.ToLower(latestUserMessage(messages))
	if latest == "" {
		return false
	}
	keywords := []string{
		"subagent",
		"sub-agent",
		"サブエージェント",
		"委譲",
		"品質レポート",
		"quality report",
		"audit",
		"調査",
		"investigate",
		"analysis",
		"analyze",
		"analyse",
		"review",
		"report",
		"repository",
		"repo",
		"codebase",
		"コードベース",
		"全て",
		"すべて",
		"全ファイル",
		"all files",
		"entire",
		"以下のファイル",
		"internal/",
		"directory",
		"ディレクトリ",
	}
	for _, keyword := range keywords {
		if strings.Contains(latest, keyword) {
			return true
		}
	}
	return false
}

func childMessages(call domain.ToolCall) []domain.Message {
	task := stringArg(call.Arguments, "task")
	if task == "" {
		task = "Subtask requested by parent agent."
	}
	return []domain.Message{{Role: domain.RoleUser, Content: task}}
}

func toolMessage(call domain.ToolCall, output string) domain.Message {
	return domain.Message{
		Role:    domain.RoleTool,
		Content: output,
		AgentID: call.RequestedByAgentID,
		Metadata: map[string]string{
			"tool_name": call.Name,
		},
	}
}

func definitionMap(defs []domain.ToolDefinition) map[string]domain.ToolDefinition {
	out := make(map[string]domain.ToolDefinition, len(defs))
	for _, def := range defs {
		out[def.Name] = def
	}
	return out
}

func flattenEvents(groups [][]domain.ExecutionEvent) []domain.ExecutionEvent {
	var out []domain.ExecutionEvent
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

func delegateToolName(id string) string {
	return "delegate_to_" + id
}

func handoffToolName(id string) string {
	return "handoff_to_" + id
}

func modelName(invocation domain.AgentInvocation) string {
	if invocation.Agent.Model != "" {
		return invocation.Agent.Model
	}
	return invocation.Model
}

func (s *Service) resolveModel(agent domain.AgentSpec, model string) domain.AgentSpec {
	if agent.Model == "" {
		agent.Model = model
	}
	return agent
}

func (s *Service) timeoutFor(agent domain.AgentSpec) time.Duration {
	if agent.Timeout > 0 {
		return agent.Timeout
	}
	return s.config.DefaultTimeout
}

func (s *Service) nextRunID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, s.runCounter.Add(1))
}

func (s *Service) newEvent(runID, parentRunID, agentID, typ, detail string, contextCount int) domain.ExecutionEvent {
	event := domain.ExecutionEvent{
		RunID:        runID,
		ParentRunID:  parentRunID,
		AgentID:      agentID,
		Type:         typ,
		Detail:       detail,
		Timestamp:    time.Now(),
		ContextCount: contextCount,
	}
	s.broadcast(event)
	return event
}

func (s *Service) broadcast(event domain.ExecutionEvent) {
	if s.config.TraceSink != nil {
		_ = s.config.TraceSink.Append(context.Background(), event)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.listeners {
		select {
		case ch <- event:
		default:
		}
	}
}

func toolNames(tools []domain.ToolDefinition) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func cloneMessages(messages []domain.Message) []domain.Message {
	cloned := make([]domain.Message, 0, len(messages))
	for _, message := range messages {
		item := message
		item.ToolCalls = append([]domain.ToolCall(nil), message.ToolCalls...)
		if message.Metadata != nil {
			item.Metadata = map[string]string{}
			for key, value := range message.Metadata {
				item.Metadata[key] = value
			}
		}
		cloned = append(cloned, item)
	}
	return cloned
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}

func defaultPurpose(call domain.ToolCall) string {
	if task := stringArg(call.Arguments, "task"); task != "" {
		return task
	}
	return call.Name
}

func countContextItems(messages []domain.Message, context domain.ContextPack) int {
	messageCount := len(messages)
	fileCount := len(uniqueStrings(append([]string(nil), context.RelevantFiles...)))
	artifactCount := len(uniqueStrings(append([]string(nil), context.ArtifactRefs...)))
	return messageCount + fileCount + artifactCount
}

func extractRelevantFiles(messages []domain.Message) []string {
	var files []string
	for _, message := range messages {
		for _, token := range strings.Fields(message.Content) {
			candidate := trimToken(token)
			if looksLikeFileRef(candidate) {
				files = append(files, candidate)
			}
		}
	}
	return uniqueStrings(files)
}

func trimToken(token string) string {
	return strings.Trim(token, "[](){}<>\"'`,.:;!?")
}

func looksLikeFileRef(token string) bool {
	if token == "" {
		return false
	}
	if strings.Contains(token, "://") {
		return false
	}
	if strings.Contains(token, "/") || strings.HasPrefix(token, ".") {
		return true
	}
	dot := strings.LastIndex(token, ".")
	if dot <= 0 || dot == len(token)-1 {
		return false
	}
	ext := strings.ToLower(token[dot+1:])
	switch ext {
	case "go", "md", "txt", "toml", "yaml", "yml", "json", "ts", "tsx", "js", "jsx", "py", "rb", "rs", "java", "c", "cc", "cpp", "h", "hpp", "sh", "sql", "html", "css":
		return true
	default:
		return false
	}
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func blockedDelegationReason(current domain.AgentSpec, target domain.AgentSpec, handoff bool) string {
	if current.ID == target.ID {
		return fmt.Sprintf("agent %q は自分自身へ委譲できません", current.ID)
	}
	if current.ID == "planner" && target.ID == "coder" {
		return "planner から coder への委譲は禁止されています。探索や計画には read-only ツールを使用してください"
	}
	if current.ID == "coder" && target.ID == "planner" {
		return "coder から planner への再委譲は禁止されています。現在の handoff 内で直接作業してください"
	}
	if handoff && current.ID == "planner" && target.ID == "coder" {
		return "planner から coder への handoff は禁止されています。manager が handoff を判断してください"
	}
	return ""
}

func (s *Service) approveContinue(ctx context.Context, invocation domain.AgentInvocation, maxTurns int) bool {
	if s.config.Approver == nil {
		return false
	}
	decision, err := s.config.Approver.Approve(ctx, domain.PermissionRequest{
		ToolName:  "agent_turn_limit",
		Operation: fmt.Sprintf("継続実行 (上限 %d 到達)", maxTurns),
		Resource:  fmt.Sprintf("%s [%s]", invocation.Agent.ID, invocation.RunID),
		AgentID:   invocation.Agent.ID,
		Purpose:   "turn_limit_reached",
		Task:      invocation.Context.TaskBrief,
	})
	if err != nil {
		return false
	}
	return decision == domain.PermissionAllowOnce || decision == domain.PermissionAllowSession
}
