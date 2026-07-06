package adapters

type openAIAdapter struct{ defaultAdapter }

func (openAIAdapter) Name() string {
	return OpenAIName
}

func (openAIAdapter) Capabilities() Capabilities {
	return Capabilities{
		InputModalities:             []string{"text", "image"},
		SupportsImageDetailOriginal: true,
		SupportsSearchTool:          true,
		ExperimentalSupportedTools:  []string{"function", "custom", "apply_patch", "tool_search", "local_shell"},
	}
}

func (openAIAdapter) ToolPolicy() ToolPolicy {
	return ToolPolicy{}
}
