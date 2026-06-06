// Package retention runs the events-table retention janitor (ADR 0015): a
// background ticker that deletes events older than a configured window. It bounds
// table age, not ingestion rate — see ADR 0015 for that distinction.
package retention

import (
	"context"
	"log/slog"
	"time"

	"github.com/chan/agentify/backend/internal/telemetry"
)

// Purger deletes events older than a cutoff and reports how many were removed.
// Implemented by the Postgres events client.
type Purger interface {
	PurgeOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// Janitor periodically purges old events.
type Janitor struct {
	purger   Purger
	window   time.Duration
	interval time.Duration
	logger   *slog.Logger
}

// New builds a Janitor. days<=0 disables retention (returns nil).
func New(purger Purger, days, intervalMinutes int, logger *slog.Logger) *Janitor {
	if days <= 0 || purger == nil {
		return nil
	}
	if intervalMinutes <= 0 {
		intervalMinutes = 60
	}
	return &Janitor{
		purger:   purger,
		window:   time.Duration(days) * 24 * time.Hour,
		interval: time.Duration(intervalMinutes) * time.Minute,
		logger:   logger,
	}
}

// Run blocks, purging on each tick until ctx is cancelled. Intended for a
// goroutine. A nil receiver is a no-op (retention disabled).
func (j *Janitor) Run(ctx context.Context) {
	if j == nil {
		return
	}
	j.logger.Info("events retention janitor started",
		"window", j.window.String(), "interval", j.interval.String())

	// Purge once at startup so a long-idle process doesn't wait a full interval.
	j.purgeOnce(ctx)

	ticker := time.NewTicker(j.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			j.logger.Info("events retention janitor stopping")
			return
		case <-ticker.C:
			j.purgeOnce(ctx)
		}
	}
}

func (j *Janitor) purgeOnce(ctx context.Context) {
	cutoff := time.Now().Add(-j.window)
	n, err := j.purger.PurgeOlderThan(ctx, cutoff)
	if err != nil {
		j.logger.Error("events retention purge failed", "error", err)
		return
	}
	telemetry.EventsPurgedTotal.Add(float64(n))
	if n > 0 {
		j.logger.Info("events retention purge complete", "deleted", n, "older_than", cutoff.UTC().Format(time.RFC3339))
	}
}
