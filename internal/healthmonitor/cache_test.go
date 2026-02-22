package healthmonitor

import (
	"testing"
)

func TestCache_UpdateAndGet(t *testing.T) {
	c := NewCache()

	c.Update("svc-1", "api", "10.0.0.1", 8080, StatusHealthy, "http", "HTTP 200", nil)

	inst := c.Get("svc-1")
	if inst == nil {
		t.Fatal("expected instance, got nil")
	}
	if inst.Status != StatusHealthy {
		t.Fatalf("expected Healthy, got %v", inst.Status)
	}
	if inst.Address != "10.0.0.1" {
		t.Fatalf("expected 10.0.0.1, got %s", inst.Address)
	}
}

func TestCache_GetAll(t *testing.T) {
	c := NewCache()

	c.Update("svc-1", "api", "10.0.0.1", 8080, StatusHealthy, "http", "", nil)
	c.Update("svc-2", "web", "10.0.0.2", 8081, StatusUnhealthy, "tcp", "", nil)

	all := c.GetAll()
	if len(all) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(all))
	}
}

func TestCache_GetByService(t *testing.T) {
	c := NewCache()

	c.Update("svc-1", "api", "10.0.0.1", 8080, StatusHealthy, "http", "", nil)
	c.Update("svc-2", "api", "10.0.0.2", 8081, StatusHealthy, "http", "", nil)
	c.Update("svc-3", "web", "10.0.0.3", 8082, StatusHealthy, "http", "", nil)

	apis := c.GetByService("api")
	if len(apis) != 2 {
		t.Fatalf("expected 2 api instances, got %d", len(apis))
	}

	webs := c.GetByService("web")
	if len(webs) != 1 {
		t.Fatalf("expected 1 web instance, got %d", len(webs))
	}
}

func TestCache_PreviousStatus(t *testing.T) {
	c := NewCache()

	// Unknown for untracked instance.
	if s := c.PreviousStatus("unknown"); s != StatusUnknown {
		t.Fatalf("expected Unknown, got %v", s)
	}

	c.Update("svc-1", "api", "10.0.0.1", 8080, StatusHealthy, "http", "", nil)

	if s := c.PreviousStatus("svc-1"); s != StatusHealthy {
		t.Fatalf("expected Healthy, got %v", s)
	}
}

func TestCache_UpdateOverwrites(t *testing.T) {
	c := NewCache()

	c.Update("svc-1", "api", "10.0.0.1", 8080, StatusHealthy, "http", "HTTP 200", nil)
	c.Update("svc-1", "api", "10.0.0.1", 8080, StatusUnhealthy, "http", "HTTP 500", nil)

	inst := c.Get("svc-1")
	if inst.Status != StatusUnhealthy {
		t.Fatalf("expected Unhealthy after overwrite, got %v", inst.Status)
	}
	if inst.Message != "HTTP 500" {
		t.Fatalf("expected 'HTTP 500', got %q", inst.Message)
	}
}

func TestCache_GetReturnsNilForUnknown(t *testing.T) {
	c := NewCache()

	if inst := c.Get("nonexistent"); inst != nil {
		t.Fatalf("expected nil, got %+v", inst)
	}
}
