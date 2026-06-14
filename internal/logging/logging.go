package logging

import (
	"io"
	"log/slog"
)

func New(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{}))
}
