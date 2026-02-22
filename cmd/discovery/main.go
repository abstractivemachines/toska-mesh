package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"github.com/toska-mesh/toska-mesh/internal/consul"
	"github.com/toska-mesh/toska-mesh/internal/discovery"
	"github.com/toska-mesh/toska-mesh/internal/messaging"
	pb "github.com/toska-mesh/toska-mesh/pkg/meshpb"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(logger); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	port := envOr("DISCOVERY_PORT", "8080")
	consulAddr := envOr("CONSUL_ADDRESS", "http://localhost:8500")
	rabbitURL := os.Getenv("RABBITMQ_URL")

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

	// gRPC server.
	grpcServer := grpc.NewServer()

	discoverySvc := discovery.NewServer(registry, publisher, logger)
	pb.RegisterDiscoveryRegistryServer(grpcServer, discoverySvc)

	// Standard gRPC health check service.
	healthSvc := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthSvc)
	healthSvc.SetServingStatus("toskamesh.discovery.DiscoveryRegistry", healthpb.HealthCheckResponse_SERVING)

	// Enable reflection for grpcurl/grpcui debugging.
	reflection.Register(grpcServer)

	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		logger.Info("shutting down gRPC server")
		grpcServer.GracefulStop()
	}()

	logger.Info("discovery server starting", "port", port, "consul", consulAddr)
	return grpcServer.Serve(lis)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
