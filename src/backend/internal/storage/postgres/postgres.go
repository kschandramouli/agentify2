package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	_ "github.com/lib/pq" // registers the "postgres" SQL driver
)

// Client wraps a PostgreSQL connection. A single Client (one connection pool)
// backs both store families for the MVP (see ADR 0010):
//   - the append-only "relational" store (events/certs) — Client itself,
//   - the "kv"/current-state store — via Client.CurrentStateStore().
type Client struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewClient opens the connection and initializes both schemas.
func NewClient(connStr string, logger *slog.Logger) (*Client, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping postgres: %w", err)
	}

	c := &Client{db: db, logger: logger}
	if err := c.initSchema(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	logger.Info("postgres client initialized")
	return c, nil
}

// initSchema creates both tables if they don't exist.
func (c *Client) initSchema(ctx context.Context) error {
	schema := `
	-- Append-only events/certs (relational store).
	CREATE TABLE IF NOT EXISTS events (
		id UUID PRIMARY KEY,
		pod_id TEXT NOT NULL,
		event_namespace TEXT NOT NULL,
		event_type TEXT NOT NULL,
		timestamp TIMESTAMP NOT NULL,
		payload JSONB NOT NULL,
		created_at TIMESTAMP DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_events_pod_id ON events(pod_id);
	CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp DESC);
	CREATE INDEX IF NOT EXISTS idx_events_namespace ON events(event_namespace);

	-- Current-state snapshots (kv store): latest value per (pod_id, entity_key).
	CREATE TABLE IF NOT EXISTS current_state (
		pod_id TEXT NOT NULL,
		entity_key TEXT NOT NULL,
		event_namespace TEXT,
		event_type TEXT,
		source TEXT,
		payload JSONB NOT NULL,
		updated_at TIMESTAMP DEFAULT NOW(),
		PRIMARY KEY (pod_id, entity_key)
	);
	CREATE INDEX IF NOT EXISTS idx_current_state_pod ON current_state(pod_id);
	`
	if _, err := c.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}
	c.logger.Info("postgres schema initialized")
	return nil
}

// CurrentStateStore returns the current-state ("kv") store backed by the same DB.
func (c *Client) CurrentStateStore() *CurrentState {
	return &CurrentState{db: c.db, logger: c.logger}
}

// --- Relational (append-only) store: Client itself ---

// Store inserts an event row (append-only).
func (c *Client) Store(ctx context.Context, podID string, data map[string]interface{}) (string, error) {
	id, ok := data["id"].(string)
	if !ok {
		return "", fmt.Errorf("missing id in data")
	}
	namespace, _ := data["event_namespace"].(string)
	eventType, _ := data["type"].(string)
	timestamp, ok := data["timestamp"].(string)
	if !ok {
		return "", fmt.Errorf("missing timestamp in data")
	}
	payloadJSON, err := marshalPayload(data)
	if err != nil {
		return "", err
	}

	const q = `
	INSERT INTO events (id, pod_id, event_namespace, event_type, timestamp, payload)
	VALUES ($1, $2, $3, $4, $5, $6)
	RETURNING id`
	var returnedID string
	if err := c.db.QueryRowContext(ctx, q, id, podID, namespace, eventType, timestamp, payloadJSON).Scan(&returnedID); err != nil {
		c.logger.Error("failed to store event", "error", err)
		return "", err
	}
	return returnedID, nil
}

// Query returns events for a pod. By default it returns the 100 most recent
// (recent-first) — which cert_check relies on. Optional params (spec 006) turn it
// into a time-windowed history read:
//   - since / until : RFC3339 bounds on the event timestamp
//   - entity        : restricts to rows whose payload pod_id/service matches
//   - type          : event_type filter
//   - order         : "asc" (chronological, for trend reading) | "desc" (default)
//   - limit         : default 100, capped at 1000
func (c *Client) Query(ctx context.Context, podID string, queryParams map[string]interface{}) ([]map[string]interface{}, error) {
	q := `SELECT id, pod_id, event_namespace, event_type, timestamp, payload
	FROM events WHERE pod_id = $1`
	args := []interface{}{podID}

	addArg := func(clause string, val interface{}) {
		args = append(args, val)
		q += fmt.Sprintf(" AND %s $%d", clause, len(args))
	}
	if v := stringParam(queryParams, "since"); v != "" {
		addArg("timestamp >=", v)
	}
	if v := stringParam(queryParams, "until"); v != "" {
		addArg("timestamp <=", v)
	}
	if v := stringParam(queryParams, "type"); v != "" {
		addArg("event_type =", v)
	}
	if v := entityParam(queryParams); v != "" {
		// Match the entity against pod_id, service, or deployment in the JSONB payload.
		args = append(args, v)
		n := len(args)
		q += fmt.Sprintf(" AND (payload->>'pod_id' = $%d OR payload->>'service' = $%d OR payload->>'deployment' = $%d)", n, n, n)
	}

	q += " ORDER BY timestamp " + sqlOrder(queryParams)
	q += fmt.Sprintf(" LIMIT %d", limitParam(queryParams))

	rows, err := c.db.QueryContext(ctx, q, args...)
	if err != nil {
		c.logger.Error("failed to query events", "error", err)
		return nil, err
	}
	defer rows.Close()

	results := []map[string]interface{}{}
	for rows.Next() {
		var id, pid, namespace, eventType, timestamp string
		var payload []byte
		if err := rows.Scan(&id, &pid, &namespace, &eventType, &timestamp, &payload); err != nil {
			c.logger.Error("failed to scan row", "error", err)
			continue
		}
		results = append(results, map[string]interface{}{
			"id":              id,
			"pod_id":          pid,
			"event_namespace": namespace,
			"type":            eventType,
			"timestamp":       timestamp,
			"payload":         decodePayload(payload), // map, not string — Tier-1/redaction expect a map
		})
	}
	return results, rows.Err()
}

// PurgeOlderThan deletes events whose event timestamp is older than cutoff and
// returns the number of rows removed (ADR 0015). Only the append-only events table
// is purged; current_state (latest-wins) is never touched.
func (c *Client) PurgeOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := c.db.ExecContext(ctx,
		`DELETE FROM events WHERE timestamp < $1`, cutoff.UTC().Format(time.RFC3339))
	if err != nil {
		c.logger.Error("failed to purge old events", "error", err)
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// HealthCheck verifies the connection.
func (c *Client) HealthCheck(ctx context.Context) error { return c.db.PingContext(ctx) }

// Close closes the shared connection pool (owns the DB lifecycle).
func (c *Client) Close() error { return c.db.Close() }

// --- Current-state ("kv") store ---

// CurrentState is the current-state store: latest value per (pod_id, entity_key).
// It shares the parent Client's *sql.DB, so its Close is a no-op.
type CurrentState struct {
	db     *sql.DB
	logger *slog.Logger
}

// Store upserts the latest state for an entity (latest-wins).
func (s *CurrentState) Store(ctx context.Context, podID string, data map[string]interface{}) (string, error) {
	entityKey, _ := data["entity_key"].(string)
	if entityKey == "" {
		entityKey, _ = data["id"].(string) // ingester sets id == entity key for kv
	}
	if entityKey == "" {
		return "", fmt.Errorf("missing entity_key for current-state store")
	}
	namespace, _ := data["event_namespace"].(string)
	eventType, _ := data["type"].(string)
	source, _ := data["source"].(string)
	payloadJSON, err := marshalPayload(data)
	if err != nil {
		return "", err
	}

	const q = `
	INSERT INTO current_state (pod_id, entity_key, event_namespace, event_type, source, payload, updated_at)
	VALUES ($1, $2, $3, $4, $5, $6, NOW())
	ON CONFLICT (pod_id, entity_key) DO UPDATE SET
		event_namespace = EXCLUDED.event_namespace,
		event_type      = EXCLUDED.event_type,
		source          = EXCLUDED.source,
		payload         = EXCLUDED.payload,
		updated_at      = NOW()`
	if _, err := s.db.ExecContext(ctx, q, podID, entityKey, namespace, eventType, source, payloadJSON); err != nil {
		s.logger.Error("failed to upsert current_state", "error", err)
		return "", err
	}
	return fmt.Sprintf("%s:%s", podID, entityKey), nil
}

// Query does a point lookup when given "key", else scans all entities in the pod.
func (s *CurrentState) Query(ctx context.Context, podID string, queryParams map[string]interface{}) ([]map[string]interface{}, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if key, ok := queryParams["key"].(string); ok && key != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT entity_key, event_namespace, event_type, source, payload, updated_at
			 FROM current_state WHERE pod_id = $1 AND entity_key = $2`, podID, key)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT entity_key, event_namespace, event_type, source, payload, updated_at
			 FROM current_state WHERE pod_id = $1`, podID)
	}
	if err != nil {
		s.logger.Error("failed to query current_state", "error", err)
		return nil, err
	}
	defer rows.Close()

	results := []map[string]interface{}{}
	for rows.Next() {
		var entityKey, namespace, eventType, source, updatedAt string
		var payload []byte
		if err := rows.Scan(&entityKey, &namespace, &eventType, &source, &payload, &updatedAt); err != nil {
			s.logger.Error("failed to scan current_state row", "error", err)
			continue
		}
		results = append(results, map[string]interface{}{
			"entity_key":      entityKey,
			"event_namespace": namespace,
			"type":            eventType,
			"source":          source,
			"timestamp":       updatedAt,
			"payload":         decodePayload(payload),
		})
	}
	return results, rows.Err()
}

// TrackedEntities returns all known namespace/entity pairs from the live-state
// shards — used to power the frontend autocomplete. Each entry is formatted as
// "namespace/entity_key" (e.g. "payments/payment-worker").
func (s *CurrentState) TrackedEntities(ctx context.Context) ([]string, error) {
	const q = `
	SELECT pod_id, entity_key
	FROM current_state
	WHERE pod_id LIKE 'k8fy.live-state.%'
	ORDER BY pod_id, entity_key`

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []string
	for rows.Next() {
		var podID, entityKey string
		if err := rows.Scan(&podID, &entityKey); err != nil {
			continue
		}
		// "k8fy.live-state.payments" → "payments"
		ns := strings.TrimPrefix(podID, "k8fy.live-state.")
		if ns != "" && entityKey != "" {
			result = append(result, ns+"/"+entityKey)
		}
	}
	return result, rows.Err()
}

// HealthCheck verifies the connection.
func (s *CurrentState) HealthCheck(ctx context.Context) error { return s.db.PingContext(ctx) }

// Close is a no-op: the parent Client owns the shared connection pool.
func (s *CurrentState) Close() error { return nil }

// --- helpers ---

// marshalPayload returns the JSON text of data["payload"] (a map), or of the
// whole record if no nested payload is present. Returned as a string so lib/pq
// stores it in a JSONB column (a []byte would be sent as bytea).
func marshalPayload(data map[string]interface{}) (string, error) {
	payload, ok := data["payload"].(map[string]interface{})
	if !ok {
		payload = data
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}
	return string(b), nil
}

// stringParam reads a string query parameter, or "" if absent/not a string.
func stringParam(params map[string]interface{}, name string) string {
	if v, ok := params[name].(string); ok {
		return v
	}
	return ""
}

// entityParam reads the entity to filter a time-series to, accepting the common
// aliases the agent/handlers use ("entity", "pod_id", "service").
func entityParam(params map[string]interface{}) string {
	for _, k := range []string{"entity", "pod_id", "service", "deployment"} {
		if v := stringParam(params, k); v != "" {
			return v
		}
	}
	return ""
}

// sqlOrder returns "ASC" only when explicitly requested, else "DESC" — preserving
// the recent-first default that cert_check depends on.
func sqlOrder(params map[string]interface{}) string {
	if strings.EqualFold(stringParam(params, "order"), "asc") {
		return "ASC"
	}
	return "DESC"
}

// limitParam returns a row cap: the requested limit clamped to [1,1000], else 100.
func limitParam(params map[string]interface{}) int {
	n := 0
	switch v := params["limit"].(type) {
	case int:
		n = v
	case int64:
		n = int(v)
	case float64: // JSON numbers decode to float64
		n = int(v)
	}
	if n <= 0 {
		return 100
	}
	if n > 1000 {
		return 1000
	}
	return n
}

// decodePayload turns a JSONB column back into a map (so Tier-1 and the redactor
// can read fields); falls back to the raw string on error.
func decodePayload(b []byte) interface{} {
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return string(b)
	}
	return m
}
