package server

import (
	"time"

	"codex-bridge/internal/diagnostics"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/toollog"
	"codex-bridge/internal/tools"
)

func (s *Server) writeSessionLog(sessionID string, fileName string, record map[string]any) {
	if sessionID == "" || record == nil {
		return
	}
	path := toollog.ConfiguredPath()
	if path == "" {
		return
	}
	if _, ok := record["time"]; !ok {
		record["time"] = time.Now().Format(time.RFC3339Nano)
	}
	diagnostics.WriteSessionRecord(path, sessionID, fileName, record)
}

func (s *Server) writePromptRequest(sessionID string, requestID string, model string, upstreamModel string, profile string, stage string, body any, extra map[string]any) {
	record := sessionLogRecord("prompt_request", requestID, model, upstreamModel, profile, stage, body, extra)
	s.writeSessionLog(sessionID, "prompt-requests.jsonl", record)
}

func (s *Server) writePromptResponse(sessionID string, requestID string, model string, upstreamModel string, profile string, stage string, body any, extra map[string]any) {
	record := sessionLogRecord("prompt_response", requestID, model, upstreamModel, profile, stage, body, extra)
	s.writeSessionLog(sessionID, "prompt-responses.jsonl", record)
}

func (s *Server) writeBridgeResponse(sessionID string, requestID string, model string, upstreamModel string, profile string, body any, extra map[string]any) {
	record := sessionLogRecord("bridge_response", requestID, model, upstreamModel, profile, "", body, extra)
	s.writeSessionLog(sessionID, "bridge-responses.jsonl", record)
}

func (s *Server) writePromptFailure(sessionID string, requestID string, model string, upstreamModel string, profile string, stage string, message string, extra map[string]any) {
	body := failureLogBody(message, extra)
	s.writePromptResponse(sessionID, requestID, model, upstreamModel, profile, stage, body, extra)
}

func (s *Server) writeBridgeFailure(sessionID string, requestID string, model string, upstreamModel string, profile string, status int, message string, extra map[string]any) {
	body := failureLogBody(message, extra)
	body["status"] = status
	s.writeBridgeResponse(sessionID, requestID, model, upstreamModel, profile, body, extra)
}

func (s *Server) writePromptStreamEvent(sessionID string, requestID string, model string, upstreamModel string, profile string, stage string, sequence int, body any) {
	record := sessionLogRecord("prompt_stream_event", requestID, model, upstreamModel, profile, stage, body, map[string]any{"sequence": sequence})
	s.writeSessionLog(sessionID, "prompt-stream-events.jsonl", record)
}

func (s *Server) writeToolCatalog(sessionID string, requestID string, model string, upstreamModel string, profile string, chatTools []providers.ChatTool, toolCtx tools.Context, toolChoice any) {
	entries := make([]map[string]any, 0, len(chatTools))
	for _, tool := range chatTools {
		name := tool.Function.Name
		entry := toolCtx.Entry(name)
		entries = append(entries, map[string]any{
			"name":           name,
			"description":    tool.Function.Description,
			"parameters":     tool.Function.Parameters,
			"kind":           entry.Kind(),
			"original_name":  entry.OriginalName(),
			"original_type":  entry.OriginalType(),
			"namespace":      entry.Namespace,
			"side_effect":    entry.Descriptor.SideEffect,
			"argument_mode":  entry.ArgumentMode,
			"schema_quality": entry.SchemaQuality,
			"contract_id":    entry.ContractID(),
		})
	}
	record := sessionLogRecord("tool_catalog", requestID, model, upstreamModel, profile, "", entries, map[string]any{"tool_choice": toolChoice})
	s.writeSessionLog(sessionID, "tool-catalog.jsonl", record)
}

func sessionLogRecord(event string, requestID string, model string, upstreamModel string, profile string, stage string, body any, extra map[string]any) map[string]any {
	record := map[string]any{
		"event":          event,
		"request_id":     requestID,
		"model":          model,
		"upstream_model": upstreamModel,
		"profile":        profile,
	}
	if stage != "" {
		record["stage"] = stage
	}
	if body != nil {
		record["body"] = body
	}
	for key, value := range extra {
		record[key] = value
	}
	return record
}

func failureLogBody(message string, extra map[string]any) map[string]any {
	body := map[string]any{
		"ok":    false,
		"error": message,
	}
	for key, value := range extra {
		body[key] = value
	}
	return body
}
