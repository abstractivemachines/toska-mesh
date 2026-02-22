package healthmonitor

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/toska-mesh/toska-mesh/internal/consul"
	"github.com/toska-mesh/toska-mesh/internal/messaging"
)

// Worker is the background health probe service. It periodically queries
// Consul for registered services, probes each instance via HTTP or TCP,
// and caches the results.
type Worker struct {
	registry  *consul.Registry
	publisher *messaging.Publisher
	cache     *Cache
	config    Config
	logger    *slog.Logger
	client    *http.Client

	mu       sync.Mutex
	breakers map[string]*CircuitBreaker
}

// NewWorker creates a HealthMonitor probe worker.
func NewWorker(registry *consul.Registry, publisher *messaging.Publisher, cache *Cache, config Config, logger *slog.Logger) *Worker {
	return &Worker{
		registry:  registry,
		publisher: publisher,
		cache:     cache,
		config:    config,
		logger:    logger,
		client: &http.Client{
			Timeout: config.HTTPTimeout,
		},
		breakers: make(map[string]*CircuitBreaker),
	}
}

// Run starts the probe loop. It blocks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	w.logger.Info("health probe worker starting",
		"probe_interval", w.config.ProbeInterval,
		"failure_threshold", w.config.FailureThreshold,
	)

	ticker := time.NewTicker(w.config.ProbeInterval)
	defer ticker.Stop()

	// Run immediately on start, then on each tick.
	w.probeAll(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("health probe worker stopping")
			return
		case <-ticker.C:
			w.probeAll(ctx)
		}
	}
}

func (w *Worker) probeAll(ctx context.Context) {
	services, err := w.registry.GetServices()
	if err != nil {
		w.logger.Error("failed to list services", "error", err)
		return
	}

	// Collect all live service IDs so we can evict stale cache entries.
	var liveIDsMu sync.Mutex
	liveIDs := make(map[string]struct{})

	// Fan out at the service level so slow services don't block others.
	var svcWg sync.WaitGroup
	for _, serviceName := range services {
		svcWg.Add(1)
		go func(serviceName string) {
			defer svcWg.Done()

			instances, err := w.registry.GetInstances(serviceName)
			if err != nil {
				w.logger.Error("failed to list instances", "service", serviceName, "error", err)
				return
			}

			liveIDsMu.Lock()
			for _, inst := range instances {
				liveIDs[inst.ServiceID] = struct{}{}
			}
			liveIDsMu.Unlock()

			// Fan out: probe all instances concurrently.
			var instWg sync.WaitGroup
			for _, inst := range instances {
				instWg.Add(1)
				go func(inst consul.Instance) {
					defer instWg.Done()
					w.probeInstance(ctx, inst)
				}(inst)
			}
			instWg.Wait()
		}(serviceName)
	}
	svcWg.Wait()

	// Evict cache entries for services no longer registered in Consul.
	for _, cached := range w.cache.GetAll() {
		if _, ok := liveIDs[cached.ServiceID]; !ok {
			w.cache.Remove(cached.ServiceID)
		}
	}
}

func (w *Worker) probeInstance(ctx context.Context, inst consul.Instance) {
	breaker := w.getBreaker(inst.ServiceID)

	if !breaker.Allow() {
		w.updateStatus(ctx, inst, StatusUnhealthy, "circuit-breaker", "Circuit open due to repeated failures")
		return
	}

	status, probeType, message := w.runProbes(ctx, inst)

	if status == StatusHealthy {
		breaker.RecordSuccess()
	} else {
		breaker.RecordFailure()
	}

	w.updateStatus(ctx, inst, status, probeType, message)
}

func (w *Worker) runProbes(ctx context.Context, inst consul.Instance) (HealthStatus, string, string) {
	// Try HTTP probe first.
	if endpoint, ok := inst.Metadata["health_check_endpoint"]; ok && endpoint != "" {
		status, msg := w.httpProbe(ctx, inst, endpoint)
		return status, "http", msg
	}

	// Fall back to TCP probe.
	if portStr, ok := inst.Metadata["tcp_port"]; ok && portStr != "" {
		status, msg := w.tcpProbe(ctx, inst, portStr)
		return status, "tcp", msg
	}

	return StatusUnknown, "none", "No probe configuration available"
}

func (w *Worker) httpProbe(ctx context.Context, inst consul.Instance, endpoint string) (HealthStatus, string) {
	scheme := "http"
	if s, ok := inst.Metadata["scheme"]; ok && s != "" {
		scheme = s
	}

	url := fmt.Sprintf("%s://%s:%d%s", scheme, inst.Address, inst.Port, endpoint)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return StatusUnhealthy, fmt.Sprintf("request error: %v", err)
	}

	for k, v := range w.config.HTTPHeaders {
		req.Header.Set(k, v)
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return StatusUnhealthy, fmt.Sprintf("probe failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return StatusHealthy, fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return StatusUnhealthy, fmt.Sprintf("HTTP %d", resp.StatusCode)
}

func (w *Worker) tcpProbe(ctx context.Context, inst consul.Instance, portStr string) (HealthStatus, string) {
	addr := net.JoinHostPort(inst.Address, portStr)

	var d net.Dialer
	d.Timeout = w.config.TCPTimeout

	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return StatusUnhealthy, fmt.Sprintf("TCP connection failed: %v", err)
	}
	conn.Close()

	return StatusHealthy, "TCP connection successful"
}

func (w *Worker) updateStatus(ctx context.Context, inst consul.Instance, status HealthStatus, probeType, message string) {
	previousStatus := w.cache.PreviousStatus(inst.ServiceID)

	w.cache.Update(
		inst.ServiceID, inst.ServiceName,
		inst.Address, inst.Port,
		status, probeType, message,
		inst.Metadata,
	)

	// Publish health change event if status transitioned.
	if previousStatus != status && previousStatus != StatusUnknown {
		_ = w.publisher.Publish(ctx, messaging.ServiceHealthChangedEvent{
			EventID:           fmt.Sprintf("%d", time.Now().UnixNano()),
			Timestamp:         time.Now().UTC(),
			ServiceID:         inst.ServiceID,
			ServiceName:       inst.ServiceName,
			PreviousStatus:    previousStatus.String(),
			CurrentStatus:     status.String(),
			HealthCheckOutput: message,
		})
	}
}

func (w *Worker) getBreaker(serviceID string) *CircuitBreaker {
	w.mu.Lock()
	defer w.mu.Unlock()

	if cb, ok := w.breakers[serviceID]; ok {
		return cb
	}

	breakDuration := w.config.ProbeInterval * 2
	cb := NewCircuitBreakerWithRecovery(w.config.FailureThreshold, w.config.RecoveryThreshold, breakDuration)
	w.breakers[serviceID] = cb
	return cb
}
