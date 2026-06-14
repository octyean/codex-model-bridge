package codex

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type SSEWriter struct {
	w http.ResponseWriter
}

func NewSSEWriter(w http.ResponseWriter) *SSEWriter {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	return &SSEWriter{w: w}
}

func (s *SSEWriter) Event(event map[string]any) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", data); err != nil {
		return err
	}
	if flusher, ok := s.w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}
