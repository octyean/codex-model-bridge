package tools

import "strings"

const externalSkillViewToolName = "skill_view"

func convertExternalTool(name string) ([]convertedTool, bool) {
	switch name {
	case externalSkillViewToolName:
		return convertMCPResourceProxy(), true
	default:
		return nil, false
	}
}

func normalizeExternalToolSummary(name string, description string) (string, string) {
	if externalToolBaseName(name) == externalSkillViewToolName {
		return mcpResourceProxyToolName, "Read local skill files and linked skill resources through the unified context resource reader."
	}
	if externalToolBaseName(name) == "file_search" {
		return FileSearchToolName, "Search local workspace files by literal text, then read matching files with codex_context_resource."
	}
	return name, normalizeExternalToolDescription(description)
}

func normalizeExternalToolDescription(description string) string {
	description = strings.ReplaceAll(description, "Use skill_view(name) to load full content.", "Use codex_context_resource with action=read_local_file to read local skill files when a file path is available.")
	description = strings.ReplaceAll(description, "skill_view(name)", "codex_context_resource read_local_file")
	return description
}

func externalToolBaseName(name string) string {
	if index := strings.LastIndex(name, "__"); index >= 0 {
		return name[index+2:]
	}
	return name
}
