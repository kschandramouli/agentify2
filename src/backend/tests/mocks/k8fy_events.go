package mocks

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/chan/agentify/backend/internal/models"
)

// K8fyEventGenerator generates fake K8fy events for testing.
type K8fyEventGenerator struct {
	namespace string
}

// NewK8fyEventGenerator creates a new event generator.
func NewK8fyEventGenerator(namespace string) *K8fyEventGenerator {
	return &K8fyEventGenerator{namespace: namespace}
}

// GeneratePodRestartEvent creates a fake pod restart event.
func (gen *K8fyEventGenerator) GeneratePodRestartEvent(podName string) *models.Event {
	return &models.Event{
		ID:             uuid.New().String(),
		Timestamp:      time.Now(),
		EventNamespace: "k8fy.live-state",
		Type:           "pod_restart",
		Source:         "kubernetes-api",
		EntityKey:      podName,
		Payload: map[string]interface{}{
			"pod_id":    podName,
			"namespace": gen.namespace,
			"phase":     "Running",
			"reason":    "CrashLoopBackOff",
			"message":   "Connection refused to database",
			"restarts":  2,
		},
		Text: ptr("Connection refused to database. Retrying..."),
		Traits: models.EventTraits{
			Shape:         "semi-structured",
			AccessPattern: "time-range-scan",
			Temporality:   "append-only",
			Mutability:    "immutable",
			Authority:     "derived",
			Retention:     "30d",
		},
	}
}

// GeneratePodHealthyEvent creates a fake healthy pod event.
func (gen *K8fyEventGenerator) GeneratePodHealthyEvent(podName string) *models.Event {
	return &models.Event{
		ID:             uuid.New().String(),
		Timestamp:      time.Now(),
		EventNamespace: "k8fy.live-state",
		Type:           "pod_healthy",
		Source:         "kubernetes-api",
		EntityKey:      podName,
		Payload: map[string]interface{}{
			"pod_id":    podName,
			"namespace": gen.namespace,
			"phase":     "Running",
			"ready":     true,
			"restarts":  0,
			"reason":    "Running",
		},
		Traits: models.EventTraits{
			Shape:         "structured",
			AccessPattern: "point-lookup",
			Temporality:   "current-state",
			Mutability:    "mutable",
			Authority:     "derived",
			Retention:     "ephemeral",
		},
	}
}

// GenerateServiceHealthyEvent creates a fake healthy service event.
func (gen *K8fyEventGenerator) GenerateServiceHealthyEvent(serviceName string, endpoints int) *models.Event {
	return &models.Event{
		ID:             uuid.New().String(),
		Timestamp:      time.Now(),
		EventNamespace: "k8fy.live-state",
		Type:           "service_healthy",
		Source:         "kubernetes-api",
		EntityKey:      serviceName,
		Payload: map[string]interface{}{
			"service":           serviceName,
			"namespace":         gen.namespace,
			"endpoints":         endpoints,
			"ready_endpoints":   endpoints,
			"ready_ratio":       1.0,
		},
		Traits: models.EventTraits{
			Shape:         "structured",
			AccessPattern: "point-lookup",
			Temporality:   "current-state",
			Mutability:    "mutable",
			Authority:     "derived",
			Retention:     "ephemeral",
		},
	}
}

// GenerateCertificateExpiringEvent creates a fake certificate expiry event.
func (gen *K8fyEventGenerator) GenerateCertificateExpiringEvent(secretName string, daysUntilExpiry int) *models.Event {
	expiryTime := time.Now().AddDate(0, 0, daysUntilExpiry)
	return &models.Event{
		ID:             uuid.New().String(),
		Timestamp:      time.Now(),
		EventNamespace: "k8fy.certificates",
		Type:           "cert_expiring",
		Source:         "kubernetes-api",
		EntityKey:      secretName,
		Payload: map[string]interface{}{
			"secret":             secretName,
			"namespace":          gen.namespace,
			"expires_at":         expiryTime.Format(time.RFC3339),
			"days_until_expiry":  daysUntilExpiry,
			"should_renew":       daysUntilExpiry < 30,
		},
		Traits: models.EventTraits{
			Shape:         "structured",
			AccessPattern: "point-lookup",
			Temporality:   "current-state",
			Mutability:    "mutable",
			Authority:     "derived",
			Retention:     "30d",
		},
	}
}

// GenerateBulkPodEvents creates multiple pod events.
func (gen *K8fyEventGenerator) GenerateBulkPodEvents(baseNamePrefix string, count int) []*models.Event {
	var events []*models.Event
	for i := 0; i < count; i++ {
		podName := fmt.Sprintf("%s-%d", baseNamePrefix, i)
		if i%5 == 0 {
			events = append(events, gen.GeneratePodRestartEvent(podName))
		} else {
			events = append(events, gen.GeneratePodHealthyEvent(podName))
		}
	}
	return events
}

// Helper
func ptr(s string) *string {
	return &s
}
