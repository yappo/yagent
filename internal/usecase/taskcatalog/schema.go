package taskcatalog

func JSONSchema() map[string]any {
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"title":                "yagent task catalog",
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"tasks": map[string]any{
				"type":        "array",
				"description": "Command tasks available through task_run.",
				"items":       commandTaskSchema(),
			},
			"mcpservers": map[string]any{
				"type":        "array",
				"description": "MCP servers available through task_bind.",
				"items":       mcpServerSchema(),
			},
		},
	}
}

func commandTaskSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"id":            nonEmptyStringSchema("Task id used by task_run."),
			"description":   stringSchema("Human and agent readable task summary."),
			"command":       nonEmptyStringSchema("Executable command."),
			"args":          stringArraySchema("Command arguments."),
			"cwd":           stringSchema("Working directory. Relative paths are resolved from the repository root."),
			"read_paths":    pathArraySchema("Paths the task is expected to read."),
			"write_paths":   pathArraySchema("Paths the task is expected to write."),
			"risk":          enumSchema([]string{"low", "medium", "high"}, "Operational risk hint."),
			"allow_network": boolSchema("Whether the task may use network access."),
			"timeout":       nonNegativeIntegerSchema("Timeout in seconds. Zero uses the tool default."),
		},
		"required": []string{"id", "command"},
	}
}

func mcpServerSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"id":                     nonEmptyStringSchema("MCP server id used by task_bind."),
			"description":            stringSchema("Human and agent readable server summary."),
			"transport":              enumSchema([]string{"stdio"}, "MCP transport. Only stdio is currently supported."),
			"command":                nonEmptyStringSchema("Executable command used to start the server."),
			"args":                   stringArraySchema("Command arguments."),
			"cwd":                    stringSchema("Working directory. Relative paths are resolved from the repository root."),
			"roots":                  pathArraySchema("Filesystem roots exposed through MCP roots/list. Defaults to cwd."),
			"env":                    stringMapSchema("Environment variables for the server process."),
			"risk":                   enumSchema([]string{"low", "medium", "high"}, "Operational risk hint."),
			"allow_network":          boolSchema("Whether the server may use network access."),
			"timeout":                nonNegativeIntegerSchema("Startup and bind timeout in seconds. Zero uses the tool default."),
			"tool_prefix":            stringSchema("Prefix used for exposed mcp__ tool names. Defaults to id."),
			"trust":                  enumSchema([]string{"untrusted", "trusted"}, "Trust boundary for server-provided tool annotations."),
			"trust_tool_annotations": boolSchema("Whether yagent may use server-provided safety annotations."),
			"parallel_safe":          boolSchema("Whether trusted server tools may be treated as parallel safe by default."),
			"read_only_tools":        nonEmptyStringArraySchema("Server tool names or globs treated as read-only."),
			"mutating_tools":         nonEmptyStringArraySchema("Server tool names or globs treated as mutating."),
			"parallel_safe_tools":    nonEmptyStringArraySchema("Server tool names or globs treated as parallel safe."),
			"include_tools":          nonEmptyStringArraySchema("Allowlist of server tool names or globs to expose."),
			"exclude_tools":          nonEmptyStringArraySchema("Denylist of server tool names or globs to hide."),
		},
		"required": []string{"id", "command"},
	}
}

func stringSchema(description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": description,
	}
}

func nonEmptyStringSchema(description string) map[string]any {
	schema := stringSchema(description)
	schema["minLength"] = 1
	return schema
}

func stringArraySchema(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       map[string]any{"type": "string"},
	}
}

func nonEmptyStringArraySchema(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       map[string]any{"type": "string", "minLength": 1},
	}
}

func pathArraySchema(description string) map[string]any {
	return nonEmptyStringArraySchema(description)
}

func stringMapSchema(description string) map[string]any {
	return map[string]any{
		"type":        "object",
		"description": description,
		"additionalProperties": map[string]any{
			"type": "string",
		},
	}
}

func boolSchema(description string) map[string]any {
	return map[string]any{
		"type":        "boolean",
		"description": description,
	}
}

func nonNegativeIntegerSchema(description string) map[string]any {
	return map[string]any{
		"type":        "integer",
		"description": description,
		"minimum":     0,
	}
}

func enumSchema(values []string, description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": description,
		"enum":        values,
	}
}
