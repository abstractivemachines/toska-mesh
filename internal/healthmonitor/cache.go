package healthmonitor

import (
	"sync"
	"time"

	"github.com/toska-mesh/toska-mesh/internal/types"
)

// HealthStatus is an alias for the shared health status type.
type HealthStatus = types.HealthStatus

// Re-export health status constants with this package's naming convention.
const (
	StatusUnknown   = types.HealthUnknown
	StatusHealthy   = types.HealthHealthy
	StatusUnhealthy = types.HealthUnhealthy
	StatusDegraded  = types.HealthDegraded
)

// MonitoredInstance holds the latest probe result for a service instance.
type MonitoredInstance struct {
	ServiceID   string            `json:"serviceId"`
	ServiceName string            `json:"serviceName"`
	Address     string            `json:"address"`
	Port        int               `json:"port"`
	Status      HealthStatus      `json:"status"`
	LastProbe   time.Time         `json:"lastProbe"`
	ProbeType   string            `json:"probeType"`
	Message     string            `json:"message,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Cache is a thread-safe store of the latest health probe results.
type Cache struct {
	mu        sync.RWMutex
	instances map[string]*MonitoredInstance
}

// NewCache creates an empty health report cache.
func NewCache() *Cache {
	return &Cache{
		instances: make(map[string]*MonitoredInstance),
	}
}

// Update records a probe result for an instance.
func (c *Cache) Update(serviceID, serviceName, address string, port int,
	status HealthStatus, probeType, message string, metadata map[string]string) {

	c.mu.Lock()
	defer c.mu.Unlock()

	c.instances[serviceID] = &MonitoredInstance{
		ServiceID:   serviceID,
		ServiceName: serviceName,
		Address:     address,
		Port:        port,
		Status:      status,
		LastProbe:   time.Now().UTC(),
		ProbeType:   probeType,
		Message:     message,
		Metadata:    metadata,
	}
}

// GetAll returns a snapshot of all monitored instances.
func (c *Cache) GetAll() []MonitoredInstance {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]MonitoredInstance, 0, len(c.instances))
	for _, inst := range c.instances {
		out = append(out, *inst)
	}
	return out
}

// GetByService returns monitored instances matching the given service name.
func (c *Cache) GetByService(serviceName string) []MonitoredInstance {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var out []MonitoredInstance
	for _, inst := range c.instances {
		if inst.ServiceName == serviceName {
			out = append(out, *inst)
		}
	}
	return out
}

// Get returns the monitored instance for a specific service ID, or nil.
func (c *Cache) Get(serviceID string) *MonitoredInstance {
	c.mu.RLock()
	defer c.mu.RUnlock()

	inst, ok := c.instances[serviceID]
	if !ok {
		return nil
	}
	copy := *inst
	return &copy
}

// Remove deletes a service instance from the cache.
func (c *Cache) Remove(serviceID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.instances, serviceID)
}

// RemoveByService deletes all instances matching the given service name.
func (c *Cache) RemoveByService(serviceName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for id, inst := range c.instances {
		if inst.ServiceName == serviceName {
			delete(c.instances, id)
		}
	}
}

// EvictOlderThan removes entries whose last probe is older than the given cutoff.
func (c *Cache) EvictOlderThan(cutoff time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for id, inst := range c.instances {
		if inst.LastProbe.Before(cutoff) {
			delete(c.instances, id)
		}
	}
}

// PreviousStatus returns the last known status for a service ID.
// Returns StatusUnknown if not tracked.
func (c *Cache) PreviousStatus(serviceID string) HealthStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if inst, ok := c.instances[serviceID]; ok {
		return inst.Status
	}
	return StatusUnknown
}
