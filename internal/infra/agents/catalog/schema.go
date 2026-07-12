package catalog

func AgentDSLJSONSchema() map[string]any {
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"title":                "yagent agent DSL",
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"id":                        nonEmptyStringSchema("Agent id used by the planner, handoff, and audit output."),
			"name":                      stringSchema("Human readable display name. Defaults to id."),
			"description":               stringSchema("Short description shown to humans and the planner."),
			"instruction":               stringSchema("Additional instruction always given to this agent."),
			"mode":                      enumSchema([]string{"manager", "tool", "handoff"}, "How the orchestrator may use this agent."),
			"allowed_tools":             nonEmptyStringArraySchema("Tool names or globs this agent may use."),
			"read_only":                 boolSchema("Whether this agent should be treated as read-only."),
			"input_schema":              openObjectSchema("Optional JSON Schema for this agent's expected input."),
			"output_schema":             openObjectSchema("Optional JSON Schema for this agent's expected output."),
			"model":                     stringSchema("Model override for this agent."),
			"routing_profile":           stringSchema("Routing profile override for this agent."),
			"timeout":                   durationSchema("LLM call timeout as a Go duration string such as 30s or 2m."),
			"max_turns":                 nonNegativeIntegerSchema("Maximum continuation turns for this agent. Zero uses the orchestrator default."),
			"max_tool_calls":            nonNegativeIntegerSchema("Maximum tool calls for this agent. Zero uses the orchestrator default, except tool-free planner contracts."),
			"token_budget":              nonNegativeIntegerSchema("Approximate context packet token budget for this agent. Zero means unlimited."),
			"tags":                      nonEmptyStringArraySchema("Human-oriented labels."),
			"task_kinds":                enumArraySchema([]string{"unknown", "casual", "question", "research", "docs", "review", "test", "mutate"}, "Task kinds this agent is a good fit for."),
			"capabilities":              nonEmptyStringArraySchema("Planner-visible capability labels."),
			"preferred_phases":          enumArraySchema([]string{"intake", "plan", "execute", "verify", "recover", "finalize"}, "Run phases this agent is a good fit for."),
			"scope_hints":               nonEmptyStringArraySchema("Planner-visible scope hints."),
			"verification_required":     boolSchema("Whether this agent requires verification when selected as primary."),
			"verification_max_attempts": nonNegativeIntegerSchema("Maximum verification attempts for this agent. Zero uses the default."),
		},
		"required": []string{"id"},
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

func durationSchema(description string) map[string]any {
	schema := stringSchema(description)
	schema["pattern"] = `^([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+$`
	return schema
}

func nonEmptyStringArraySchema(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       map[string]any{"type": "string", "minLength": 1},
	}
}

func enumArraySchema(values []string, description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       enumSchema(values, ""),
	}
}

func enumSchema(values []string, description string) map[string]any {
	schema := map[string]any{
		"type": "string",
		"enum": values,
	}
	if description != "" {
		schema["description"] = description
	}
	return schema
}

func openObjectSchema(description string) map[string]any {
	return map[string]any{
		"type":        "object",
		"description": description,
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
