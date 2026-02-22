package router

import (
	"testing"
	"time"
)

// stubProvider is a test InstanceProvider backed by a static list.
type stubProvider struct {
	instances map[string][]Instance
}

func (s *stubProvider) GetInstances(serviceName string) ([]Instance, error) {
	return s.instances[serviceName], nil
}

func newProvider(instances ...Instance) *stubProvider {
	m := make(map[string][]Instance)
	for _, inst := range instances {
		m[inst.ServiceName] = append(m[inst.ServiceName], inst)
	}
	return &stubProvider{instances: m}
}

func makeInstance(id, serviceName string, status HealthStatus) Instance {
	return Instance{
		ServiceName: serviceName,
		ServiceID:   id,
		Address:     "localhost",
		Port:        8080,
		Status:      status,
		Metadata:    map[string]string{},
		RegisteredAt: time.Now(),
		LastHealthCheck: time.Now(),
	}
}

func makeInstanceWithMeta(id, serviceName string, status HealthStatus, meta map[string]string) Instance {
	inst := makeInstance(id, serviceName, status)
	inst.Metadata = meta
	return inst
}

func TestSelect_NoInstances_ReturnsNil(t *testing.T) {
	lb := NewLoadBalancer(newProvider())

	result, err := lb.Select("nonexistent", Context{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil, got %+v", result)
	}
}

func TestSelect_SingleInstance_ReturnsThatInstance(t *testing.T) {
	lb := NewLoadBalancer(newProvider(
		makeInstance("svc-1", "my-service", HealthHealthy),
	))

	result, err := lb.Select("my-service", Context{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected instance, got nil")
	}
	if result.ServiceID != "svc-1" {
		t.Fatalf("expected svc-1, got %s", result.ServiceID)
	}
}

func TestSelect_RoundRobin_DistributesEvenly(t *testing.T) {
	lb := NewLoadBalancer(newProvider(
		makeInstance("svc-1", "api", HealthHealthy),
		makeInstance("svc-2", "api", HealthHealthy),
		makeInstance("svc-3", "api", HealthHealthy),
	))

	counts := map[string]int{}
	for range 9 {
		result, err := lb.Select("api", Context{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		counts[result.ServiceID]++
	}

	for _, id := range []string{"svc-1", "svc-2", "svc-3"} {
		if counts[id] != 3 {
			t.Errorf("expected %s selected 3 times, got %d", id, counts[id])
		}
	}
}

func TestSelect_PrefersHealthyInstances(t *testing.T) {
	lb := NewLoadBalancer(newProvider(
		makeInstance("unhealthy-1", "api", HealthUnhealthy),
		makeInstance("healthy-1", "api", HealthHealthy),
		makeInstance("unhealthy-2", "api", HealthUnhealthy),
	))

	for range 5 {
		result, err := lb.Select("api", Context{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.ServiceID != "healthy-1" {
			t.Fatalf("expected healthy-1, got %s", result.ServiceID)
		}
	}
}

func TestSelect_FallsBackToNonUnknown(t *testing.T) {
	lb := NewLoadBalancer(newProvider(
		makeInstance("degraded-1", "api", HealthDegraded),
		makeInstance("unknown-1", "api", HealthUnknown),
	))

	result, err := lb.Select("api", Context{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ServiceID != "degraded-1" {
		t.Fatalf("expected degraded-1, got %s", result.ServiceID)
	}
}

func TestSelect_LeastConnections_AlternatesInstances(t *testing.T) {
	lb := NewLoadBalancer(newProvider(
		makeInstanceWithMeta("svc-1", "api", HealthHealthy, map[string]string{"lb_strategy": "LeastConnections"}),
		makeInstanceWithMeta("svc-2", "api", HealthHealthy, map[string]string{"lb_strategy": "LeastConnections"}),
	))

	first, _ := lb.Select("api", Context{})
	second, _ := lb.Select("api", Context{})

	if first.ServiceID == second.ServiceID {
		t.Fatalf("expected different instances, both got %s", first.ServiceID)
	}
}

func TestSelect_WeightedRoundRobin_RespectsWeights(t *testing.T) {
	lb := NewLoadBalancer(newProvider(
		makeInstanceWithMeta("svc-heavy", "api", HealthHealthy, map[string]string{"lb_strategy": "WeightedRoundRobin", "weight": "3"}),
		makeInstanceWithMeta("svc-light", "api", HealthHealthy, map[string]string{"lb_strategy": "WeightedRoundRobin", "weight": "1"}),
	))

	counts := map[string]int{}
	for range 8 {
		result, _ := lb.Select("api", Context{})
		counts[result.ServiceID]++
	}

	if counts["svc-heavy"] <= counts["svc-light"] {
		t.Fatalf("expected svc-heavy (%d) > svc-light (%d)", counts["svc-heavy"], counts["svc-light"])
	}
}

func TestSelect_IPHash_SameSessionSameInstance(t *testing.T) {
	lb := NewLoadBalancer(newProvider(
		makeInstanceWithMeta("svc-1", "api", HealthHealthy, map[string]string{"lb_strategy": "IPHash"}),
		makeInstanceWithMeta("svc-2", "api", HealthHealthy, map[string]string{"lb_strategy": "IPHash"}),
		makeInstanceWithMeta("svc-3", "api", HealthHealthy, map[string]string{"lb_strategy": "IPHash"}),
	))

	ctx := Context{SessionID: "user-session-123"}

	first, _ := lb.Select("api", ctx)
	second, _ := lb.Select("api", ctx)
	third, _ := lb.Select("api", ctx)

	if first.ServiceID != second.ServiceID || second.ServiceID != third.ServiceID {
		t.Fatalf("expected same instance for same session, got %s, %s, %s",
			first.ServiceID, second.ServiceID, third.ServiceID)
	}
}

func TestSelect_IPHash_DifferentSessionsCanDiffer(t *testing.T) {
	lb := NewLoadBalancer(newProvider(
		makeInstanceWithMeta("svc-1", "api", HealthHealthy, map[string]string{"lb_strategy": "IPHash"}),
		makeInstanceWithMeta("svc-2", "api", HealthHealthy, map[string]string{"lb_strategy": "IPHash"}),
	))

	seen := map[string]bool{}
	for i := range 20 {
		ctx := Context{SessionID: "session-" + string(rune('A'+i))}
		result, _ := lb.Select("api", ctx)
		seen[result.ServiceID] = true
	}

	if len(seen) < 2 {
		t.Fatal("expected different sessions to map to different instances")
	}
}

func TestReportResult_TracksSuccess(t *testing.T) {
	lb := NewLoadBalancer(newProvider(
		makeInstance("svc-1", "api", HealthHealthy),
	))

	lb.Select("api", Context{})
	lb.ReportResult("svc-1", RequestResult{
		ServiceID:    "svc-1",
		Success:      true,
		ResponseTime: 50 * time.Millisecond,
	})

	stats := lb.Stats("api")
	if stats.TotalRequests != 1 {
		t.Fatalf("expected 1 total request, got %d", stats.TotalRequests)
	}
	if stats.SuccessfulRequests != 1 {
		t.Fatalf("expected 1 successful request, got %d", stats.SuccessfulRequests)
	}
	if stats.FailedRequests != 0 {
		t.Fatalf("expected 0 failed requests, got %d", stats.FailedRequests)
	}
}

func TestReportResult_TracksFailure(t *testing.T) {
	lb := NewLoadBalancer(newProvider(
		makeInstance("svc-1", "api", HealthHealthy),
	))

	lb.Select("api", Context{})
	lb.ReportResult("svc-1", RequestResult{
		ServiceID:    "svc-1",
		Success:      false,
		ResponseTime: 100 * time.Millisecond,
		StatusCode:   500,
		ErrorMessage: "Internal error",
	})

	stats := lb.Stats("api")
	if stats.TotalRequests != 1 {
		t.Fatalf("expected 1 total request, got %d", stats.TotalRequests)
	}
	if stats.SuccessfulRequests != 0 {
		t.Fatalf("expected 0 successful, got %d", stats.SuccessfulRequests)
	}
	if stats.FailedRequests != 1 {
		t.Fatalf("expected 1 failed, got %d", stats.FailedRequests)
	}
}

func TestStats_UnknownService_ReturnsEmpty(t *testing.T) {
	lb := NewLoadBalancer(newProvider())

	stats := lb.Stats("unknown-service")
	if stats.ServiceName != "unknown-service" {
		t.Fatalf("expected service name 'unknown-service', got %s", stats.ServiceName)
	}
	if stats.TotalRequests != 0 {
		t.Fatalf("expected 0 requests, got %d", stats.TotalRequests)
	}
}

func TestSelect_Random_ReturnsValidInstance(t *testing.T) {
	lb := NewLoadBalancer(newProvider(
		makeInstanceWithMeta("svc-1", "api", HealthHealthy, map[string]string{"lb_strategy": "Random"}),
		makeInstanceWithMeta("svc-2", "api", HealthHealthy, map[string]string{"lb_strategy": "Random"}),
	))

	for range 10 {
		result, err := lb.Select("api", Context{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
	}
}

func TestFNV1a_Deterministic(t *testing.T) {
	h1 := fnv1a("test-session")
	h2 := fnv1a("test-session")
	if h1 != h2 {
		t.Fatalf("expected same hash, got %d and %d", h1, h2)
	}
}

func TestParseStrategy(t *testing.T) {
	tests := []struct {
		input    string
		expected Strategy
	}{
		{"RoundRobin", RoundRobin},
		{"LeastConnections", LeastConnections},
		{"Random", Random},
		{"WeightedRoundRobin", WeightedRoundRobin},
		{"IPHash", IPHash},
		{"unknown", RoundRobin},
		{"", RoundRobin},
	}

	for _, tt := range tests {
		got := ParseStrategy(tt.input)
		if got != tt.expected {
			t.Errorf("ParseStrategy(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}
