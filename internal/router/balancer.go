package router

import (
	"math/rand/v2"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// LoadBalancer implements the Balancer interface with support for multiple strategies.
type LoadBalancer struct {
	provider InstanceProvider

	mu              sync.Mutex
	roundRobinIdx   map[string]*atomic.Int64
	connectionCount map[string]map[string]*atomic.Int64
	stats           map[string]*serviceStats
}

// NewLoadBalancer creates a LoadBalancer that fetches instances from provider.
func NewLoadBalancer(provider InstanceProvider) *LoadBalancer {
	return &LoadBalancer{
		provider:        provider,
		roundRobinIdx:   make(map[string]*atomic.Int64),
		connectionCount: make(map[string]map[string]*atomic.Int64),
		stats:           make(map[string]*serviceStats),
	}
}

func (lb *LoadBalancer) Select(serviceName string, ctx Context) (*Instance, error) {
	instances, err := lb.provider.GetInstances(serviceName)
	if err != nil {
		return nil, err
	}

	candidates := filterHealthy(instances)
	if len(candidates) == 0 {
		candidates = filterNonUnknown(instances)
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	strategy := resolveStrategy(candidates)
	var selected *Instance

	switch strategy {
	case LeastConnections:
		selected = lb.selectLeastConnections(serviceName, candidates)
	case WeightedRoundRobin:
		selected = lb.selectWeightedRoundRobin(serviceName, candidates)
	case IPHash:
		selected = selectIPHash(candidates, ctx)
	case Random:
		selected = selectRandom(candidates)
	default:
		selected = lb.selectRoundRobin(serviceName, candidates)
	}

	if selected != nil {
		lb.recordRequest(serviceName, selected)
	}

	return selected, nil
}

func (lb *LoadBalancer) ReportResult(serviceID string, result RequestResult) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	// Decrement connection count across all services.
	for _, counts := range lb.connectionCount {
		if c, ok := counts[serviceID]; ok {
			if v := c.Load(); v > 0 {
				c.Add(-1)
			}
		}
	}

	if s, ok := lb.stats[serviceID]; ok {
		s.report(result)
	}
}

func (lb *LoadBalancer) Stats(serviceName string) Stats {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	var totalReq, successReq, failedReq int64
	var totalTicks int64
	instanceCounts := make(map[string]int)

	for _, s := range lb.stats {
		if s.serviceName != serviceName {
			continue
		}
		totalReq += s.totalRequests.Load()
		successReq += s.successfulRequests.Load()
		failedReq += s.failedRequests.Load()
		totalTicks += s.totalResponseNanos.Load()
		s.mu.Lock()
		for instID, count := range s.instanceCounts {
			instanceCounts[instID] += count
		}
		s.mu.Unlock()
	}

	var avg time.Duration
	if totalReq > 0 {
		avg = time.Duration(totalTicks / totalReq)
	}

	return Stats{
		ServiceName:           serviceName,
		TotalRequests:         int(totalReq),
		SuccessfulRequests:    int(successReq),
		FailedRequests:        int(failedReq),
		AverageResponseTime:   avg,
		InstanceRequestCounts: instanceCounts,
	}
}

// --- Strategy implementations ---

func (lb *LoadBalancer) selectRoundRobin(serviceName string, instances []Instance) *Instance {
	idx := lb.getRoundRobinIdx(serviceName)
	n := idx.Add(1)
	i := abs64(n) % int64(len(instances))
	return &instances[i]
}

func (lb *LoadBalancer) selectLeastConnections(serviceName string, instances []Instance) *Instance {
	counts := lb.getConnectionCounts(serviceName)

	var best *Instance
	var bestCount int64 = -1

	for i := range instances {
		c := lb.getOrCreateCounter(counts, instances[i].ServiceID)
		v := c.Load()
		if bestCount < 0 || v < bestCount {
			bestCount = v
			best = &instances[i]
		}
	}

	if best != nil {
		c := lb.getOrCreateCounter(counts, best.ServiceID)
		c.Add(1)
	}

	return best
}

func (lb *LoadBalancer) selectWeightedRoundRobin(serviceName string, instances []Instance) *Instance {
	var weighted []Instance
	for _, inst := range instances {
		weight := 1
		if w, ok := inst.Metadata["weight"]; ok {
			if parsed, err := strconv.Atoi(w); err == nil && parsed > 0 {
				weight = parsed
			}
		}
		for range weight {
			weighted = append(weighted, inst)
		}
	}
	return lb.selectRoundRobin(serviceName+"-weighted", weighted)
}

func selectIPHash(instances []Instance, ctx Context) *Instance {
	key := ctx.SessionID
	if key == "" {
		if ctx.Headers != nil {
			key = ctx.Headers["X-Correlation-ID"]
		}
	}
	if key == "" {
		key = strconv.FormatInt(rand.Int64(), 16)
	}
	h := fnv1a(key)
	i := h % uint32(len(instances))
	return &instances[i]
}

func selectRandom(instances []Instance) *Instance {
	i := rand.IntN(len(instances))
	return &instances[i]
}

// --- Helpers ---

func filterHealthy(instances []Instance) []Instance {
	var out []Instance
	for _, inst := range instances {
		if inst.Status == HealthHealthy {
			out = append(out, inst)
		}
	}
	return out
}

func filterNonUnknown(instances []Instance) []Instance {
	var out []Instance
	for _, inst := range instances {
		if inst.Status != HealthUnknown {
			out = append(out, inst)
		}
	}
	return out
}

func resolveStrategy(candidates []Instance) Strategy {
	for _, inst := range candidates {
		if s, ok := inst.Metadata["lb_strategy"]; ok && s != "" {
			return ParseStrategy(s)
		}
	}
	return RoundRobin
}

func (lb *LoadBalancer) getRoundRobinIdx(name string) *atomic.Int64 {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	idx, ok := lb.roundRobinIdx[name]
	if !ok {
		idx = &atomic.Int64{}
		lb.roundRobinIdx[name] = idx
	}
	return idx
}

func (lb *LoadBalancer) getConnectionCounts(serviceName string) map[string]*atomic.Int64 {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	counts, ok := lb.connectionCount[serviceName]
	if !ok {
		counts = make(map[string]*atomic.Int64)
		lb.connectionCount[serviceName] = counts
	}
	return counts
}

func (lb *LoadBalancer) getOrCreateCounter(counts map[string]*atomic.Int64, serviceID string) *atomic.Int64 {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	c, ok := counts[serviceID]
	if !ok {
		c = &atomic.Int64{}
		counts[serviceID] = c
	}
	return c
}

func (lb *LoadBalancer) recordRequest(serviceName string, inst *Instance) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	s, ok := lb.stats[inst.ServiceID]
	if !ok {
		s = newServiceStats(serviceName)
		lb.stats[inst.ServiceID] = s
	}
	s.recordRequest(inst.ServiceID)
}

// fnv1a computes FNV-1a hash matching the C# implementation.
func fnv1a(s string) uint32 {
	const (
		offsetBasis = 2166136261
		prime       = 16777619
	)
	h := uint32(offsetBasis)
	for _, c := range s {
		h ^= uint32(c)
		h *= prime
	}
	return h
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// --- Stats tracking ---

type serviceStats struct {
	serviceName        string
	totalRequests      atomic.Int64
	successfulRequests atomic.Int64
	failedRequests     atomic.Int64
	totalResponseNanos atomic.Int64

	mu             sync.Mutex
	instanceCounts map[string]int
}

func newServiceStats(serviceName string) *serviceStats {
	return &serviceStats{
		serviceName:    serviceName,
		instanceCounts: make(map[string]int),
	}
}

func (s *serviceStats) recordRequest(instanceID string) {
	s.totalRequests.Add(1)
	s.mu.Lock()
	s.instanceCounts[instanceID]++
	s.mu.Unlock()
}

func (s *serviceStats) report(result RequestResult) {
	if result.Success {
		s.successfulRequests.Add(1)
	} else {
		s.failedRequests.Add(1)
	}
	s.totalResponseNanos.Add(int64(result.ResponseTime))
}
