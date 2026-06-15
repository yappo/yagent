package stdio

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"yagent/internal/domain"
)

type Factory struct {
	logger domain.StructuredLogSink
}

func NewFactory(logger domain.StructuredLogSink) *Factory {
	return &Factory{logger: logger}
}

func (f *Factory) Open(_ context.Context, task domain.TaskDefinition) (domain.MCPSession, error) {
	if task.MCPServer == nil {
		return nil, fmt.Errorf("MCP server spec がありません")
	}
	if task.MCPServer.Transport != "" && task.MCPServer.Transport != domain.MCPTransportStdio {
		return nil, fmt.Errorf("未対応の MCP transport です: %s", task.MCPServer.Transport)
	}
	cmd := exec.Command(task.MCPServer.Command, task.MCPServer.Args...)
	cmd.Dir = task.MCPServer.Cwd
	cmd.Env = mergeEnv(task.MCPServer.Env)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	s := &Session{
		taskID:   task.ID,
		cmd:      cmd,
		stdin:    stdin,
		stdout:   bufio.NewReader(stdout),
		pending:  map[int64]chan responseEnvelope{},
		callLock: &sync.Mutex{},
		logger:   f.logger,
		roots:    mcpRoots(task),
	}
	go s.copyStderr(stderr)
	go s.readLoop()
	return s, nil
}

type Session struct {
	taskID       string
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       *bufio.Reader
	writeMu      sync.Mutex
	callLock     *sync.Mutex
	pendingMu    sync.Mutex
	pending      map[int64]chan responseEnvelope
	nextID       atomic.Int64
	toolsOnce    sync.Once
	tools        []domain.MCPToolDescriptor
	toolsErr     error
	closeOnce    sync.Once
	initializeMu sync.Mutex
	initialized  bool
	logger       domain.StructuredLogSink
	roots        []mcpRoot
}

type requestEnvelope struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int64          `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

type responseEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *responseError  `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpRoot struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

func (s *Session) Initialize(ctx context.Context) error {
	s.initializeMu.Lock()
	defer s.initializeMu.Unlock()
	if s.initialized {
		return nil
	}
	_, err := s.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"tools":     map[string]any{},
			"prompts":   map[string]any{},
			"resources": map[string]any{},
			"roots":     map[string]any{"listChanged": false},
		},
		"clientInfo": map[string]any{
			"name":    "yagent",
			"version": "dev",
		},
	})
	if err != nil {
		return err
	}
	if err := s.notify(ctx, "notifications/initialized", map[string]any{}); err != nil {
		return err
	}
	s.initialized = true
	return nil
}

func (s *Session) ListTools(ctx context.Context) ([]domain.MCPToolDescriptor, error) {
	s.toolsOnce.Do(func() {
		raw, err := s.call(ctx, "tools/list", map[string]any{})
		if err != nil {
			s.toolsErr = err
			return
		}
		var payload struct {
			Tools []struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				InputSchema map[string]any `json:"inputSchema"`
				Annotations map[string]any `json:"annotations"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			s.toolsErr = err
			return
		}
		s.tools = make([]domain.MCPToolDescriptor, 0, len(payload.Tools))
		for _, tool := range payload.Tools {
			s.tools = append(s.tools, domain.MCPToolDescriptor{
				Name:         tool.Name,
				Description:  tool.Description,
				InputSchema:  tool.InputSchema,
				Annotations:  tool.Annotations,
				ReadOnly:     annotationBool(tool.Annotations, "readOnlyHint"),
				ParallelSafe: !annotationBool(tool.Annotations, "destructiveHint"),
			})
		}
	})
	if s.toolsErr != nil {
		return nil, s.toolsErr
	}
	out := make([]domain.MCPToolDescriptor, len(s.tools))
	copy(out, s.tools)
	return out, nil
}

func (s *Session) CallTool(ctx context.Context, name string, arguments map[string]any) (string, error) {
	s.callLock.Lock()
	defer s.callLock.Unlock()

	raw, err := s.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	})
	if err != nil {
		return "", err
	}
	var payload struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil && len(payload.Content) > 0 {
		var parts []string
		for _, item := range payload.Content {
			if item.Text != "" {
				parts = append(parts, item.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n"), nil
		}
	}
	return string(raw), nil
}

func (s *Session) Close() error {
	var err error
	s.closeOnce.Do(func() {
		if s.stdin != nil {
			_ = s.stdin.Close()
		}
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
			err = s.cmd.Wait()
		}
	})
	return err
}

func (s *Session) call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	id := s.nextID.Add(1)
	request := requestEnvelope{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	responseCh := make(chan responseEnvelope, 1)
	s.pendingMu.Lock()
	s.pending[id] = responseCh
	s.pendingMu.Unlock()
	defer func() {
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
	}()

	if err := s.writeMessage(ctx, request); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case response := <-responseCh:
		if response.Error != nil {
			return nil, fmt.Errorf("mcp %s failed: %s", method, response.Error.Message)
		}
		return response.Result, nil
	}
}

func (s *Session) notify(ctx context.Context, method string, params map[string]any) error {
	request := requestEnvelope{
		JSONRPC: "2.0",
		ID:      0,
		Method:  method,
		Params:  params,
	}
	return s.writeMessage(ctx, request)
}

func (s *Session) readLoop() {
	scanner := bufio.NewScanner(s.stdout)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		payload := append([]byte(nil), scanner.Bytes()...)
		s.logProtocol(context.Background(), "receive", payload)
		var response responseEnvelope
		if err := json.Unmarshal(payload, &response); err != nil {
			s.logInvalidStdout(context.Background(), payload, err)
			s.failPending(err)
			return
		}
		if response.Method != "" {
			s.handleServerRequest(context.Background(), response)
			continue
		}

		s.pendingMu.Lock()
		ch := s.pending[response.ID]
		s.pendingMu.Unlock()
		if ch != nil {
			ch <- response
		}
	}
	if err := scanner.Err(); err != nil {
		s.failPending(err)
	}
}

func (s *Session) handleServerRequest(ctx context.Context, request responseEnvelope) {
	if request.ID == 0 {
		return
	}
	switch request.Method {
	case "roots/list":
		_ = s.writeServerResult(ctx, request.ID, map[string]any{"roots": append([]mcpRoot(nil), s.roots...)})
	case "sampling/createMessage":
		_ = s.writeServerError(ctx, request.ID, -32000, "MCP sampling is disabled; yagent does not allow server-initiated LLM calls")
	default:
		_ = s.writeServerError(ctx, request.ID, -32601, "method not found")
	}
}

func (s *Session) writeServerResult(ctx context.Context, id int64, result any) error {
	return s.writeMessage(ctx, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func (s *Session) writeServerError(ctx context.Context, id int64, code int, message string) error {
	return s.writeMessage(ctx, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func (s *Session) failPending(err error) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	for id, ch := range s.pending {
		ch <- responseEnvelope{
			ID:    id,
			Error: &responseError{Message: err.Error()},
		}
	}
}

func (s *Session) writeMessage(ctx context.Context, message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	s.logProtocol(ctx, "send", data)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = fmt.Fprintf(s.stdin, "%s\n", data)
	return err
}

func (s *Session) copyStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		s.logStderr(context.Background(), scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		s.logStderr(context.Background(), "scanner_error: "+err.Error())
	}
}

func annotationBool(values map[string]any, key string) bool {
	if values == nil {
		return false
	}
	value, ok := values[key]
	if !ok {
		return false
	}
	result, _ := value.(bool)
	return result
}

func mergeEnv(extra map[string]string) []string {
	if len(extra) == 0 {
		return os.Environ()
	}
	cmdEnv := append([]string(nil), os.Environ()...)
	for key, value := range extra {
		cmdEnv = append(cmdEnv, key+"="+value)
	}
	return cmdEnv
}

func mcpRoots(task domain.TaskDefinition) []mcpRoot {
	if task.MCPServer == nil {
		return nil
	}
	values := append([]string(nil), task.MCPServer.Roots...)
	if len(values) == 0 && strings.TrimSpace(task.MCPServer.Cwd) != "" {
		values = append(values, task.MCPServer.Cwd)
	}
	roots := make([]mcpRoot, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		abs, err := filepath.Abs(value)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		roots = append(roots, mcpRoot{
			URI:  (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String(),
			Name: filepath.Base(abs),
		})
	}
	return roots
}

func (s *Session) logProtocol(ctx context.Context, direction string, payload []byte) {
	if s.logger == nil {
		return
	}
	fields := map[string]any{
		"task_id":   s.taskID,
		"direction": direction,
		"bytes":     len(payload),
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err == nil {
		if method, ok := decoded["method"].(string); ok {
			fields["method"] = method
			fields["message_kind"] = "request"
		}
		if id, ok := decoded["id"]; ok {
			fields["id"] = id
		}
		if _, ok := decoded["result"]; ok {
			fields["message_kind"] = "response"
		}
		if _, ok := decoded["error"]; ok {
			fields["message_kind"] = "error"
		}
	}
	_ = s.logger.WriteRecord(ctx, "mcp.protocol", fields)
}

func (s *Session) logStderr(ctx context.Context, line string) {
	if s.logger == nil {
		return
	}
	_ = s.logger.WriteRecord(ctx, "mcp.stderr", map[string]any{
		"task_id": s.taskID,
		"line":    line,
	})
}

func (s *Session) logInvalidStdout(ctx context.Context, payload []byte, err error) {
	if s.logger == nil {
		return
	}
	_ = s.logger.WriteRecord(ctx, "mcp.stdout_invalid", map[string]any{
		"task_id": s.taskID,
		"bytes":   len(payload),
		"error":   err.Error(),
	})
}
