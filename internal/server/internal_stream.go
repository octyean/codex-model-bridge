package server

import (
	"net/http"
	"time"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/optimization"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/tools"
)

func (s *Server) streamInternalToolResponse(w http.ResponseWriter, r *http.Request, requestID string, req codex.ResponsesRequest, chatReq providers.ChatCompletionRequest, provider providers.ChatProvider, toolCtx tools.Context, adapter adapters.Adapter, profile string, shape optimization.Shape) {
	chatReq.Stream = false
	chatReq.StreamOptions = nil
	resp, err := provider.Create(r.Context(), chatReq)
	if err != nil {
		s.logger.Error("upstream_failed", "request_id", requestID, "error", err.Error())
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if len(resp.Choices) == 0 {
		writeError(w, http.StatusBadGateway, "upstream returned no choices")
		return
	}
	if followUp, followUpReq, ok := s.resolveInternalTools(r.Context(), provider, chatReq, resp.Choices[0].Message); ok {
		resp = followUp
		shape = optimization.CaptureShape(followUpReq)
		if len(resp.Choices) == 0 {
			writeError(w, http.StatusBadGateway, "upstream returned no choices")
			return
		}
	}
	items := responseItemsFromMessage(resp.Choices[0].Message, toolCtx, adapter, requestID, s.logger)
	usage := providers.NormalizeUsage(resp.Usage)
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
	for index, item := range items {
		itemID, _ := item["id"].(string)
		if itemID == "" {
			itemID = "msg_0"
			item["id"] = itemID
		}
		_ = writer.Event(map[string]any{
			"type":         "response.output_item.added",
			"item":         item,
			"output_index": index,
		})
		for _, event := range toolDoneEvents(item) {
			event["output_index"] = index
			_ = writer.Event(event)
		}
	}
	_ = writer.Event(map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": respID, "object": "response", "created_at": createdAt, "model": req.Model, "status": "completed", "output": items,
			"usage": codexUsage(usage),
		},
	})
	s.logUsage(requestID, req.Model, profile, adapter, shape, usage)
}
