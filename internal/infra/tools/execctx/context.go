package execctx

import (
	"context"

	"yagent/internal/domain"
)

type contextKey string

const (
	agentIDContextKey contextKey = "agent_id"
	purposeContextKey contextKey = "purpose"
	runIDContextKey   contextKey = "run_id"
	rootIDContextKey  contextKey = "root_run_id"
	phaseContextKey   contextKey = "phase"
	attemptContextKey contextKey = "attempt"
)

func WithExecutionContext(ctx context.Context, agentID, purpose string) context.Context {
	ctx = context.WithValue(ctx, agentIDContextKey, agentID)
	return context.WithValue(ctx, purposeContextKey, purpose)
}

func WithRunContext(ctx context.Context, runID, rootRunID string, phase domain.RunPhase, attempt int) context.Context {
	ctx = context.WithValue(ctx, runIDContextKey, runID)
	ctx = context.WithValue(ctx, rootIDContextKey, rootRunID)
	ctx = context.WithValue(ctx, phaseContextKey, phase)
	return context.WithValue(ctx, attemptContextKey, attempt)
}

func AgentID(ctx context.Context) string {
	value, _ := ctx.Value(agentIDContextKey).(string)
	return value
}

func Purpose(ctx context.Context) string {
	value, _ := ctx.Value(purposeContextKey).(string)
	return value
}

func FillPermissionRequest(ctx context.Context, request *domain.PermissionRequest) {
	if request == nil {
		return
	}
	request.AgentID = AgentID(ctx)
	request.Purpose = Purpose(ctx)
	request.RunID, _ = ctx.Value(runIDContextKey).(string)
	request.RootRunID, _ = ctx.Value(rootIDContextKey).(string)
	request.Phase, _ = ctx.Value(phaseContextKey).(domain.RunPhase)
	request.Attempt, _ = ctx.Value(attemptContextKey).(int)
}
