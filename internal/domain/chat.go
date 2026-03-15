package domain

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role      Role
	Content   string
	ToolCalls []ToolCall
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]any
}

type CompletionRequest struct {
	Messages []Message
	Model    string
	Stream   bool
	Tools    []ToolDefinition
}

type CompletionResponse struct {
	Message      Message
	FinishReason string
}
