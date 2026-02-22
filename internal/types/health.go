// Package types defines shared domain types used across internal packages.
package types

// HealthStatus represents the health state of a service instance.
type HealthStatus int

const (
	HealthUnknown   HealthStatus = iota
	HealthHealthy
	HealthUnhealthy
	HealthDegraded
)

func (s HealthStatus) String() string {
	switch s {
	case HealthHealthy:
		return "Healthy"
	case HealthUnhealthy:
		return "Unhealthy"
	case HealthDegraded:
		return "Degraded"
	default:
		return "Unknown"
	}
}
