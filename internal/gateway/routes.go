package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/toska-mesh/toska-mesh/internal/consul"
)

// Backend represents a single healthy service instance that can receive traffic.
type Backend struct {
	ServiceID string
	Address   string // full URL: scheme://host:port
}

// ServiceRoute holds the backends for a single service.
type ServiceRoute struct {
	ServiceName string
	Backends    []Backend
}

// RouteTable maintains a dynamic mapping of service names to healthy backends,
// refreshed periodically from Consul.
type RouteTable struct {
	registry *consul.Registry
	config   RoutingConfig
	logger   *slog.Logger

	mu     sync.RWMutex
	routes map[string]*ServiceRoute // keyed by lowercase service name
}

// NewRouteTable creates a RouteTable that will poll Consul on the given interval.
func NewRouteTable(registry *consul.Registry, config RoutingConfig, logger *slog.Logger) *RouteTable {
	return &RouteTable{
		registry: registry,
		config:   config,
		logger:   logger,
		routes:   make(map[string]*ServiceRoute),
	}
}

// Run starts the background refresh loop. Blocks until ctx is cancelled.
func (rt *RouteTable) Run(ctx context.Context) {
	rt.refresh()

	ticker := time.NewTicker(rt.config.RefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rt.refresh()
		}
	}
}

// Lookup returns a random healthy backend for the given service name, or nil.
func (rt *RouteTable) Lookup(serviceName string) *Backend {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	route, ok := rt.routes[strings.ToLower(serviceName)]
	if !ok || len(route.Backends) == 0 {
		return nil
	}

	// Simple random selection (YARP default is round-robin, but random is
	// sufficient for the initial port — the router package has full LB).
	idx := rand.IntN(len(route.Backends))
	return &route.Backends[idx]
}

// Services returns the list of currently routed service names.
func (rt *RouteTable) Services() []string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	names := make([]string, 0, len(rt.routes))
	for _, route := range rt.routes {
		names = append(names, route.ServiceName)
	}
	return names
}

// Prefix returns the normalized route prefix (e.g. "/api/").
func (rt *RouteTable) Prefix() string {
	return normalizePrefix(rt.config.RoutePrefix)
}

func (rt *RouteTable) refresh() {
	services, err := rt.registry.GetServices()
	if err != nil {
		rt.logger.Error("failed to list services from Consul", "error", err)
		return
	}

	newRoutes := make(map[string]*ServiceRoute, len(services))

	for _, serviceName := range services {
		if strings.EqualFold(serviceName, "consul") {
			continue
		}

		instances, err := rt.registry.GetInstances(serviceName)
		if err != nil {
			rt.logger.Error("failed to get instances", "service", serviceName, "error", err)
			continue
		}

		var backends []Backend
		for _, inst := range instances {
			if inst.Status != consul.HealthHealthy {
				continue
			}

			scheme := "http"
			if s, ok := inst.Metadata["scheme"]; ok && s != "" {
				scheme = s
			}

			backends = append(backends, Backend{
				ServiceID: inst.ServiceID,
				Address:   fmt.Sprintf("%s://%s:%d", scheme, inst.Address, inst.Port),
			})
		}

		if len(backends) == 0 {
			rt.logger.Warn("no healthy instances", "service", serviceName)
			continue
		}

		newRoutes[strings.ToLower(serviceName)] = &ServiceRoute{
			ServiceName: serviceName,
			Backends:    backends,
		}
	}

	rt.mu.Lock()
	rt.routes = newRoutes
	rt.mu.Unlock()

	rt.logger.Info("route table refreshed", "services", len(newRoutes))
}

// normalizePrefix ensures the prefix starts and ends with "/".
func normalizePrefix(prefix string) string {
	if prefix == "" {
		return "/"
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return prefix
}

// ParseServiceFromPath extracts the service name from a request path given a prefix.
// For example, with prefix "/api/" and path "/api/my-service/foo/bar",
// returns ("my-service", "/foo/bar", true).
func ParseServiceFromPath(prefix, path string) (serviceName, remainder string, ok bool) {
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}

	rest := path[len(prefix):]
	if rest == "" {
		return "", "", false
	}

	// Split on first "/".
	idx := strings.IndexByte(rest, '/')
	if idx < 0 {
		return rest, "/", true
	}
	return rest[:idx], rest[idx:], true
}

// BuildBackendURL constructs the full backend URL for a request.
func BuildBackendURL(backendAddr, remainder, rawQuery string) string {
	u, err := url.Parse(backendAddr)
	if err != nil {
		return backendAddr + remainder
	}
	u.Path = remainder
	u.RawQuery = rawQuery
	return u.String()
}
