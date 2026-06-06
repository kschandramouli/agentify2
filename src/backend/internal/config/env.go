package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all application configuration.
type Config struct {
	Port                string
	Env                 string // "dev" | "prod"
	DBHost              string
	DBPort              string
	DBUser              string
	DBPassword          string
	DBName              string
	AgentServiceURL     string
	AdapterURL          string // K8fy adapter base URL for on-demand log fetch (spec 008)
	VectorStoreType     string // "weaviate" | "pinecone"
	VectorStoreEndpoint string // e.g., "http://localhost:8080" or pinecone URL
	VectorStoreAPIKey   string // for Pinecone or other SaaS
	AWSRegion           string
	CloudWatchLogGroup  string
	RegistryBackend     string // "dynamodb" (default) | "memory" (local dev, no AWS)

	// Pod-registry cache (ADR 0012; applies to the dynamodb backend only)
	RegistryCacheTTLSeconds      int // snapshot freshness window
	RegistryCacheMaxStaleSeconds int // max age to serve a stale snapshot on refresh error

	// Egress data governance (ADR 0007 / policies/data-governance.md)
	RedactionEnabled      bool // allowlist-redact data sent toward the agent/model (default true)
	RedactionPseudonymize bool // replace identifier values with stable hashes (default false)

	// Adapter (on-demand log fetch — spec 008). Shared bearer token guards the
	// adapter's /logs surface; empty disables auth (dev only).
	AdapterAuthToken string

	// Events-table retention (ADR 0015). Days=0 disables the janitor.
	EventsRetentionDays            int
	EventsRetentionIntervalMinutes int

	// Proactive investigation loop (ADR 0016 / spec 009). Opt-in: disabled by default.
	InvestigationEnabled              bool
	InvestigationWebhookURL           string
	InvestigationSweepIntervalMinutes int
	InvestigationMaxPerSweep          int
	InvestigationCooldownMinutes      int
	InvestigationCertCriticalDays     int
}

// LoadFromEnv loads configuration from environment variables.
func LoadFromEnv() (*Config, error) {
	// Load .env file if it exists (for local development)
	_ = godotenv.Load()

	cfg := &Config{
		Port:                getEnv("PORT", ":8080"),
		Env:                 getEnv("ENV", "dev"),
		DBHost:              getEnv("DB_HOST", "localhost"),
		DBPort:              getEnv("DB_PORT", "5432"),
		DBUser:              getEnv("DB_USER", "postgres"),
		DBPassword:          getEnv("DB_PASSWORD", ""),
		DBName:              getEnv("DB_NAME", "agentify"),
		AgentServiceURL:     getEnv("AGENT_SERVICE_URL", "http://localhost:8001"),
		AdapterURL:          getEnv("ADAPTER_URL", "http://localhost:8200"),
		VectorStoreType:     getEnv("VECTOR_STORE_TYPE", "weaviate"),
		VectorStoreEndpoint: getEnv("VECTOR_STORE_ENDPOINT", "localhost:8080"),
		VectorStoreAPIKey:   getEnv("VECTOR_STORE_API_KEY", ""),
		AWSRegion:           getEnv("AWS_REGION", "ap-southeast-2"),
		CloudWatchLogGroup:  getEnv("CLOUDWATCH_LOG_GROUP", "/agentify/backend"),
		RegistryBackend:     getEnv("REGISTRY_BACKEND", "dynamodb"),

		RedactionEnabled:      getEnvBool("REDACTION_ENABLED", true),
		RedactionPseudonymize: getEnvBool("REDACTION_PSEUDONYMIZE", false),

		AdapterAuthToken: getEnv("ADAPTER_AUTH_TOKEN", ""),

		EventsRetentionDays:            getEnvInt("EVENTS_RETENTION_DAYS", 30),
		EventsRetentionIntervalMinutes: getEnvInt("EVENTS_RETENTION_INTERVAL_MINUTES", 60),

		InvestigationEnabled:              getEnvBool("INVESTIGATION_ENABLED", false),
		InvestigationWebhookURL:           getEnv("INVESTIGATION_WEBHOOK_URL", ""),
		InvestigationSweepIntervalMinutes: getEnvInt("INVESTIGATION_SWEEP_INTERVAL_MINUTES", 5),
		InvestigationMaxPerSweep:          getEnvInt("INVESTIGATION_MAX_PER_SWEEP", 5),
		InvestigationCooldownMinutes:      getEnvInt("INVESTIGATION_COOLDOWN_MINUTES", 60),
		InvestigationCertCriticalDays:     getEnvInt("INVESTIGATION_CERT_CRITICAL_DAYS", 7),

		RegistryCacheTTLSeconds:      getEnvInt("REGISTRY_CACHE_TTL_SECONDS", 30),
		RegistryCacheMaxStaleSeconds: getEnvInt("REGISTRY_CACHE_MAX_STALE_SECONDS", 300),
	}

	// Validate required fields for production
	if cfg.Env == "prod" {
		if cfg.DBPassword == "" {
			return nil, fmt.Errorf("DB_PASSWORD required in production")
		}
	}

	return cfg, nil
}

// getEnv returns an environment variable or a default value.
func getEnv(key, defaultVal string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultVal
}

// getEnvInt parses an integer env var, falling back to a default.
func getEnvInt(key string, defaultVal int) int {
	if value := os.Getenv(key); value != "" {
		if n, err := strconv.Atoi(value); err == nil {
			return n
		}
	}
	return defaultVal
}

// getEnvBool parses a boolean env var ("true"/"1"/"yes"), falling back to a default.
func getEnvBool(key string, defaultVal bool) bool {
	switch strings.ToLower(os.Getenv(key)) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	default:
		return defaultVal
	}
}

