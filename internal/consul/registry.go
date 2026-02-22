// Package consul wraps the HashiCorp Consul API to implement service
// registration, discovery, and TTL-based health checking.
package consul

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/toska-mesh/toska-mesh/internal/types"
)

// HealthStatus is an alias for the shared health status type.
type HealthStatus = types.HealthStatus

// Re-export health status constants so existing consumers compile unchanged.
const (
	HealthUnknown   = types.HealthUnknown
	HealthHealthy   = types.HealthHealthy
	HealthUnhealthy = types.HealthUnhealthy
	HealthDegraded  = types.HealthDegraded
)

// Instance represents a service instance stored in Consul.
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

// Registration contains the information needed to register a service.
type Registration struct {
	ServiceName string
	ServiceID   string
	Address     string
	Port        int
	Metadata    map[string]string
	HealthCheck *HealthCheckConfig
}

// HealthCheckConfig defines health check parameters for registration.
type HealthCheckConfig struct {
	Endpoint           string
	IntervalSeconds    int
	TimeoutSeconds     int
	UnhealthyThreshold int
}

// Registry is a Consul-backed service registry.
type Registry struct {
	client *api.Client
	logger *slog.Logger

	mu                sync.RWMutex
	registrationTimes map[string]time.Time
}

// NewRegistry creates a Registry using the provided Consul address.
func NewRegistry(addr string, logger *slog.Logger) (*Registry, error) {
	cfg := api.DefaultConfig()
	if addr != "" {
		cfg.Address = addr
	}

	client, err := api.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("consul client: %w", err)
	}

	return &Registry{
		client:            client,
		logger:            logger,
		registrationTimes: make(map[string]time.Time),
	}, nil
}

// Register registers a service instance with Consul using TTL health checks.
func (r *Registry) Register(reg Registration) error {
	ttlInterval := 30 * time.Second
	if reg.HealthCheck != nil && reg.HealthCheck.IntervalSeconds > 0 {
		ttlInterval = time.Duration(reg.HealthCheck.IntervalSeconds) * time.Second
	}

	ttlWithBuffer := ttlInterval + 5*time.Second
	if ttlWithBuffer < 10*time.Second {
		ttlWithBuffer = 10 * time.Second
	}

	consulReg := &api.AgentServiceRegistration{
		ID:      reg.ServiceID,
		Name:    reg.ServiceName,
		Address: reg.Address,
		Port:    reg.Port,
		Meta:    reg.Metadata,
		Check: &api.AgentServiceCheck{
			CheckID:                        fmt.Sprintf("service:%s", reg.ServiceID),
			Name:                           fmt.Sprintf("%s TTL Health", reg.ServiceName),
			TTL:                            ttlWithBuffer.String(),
			DeregisterCriticalServiceAfter: (1 * time.Minute).String(),
		},
	}

	if err := r.client.Agent().ServiceRegister(consulReg); err != nil {
		return fmt.Errorf("consul register: %w", err)
	}

	// Mark TTL check as passing so service starts healthy.
	checkID := fmt.Sprintf("service:%s", reg.ServiceID)
	if err := r.client.Agent().PassTTL(checkID, "Service registered"); err != nil {
		r.logger.Warn("failed to pass initial TTL", "service_id", reg.ServiceID, "error", err)
	}

	r.mu.Lock()
	r.registrationTimes[reg.ServiceID] = time.Now().UTC()
	r.mu.Unlock()

	r.logger.Info("registered service", "service_id", reg.ServiceID, "service_name", reg.ServiceName)
	return nil
}

// Deregister removes a service instance from Consul.
func (r *Registry) Deregister(serviceID string) error {
	if err := r.client.Agent().ServiceDeregister(serviceID); err != nil {
		return fmt.Errorf("consul deregister: %w", err)
	}

	r.mu.Lock()
	delete(r.registrationTimes, serviceID)
	r.mu.Unlock()

	r.logger.Info("deregistered service", "service_id", serviceID)
	return nil
}

// GetInstances returns all instances of a service, including health status.
func (r *Registry) GetInstances(serviceName string) ([]Instance, error) {
	entries, _, err := r.client.Health().Service(serviceName, "", false, nil)
	if err != nil {
		return nil, fmt.Errorf("consul get instances: %w", err)
	}

	instances := make([]Instance, 0, len(entries))
	for _, entry := range entries {
		meta := make(map[string]string)
		for k, v := range entry.Service.Meta {
			meta[k] = v
		}

		r.mu.RLock()
		regTime := r.registrationTimes[entry.Service.ID]
		r.mu.RUnlock()

		instances = append(instances, Instance{
			ServiceName:    entry.Service.Service,
			ServiceID:      entry.Service.ID,
			Address:        entry.Service.Address,
			Port:           entry.Service.Port,
			Status:         mapHealthStatus(entry.Checks),
			Metadata:       meta,
			RegisteredAt:   regTime,
			LastHealthCheck: time.Time{},
		})
	}

	return instances, nil
}

// GetServices returns a list of all registered service names.
func (r *Registry) GetServices() ([]string, error) {
	services, _, err := r.client.Catalog().Services(nil)
	if err != nil {
		return nil, fmt.Errorf("consul get services: %w", err)
	}

	names := make([]string, 0, len(services))
	for name := range services {
		if name == "consul" {
			continue // skip the consul service itself
		}
		names = append(names, name)
	}
	return names, nil
}

// UpdateHealth updates the TTL health check status for a service instance.
func (r *Registry) UpdateHealth(serviceID string, status HealthStatus, output string) error {
	checkID := fmt.Sprintf("service:%s", serviceID)

	switch status {
	case HealthHealthy:
		return r.client.Agent().PassTTL(checkID, output)
	case HealthUnhealthy:
		return r.client.Agent().FailTTL(checkID, output)
	case HealthDegraded:
		return r.client.Agent().WarnTTL(checkID, output)
	default:
		return r.client.Agent().PassTTL(checkID, output)
	}
}

// GetInstance returns a single service instance by ID, or nil if not found.
func (r *Registry) GetInstance(serviceID string) (*Instance, error) {
	svc, _, err := r.client.Agent().Service(serviceID, nil)
	if err != nil {
		return nil, fmt.Errorf("consul get instance: %w", err)
	}
	if svc == nil {
		return nil, nil
	}

	meta := make(map[string]string)
	for k, v := range svc.Meta {
		meta[k] = v
	}

	r.mu.RLock()
	regTime := r.registrationTimes[serviceID]
	r.mu.RUnlock()

	return &Instance{
		ServiceName: svc.Service,
		ServiceID:   svc.ID,
		Address:     svc.Address,
		Port:        svc.Port,
		Status:      HealthUnknown, // single-instance lookup doesn't include health
		Metadata:    meta,
		RegisteredAt: regTime,
	}, nil
}

func mapHealthStatus(checks api.HealthChecks) HealthStatus {
	if len(checks) == 0 {
		return HealthUnknown
	}

	for _, c := range checks {
		if c.Status == "critical" || c.Status == "maintenance" {
			return HealthUnhealthy
		}
	}
	for _, c := range checks {
		if c.Status == "warning" {
			return HealthDegraded
		}
	}

	allPassing := true
	for _, c := range checks {
		if c.Status != "passing" {
			allPassing = false
			break
		}
	}
	if allPassing {
		return HealthHealthy
	}

	return HealthUnknown
}
