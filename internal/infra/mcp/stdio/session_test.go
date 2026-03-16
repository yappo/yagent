package stdio

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yagent/internal/domain"
)

type captureLogger struct {
	types []string
}

func (c *captureLogger) WriteRecord(_ context.Context, typ string, fields map[string]any) error {
	if method, ok := fields["method"].(string); ok {
		typ += ":" + method
	}
	if line, ok := fields["line"].(string); ok {
		typ += ":" + line
	}
	c.types = append(c.types, typ)
	return nil
}

func TestSessionInitializeListToolsAndCall(t *testing.T) {
	counterFile := filepath.Join(t.TempDir(), "tools-list-count")
	logger := &captureLogger{}
	task := domain.TaskDefinition{
		ID:   "docs",
		Kind: domain.TaskKindMCPServer,
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
	assertContains(t, logger.types, "mcp.protocol:initialize")
	assertContains(t, logger.types, "mcp.protocol:notifications/initialized")
	assertContains(t, logger.types, "mcp.protocol:tools/list")
	assertContains(t, logger.types, "mcp.protocol:tools/call")
	assertContains(t, logger.types, "mcp.stderr:helper stderr ready")
}

func TestSessionInitializeFailsOnInvalidPayload(t *testing.T) {
	task := domain.TaskDefinition{
		ID:   "broken",
		Kind: domain.TaskKindMCPServer,
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
		payload, err := readFramedMessage(reader)
		if err != nil {
			if err == io.EOF {
				os.Exit(0)
			}
			os.Exit(1)
		}
		if mode == "mcp-invalid" {
			fmt.Fprint(writer, "Content-Length: 3\r\n\r\nbad")
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
	fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(payload), payload)
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
