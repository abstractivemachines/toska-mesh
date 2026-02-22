package gateway

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestProxy_RoutesToBackend(t *testing.T) {
	// Spin up a fake backend.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify path stripping: the gateway should strip /api/my-service.
		if r.URL.Path != "/hello" {
			t.Errorf("expected backend path /hello, got %s", r.URL.Path)
		}
		fmt.Fprintln(w, "OK from backend")
	}))
	defer backend.Close()

	// Build a route table with a single service.
	rt := &RouteTable{
		config: RoutingConfig{RoutePrefix: "/api/"},
		routes: map[string]*ServiceRoute{
			"my-service": {
				ServiceName: "my-service",
				Backends:    []Backend{{ServiceID: "svc-1", Address: backend.URL}},
			},
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	proxy := NewProxy(rt, ResilienceConfig{RetryCount: 0, BreakerFailureThreshold: 10, BreakerBreakDuration: 60_000_000_000}, logger)

	req := httptest.NewRequest("GET", "/api/my-service/hello", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if !strings.Contains(w.Body.String(), "OK from backend") {
		t.Fatalf("expected response from backend, got %q", w.Body.String())
	}
}

func TestProxy_Returns502ForUnknownService(t *testing.T) {
	rt := &RouteTable{
		config: RoutingConfig{RoutePrefix: "/api/"},
		routes: map[string]*ServiceRoute{},
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	proxy := NewProxy(rt, ResilienceConfig{RetryCount: 0, BreakerFailureThreshold: 10, BreakerBreakDuration: 60_000_000_000}, logger)

	req := httptest.NewRequest("GET", "/api/unknown-svc/foo", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

func TestProxy_Retries5xxAndReturnsLastResponse(t *testing.T) {
	attempts := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			http.Error(w, "error", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "recovered")
	}))
	defer backend.Close()

	rt := &RouteTable{
		config: RoutingConfig{RoutePrefix: "/api/"},
		routes: map[string]*ServiceRoute{
			"svc": {
				ServiceName: "svc",
				Backends:    []Backend{{ServiceID: "svc-1", Address: backend.URL}},
			},
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	proxy := NewProxy(rt, ResilienceConfig{
		RetryCount:              3,
		RetryBaseDelay:          1 * time.Millisecond,
		RetryBackoffExponent:    1.0,
		RetryJitterMax:          0,
		BreakerFailureThreshold: 10,
		BreakerBreakDuration:    60_000_000_000,
	}, logger)

	req := httptest.NewRequest("GET", "/api/svc/data", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 after retry recovery, got %d: %s", w.Code, w.Body.String())
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestProxy_PreservesQueryString(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "page=2&limit=10" {
			t.Errorf("expected query string 'page=2&limit=10', got %q", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	rt := &RouteTable{
		config: RoutingConfig{RoutePrefix: "/api/"},
		routes: map[string]*ServiceRoute{
			"svc": {
				ServiceName: "svc",
				Backends:    []Backend{{ServiceID: "svc-1", Address: backend.URL}},
			},
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	proxy := NewProxy(rt, ResilienceConfig{RetryCount: 0, BreakerFailureThreshold: 10, BreakerBreakDuration: 60_000_000_000}, logger)

	req := httptest.NewRequest("GET", "/api/svc/data?page=2&limit=10", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
