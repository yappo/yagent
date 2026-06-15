package stdio

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"yagent/internal/domain"
)

type captureLogger struct {
	mu    sync.Mutex
	types []string
}

func (c *captureLogger) WriteRecord(_ context.Context, typ string, fields map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if method, ok := fields["method"].(string); ok {
		typ += ":" + method
	}
	if line, ok := fields["line"].(string); ok {
		typ += ":" + line
	}
	c.types = append(c.types, typ)
	return nil
}

func (c *captureLogger) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.types...)
}

func TestSessionInitializeListToolsAndCall(t *testing.T) {
	counterFile := filepath.Join(t.TempDir(), "tools-list-count")
	logger := &captureLogger{}
	task := domain.TaskDefinition{
		ID:   "docs",
		Kind: domain.TaskSpecKindMCPServer,
		MCPServer: &domain.MCPServerSpec{
			Transport: domain.MCPTransportStdio,
			Command:   os.Args[0],
			Args:      []string{"-test.run", "TestHelperProcess", "--", "mcp-ok"},
			Cwd:       t.TempDir(),
			Env:       map[string]string{"GO_WANT_HELPER_PROCESS": "1", "YAGENT_MCP_COUNTER_FILE": counterFile},
		},
	}

	sessionRaw, err := NewFactory(logger).Open(context.Background(), task)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer sessionRaw.Close()

	session := sessionRaw.(*Session)
	if err := session.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize returned error: %v", err)
	}
	tools, err := session.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools returned error: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "search_docs" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
	tools, err = session.ListTools(context.Background())
	if err != nil {
		t.Fatalf("second ListTools returned error: %v", err)
	}
	data, err := os.ReadFile(counterFile)
	if err != nil {
		t.Fatalf("failed reading counter file: %v", err)
	}
	if strings.TrimSpace(string(data)) != "1" {
		t.Fatalf("expected tools/list to be cached, got %q", string(data))
	}
	output, err := session.CallTool(context.Background(), "search_docs", map[string]any{"query": "mcp"})
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}
	if !strings.Contains(output, "search_docs") {
		t.Fatalf("unexpected output: %q", output)
	}
	types := logger.snapshot()
	assertContains(t, types, "mcp.protocol:initialize")
	assertContains(t, types, "mcp.protocol:notifications/initialized")
	assertContains(t, types, "mcp.protocol:tools/list")
	assertContains(t, types, "mcp.protocol:tools/call")
	assertContains(t, types, "mcp.stderr:helper stderr ready")
}

func TestSessionInitializeFailsOnInvalidPayload(t *testing.T) {
	task := domain.TaskDefinition{
		ID:   "broken",
		Kind: domain.TaskSpecKindMCPServer,
		MCPServer: &domain.MCPServerSpec{
			Transport: domain.MCPTransportStdio,
			Command:   os.Args[0],
			Args:      []string{"-test.run", "TestHelperProcess", "--", "mcp-invalid"},
			Cwd:       t.TempDir(),
			Env:       map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
		},
	}

	session, err := NewFactory(nil).Open(context.Background(), task)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer session.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := session.Initialize(ctx); err == nil {
		t.Fatal("expected initialize failure")
	}
}

func TestSessionHandlesRootsAndRejectsSamplingRequests(t *testing.T) {
	root := t.TempDir()
	logger := &captureLogger{}
	task := domain.TaskDefinition{
		ID:   "docs",
		Kind: domain.TaskSpecKindMCPServer,
		MCPServer: &domain.MCPServerSpec{
			Transport: domain.MCPTransportStdio,
			Command:   os.Args[0],
			Args:      []string{"-test.run", "TestHelperProcess", "--", "mcp-client-requests"},
			Cwd:       t.TempDir(),
			Roots:     []string{root},
			Env: map[string]string{
				"GO_WANT_HELPER_PROCESS":   "1",
				"YAGENT_MCP_ROOT_BASENAME": filepath.Base(root),
			},
		},
	}

	session, err := NewFactory(logger).Open(context.Background(), task)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer session.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := session.Initialize(ctx); err != nil {
		t.Fatalf("Initialize returned error: %v", err)
	}
	assertContains(t, logger.types, "mcp.protocol:roots/list")
	assertEventuallyContains(t, logger, "mcp.protocol:sampling/createMessage")
	assertEventuallyContains(t, logger, "mcp.stderr:roots ok")
	assertEventuallyContains(t, logger, "mcp.stderr:sampling denied")
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	mode := "mcp-ok"
	for idx, arg := range os.Args {
		if arg == "--" && idx+1 < len(os.Args) {
			mode = os.Args[idx+1]
			break
		}
	}

	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	initialized := false
	_, _ = fmt.Fprintln(os.Stderr, "helper stderr ready")
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			os.Exit(0)
		}
		payload := bytesTrimSpace(line)
		if len(payload) == 0 {
			continue
		}
		if mode == "mcp-invalid" {
			fmt.Fprintln(writer, "bad")
			_ = writer.Flush()
			os.Exit(0)
		}
		var req struct {
			ID     int64          `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			os.Exit(1)
		}
		switch req.Method {
		case "initialize":
			writeHelperResponse(writer, req.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "helper", "version": "1.0.0"},
			})
			if mode == "mcp-client-requests" {
				writeHelperRequest(writer, 9001, "roots/list", nil)
				rootResponse := readHelperResponsePayload(reader)
				if helperRootsOK(rootResponse) {
					_, _ = fmt.Fprintln(os.Stderr, "roots ok")
				}
				writeHelperRequest(writer, 9002, "sampling/createMessage", map[string]any{
					"messages": []map[string]any{{
						"role": "user",
						"content": map[string]any{
							"type": "text",
							"text": "nested sampling",
						},
					}},
				})
				samplingResponse := readHelperResponsePayload(reader)
				if strings.Contains(string(samplingResponse), "sampling is disabled") {
					_, _ = fmt.Fprintln(os.Stderr, "sampling denied")
				}
			}
		case "notifications/initialized":
			initialized = true
		case "tools/list":
			if !initialized {
				os.Exit(2)
			}
			bumpCounterFile()
			writeHelperResponse(writer, req.ID, map[string]any{
				"tools": []map[string]any{{
					"name":        "search_docs",
					"description": "Search docs",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"query": map[string]any{"type": "string"},
						},
					},
					"annotations": map[string]any{"readOnlyHint": true},
				}},
			})
		case "tools/call":
			writeHelperResponse(writer, req.ID, map[string]any{
				"content": []map[string]any{{
					"type": "text",
					"text": fmt.Sprintf("called %s", req.Params["name"]),
				}},
			})
		default:
			writeHelperResponse(writer, req.ID, map[string]any{})
		}
	}
}

func readHelperResponsePayload(reader *bufio.Reader) []byte {
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			os.Exit(3)
		}
		payload := bytesTrimSpace(line)
		if len(payload) == 0 {
			continue
		}
		var message struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(payload, &message); err == nil && message.Method == "" {
			return payload
		}
	}
}

func helperRootsOK(payload []byte) bool {
	rootName := os.Getenv("YAGENT_MCP_ROOT_BASENAME")
	if rootName == "" {
		return false
	}
	return strings.Contains(string(payload), `"roots"`) &&
		strings.Contains(string(payload), `"file://`) &&
		strings.Contains(string(payload), rootName)
}

func bumpCounterFile() {
	path := os.Getenv("YAGENT_MCP_COUNTER_FILE")
	if path == "" {
		return
	}
	current := 0
	if data, err := os.ReadFile(path); err == nil {
		_, _ = fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &current)
	}
	current++
	_ = os.WriteFile(path, []byte(fmt.Sprintf("%d", current)), 0o644)
}

func writeHelperResponse(w *bufio.Writer, id int64, result map[string]any) {
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
	fmt.Fprintf(w, "%s\n", payload)
	_ = w.Flush()
}

func writeHelperRequest(w *bufio.Writer, id int64, method string, params map[string]any) {
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	fmt.Fprintf(w, "%s\n", payload)
	_ = w.Flush()
}

func assertContains(t *testing.T, items []string, want string) {
	t.Helper()
	for _, item := range items {
		if item == want {
			return
		}
	}
	t.Fatalf("expected %q in %+v", want, items)
}

func assertEventuallyContains(t *testing.T, logger *captureLogger, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var items []string
	for time.Now().Before(deadline) {
		items = logger.snapshot()
		for _, item := range items {
			if item == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected %q in %+v", want, items)
}

func bytesTrimSpace(input []byte) []byte {
	return []byte(strings.TrimSpace(string(input)))
}
