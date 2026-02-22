// Package router implements load balancing algorithms for distributing
// requests across service instances.
package router

import (
	"time"
)

// Strategy defines the load balancing algorithm to use.
type Strategy int

const (
	RoundRobin Strategy = iota
	LeastConnections
	Random
	WeightedRoundRobin
	IPHash
)

// ParseStrategy parses a strategy name (case-insensitive) into a Strategy.
// Returns RoundRobin if the name is unrecognized.
func ParseStrategy(name string) Strategy {
	switch name {
	case "RoundRobin", "roundrobin":
		return RoundRobin
	case "LeastConnections", "leastconnections":
		return LeastConnections
	case "Random", "random":
		return Random
	case "WeightedRoundRobin", "weightedroundrobin":
		return WeightedRoundRobin
	case "IPHash", "iphash":
		return IPHash
	default:
		return RoundRobin
	}
}

func (s Strategy) String() string {
	switch s {
	case RoundRobin:
		return "RoundRobin"
	case LeastConnections:
		return "LeastConnections"
	case Random:
		return "Random"
	case WeightedRoundRobin:
		return "WeightedRoundRobin"
	case IPHash:
		return "IPHash"
	default:
		return "RoundRobin"
	}
}

// HealthStatus represents the health state of a service instance.
type HealthStatus int

const (
	HealthUnknown   HealthStatus = iota
	HealthHealthy
	HealthUnhealthy
	HealthDegraded
)

// Instance represents a registered service instance available for routing.
type Instance struct {
	ServiceName    string
	ServiceID      string
	Address        string
	Port           int
	Status         HealthStatus
	Metadata       map[string]string
	RegisteredAt   time.Time
	LastHealthCheck time.Time
}

// Context provides request-scoped information for load balancing decisions.
type Context struct {
	PreferredZone string
	Headers       map[string]string
	SessionID     string
}

// RequestResult reports the outcome of a proxied request for tracking.
type RequestResult struct {
	ServiceID    string
	Success      bool
	ResponseTime time.Duration
	StatusCode   int
	ErrorMessage string
}

// Stats provides aggregate load balancing statistics for a service.
type Stats struct {
	ServiceName           string
	TotalRequests         int
	SuccessfulRequests    int
	FailedRequests        int
	AverageResponseTime   time.Duration
	InstanceRequestCounts map[string]int
}

// InstanceProvider fetches instances for a given service name.
// This decouples the load balancer from the service registry implementation.
type InstanceProvider interface {
	GetInstances(serviceName string) ([]Instance, error)
}

// Balancer selects service instances using a configured load balancing strategy.
type Balancer interface {
	// Select picks the next instance for the given service and request context.
	Select(serviceName string, ctx Context) (*Instance, error)

	// ReportResult feeds back request outcomes for connection tracking.
	ReportResult(serviceID string, result RequestResult)

	// Stats returns aggregate statistics for a service.
	Stats(serviceName string) Stats
}
