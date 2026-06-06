package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/chan/agentify/backend/internal/telemetry"
)

// statusRecorder captures the response status code for logging + metrics.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Middleware wraps the HTTP handler with logging and metrics.
func NewMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		logger.Info("request started", "method", r.Method, "path", r.URL.Path)

		// TODO: add auth middleware
		// TODO: add rate limiting middleware

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		// Record metrics. r.URL.Path is bounded (routes carry no path-param ids — ADR 0011).
		elapsed := time.Since(start)
		telemetry.HTTPRequestsTotal.WithLabelValues(r.Method, r.URL.Path, strconv.Itoa(rec.status)).Inc()
		telemetry.HTTPRequestDuration.WithLabelValues(r.Method, r.URL.Path).Observe(elapsed.Seconds())

		logger.Info("request completed",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"latency_ms", elapsed.Milliseconds(),
		)
	})
}
