package audit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"yagent/internal/domain"
	"yagent/internal/infra/state"
)

type stubApprover struct {
	decision domain.PermissionDecision
}

func (s stubApprover) Approve(context.Context, domain.PermissionRequest) (domain.PermissionDecision, error) {
	return s.decision, nil
}

func TestPermissionAuditApproverSavesDecisionScratch(t *testing.T) {
	store, err := state.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	approver := PermissionAuditApprover{
		Base:  stubApprover{decision: domain.PermissionAllowOnce},
		Store: store,
	}

	decision, err := approver.Approve(context.Background(), domain.PermissionRequest{
		ToolName:     "fs_write",
		Operation:    "ファイル書き込み",
		Resource:     "/tmp/a.txt",
		Action:       "write",
		ResourceKind: "file",
		Risk:         "high",
		Scope:        "/tmp/a.txt",
		SideEffects:  []string{"filesystem_write"},
		PreviewKind:  "diff",
		Preview:      "/tmp/a.txt\n- old\n+ new",
		ChangeFiles:  1,
		Additions:    1,
		Deletions:    1,
		RunID:        "run-1",
		RootRunID:    "root-1",
		Phase:        domain.RunPhaseExecute,
		Attempt:      2,
		AgentID:      "coder",
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision != domain.PermissionAllowOnce {
		t.Fatalf("unexpected decision: %s", decision)
	}

	items, err := store.ListScratch(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one scratch record, got %+v", items)
	}
	if items[0].Kind != "permission_decision" || items[0].SessionID != "root-1" {
		t.Fatalf("unexpected scratch metadata: %+v", items[0])
	}
	var record domain.PermissionDecisionRecord
	if err := json.Unmarshal(items[0].Payload, &record); err != nil {
		t.Fatal(err)
	}
	if record.Decision != domain.PermissionAllowOnce || record.PreviewLines != 3 || record.ChangeFiles != 1 || record.Additions != 1 || record.Deletions != 1 || record.AgentID != "coder" || record.Attempt != 2 {
		t.Fatalf("unexpected permission record: %+v", record)
	}
	if strings.Contains(string(items[0].Payload), "- old") {
		t.Fatalf("expected audit payload to omit preview body: %s", string(items[0].Payload))
	}
}
