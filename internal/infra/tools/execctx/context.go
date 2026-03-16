package execctx

import "context"

type contextKey string

const (
	agentIDContextKey contextKey = "agent_id"
	purposeContextKey contextKey = "purpose"
)

func WithExecutionContext(ctx context.Context, agentID, purpose string) context.Context {
	ctx = context.WithValue(ctx, agentIDContextKey, agentID)
	return context.WithValue(ctx, purposeContextKey, purpose)
}

func AgentID(ctx context.Context) string {
	value, _ := ctx.Value(agentIDContextKey).(string)
	return value
}

func Purpose(ctx context.Context) string {
	value, _ := ctx.Value(purposeContextKey).(string)
	return value
}
