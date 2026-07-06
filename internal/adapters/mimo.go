package adapters

import (
	"strings"

	"codex-bridge/internal/providers"
)

const mimoStructuredOutputNote = `MIMO_CODEX_STRUCTURED_OUTPUT
The final assistant content must be one valid JSON object matching the requested response_format schema.
Use exactly the JSON property names defined in the schema. Do not invent or rename keys.
Do not include markdown, code fences, explanations, metadata, or any text outside the JSON object.`

type mimoAdapter struct{ defaultAdapter }

func (mimoAdapter) Name() string {
	return MimoName
}

func (mimoAdapter) Capabilities() Capabilities {
	return Capabilities{
		InputModalities:             []string{"text", "image"},
		SupportsImageDetailOriginal: true,
		SupportsSearchTool:          true,
		ExperimentalSupportedTools:  []string{"function", "custom", "apply_patch", "tool_search", "local_shell"},
	}
}

func (mimoAdapter) PrepareChatRequest(req providers.ChatCompletionRequest) providers.ChatCompletionRequest {
	if req.ResponseFormat != nil && !hasMimoStructuredOutputNote(req.Messages) {
		req.Messages = append([]providers.ChatMessage{{
			Role:    "system",
			Content: mimoStructuredOutputInstruction(req.ResponseFormat),
		}}, req.Messages...)
	}
	return defaultAdapter{}.PrepareChatRequest(req)
}

func mimoStructuredOutputInstruction(responseFormat any) string {
	schema := responseFormatSchema(responseFormat)
	if schema == "" {
		return mimoStructuredOutputNote
	}
	return mimoStructuredOutputNote + "\nJSON schema:\n" + schema
}

func responseFormatSchema(responseFormat any) string {
	format, ok := responseFormat.(map[string]any)
	if !ok {
		return ""
	}
	jsonSchema, ok := format["json_schema"].(map[string]any)
	if !ok {
		return ""
	}
	return canonicalJSON(jsonSchema["schema"])
}

func (mimoAdapter) PrepareResponseRequest(req map[string]any) map[string]any {
	return req
}

func hasMimoStructuredOutputNote(messages []providers.ChatMessage) bool {
	for _, message := range messages {
		if message.Role != "system" {
			continue
		}
		if text, ok := message.Content.(string); ok && strings.Contains(text, "MIMO_CODEX_STRUCTURED_OUTPUT") {
			return true
		}
	}
	return false
}
