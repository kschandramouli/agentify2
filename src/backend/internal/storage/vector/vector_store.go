package vector

import (
	"context"
	"fmt"
)

// VectorStore is an abstraction for vector database operations.
// Implementations can be Weaviate, Pinecone, Milvus, etc.
type VectorStore interface {
	// HealthCheck verifies the vector store is accessible.
	HealthCheck(ctx context.Context) error

	// Upsert stores or updates a vector with metadata.
	Upsert(ctx context.Context, id string, vector []float32, metadata map[string]interface{}) error

	// Search finds similar vectors and returns matches.
	Search(ctx context.Context, vector []float32, limit int) ([]SearchResult, error)

	// Delete removes a vector by ID.
	Delete(ctx context.Context, id string) error

	// Close closes the connection.
	Close() error
}

// SearchResult represents a vector search match.
type SearchResult struct {
	ID        string                 `json:"id"`
	Distance  float32                `json:"distance"` // similarity score
	Metadata  map[string]interface{} `json:"metadata"`
}

// Config holds vector store configuration.
type Config struct {
	Type     string // "weaviate" | "pinecone" | "milvus"
	Endpoint string // e.g., "http://localhost:8080" (Weaviate) or API URL
	APIKey   string // if needed (e.g., Pinecone)
	Class    string // collection/index name (e.g., "K8fyEvents")
}

// New creates a new vector store client based on config.
func New(cfg Config) (VectorStore, error) {
	switch cfg.Type {
	case "weaviate":
		return NewWeaviateClient(cfg)
	// case "pinecone":
	//   return NewPineconeClient(cfg)
	// case "milvus":
	//   return NewMilvusClient(cfg)
	default:
		return nil, fmt.Errorf("unknown vector store type: %s", cfg.Type)
	}
}
