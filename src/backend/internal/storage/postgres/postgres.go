package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	_ "github.com/lib/pq" // registers the "postgres" SQL driver
)

// k8sPodSuffix matches the two trailing hash segments K8s appends to pod names:
// {deployment}-{rs-hash(6-12 hex chars)}-{pod-hash(5 alphanum)}.
// Stripping them recovers the deployment name (e.g. payment-worker-68795899ff-kngf7 → payment-worker).
var k8sPodSuffix = regexp.MustCompile(`-[a-z0-9]{6,12}-[a-z0-9]{5}$`)

// Client wraps a PostgreSQL connection. A single Client (one connection pool)
// backs both store families for the MVP (see ADR 0010):
//   - the append-only "relational" store (events/certs) — Client itself,
//   - the "kv"/current-state store — via Client.CurrentStateStore().
type Client struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewClient opens the connection and initializes both schemas.
// NewClient opens the connection and initializes both schemas.
//
// ctx controls how long to wait for the initial Postgres ping. Pass a context
// with a generous deadline in production (e.g. 3 minutes) so the backend
// survives the 30–60 s window where AWS RDS is marked "available" but not yet
// accepting TCP connections — the common cause of both the namespace autocomplete
// and Query History being empty after a scale-up / resume cycle.
//
// Pass a context with a short deadline (e.g. 5 s) in tests so a missing Postgres
// instance returns an error quickly and the test can call t.Skip().
func NewClient(ctx context.Context, connStr string, logger *slog.Logger) (*Client, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres: %w", err)
	}

	// Retry the initial ping at 15 s intervals until the context is cancelled.
	// Each failure is logged at WARN so the cause is visible in CloudWatch.
	const retryInterval = 15 * time.Second
	var pingErr error
	for attempt := 1; ; attempt++ {
		if pingErr = db.PingContext(ctx); pingErr == nil {
			break
		}
		// If the context deadline was exceeded or cancelled, give up immediately.
		if ctx.Err() != nil {
			_ = db.Close()
			return nil, fmt.Errorf("postgres unavailable (context: %w): %v", ctx.Err(), pingErr)
		}
		logger.Warn("postgres not ready, will retry",
			"attempt", attempt, "error", pingErr)
		select {
		case <-ctx.Done():
			_ = db.Close()
			return nil, fmt.Errorf("postgres unavailable (context: %w): %v", ctx.Err(), pingErr)
		case <-time.After(retryInterval):
		}
	}

	c := &Client{db: db, logger: logger}
	if err := c.initSchema(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	logger.Info("postgres client initialized")
	return c, nil
}

// initSchema creates all tables and extensions if they don't exist.
func (c *Client) initSchema(ctx context.Context) error {
	schema := `
	-- pgvector: enable vector similarity search (P8 — semantic memory layer).
	-- Wrapped in DO $$ so the error is swallowed when the OS package is absent
	-- (e.g. embedded-postgres in CI tests which don't ship pgvector).
	DO $$ BEGIN
		CREATE EXTENSION IF NOT EXISTS vector;
	EXCEPTION WHEN OTHERS THEN NULL; END $$;

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

	-- Query traces: persisted per-query provenance records (spec 004).
	CREATE TABLE IF NOT EXISTS traces (
		id TEXT PRIMARY KEY,
		trace_id TEXT NOT NULL,
		question TEXT NOT NULL,
		intent TEXT NOT NULL DEFAULT '',
		namespace TEXT NOT NULL DEFAULT '',
		tier TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT '',
		confidence FLOAT NOT NULL DEFAULT 0,
		sources JSONB NOT NULL DEFAULT '[]',
		tool_calls JSONB NOT NULL DEFAULT '[]',
		latency_ms BIGINT NOT NULL DEFAULT 0,
		started_at TIMESTAMP,
		created_at TIMESTAMP DEFAULT NOW(),
		input_tokens BIGINT NOT NULL DEFAULT 0,
		output_tokens BIGINT NOT NULL DEFAULT 0,
		estimated_cost_usd FLOAT NOT NULL DEFAULT 0
	);
	-- Idempotent migrations: add new columns to pre-existing tables.
	ALTER TABLE IF EXISTS traces ADD COLUMN IF NOT EXISTS started_at TIMESTAMP;
	ALTER TABLE IF EXISTS traces ADD COLUMN IF NOT EXISTS input_tokens BIGINT NOT NULL DEFAULT 0;
	ALTER TABLE IF EXISTS traces ADD COLUMN IF NOT EXISTS output_tokens BIGINT NOT NULL DEFAULT 0;
	ALTER TABLE IF EXISTS traces ADD COLUMN IF NOT EXISTS estimated_cost_usd FLOAT NOT NULL DEFAULT 0;
	ALTER TABLE IF EXISTS traces ADD COLUMN IF NOT EXISTS cache_creation_input_tokens BIGINT NOT NULL DEFAULT 0;
	ALTER TABLE IF EXISTS traces ADD COLUMN IF NOT EXISTS cache_read_input_tokens BIGINT NOT NULL DEFAULT 0;
	CREATE INDEX IF NOT EXISTS idx_traces_created ON traces(created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_traces_intent  ON traces(intent);

	-- Admin integrations: configured K8s adapter connections.
	CREATE TABLE IF NOT EXISTS integrations (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		adapter_url TEXT NOT NULL,
		namespaces JSONB NOT NULL DEFAULT '[]',
		status TEXT NOT NULL DEFAULT 'inactive',
		token TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMP DEFAULT NOW(),
		updated_at TIMESTAMP DEFAULT NOW()
	);

	-- Multi-turn chat sessions: persists conversation history across pod restarts.
	CREATE TABLE IF NOT EXISTS chat_sessions (
		id                 TEXT PRIMARY KEY,
		title              TEXT NOT NULL DEFAULT '',
		namespace          TEXT NOT NULL DEFAULT '',
		service            TEXT NOT NULL DEFAULT '',
		messages           JSONB NOT NULL DEFAULT '[]',
		context_cache      JSONB NOT NULL DEFAULT '{}',
		context_fetched_at TIMESTAMP,
		created_at         TIMESTAMP DEFAULT NOW(),
		last_active        TIMESTAMP DEFAULT NOW(),
		expires_at         TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_chat_sessions_active ON chat_sessions(last_active DESC);

	-- Model pricing: retail $/MTok rates shown in Admin UI and used for trace cost estimates.
	-- Seeded with Anthropic list prices (June 2026). cache_write_per_mtok = 5-min TTL rate.
	CREATE TABLE IF NOT EXISTS model_pricing (
		model_id             TEXT PRIMARY KEY,
		display_name         TEXT NOT NULL DEFAULT '',
		input_per_mtok       FLOAT NOT NULL DEFAULT 0,
		output_per_mtok      FLOAT NOT NULL DEFAULT 0,
		cache_write_per_mtok FLOAT NOT NULL DEFAULT 0,
		cache_read_per_mtok  FLOAT NOT NULL DEFAULT 0,
		updated_at           TIMESTAMP DEFAULT NOW()
	);
	INSERT INTO model_pricing (model_id, display_name, input_per_mtok, output_per_mtok, cache_write_per_mtok, cache_read_per_mtok) VALUES
		('claude-fable-5',    'Claude Fable 5',    10.0, 50.0, 12.50, 1.00),
		('claude-mythos-5',   'Claude Mythos 5',   10.0, 50.0, 12.50, 1.00),
		('claude-opus-4-8',   'Claude Opus 4.8',    5.0, 25.0,  6.25, 0.50),
		('claude-opus-4-7',   'Claude Opus 4.7',    5.0, 25.0,  6.25, 0.50),
		('claude-opus-4-6',   'Claude Opus 4.6',    5.0, 25.0,  6.25, 0.50),
		('claude-opus-4-5',   'Claude Opus 4.5',    5.0, 25.0,  6.25, 0.50),
		('claude-sonnet-4-6', 'Claude Sonnet 4.6',  3.0, 15.0,  3.75, 0.30),
		('claude-sonnet-4-5', 'Claude Sonnet 4.5',  3.0, 15.0,  3.75, 0.30),
		('claude-haiku-4-5',  'Claude Haiku 4.5',   1.0,  5.0,  1.25, 0.10),
		('claude-haiku-3-5',  'Claude Haiku 3.5',   0.8,  4.0,  1.00, 0.08)
	ON CONFLICT (model_id) DO NOTHING;

	-- Semantic memory base table (always created — no pgvector type here).
	-- The embedding column is added below only when pgvector is available.
	CREATE TABLE IF NOT EXISTS incident_embeddings (
		id          TEXT PRIMARY KEY,
		trace_id    TEXT NOT NULL REFERENCES traces(id) ON DELETE CASCADE,
		namespace   TEXT NOT NULL DEFAULT '',
		service     TEXT NOT NULL DEFAULT '',
		summary     TEXT NOT NULL DEFAULT '',
		created_at  TIMESTAMP DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_incident_embeddings_ns_svc
		ON incident_embeddings (namespace, service);

	-- Add the vector column + IVFFlat index only when pgvector is installed.
	-- Silently skipped on embedded-postgres (CI tests) which don't ship pgvector.
	DO $$
	BEGIN
		-- Add embedding column (vector(512) = voyage-3-lite dimensions)
		IF NOT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'incident_embeddings' AND column_name = 'embedding'
		) THEN
			EXECUTE 'ALTER TABLE incident_embeddings ADD COLUMN embedding vector(512)';
		END IF;
		-- IVFFlat cosine index for fast similarity search
		IF NOT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE tablename = 'incident_embeddings' AND indexname = 'idx_incident_embeddings_vec'
		) THEN
			EXECUTE 'CREATE INDEX idx_incident_embeddings_vec
				ON incident_embeddings USING ivfflat (embedding vector_cosine_ops)
				WITH (lists = 10)';
		END IF;
	EXCEPTION WHEN OTHERS THEN
		NULL;  -- pgvector not available — vector search disabled, keyword fallback active
	END $$;
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
	} else if v := stringParam(queryParams, "namespace"); v != "" {
		// No entity filter but namespace present — filter by namespace in payload.
		// Used for cert queries: cert payloads have namespace but no service/pod_id.
		addArg("payload->>'namespace' =", v)
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
//
// Per-pod TTLs: high-frequency pods (metrics, certificates) use a shorter window
// than sparse event pods, keeping storage bounded at ~20k rows steady-state.
func (c *Client) PurgeOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	var total int64

	// High-frequency pods accumulate ~2,880 rows/day at 30s scrape intervals.
	// 7 days is enough for trend analysis; live-state is a latest-wins snapshot
	// so 2 days of history is more than sufficient.
	type podWindow struct {
		podID  string
		cutoff time.Time
	}
	windows := []podWindow{
		{"k8fy.metrics",      time.Now().Add(-7 * 24 * time.Hour)},
		{"k8fy.certificates", time.Now().Add(-7 * 24 * time.Hour)},
	}
	for _, w := range windows {
		res, err := c.db.ExecContext(ctx,
			`DELETE FROM events WHERE pod_id = $1 AND timestamp < $2`,
			w.podID, w.cutoff.UTC().Format(time.RFC3339))
		if err != nil {
			c.logger.Error("failed to purge events", "pod_id", w.podID, "error", err)
			continue
		}
		n, _ := res.RowsAffected()
		total += n
	}

	// All other pods use the caller-supplied cutoff (EVENTS_RETENTION_DAYS env var).
	res, err := c.db.ExecContext(ctx,
		`DELETE FROM events WHERE pod_id NOT IN ('k8fy.metrics','k8fy.certificates')
		 AND timestamp < $1`, cutoff.UTC().Format(time.RFC3339))
	if err != nil {
		c.logger.Error("failed to purge old events", "error", err)
		return total, err
	}
	n, _ := res.RowsAffected()
	total += n
	return total, nil
}

// HealthCheck verifies the connection.
func (c *Client) HealthCheck(ctx context.Context) error { return c.db.PingContext(ctx) }

// Close closes the shared connection pool (owns the DB lifecycle).
func (c *Client) Close() error { return c.db.Close() }

// --- Traces (query history) ---

// TraceRecord is one persisted query provenance entry.
type TraceRecord struct {
	ID                       string
	TraceID                  string
	Question                 string
	Intent                   string
	Namespace                string
	Tier                     string
	Status                   string
	Confidence               float64
	Sources                  []string
	ToolCalls                []string
	LatencyMs                int64
	StartedAt                time.Time
	CreatedAt                time.Time
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	EstimatedCostUSD         float64
}

// TracesSummary holds aggregated statistics derived from the traces table.
type TracesSummary struct {
	TotalQueries      int64
	QueriesByTier     map[string]int64
	QueriesByStatus   map[string]int64
	QueriesByIntent   map[string]int64
	AvgAgentLatencyMs float64
	P95AgentLatencyMs float64
	Last24hCount      int64
}

// InsertTrace persists one query trace row. Errors are logged by the caller.
func (c *Client) InsertTrace(ctx context.Context, t TraceRecord) error {
	srcJSON, _ := json.Marshal(t.Sources)
	tcJSON, _ := json.Marshal(t.ToolCalls)
	startedAt := t.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	_, err := c.db.ExecContext(ctx,
		`INSERT INTO traces (id, trace_id, question, intent, namespace, tier, status,
		  confidence, sources, tool_calls, latency_ms, started_at, created_at,
		  input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens,
		  estimated_cost_usd)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NOW(),$13,$14,$15,$16,$17)
		 ON CONFLICT (id) DO NOTHING`,
		t.ID, t.TraceID, t.Question, t.Intent, t.Namespace, t.Tier, t.Status,
		t.Confidence, srcJSON, tcJSON, t.LatencyMs, startedAt,
		t.InputTokens, t.OutputTokens,
		t.CacheCreationInputTokens, t.CacheReadInputTokens,
		t.EstimatedCostUSD)
	return err
}

const traceSelectCols = `
	SELECT id, trace_id, question, intent, namespace, tier, status,
	       confidence, sources, tool_calls, latency_ms,
	       COALESCE(started_at, created_at) AS started_at, created_at,
	       COALESCE(input_tokens, 0), COALESCE(output_tokens, 0),
	       COALESCE(cache_creation_input_tokens, 0), COALESCE(cache_read_input_tokens, 0),
	       COALESCE(estimated_cost_usd, 0)
	FROM traces`

func scanTrace(row interface{ Scan(...any) error }) (TraceRecord, error) {
	var t TraceRecord
	var srcJSON, tcJSON []byte
	err := row.Scan(
		&t.ID, &t.TraceID, &t.Question, &t.Intent, &t.Namespace,
		&t.Tier, &t.Status, &t.Confidence, &srcJSON, &tcJSON, &t.LatencyMs,
		&t.StartedAt, &t.CreatedAt,
		&t.InputTokens, &t.OutputTokens,
		&t.CacheCreationInputTokens, &t.CacheReadInputTokens,
		&t.EstimatedCostUSD)
	if err != nil {
		return t, err
	}
	_ = json.Unmarshal(srcJSON, &t.Sources)
	_ = json.Unmarshal(tcJSON, &t.ToolCalls)
	return t, nil
}

// GetTrace returns a single trace by its primary key ID.
func (c *Client) GetTrace(ctx context.Context, id string) (*TraceRecord, error) {
	row := c.db.QueryRowContext(ctx, traceSelectCols+` WHERE id = $1`, id)
	t, err := scanTrace(row)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ListTraces returns the most recent traces (newest first), capped at limit.
func (c *Client) ListTraces(ctx context.Context, limit int) ([]TraceRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := c.db.QueryContext(ctx,
		traceSelectCols+` ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list traces: %w", err)
	}
	defer rows.Close()

	var result []TraceRecord
	for rows.Next() {
		t, err := scanTrace(rows)
		if err != nil {
			return nil, fmt.Errorf("scan trace: %w", err)
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

// GetTracesSummary returns aggregated query statistics for the metrics dashboard.
func (c *Client) GetTracesSummary(ctx context.Context) (*TracesSummary, error) {
	s := &TracesSummary{
		QueriesByTier:   make(map[string]int64),
		QueriesByStatus: make(map[string]int64),
		QueriesByIntent: make(map[string]int64),
	}

	// Total + last 24h
	if err := c.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(CASE WHEN created_at > NOW()-INTERVAL '24 hours' THEN 1 ELSE 0 END),0)
		 FROM traces`).Scan(&s.TotalQueries, &s.Last24hCount); err != nil {
		return nil, fmt.Errorf("summary totals: %w", err)
	}

	// By tier
	tierRows, err := c.db.QueryContext(ctx, `SELECT tier, COUNT(*) FROM traces GROUP BY tier`)
	if err == nil {
		defer tierRows.Close()
		for tierRows.Next() {
			var k string; var v int64
			if tierRows.Scan(&k, &v) == nil {
				s.QueriesByTier[k] = v
			}
		}
	}

	// By status
	statusRows, err := c.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM traces GROUP BY status`)
	if err == nil {
		defer statusRows.Close()
		for statusRows.Next() {
			var k string; var v int64
			if statusRows.Scan(&k, &v) == nil {
				s.QueriesByStatus[k] = v
			}
		}
	}

	// By intent
	intentRows, err := c.db.QueryContext(ctx, `SELECT intent, COUNT(*) FROM traces GROUP BY intent`)
	if err == nil {
		defer intentRows.Close()
		for intentRows.Next() {
			var k string; var v int64
			if intentRows.Scan(&k, &v) == nil {
				s.QueriesByIntent[k] = v
			}
		}
	}

	// Avg + P95 agent latency (tier2 only)
	c.db.QueryRowContext(ctx,
		`SELECT COALESCE(AVG(latency_ms),0),
		        COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY latency_ms),0)
		 FROM traces WHERE tier='tier2'`).
		Scan(&s.AvgAgentLatencyMs, &s.P95AgentLatencyMs)

	return s, nil
}

// --- Integrations (admin CRUD) ---

// Integration is the storage-layer representation of an admin integration.
// Token is stored plaintext in Postgres for the prototype; in production it
// should be a reference to a Secrets Manager secret ID.
type Integration struct {
	ID         string
	Name       string
	AdapterURL string
	Namespaces []string
	Status     string
	Token      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ListIntegrations returns all integrations ordered by creation time.
func (c *Client) ListIntegrations(ctx context.Context) ([]Integration, error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT id, name, adapter_url, namespaces, status, token, created_at, updated_at
		 FROM integrations ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list integrations: %w", err)
	}
	defer rows.Close()

	var result []Integration
	for rows.Next() {
		var in Integration
		var nsJSON []byte
		if err := rows.Scan(&in.ID, &in.Name, &in.AdapterURL, &nsJSON, &in.Status, &in.Token, &in.CreatedAt, &in.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan integration: %w", err)
		}
		if err := json.Unmarshal(nsJSON, &in.Namespaces); err != nil {
			in.Namespaces = nil
		}
		result = append(result, in)
	}
	return result, rows.Err()
}

// GetIntegration returns one integration by ID, or sql.ErrNoRows if not found.
func (c *Client) GetIntegration(ctx context.Context, id string) (*Integration, error) {
	var in Integration
	var nsJSON []byte
	err := c.db.QueryRowContext(ctx,
		`SELECT id, name, adapter_url, namespaces, status, token, created_at, updated_at
		 FROM integrations WHERE id = $1`, id).
		Scan(&in.ID, &in.Name, &in.AdapterURL, &nsJSON, &in.Status, &in.Token, &in.CreatedAt, &in.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(nsJSON, &in.Namespaces); err != nil {
		in.Namespaces = nil
	}
	return &in, nil
}

// CreateIntegration inserts a new integration row. in.ID must be set by the caller.
func (c *Client) CreateIntegration(ctx context.Context, in *Integration) error {
	nsJSON, err := json.Marshal(in.Namespaces)
	if err != nil {
		return fmt.Errorf("marshal namespaces: %w", err)
	}
	_, err = c.db.ExecContext(ctx,
		`INSERT INTO integrations (id, name, adapter_url, namespaces, status, token, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())`,
		in.ID, in.Name, in.AdapterURL, nsJSON, in.Status, in.Token)
	return err
}

// UpdateIntegration replaces all mutable fields for an existing integration.
// Preserves created_at. Token is updated only when non-empty (empty = keep existing).
func (c *Client) UpdateIntegration(ctx context.Context, in *Integration) error {
	nsJSON, err := json.Marshal(in.Namespaces)
	if err != nil {
		return fmt.Errorf("marshal namespaces: %w", err)
	}
	if in.Token != "" {
		_, err = c.db.ExecContext(ctx,
			`UPDATE integrations SET name=$1, adapter_url=$2, namespaces=$3, status=$4, token=$5, updated_at=NOW()
			 WHERE id=$6`,
			in.Name, in.AdapterURL, nsJSON, in.Status, in.Token, in.ID)
	} else {
		_, err = c.db.ExecContext(ctx,
			`UPDATE integrations SET name=$1, adapter_url=$2, namespaces=$3, status=$4, updated_at=NOW()
			 WHERE id=$5`,
			in.Name, in.AdapterURL, nsJSON, in.Status, in.ID)
	}
	return err
}

// DeleteIntegration removes an integration by ID.
func (c *Client) DeleteIntegration(ctx context.Context, id string) error {
	_, err := c.db.ExecContext(ctx, `DELETE FROM integrations WHERE id = $1`, id)
	return err
}

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

// Query does a point lookup when given "key", a prefix scan when given "service",
// else returns all entities in the shard.
//
// "key"     — exact entity_key match (use for known full pod names)
// "service" — matches the K8s Service row exactly OR any pod replica whose
//             entity_key starts with "{service}-" (covers Deployment-only
//             workloads that have no K8s Service object)
func (s *CurrentState) Query(ctx context.Context, podID string, queryParams map[string]interface{}) ([]map[string]interface{}, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if key, ok := queryParams["key"].(string); ok && key != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT entity_key, event_namespace, event_type, source, payload, updated_at
			 FROM current_state WHERE pod_id = $1 AND entity_key = $2`, podID, key)
	} else if svc, ok := queryParams["service"].(string); ok && svc != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT entity_key, event_namespace, event_type, source, payload, updated_at
			 FROM current_state WHERE pod_id = $1 AND (entity_key = $2 OR entity_key LIKE $3)`,
			podID, svc, svc+"-%")
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

// TrackedEntities returns active namespace/service pairs from the live-state
// shards — used to power the frontend autocomplete. Each entry is formatted as
// "namespace/service_name" (e.g. "payments/payment-worker").
//
// K8s Services (event_type service_*) are included by name directly.
// Deployments that have no K8s Service (workers, consumers) are derived from
// pod_* rows by stripping the two trailing K8s hash segments
// ({rs-hash}-{pod-hash}), recovering the deployment name. Results are
// deduplicated so each namespace/name pair appears only once.
func (s *CurrentState) TrackedEntities(ctx context.Context) ([]string, error) {
	const q = `
	SELECT pod_id, entity_key, event_type
	FROM current_state
	WHERE pod_id LIKE 'k8fy.live-state.%'
	  AND (event_type LIKE 'service_%' OR event_type LIKE 'pod_%')
	ORDER BY pod_id, entity_key`

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	var result []string

	for rows.Next() {
		var podID, entityKey, eventType string
		if err := rows.Scan(&podID, &entityKey, &eventType); err != nil {
			continue
		}
		ns := strings.TrimPrefix(podID, "k8fy.live-state.")
		if ns == "" || entityKey == "" {
			continue
		}
		// For pod rows, strip K8s hash suffixes to recover the deployment name.
		name := entityKey
		if strings.HasPrefix(eventType, "pod_") {
			name = k8sPodSuffix.ReplaceAllString(entityKey, "")
		}
		key := ns + "/" + name
		if _, dup := seen[key]; !dup {
			seen[key] = struct{}{}
			result = append(result, key)
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

// ── Chat sessions ────────────────────────────────────────────────────────────

// ChatMessage is one turn in a multi-turn conversation.
type ChatMessage struct {
	Role      string    `json:"role"`       // "user" | "assistant"
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// ChatSession holds the full state of one multi-turn conversation.
type ChatSession struct {
	ID               string            `json:"id"`
	Title            string            `json:"title"`
	Namespace        string            `json:"namespace"`
	Service          string            `json:"service"`
	Messages         []ChatMessage     `json:"messages"`
	ContextCache     map[string]any    `json:"context_cache"`
	ContextFetchedAt *time.Time        `json:"context_fetched_at,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	LastActive       time.Time         `json:"last_active"`
	ExpiresAt        time.Time         `json:"expires_at"`
}

// CreateChatSession inserts a new session and returns it.
func (c *Client) CreateChatSession(ctx context.Context, s *ChatSession) error {
	msgsJSON, _ := json.Marshal(s.Messages)
	cacheJSON, _ := json.Marshal(s.ContextCache)
	_, err := c.db.ExecContext(ctx,
		`INSERT INTO chat_sessions (id, title, namespace, service, messages, context_cache,
		  created_at, last_active, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6,NOW(),NOW(),$7)`,
		s.ID, s.Title, s.Namespace, s.Service, msgsJSON, cacheJSON, s.ExpiresAt)
	return err
}

// GetChatSession loads a session by id.
func (c *Client) GetChatSession(ctx context.Context, id string) (*ChatSession, error) {
	row := c.db.QueryRowContext(ctx,
		`SELECT id, title, namespace, service, messages, context_cache,
		        context_fetched_at, created_at, last_active, expires_at
		 FROM chat_sessions WHERE id = $1`, id)
	return scanChatSession(row)
}

// UpdateChatSession persists the full session state (messages, cache, timestamps).
func (c *Client) UpdateChatSession(ctx context.Context, s *ChatSession) error {
	msgsJSON, _ := json.Marshal(s.Messages)
	cacheJSON, _ := json.Marshal(s.ContextCache)
	_, err := c.db.ExecContext(ctx,
		`UPDATE chat_sessions SET
		   title = $2, messages = $3, context_cache = $4,
		   context_fetched_at = $5, last_active = NOW(), expires_at = $6
		 WHERE id = $1`,
		s.ID, s.Title, msgsJSON, cacheJSON, s.ContextFetchedAt, s.ExpiresAt)
	return err
}

// ListChatSessions returns the most recently active sessions (newest first).
func (c *Client) ListChatSessions(ctx context.Context, limit int) ([]ChatSession, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := c.db.QueryContext(ctx,
		`SELECT id, title, namespace, service, messages, context_cache,
		        context_fetched_at, created_at, last_active, expires_at
		 FROM chat_sessions ORDER BY last_active DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ChatSession
	for rows.Next() {
		s, err := scanChatSession(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *s)
	}
	return result, rows.Err()
}

// DeleteChatSession removes a session permanently.
func (c *Client) DeleteChatSession(ctx context.Context, id string) error {
	_, err := c.db.ExecContext(ctx, `DELETE FROM chat_sessions WHERE id = $1`, id)
	return err
}

func scanChatSession(row interface{ Scan(...any) error }) (*ChatSession, error) {
	var s ChatSession
	var msgsJSON, cacheJSON []byte
	err := row.Scan(
		&s.ID, &s.Title, &s.Namespace, &s.Service,
		&msgsJSON, &cacheJSON, &s.ContextFetchedAt,
		&s.CreatedAt, &s.LastActive, &s.ExpiresAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(msgsJSON, &s.Messages)
	if s.Messages == nil {
		s.Messages = []ChatMessage{}
	}
	if s.ContextCache == nil {
		s.ContextCache = map[string]any{}
	}
	_ = json.Unmarshal(cacheJSON, &s.ContextCache)
	return &s, nil
}

// ── Semantic memory (incident embeddings) ────────────────────────────────────

// IncidentEmbedding is one row in incident_embeddings: a Tier-2 trace
// paired with its vector representation for similarity search (P8).
type IncidentEmbedding struct {
	ID        string
	TraceID   string
	Namespace string
	Service   string
	Summary   string
	Embedding []float32 // nil when embed service was unavailable
}

// InsertIncidentEmbedding upserts an incident embedding row. The embedding
// column is set to NULL when vec is empty so the row is still queryable via
// keyword filters while the vector index is being populated.
func (c *Client) InsertIncidentEmbedding(ctx context.Context, e IncidentEmbedding) error {
	if e.Embedding == nil {
		_, err := c.db.ExecContext(ctx, `
			INSERT INTO incident_embeddings (id, trace_id, namespace, service, summary)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (id) DO UPDATE SET summary=EXCLUDED.summary`,
			e.ID, e.TraceID, e.Namespace, e.Service, e.Summary)
		return err
	}

	// pgvector expects the vector as a Postgres-formatted literal: '[0.1,0.2,...]'
	var sb strings.Builder
	sb.WriteByte('[')
	for i, v := range e.Embedding {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(fmt.Sprintf("%g", v))
	}
	sb.WriteByte(']')

	_, err := c.db.ExecContext(ctx, `
		INSERT INTO incident_embeddings (id, trace_id, namespace, service, summary, embedding)
		VALUES ($1,$2,$3,$4,$5,$6::vector)
		ON CONFLICT (id) DO UPDATE SET
			summary   = EXCLUDED.summary,
			embedding = EXCLUDED.embedding`,
		e.ID, e.TraceID, e.Namespace, e.Service, e.Summary, sb.String())
	return err
}

// FindSimilarIncidents returns up to limit incidents whose embedding is closest
// to queryVec (cosine similarity). Falls back to recency order when pgvector is
// unavailable (vec is nil) so callers always get a useful result.
func (c *Client) FindSimilarIncidents(ctx context.Context, namespace, service string, queryVec []float32, limit int) ([]IncidentEmbedding, error) {
	if limit <= 0 {
		limit = 3
	}

	var rows *sql.Rows
	var err error

	if len(queryVec) > 0 {
		// Vector similarity search via pgvector (<-> = cosine distance, lower = more similar).
		var sb strings.Builder
		sb.WriteByte('[')
		for i, v := range queryVec {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(fmt.Sprintf("%g", v))
		}
		sb.WriteByte(']')
		rows, err = c.db.QueryContext(ctx, `
			SELECT id, trace_id, namespace, service, summary
			FROM incident_embeddings
			WHERE embedding IS NOT NULL
			ORDER BY embedding <-> $1::vector
			LIMIT $2`,
			sb.String(), limit)
	} else {
		// Fallback: most recent incidents matching namespace/service.
		rows, err = c.db.QueryContext(ctx, `
			SELECT id, trace_id, namespace, service, summary
			FROM incident_embeddings
			WHERE ($1 = '' OR namespace = $1)
			  AND ($2 = '' OR service  = $2)
			ORDER BY created_at DESC
			LIMIT $3`,
			namespace, service, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []IncidentEmbedding
	for rows.Next() {
		var e IncidentEmbedding
		if serr := rows.Scan(&e.ID, &e.TraceID, &e.Namespace, &e.Service, &e.Summary); serr != nil {
			continue
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// ── Model pricing ────────────────────────────────────────────────────────────

// ModelPricing holds the indicative retail $/MTok rates for one Claude model.
type ModelPricing struct {
	ModelID           string    `json:"model_id"`
	DisplayName       string    `json:"display_name"`
	InputPerMTok      float64   `json:"input_per_mtok"`
	OutputPerMTok     float64   `json:"output_per_mtok"`
	CacheWritePerMTok float64   `json:"cache_write_per_mtok"`
	CacheReadPerMTok  float64   `json:"cache_read_per_mtok"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ListModelPricing returns all rows from the model_pricing table, sorted by model_id.
func (c *Client) ListModelPricing(ctx context.Context) ([]ModelPricing, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT model_id, display_name, input_per_mtok, output_per_mtok,
		       cache_write_per_mtok, cache_read_per_mtok, updated_at
		FROM model_pricing ORDER BY model_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ModelPricing
	for rows.Next() {
		var p ModelPricing
		if err := rows.Scan(&p.ModelID, &p.DisplayName, &p.InputPerMTok, &p.OutputPerMTok,
			&p.CacheWritePerMTok, &p.CacheReadPerMTok, &p.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

// UpsertModelPricing inserts or updates a model pricing row.
func (c *Client) UpsertModelPricing(ctx context.Context, p *ModelPricing) error {
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO model_pricing
		  (model_id, display_name, input_per_mtok, output_per_mtok, cache_write_per_mtok, cache_read_per_mtok, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (model_id) DO UPDATE SET
		  display_name         = EXCLUDED.display_name,
		  input_per_mtok       = EXCLUDED.input_per_mtok,
		  output_per_mtok      = EXCLUDED.output_per_mtok,
		  cache_write_per_mtok = EXCLUDED.cache_write_per_mtok,
		  cache_read_per_mtok  = EXCLUDED.cache_read_per_mtok,
		  updated_at           = NOW()`,
		p.ModelID, p.DisplayName, p.InputPerMTok, p.OutputPerMTok,
		p.CacheWritePerMTok, p.CacheReadPerMTok)
	return err
}
