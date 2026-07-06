package server

func responseCreatedEvent(respID string, createdAt int64, model string) map[string]any {
	return map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id": respID, "object": "response", "created_at": createdAt, "model": model, "status": "in_progress", "output": []any{},
		},
	}
}

func responseInProgressEvent(respID string, createdAt int64, model string) map[string]any {
	return map[string]any{
		"type": "response.in_progress",
		"response": map[string]any{
			"id": respID, "object": "response", "created_at": createdAt, "model": model, "status": "in_progress", "output": []any{},
		},
	}
}
