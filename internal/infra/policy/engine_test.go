package policy

import (
	"context"
	"testing"

	"yagent/internal/domain"
)

func TestEngineAllowsMatchingReadRule(t *testing.T) {
	engine := NewEngine(Rule{
		Decision:  domain.PolicyAllow,
		Tool:      "fs_read",
		Action:    "read",
		Resources: []string{"*.go"},
	})

	decision, request, err := engine.Evaluate(context.Background(), domain.ToolCall{
		Name:      "fs_read",
		Arguments: map[string]any{"path": "/tmp/main.go"},
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if decision != domain.PolicyAllow {
		t.Fatalf("expected allow, got %s with request %+v", decision, request)
	}
}

func TestEngineDeniesMatchingTaskRule(t *testing.T) {
	engine := NewEngine(Rule{
		Decision: domain.PolicyDeny,
		Tool:     "task_run",
		Risk:     "high",
	})

	decision, request, err := engine.Evaluate(context.Background(), domain.ToolCall{
		Name:      "task_run",
		Arguments: map[string]any{"task_id": "deploy:prod"},
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if decision != domain.PolicyDeny {
		t.Fatalf("expected deny, got %s with request %+v", decision, request)
	}
}

func TestEngineFallsBackToApprovalWhenNoRuleMatches(t *testing.T) {
	engine := NewEngine(Rule{
		Decision:  domain.PolicyAllow,
		Tool:      "fs_read",
		Resources: []string{"*.go"},
	})

	decision, request, err := engine.Evaluate(context.Background(), domain.ToolCall{
		Name:      "fs_read",
		Arguments: map[string]any{"path": "/tmp/README.md"},
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if decision != domain.PolicyRequireApproval {
		t.Fatalf("expected require approval, got %s with request %+v", decision, request)
	}
}

func TestEngineMatchesSideEffectRule(t *testing.T) {
	engine := NewEngine(Rule{
		Decision:    domain.PolicyAllow,
		SideEffects: []string{"llm_disclosure"},
		Risk:        "low",
	})

	decision, request, err := engine.Evaluate(context.Background(), domain.ToolCall{
		Name:      "fs_stat",
		Arguments: map[string]any{"path": "/tmp/main.go"},
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if decision != domain.PolicyAllow {
		t.Fatalf("expected allow, got %s with request %+v", decision, request)
	}
}

func TestEngineMatchesNetworkTaskMetadata(t *testing.T) {
	engine := NewEngine(Rule{
		Decision:    domain.PolicyDeny,
		Tool:        "task_run",
		SideEffects: []string{"network_access"},
	})

	decision, request, err := engine.Evaluate(context.Background(), domain.ToolCall{
		Name: "task_run",
		Arguments: map[string]any{
			"task_id":               "docs:fetch",
			"_policy_risk":          "medium",
			"_policy_allow_network": true,
		},
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if decision != domain.PolicyDeny {
		t.Fatalf("expected deny, got %s with request %+v", decision, request)
	}
}

func TestEngineMatchesAgentSelector(t *testing.T) {
	engine := NewEngine(Rule{
		Decision: domain.PolicyDeny,
		Tool:     "fs_write",
		Agent:    "researcher",
	})

	decision, request, err := engine.Evaluate(context.Background(), domain.ToolCall{
		Name:               "fs_write",
		RequestedByAgentID: "researcher",
		Arguments: map[string]any{
			"path":    "/tmp/notes.md",
			"content": "notes",
		},
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if decision != domain.PolicyDeny {
		t.Fatalf("expected deny, got %s with request %+v", decision, request)
	}
}

func TestEngineMatchesMCPPolicyMetadata(t *testing.T) {
	engine := NewEngine(Rule{
		Decision:  domain.PolicyDeny,
		Tool:      "mcp__*",
		Resources: []string{"docs:write_doc"},
	})

	decision, request, err := engine.Evaluate(context.Background(), domain.ToolCall{
		Name: "mcp__docs__write_doc__docs",
		Arguments: map[string]any{
			"_policy_task_id":          "docs",
			"_policy_server_tool_name": "write_doc",
			"_policy_risk":             "medium",
			"_policy_read_only":        false,
		},
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if decision != domain.PolicyDeny {
		t.Fatalf("expected deny, got %s with request %+v", decision, request)
	}
	if request.Scope != "docs:write_doc" || request.Resource != "docs:write_doc" {
		t.Fatalf("expected task-scoped mcp resource, got %+v", request)
	}
}
