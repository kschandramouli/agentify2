package models

import "time"

// Integration represents a configured K8s adapter connection.
// Each integration points to one running adapter that watches one or more
// K8s namespaces and forwards events to the backend.
type Integration struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	AdapterURL string   `json:"adapter_url"`
	Namespaces []string `json:"namespaces"`
	Status     string   `json:"status"`    // active | inactive | error
	HasToken   bool     `json:"has_token"` // whether a bearer token is configured
	Token      string    `json:"-"` // token stored in DB but never serialized
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}
