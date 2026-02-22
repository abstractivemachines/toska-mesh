package healthmonitor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/toska-mesh/toska-mesh/internal/consul"
)

func TestWorker_HTTPProbe_Healthy(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"status":"Healthy"}`)
	}))
	defer ts.Close()

	// Parse test server address.
	addr := ts.Listener.Addr().String()
	parts := strings.SplitN(addr, ":", 2)

	w := &Worker{
		config: DefaultConfig(),
		client: ts.Client(),
	}

	inst := consul.Instance{
		ServiceID:   "svc-1",
		ServiceName: "api",
		Address:     parts[0],
		Port:        mustPort(parts[1]),
		Metadata: map[string]string{
			"health_check_endpoint": "/health",
			"scheme":                "http",
		},
	}

	status, msg := w.httpProbe(context.Background(), inst, "/health")
	if status != StatusHealthy {
		t.Fatalf("expected Healthy, got %v (%s)", status, msg)
	}
}

func TestWorker_HTTPProbe_Unhealthy(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	addr := ts.Listener.Addr().String()
	parts := strings.SplitN(addr, ":", 2)

	w := &Worker{
		config: DefaultConfig(),
		client: ts.Client(),
	}

	inst := consul.Instance{
		ServiceID:   "svc-1",
		ServiceName: "api",
		Address:     parts[0],
		Port:        mustPort(parts[1]),
		Metadata: map[string]string{
			"health_check_endpoint": "/health",
			"scheme":                "http",
		},
	}

	status, msg := w.httpProbe(context.Background(), inst, "/health")
	if status != StatusUnhealthy {
		t.Fatalf("expected Unhealthy, got %v (%s)", status, msg)
	}
	if !strings.Contains(msg, "503") {
		t.Fatalf("expected message to contain 503, got %q", msg)
	}
}

func TestWorker_HTTPProbe_ConnectionRefused(t *testing.T) {
	w := &Worker{
		config: Config{HTTPTimeout: 1 * time.Second},
		client: &http.Client{Timeout: 1 * time.Second},
	}

	inst := consul.Instance{
		ServiceID:   "svc-1",
		ServiceName: "api",
		Address:     "127.0.0.1",
		Port:        19999, // nothing listening
		Metadata: map[string]string{
			"health_check_endpoint": "/health",
			"scheme":                "http",
		},
	}

	status, _ := w.httpProbe(context.Background(), inst, "/health")
	if status != StatusUnhealthy {
		t.Fatalf("expected Unhealthy for connection refused, got %v", status)
	}
}

func TestWorker_RunProbes_NoConfig_ReturnsUnknown(t *testing.T) {
	w := &Worker{
		config: DefaultConfig(),
		client: &http.Client{Timeout: 1 * time.Second},
	}

	inst := consul.Instance{
		ServiceID:   "svc-1",
		ServiceName: "api",
		Address:     "127.0.0.1",
		Port:        8080,
		Metadata:    map[string]string{}, // no health_check_endpoint or tcp_port
	}

	status, probeType, _ := w.runProbes(context.Background(), inst)
	if status != StatusUnknown {
		t.Fatalf("expected Unknown, got %v", status)
	}
	if probeType != "none" {
		t.Fatalf("expected probe type 'none', got %q", probeType)
	}
}

func mustPort(s string) int {
	var port int
	fmt.Sscanf(s, "%d", &port)
	return port
}
