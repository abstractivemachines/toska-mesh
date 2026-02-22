package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// DashboardProxy proxies requests to internal observability services
// (Prometheus, Tracing, Discovery, HealthMonitor).
type DashboardProxy struct {
	config DashboardConfig
	logger *slog.Logger
	client *http.Client
}

// NewDashboardProxy creates a proxy for dashboard API routes.
func NewDashboardProxy(config DashboardConfig, logger *slog.Logger) *DashboardProxy {
	return &DashboardProxy{
		config: config,
		logger: logger,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Handler returns an http.Handler mounted at /api/dashboard/.
func (dp *DashboardProxy) Handler() http.Handler {
	mux := http.NewServeMux()

	// Prometheus proxy.
	mux.HandleFunc("/api/dashboard/prometheus/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/dashboard/prometheus")
		dp.proxy(w, r, dp.config.PrometheusBaseURL, "/api/v1"+path)
	})

	// Tracing proxy.
	mux.HandleFunc("/api/dashboard/traces/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/dashboard/traces")
		dp.proxy(w, r, dp.config.TracingBaseURL, "/api/traces"+path)
	})

	// Services catalog (via Discovery).
	mux.HandleFunc("/api/dashboard/services", func(w http.ResponseWriter, r *http.Request) {
		dp.proxy(w, r, dp.config.DiscoveryBaseURL, "/api/ServiceDiscovery/services")
	})

	// Health snapshots (via HealthMonitor).
	mux.HandleFunc("/api/dashboard/health", func(w http.ResponseWriter, r *http.Request) {
		dp.proxy(w, r, dp.config.HealthMonitorBaseURL, "/api/status")
	})

	return mux
}

func (dp *DashboardProxy) proxy(w http.ResponseWriter, r *http.Request, baseURL, path string) {
	targetURL := baseURL + path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Forward relevant headers.
	for _, h := range []string{"Authorization", "Content-Type", "Accept"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}

	resp, err := dp.client.Do(req)
	if err != nil {
		dp.logger.Warn("dashboard proxy failed", "url", targetURL, "error", err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
