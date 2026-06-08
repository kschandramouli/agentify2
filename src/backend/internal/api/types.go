package api

// QueryRequest represents a user query.
type QueryRequest struct {
	Question string                 `json:"question"`
	Context  map[string]interface{} `json:"context"`
}

// QueryResponse represents the answer to a query.
type QueryResponse struct {
	Answer     string                 `json:"answer"`
	Status     string                 `json:"status"`
	Confidence float64                `json:"confidence"`
	Sources    []string               `json:"sources"`
	TraceID    string                 `json:"trace_id,omitempty"` // provenance correlation id (spec 004)
	Details    map[string]interface{} `json:"details,omitempty"`  // structured data for UI rendering
}
