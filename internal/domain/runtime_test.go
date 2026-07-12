package domain

import (
	"context"
	"testing"
)

func TestDurableActionExecutionContextRoundTrip(t *testing.T) {
	want := DurableActionExecutionContext{
		ActionID: "action-1", WorkflowID: "workflow-1", WorkUnitID: "unit-1", Attempt: 2,
		IdempotencyKey: "idem-1", LeaseToken: "lease-1", FencingToken: 3,
	}
	got, ok := DurableActionExecutionContextFrom(WithDurableActionExecutionContext(context.Background(), want))
	if !ok || got != want {
		t.Fatalf("DurableActionExecutionContextFrom() = %+v, %t", got, ok)
	}
}
