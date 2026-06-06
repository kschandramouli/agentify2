package vector

import (
	"context"
	"fmt"

	"github.com/weaviate/weaviate-go-client/v4/weaviate"
	"github.com/weaviate/weaviate/entities/models"
)

// WeaviateClient wraps the Weaviate vector database.
type WeaviateClient struct {
	client *weaviate.Client
	class  string // collection class name (e.g., "K8fyEvents")
}

// NewWeaviateClient creates a new Weaviate client.
func NewWeaviateClient(cfg Config) (*WeaviateClient, error) {
	wvClient, err := weaviate.NewClient(weaviate.Config{
		Scheme: "http",
		Host:   cfg.Endpoint, // e.g., "localhost:8080"
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create weaviate client: %w", err)
	}

	wc := &WeaviateClient{
		client: wvClient,
		class:  cfg.Class,
	}

	// Ensure class exists
	if err := wc.ensureClass(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to ensure class: %w", err)
	}

	return wc, nil
}

// ensureClass creates the schema class if it doesn't exist.
func (wc *WeaviateClient) ensureClass(ctx context.Context) error {
	// Check if class already exists
	exists, err := wc.client.Schema().ClassExistenceChecker().WithClassName(wc.class).Do(ctx)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	// Create class
	classObj := &models.Class{
		Class:             wc.class,
		VectorIndexType:   "hnsw",
		VectorIndexConfig: map[string]interface{}{},
		Properties: []*models.Property{
			{
				Name:     "content",
				DataType: []string{"text"},
			},
			{
				Name:     "source",
				DataType: []string{"text"},
			},
			{
				Name:     "timestamp",
				DataType: []string{"date"},
			},
		},
	}

	schemaCreator := wc.client.Schema().ClassCreator()
	return schemaCreator.WithClass(classObj).Do(ctx)
}

// HealthCheck verifies Weaviate is accessible.
func (wc *WeaviateClient) HealthCheck(ctx context.Context) error {
	alive, err := wc.client.Misc().LiveChecker().Do(ctx)
	if err != nil {
		return err
	}
	if !alive {
		return fmt.Errorf("weaviate is not live")
	}
	return nil
}

// Upsert stores or updates a vector with metadata.
func (wc *WeaviateClient) Upsert(ctx context.Context, id string, vector []float32, metadata map[string]interface{}) error {
	_, err := wc.client.Data().Creator().
		WithClassName(wc.class).
		WithID(id).
		WithProperties(metadata).
		WithVector(vector).
		Do(ctx)
	return err
}

// Search finds similar vectors using Weaviate's nearVector search.
//
// NOTE: result parsing is not yet implemented. The GraphQL response is a nested
// map[string]interface{}; mapping it to SearchResult is deferred until the vector
// query path is exercised (no events route to the vector store in the MVP — see
// context-mesh/policies/storage-strategy.md). This returns an empty result set so
// callers degrade gracefully rather than erroring.
func (wc *WeaviateClient) Search(ctx context.Context, vector []float32, limit int) ([]SearchResult, error) {
	nearVector := wc.client.GraphQL().NearVectorArgBuilder().WithVector(vector)

	_, err := wc.client.GraphQL().Get().
		WithClassName(wc.class).
		WithNearVector(nearVector).
		WithLimit(limit).
		Do(ctx)
	if err != nil {
		return nil, err
	}

	// TODO(vector): parse the GraphQL result into []SearchResult.
	return []SearchResult{}, nil
}

// Delete removes a vector by ID.
func (wc *WeaviateClient) Delete(ctx context.Context, id string) error {
	return wc.client.Data().Deleter().WithClassName(wc.class).WithID(id).Do(ctx)
}

// Close closes the Weaviate connection.
func (wc *WeaviateClient) Close() error {
	// Weaviate client doesn't require explicit closing
	return nil
}
