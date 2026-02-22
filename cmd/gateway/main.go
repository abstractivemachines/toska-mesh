package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/toska-mesh/toska-mesh/internal/consul"
	"github.com/toska-mesh/toska-mesh/internal/gateway"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(logger); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg := loadConfig()

	// Consul registry.
	registry, err := consul.NewRegistry(cfg.ConsulAddr, logger)
	if err != nil {
		return fmt.Errorf("consul registry: %w", err)
	}

	// Route table (polls Consul periodically).
	routeTable := gateway.NewRouteTable(registry, cfg.Routing, logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start route table refresh in background.
	go routeTable.Run(ctx)

	// Build the handler chain.
	proxy := gateway.NewProxy(routeTable, cfg.Resilience, logger)
	dashboard := gateway.NewDashboardProxy(cfg.Dashboard, logger)

	mux := http.NewServeMux()

	// Health endpoint (no auth, no rate limiting).
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "Healthy"})
	})

	// Dashboard proxy routes.
	mux.Handle("/api/dashboard/", dashboard.Handler())

	// Dynamic service proxy (catch-all under the route prefix).
	mux.Handle(cfg.Routing.RoutePrefix, proxy)

	// Compose middleware stack (outermost first).
	var handler http.Handler = mux

	// JWT auth (skip health and dashboard).
	handler = gateway.JWTAuth(cfg.JWT, []string{"/health", "/api/dashboard/"})(handler)

	// Rate limiting.
	if cfg.RateLimit.Enabled {
		rl := gateway.NewRateLimiter(cfg.RateLimit.PermitLimit, cfg.RateLimit.WindowSeconds)
		handler = rl.Middleware(handler)
	}

	// CORS.
	handler = gateway.CORS(cfg.CORS)(handler)

	// Request logging.
	handler = gateway.RequestLogging(logger, handler)

	server := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: handler,
	}

	go func() {
		<-ctx.Done()
		logger.Info("shutting down gateway")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	logger.Info("gateway starting",
		"port", cfg.Port,
		"consul", cfg.ConsulAddr,
		"route_prefix", cfg.Routing.RoutePrefix,
	)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("http server: %w", err)
	}
	return nil
}

func loadConfig() gateway.Config {
	cfg := gateway.DefaultConfig()

	if v := os.Getenv("GATEWAY_PORT"); v != "" {
		cfg.Port = v
	}
	if v := os.Getenv("CONSUL_ADDRESS"); v != "" {
		cfg.ConsulAddr = v
	}
	if v := os.Getenv("GATEWAY_ROUTE_PREFIX"); v != "" {
		cfg.Routing.RoutePrefix = v
	}
	if v, err := strconv.Atoi(os.Getenv("GATEWAY_ROUTE_REFRESH_SECONDS")); err == nil && v > 0 {
		cfg.Routing.RefreshInterval = time.Duration(v) * time.Second
	}

	// Rate limit.
	if os.Getenv("GATEWAY_RATE_LIMIT_ENABLED") == "false" {
		cfg.RateLimit.Enabled = false
	}
	if v, err := strconv.Atoi(os.Getenv("GATEWAY_RATE_LIMIT_PERMITS")); err == nil && v > 0 {
		cfg.RateLimit.PermitLimit = v
	}
	if v, err := strconv.Atoi(os.Getenv("GATEWAY_RATE_LIMIT_WINDOW_SECONDS")); err == nil && v > 0 {
		cfg.RateLimit.WindowSeconds = v
	}

	// CORS.
	if os.Getenv("GATEWAY_CORS_ALLOW_ANY_ORIGIN") == "false" {
		cfg.CORS.AllowAnyOrigin = false
	}
	if v := os.Getenv("GATEWAY_CORS_ALLOWED_ORIGINS"); v != "" {
		cfg.CORS.AllowedOrigins = splitComma(v)
	}

	// JWT.
	cfg.JWT.SecretKey = os.Getenv("JWT_SECRET_KEY")
	cfg.JWT.Issuer = envOr("JWT_ISSUER", "ToskaMesh.Gateway")
	cfg.JWT.Audience = envOr("JWT_AUDIENCE", "ToskaMesh.Services")

	// Resilience.
	if v, err := strconv.Atoi(os.Getenv("GATEWAY_RETRY_COUNT")); err == nil && v >= 0 {
		cfg.Resilience.RetryCount = v
	}

	// Dashboard.
	if v := os.Getenv("DASHBOARD_PROMETHEUS_URL"); v != "" {
		cfg.Dashboard.PrometheusBaseURL = v
	}
	if v := os.Getenv("DASHBOARD_TRACING_URL"); v != "" {
		cfg.Dashboard.TracingBaseURL = v
	}
	if v := os.Getenv("DASHBOARD_DISCOVERY_URL"); v != "" {
		cfg.Dashboard.DiscoveryBaseURL = v
	}
	if v := os.Getenv("DASHBOARD_HEALTHMONITOR_URL"); v != "" {
		cfg.Dashboard.HealthMonitorBaseURL = v
	}

	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func splitComma(s string) []string {
	parts := make([]string, 0)
	for _, p := range splitOnComma(s) {
		p = trimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func splitOnComma(s string) []string {
	var result []string
	start := 0
	for i := range len(s) {
		if s[i] == ',' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	result = append(result, s[start:])
	return result
}

func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && s[i] == ' ' {
		i++
	}
	for j > i && s[j-1] == ' ' {
		j--
	}
	return s[i:j]
}
