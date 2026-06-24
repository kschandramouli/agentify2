package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/chan/agentify/backend/internal/models"
	"github.com/chan/agentify/backend/internal/storage"
	"github.com/chan/agentify/backend/internal/storage/postgres"
	"github.com/chan/agentify/backend/internal/storage/registry"
)

// Router handles query routing to the appropriate pod(s).
// It consults the pod registry to find the best pod(s) to answer a query.
type Router struct {
	config         *Config
	registry       registry.PodStore
	backendFactory *storage.BackendFactory
	logger         *slog.Logger
}

// New creates a new orchestrator router.
// It connects the storage backends from config so the query and ingestion paths
// have live connections (with connection pooling handled by each client).
func New(cfg *Config, dbClient *dynamodb.Client, logger *slog.Logger) (*Router, error) {
	var reg registry.PodStore = registry.NewRegistry(dbClient, "agentify-pod-registry", logger)

	// Wrap the DynamoDB-backed registry in a read-through snapshot cache (ADR 0012):
	// eliminates per-query Scans and serves stale on a registry blip. The in-memory
	// mode (dbClient == nil, local dev) needs no cache.
	if dbClient != nil {
		ttl := time.Duration(cfg.RegistryCacheTTLSeconds) * time.Second
		maxStale := time.Duration(cfg.RegistryCacheMaxStaleSeconds) * time.Second
		reg = registry.NewCache(reg, ttl, maxStale, logger)
		logger.Info("pod-registry cache enabled", "ttl_s", cfg.RegistryCacheTTLSeconds, "max_stale_s", cfg.RegistryCacheMaxStaleSeconds)
	}

	return &Router{
		config:         cfg,
		registry:       reg,
		backendFactory: buildBackendFactory(cfg, logger),
		logger:         logger,
	}, nil
}

// buildBackendFactory connects the storage backends from config.
//
// Per ADR 0010 a single Postgres backs both store families: "relational"
// (append-only events/certs) and "kv" (current-state) share one connection pool.
// A connection failure is logged but non-fatal — the affected store types become
// unavailable (GetBackend returns an error) rather than crashing the service, so
// health/registry/agent paths keep working when Postgres isn't running yet.
func buildBackendFactory(cfg *Config, logger *slog.Logger) *storage.BackendFactory {
	var relational, kv storage.Backend

	// sslmode=require for RDS (AWS enforces TLS); disable only for localhost dev.
	sslMode := "require"
	if cfg.DBHost == "localhost" || cfg.DBHost == "127.0.0.1" {
		sslMode = "disable"
	}
	connStr := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, sslMode,
	)
	// Give Postgres up to 3 minutes to become reachable — this covers the 30–60 s
	// window where AWS RDS is "available" per API but not yet accepting connections.
	pgCtx, pgCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer pgCancel()
	if pgClient, err := postgres.NewClient(pgCtx, connStr, logger); err != nil {
		logger.Warn("postgres unavailable; relational + kv queries will fail", "error", err)
	} else {
		relational = pgClient
		kv = pgClient.CurrentStateStore() // same Postgres backs current-state
	}

	// vector (pgvector/Weaviate) is unprovisioned — deferred until a semantic-search
	// feature exists (ADR 0010 / storage-strategy.md).
	return storage.NewBackendFactory(relational, kv, nil)
}

// GetBackendFactory returns the connected backend factory.
func (r *Router) GetBackendFactory() *storage.BackendFactory {
	return r.backendFactory
}

// Close releases the storage backend connections. Safe to call on shutdown.
func (r *Router) Close() error {
	if r.backendFactory == nil {
		return nil
	}
	return r.backendFactory.Close()
}

// RouteQuery determines which pod(s) should handle a given query.
// Returns pod IDs that match the query intent and context.
//
// Implementation follows context-mesh/policies/storage-strategy.md:
// - Classify the query by intent (health_check, cert_check, etc.)
// - Find pods matching that intent (by tags, namespace, store type)
// - Return the best pod (or multiple if correlation needed)
func (r *Router) RouteQuery(ctx context.Context, intent string, namespace string) ([]*models.Pod, error) {
	if intent == "" {
		return nil, fmt.Errorf("intent cannot be empty")
	}

	// TODO: implement intent parsing
	// For now, hardcoded routing for K8fy
	var podsToCheck []*models.Pod

	// Get all active pods in the namespace
	filter := &models.PodFilter{
		Namespace: &namespace,
	}
	pods, err := r.registry.ListPods(ctx, filter)
	if err != nil {
		r.logger.Error("failed to list pods", "error", err)
		return nil, err
	}

	if len(pods) == 0 {
		return nil, fmt.Errorf("no active pods found for namespace: %s", namespace)
	}

	// TODO: implement scoring/ranking
	// For MVP, return all active pods (they'll be correlated later)
	podsToCheck = pods

	r.logger.Info("routed query to pods", "intent", intent, "namespace", namespace, "pod_count", len(podsToCheck))
	return podsToCheck, nil
}

// CorrelateResults combines results from multiple pods into a single answer.
// Implements context-mesh/policies/correlation.md
func (r *Router) CorrelateResults(ctx context.Context, results map[string]interface{}) (interface{}, error) {
	// TODO: implement correlation logic
	// For now, just merge the results
	return results, nil
}

// GetPodRegistry returns the pod registry (cache-wrapped for the dynamodb backend).
func (r *Router) GetPodRegistry() registry.PodStore {
	return r.registry
}

