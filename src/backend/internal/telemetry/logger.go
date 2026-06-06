package telemetry

import (
	"log/slog"
	"os"
)

// NewLogger creates a structured JSON logger for CloudWatch.
func NewLogger() *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}
	handler := slog.NewJSONHandler(os.Stdout, opts)
	return slog.New(handler)
}
