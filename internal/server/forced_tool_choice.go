package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/tools"
)

func (s *Server) writeForcedLocalToolChoice(w http.ResponseWriter, requestID string, req codex.ResponsesRequest, toolCtx tools.Context, adapter adapters.Adapter) bool {
	if adapter.ToolPolicy().RequiredToolChoice || inputHasToolOutput(req.Input) {
		return false
	}
	name, arguments, ok := tools.ForcedToolChoiceLocalCall(req.ToolChoice, toolCtx)
	if !ok {
		return false
	}
	callID := "call_forced_" + strings.ReplaceAll(name, "-", "_")
	item := codex.ResponseItem{
		"id":        toolItemID("function_call", callID),
		"type":      "function_call",
		"call_id":   callID,
		"name":      name,
		"arguments": arguments,
		"status":    "completed",
	}
	s.logger.Info("forced_tool_choice_local_call",
		slog.String("request_id", requestID),
		slog.String("model", req.Model),
		slog.String("tool", name),
	)
	if req.Stream {
		writeForcedLocalToolChoiceStream(w, requestID, req, item)
		return true
	}
	writeJSON(w, http.StatusOK, codex.ResponseObject{
		ID:        "resp_" + requestID,
		Object:    "response",
		CreatedAt: time.Now().Unix(),
		Model:     req.Model,
		Status:    "completed",
		Output:    []codex.ResponseItem{item},
	})
	return true
}

func writeForcedLocalToolChoiceStream(w http.ResponseWriter, requestID string, req codex.ResponsesRequest, item codex.ResponseItem) {
	writer := codex.NewSSEWriter(w)
	respID := "resp_" + requestID
	createdAt := time.Now().Unix()
	_ = writer.Event(map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id": respID, "object": "response", "created_at": createdAt, "model": req.Model, "status": "in_progress", "output": []any{},
		},
	})
	_ = writer.Event(map[string]any{
		"type": "response.in_progress",
		"response": map[string]any{
			"id": respID, "object": "response", "created_at": createdAt, "model": req.Model, "status": "in_progress", "output": []any{},
		},
	})
	for _, event := range outputDoneEvents(item, 0, false) {
		_ = writer.Event(event)
	}
	_ = writer.Event(map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": respID, "object": "response", "created_at": createdAt, "model": req.Model, "status": "completed", "output": []codex.ResponseItem{item},
		},
	})
}

func inputHasToolOutput(input json.RawMessage) bool {
	var items []map[string]any
	if err := json.Unmarshal(input, &items); err != nil {
		return false
	}
	for _, item := range items {
		itemType, _ := item["type"].(string)
		if strings.HasSuffix(itemType, "_output") {
			return true
		}
	}
	return false
}
