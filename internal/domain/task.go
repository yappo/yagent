package domain

import "context"

type TaskSpecKind string

const (
	TaskSpecKindCommand   TaskSpecKind = "command"
	TaskSpecKindMCPServer TaskSpecKind = "mcp_server"
)

type MCPTransport string

const (
	MCPTransportStdio MCPTransport = "stdio"
)

type CommandTaskSpec struct {
	Command      string
	Args         []string
	Cwd          string
	ReadPaths    []string
	WritePaths   []string
	Risk         string
	AllowNetwork bool
	Timeout      int
}

type MCPServerSpec struct {
	Transport            MCPTransport
	Command              string
	Args                 []string
	Cwd                  string
	Env                  map[string]string
	Risk                 string
	AllowNetwork         bool
	Timeout              int
	ToolPrefix           string
	Roots                []string
	Trust                string
	TrustToolAnnotations bool
	ParallelSafe         bool
	ReadOnlyTools        []string
	MutatingTools        []string
	ParallelSafeTools    []string
	IncludeTools         []string
	ExcludeTools         []string
}

type TaskDefinition struct {
	ID          string
	Description string
	Kind        TaskSpecKind
	Command     *CommandTaskSpec
	MCPServer   *MCPServerSpec
	Source      string
}

type TaskCatalog interface {
	List(context.Context) []TaskDefinition
	Get(context.Context, string) (TaskDefinition, bool)
}

type MCPToolDescriptor struct {
	Name         string
	Description  string
	InputSchema  map[string]any
	Annotations  map[string]any
	ReadOnly     bool
	ParallelSafe bool
}

type MCPPromptDescriptor struct {
	Name        string
	Description string
	Arguments   map[string]any
}

type MCPResourceDescriptor struct {
	URI         string
	Name        string
	Description string
	MIMEType    string
}

type MCPConnectionManager interface {
	Bind(context.Context, TaskDefinition) ([]MCPToolDescriptor, error)
	BoundTools() []BoundMCPTool
	CallTool(context.Context, string, string, map[string]any) (string, error)
}

type MCPSessionFactory interface {
	Open(context.Context, TaskDefinition) (MCPSession, error)
}

type BoundMCPTool struct {
	TaskID         string
	ToolName       string
	QualifiedName  string
	Description    string
	InputSchema    map[string]any
	ReadOnly       bool
	ParallelSafe   bool
	ServerToolName string
	Risk           string
	AllowNetwork   bool
	Roots          []string
	TrustBoundary  string
	SafetySource   string
}

type MCPSession interface {
	Initialize(context.Context) error
	ListTools(context.Context) ([]MCPToolDescriptor, error)
	CallTool(context.Context, string, map[string]any) (string, error)
	Close() error
}
