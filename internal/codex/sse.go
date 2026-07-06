package codex

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type SSEWriter struct {
	w              http.ResponseWriter
	sequenceNumber int
}

func NewSSEWriter(w http.ResponseWriter) *SSEWriter {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	return &SSEWriter{w: w}
}

func (s *SSEWriter) Event(event map[string]any) error {
	if sequenceNumber, ok := responseSequenceNumber(event["sequence_number"]); ok {
		if sequenceNumber >= s.sequenceNumber {
			s.sequenceNumber = sequenceNumber + 1
		}
	} else {
		event["sequence_number"] = s.sequenceNumber
		s.sequenceNumber++
	}
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

func responseSequenceNumber(value any) (int, bool) {
	switch n := value.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

func (s *SSEWriter) Comment(text string) error {
	text = strings.NewReplacer("\r", " ", "\n", " ").Replace(text)
	if _, err := fmt.Fprintf(s.w, ": %s\n\n", text); err != nil {
		return err
	}
	if flusher, ok := s.w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}
