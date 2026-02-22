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
	"github.com/toska-mesh/toska-mesh/internal/healthmonitor"
	"github.com/toska-mesh/toska-mesh/internal/messaging"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(logger); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	port := envOr("HEALTHMONITOR_PORT", "8081")
	consulAddr := envOr("CONSUL_ADDRESS", "http://localhost:8500")
	rabbitURL := os.Getenv("RABBITMQ_URL")

	cfg := healthmonitor.DefaultConfig()
	if v, err := strconv.Atoi(os.Getenv("HEALTHMONITOR_PROBE_INTERVAL_SECONDS")); err == nil && v > 0 {
		cfg.ProbeInterval = time.Duration(v) * time.Second
	}
	if v, err := strconv.Atoi(os.Getenv("HEALTHMONITOR_HTTP_TIMEOUT_SECONDS")); err == nil && v > 0 {
		cfg.HTTPTimeout = time.Duration(v) * time.Second
	}
	if v, err := strconv.Atoi(os.Getenv("HEALTHMONITOR_TCP_TIMEOUT_SECONDS")); err == nil && v > 0 {
		cfg.TCPTimeout = time.Duration(v) * time.Second
	}
	if v, err := strconv.Atoi(os.Getenv("HEALTHMONITOR_FAILURE_THRESHOLD")); err == nil && v > 0 {
		cfg.FailureThreshold = v
	}

	// Consul registry.
	registry, err := consul.NewRegistry(consulAddr, logger)
	if err != nil {
		return fmt.Errorf("consul registry: %w", err)
	}

	// RabbitMQ publisher (no-op if URL is empty).
	publisher, err := messaging.NewPublisher(rabbitURL, logger)
	if err != nil {
		return fmt.Errorf("rabbitmq publisher: %w", err)
	}
	defer publisher.Close()

	cache := healthmonitor.NewCache()
	worker := healthmonitor.NewWorker(registry, publisher, cache, cfg, logger)

	// Graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start probe worker in background.
	go worker.Run(ctx)

	// HTTP API.
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "Healthy"})
	})

	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cache.GetAll())
	})

	mux.HandleFunc("GET /api/status/{serviceName}", func(w http.ResponseWriter, r *http.Request) {
		serviceName := r.PathValue("serviceName")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cache.GetByService(serviceName))
	})

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		logger.Info("shutting down HTTP server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	logger.Info("healthmonitor starting", "port", port, "consul", consulAddr, "probe_interval", cfg.ProbeInterval)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("http server: %w", err)
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
