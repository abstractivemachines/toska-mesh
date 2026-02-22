// Package messaging defines event types and a publisher for MassTransit-compatible
// RabbitMQ message publishing.
package messaging

import "time"

// ServiceRegisteredEvent is published when a service instance registers.
type ServiceRegisteredEvent struct {
	EventID       string            `json:"eventId"`
	Timestamp     time.Time         `json:"timestamp"`
	CorrelationID string            `json:"correlationId,omitempty"`
	ServiceID     string            `json:"serviceId"`
	ServiceName   string            `json:"serviceName"`
	Address       string            `json:"address"`
	Port          int               `json:"port"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// ServiceDeregisteredEvent is published when a service instance deregisters.
type ServiceDeregisteredEvent struct {
	EventID       string    `json:"eventId"`
	Timestamp     time.Time `json:"timestamp"`
	CorrelationID string    `json:"correlationId,omitempty"`
	ServiceID     string    `json:"serviceId"`
	ServiceName   string    `json:"serviceName"`
	Reason        string    `json:"reason,omitempty"`
}

// ServiceHealthChangedEvent is published when a service's health status changes.
type ServiceHealthChangedEvent struct {
	EventID           string    `json:"eventId"`
	Timestamp         time.Time `json:"timestamp"`
	CorrelationID     string    `json:"correlationId,omitempty"`
	ServiceID         string    `json:"serviceId"`
	ServiceName       string    `json:"serviceName"`
	PreviousStatus    string    `json:"previousStatus"`
	CurrentStatus     string    `json:"currentStatus"`
	HealthCheckOutput string    `json:"healthCheckOutput,omitempty"`
}
