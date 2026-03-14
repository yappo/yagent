package llm

// ToolRegistry ツール登録管理
type ToolRegistry struct {
	tools map[string]ToolInterface
}

// NewToolRegistry 新しいレジストリを作成
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]ToolInterface),
	}
}

// Register ツールを登録
func (r *ToolRegistry) Register(tool ToolInterface) {
	r.tools[tool.Name()] = tool
}

// Get 登録されたツールを取得
func (r *ToolRegistry) Get(name string) ToolInterface {
	return r.tools[name]
}

// List 登録されているすべてのツールの情報を取得
func (r *ToolRegistry) List() []ToolDefinition {
	definitions := make([]ToolDefinition, 0, len(r.tools))

	for _, tool := range r.tools {
		definitions = append(definitions, ToolDefinition{
			Type: "function",
			Function: ToolFunctionConfig{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  tool.Parameters(),
			},
		})
	}

	return definitions
}

// ToolRegistryBuilder ツールレジストリビルダー
type ToolRegistryBuilder struct {
	registry *ToolRegistry
}

// NewToolRegistryBuilder 新しいビルダーを作成
func NewToolRegistryBuilder() *ToolRegistryBuilder {
	return &ToolRegistryBuilder{
		registry: NewToolRegistry(),
	}
}

// WithTool ツールを追加
func (b *ToolRegistryBuilder) WithTool(tool ToolInterface) *ToolRegistryBuilder {
	b.registry.Register(tool)
	return b
}

// Build レジストリを構築
func (b *ToolRegistryBuilder) Build() *ToolRegistry {
	return b.registry
}
