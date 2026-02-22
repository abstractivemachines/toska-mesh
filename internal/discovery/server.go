// Package discovery implements the DiscoveryRegistry gRPC service that
// manages service registration, health reporting, and instance queries.
package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/toska-mesh/toska-mesh/internal/consul"
	"github.com/toska-mesh/toska-mesh/internal/messaging"
	pb "github.com/toska-mesh/toska-mesh/pkg/meshpb"
)

// Server implements the DiscoveryRegistry gRPC service.
type Server struct {
	pb.UnimplementedDiscoveryRegistryServer

	registry  *consul.Registry
	publisher *messaging.Publisher
	logger    *slog.Logger

	// In-memory tracking for metadata and timestamps that Consul doesn't store.
	mu       sync.RWMutex
	tracking map[string]*trackingInfo
}

type trackingInfo struct {
	ServiceName    string
	RegisteredAt   time.Time
	DeregisteredAt *time.Time
	LastUpdated    time.Time
	Status         consul.HealthStatus
	LastHealthCheck *time.Time
	Metadata       map[string]string
}

// NewServer creates a Discovery gRPC server backed by Consul.
func NewServer(registry *consul.Registry, publisher *messaging.Publisher, logger *slog.Logger) *Server {
	return &Server{
		registry:  registry,
		publisher: publisher,
		logger:    logger,
		tracking:  make(map[string]*trackingInfo),
	}
}

func (s *Server) Register(ctx context.Context, req *pb.RegisterServiceRequest) (*pb.RegisterServiceResponse, error) {
	serviceID := req.ServiceId
	if serviceID == "" {
		serviceID = fmt.Sprintf("%s-%d", req.ServiceName, time.Now().UnixNano())
	}

	// Resolve address: replace loopback/unspecified with caller's actual IP.
	address := resolveAddress(req.Address, ctx)

	metadata := make(map[string]string)
	for k, v := range req.Metadata {
		metadata[k] = v
	}

	reg := consul.Registration{
		ServiceName: req.ServiceName,
		ServiceID:   serviceID,
		Address:     address,
		Port:        int(req.Port),
		Metadata:    metadata,
	}

	if req.HealthCheck != nil {
		reg.HealthCheck = &consul.HealthCheckConfig{
			Endpoint:           req.HealthCheck.Endpoint,
			IntervalSeconds:    int(req.HealthCheck.IntervalSeconds),
			TimeoutSeconds:     int(req.HealthCheck.TimeoutSeconds),
			UnhealthyThreshold: int(req.HealthCheck.UnhealthyThreshold),
		}
	}

	if err := s.registry.Register(reg); err != nil {
		s.logger.Error("registration failed", "service_id", serviceID, "error", err)
		return &pb.RegisterServiceResponse{
			Success:      false,
			ServiceId:    serviceID,
			ErrorMessage: err.Error(),
		}, nil
	}

	// Track registration in memory.
	now := time.Now().UTC()
	s.mu.Lock()
	s.tracking[serviceID] = &trackingInfo{
		ServiceName:  req.ServiceName,
		RegisteredAt: now,
		LastUpdated:  now,
		Status:       consul.HealthHealthy,
		Metadata:     metadata,
	}
	s.mu.Unlock()

	// Publish event.
	_ = s.publisher.Publish(ctx, messaging.ServiceRegisteredEvent{
		EventID:     fmt.Sprintf("%d", time.Now().UnixNano()),
		Timestamp:   now,
		ServiceID:   serviceID,
		ServiceName: req.ServiceName,
		Address:     address,
		Port:        int(req.Port),
		Metadata:    metadata,
	})

	s.logger.Info("service registered",
		"service_id", serviceID,
		"service_name", req.ServiceName,
		"address", address,
		"port", req.Port,
	)

	return &pb.RegisterServiceResponse{
		Success:   true,
		ServiceId: serviceID,
	}, nil
}

func (s *Server) Deregister(ctx context.Context, req *pb.DeregisterServiceRequest) (*pb.DeregisterServiceResponse, error) {
	// Capture service name before deregistration for the event.
	s.mu.RLock()
	info := s.tracking[req.ServiceId]
	s.mu.RUnlock()

	serviceName := ""
	if info != nil {
		serviceName = info.ServiceName
	}

	if err := s.registry.Deregister(req.ServiceId); err != nil {
		s.logger.Error("deregistration failed", "service_id", req.ServiceId, "error", err)
		return &pb.DeregisterServiceResponse{Removed: false}, nil
	}

	// Update tracking.
	now := time.Now().UTC()
	s.mu.Lock()
	if t, ok := s.tracking[req.ServiceId]; ok {
		t.DeregisteredAt = &now
		t.LastUpdated = now
	}
	s.mu.Unlock()

	// Publish event.
	_ = s.publisher.Publish(ctx, messaging.ServiceDeregisteredEvent{
		EventID:     fmt.Sprintf("%d", time.Now().UnixNano()),
		Timestamp:   now,
		ServiceID:   req.ServiceId,
		ServiceName: serviceName,
		Reason:      "Manual deregistration",
	})

	return &pb.DeregisterServiceResponse{Removed: true}, nil
}

func (s *Server) GetInstances(ctx context.Context, req *pb.GetInstancesRequest) (*pb.GetInstancesResponse, error) {
	instances, err := s.registry.GetInstances(req.ServiceName)
	if err != nil {
		return nil, fmt.Errorf("get instances: %w", err)
	}

	resp := &pb.GetInstancesResponse{}
	for _, inst := range instances {
		// Merge tracking metadata with Consul metadata.
		meta := s.mergeMetadata(inst.ServiceID, inst.Metadata)
		regTime, lastCheck := s.getTimestamps(inst.ServiceID, inst.RegisteredAt)

		resp.Instances = append(resp.Instances, &pb.ServiceInstance{
			ServiceName:     inst.ServiceName,
			ServiceId:       inst.ServiceID,
			Address:         inst.Address,
			Port:            int32(inst.Port),
			Status:          toProtoHealth(inst.Status),
			Metadata:        meta,
			RegisteredAt:    timestamppb.New(regTime),
			LastHealthCheck: timestamppb.New(lastCheck),
		})
	}

	return resp, nil
}

func (s *Server) GetServices(ctx context.Context, req *pb.GetServicesRequest) (*pb.GetServicesResponse, error) {
	names, err := s.registry.GetServices()
	if err != nil {
		return nil, fmt.Errorf("get services: %w", err)
	}

	return &pb.GetServicesResponse{ServiceNames: names}, nil
}

func (s *Server) ReportHealth(ctx context.Context, req *pb.ReportHealthRequest) (*pb.ReportHealthResponse, error) {
	newStatus := fromProtoHealth(req.Status)

	// Detect health transition for event publishing.
	s.mu.RLock()
	info := s.tracking[req.ServiceId]
	s.mu.RUnlock()

	var previousStatus consul.HealthStatus
	serviceName := ""
	if info != nil {
		previousStatus = info.Status
		serviceName = info.ServiceName
	}

	if err := s.registry.UpdateHealth(req.ServiceId, newStatus, req.Output); err != nil {
		s.logger.Error("health update failed", "service_id", req.ServiceId, "error", err)
		return &pb.ReportHealthResponse{Success: false}, nil
	}

	// Update tracking.
	now := time.Now().UTC()
	s.mu.Lock()
	if t, ok := s.tracking[req.ServiceId]; ok {
		t.Status = newStatus
		t.LastHealthCheck = &now
		t.LastUpdated = now
	}
	s.mu.Unlock()

	// Publish health change event if status actually changed.
	if info != nil && previousStatus != newStatus {
		_ = s.publisher.Publish(ctx, messaging.ServiceHealthChangedEvent{
			EventID:           fmt.Sprintf("%d", time.Now().UnixNano()),
			Timestamp:         now,
			ServiceID:         req.ServiceId,
			ServiceName:       serviceName,
			PreviousStatus:    healthStatusName(previousStatus),
			CurrentStatus:     healthStatusName(newStatus),
			HealthCheckOutput: req.Output,
		})
	}

	return &pb.ReportHealthResponse{Success: true}, nil
}

// --- Helpers ---

// resolveAddress replaces loopback/unspecified addresses with the caller's
// actual IP extracted from the gRPC peer context.
func resolveAddress(requested string, ctx context.Context) string {
	if isRoutable(requested) {
		return requested
	}

	// Extract caller IP from gRPC peer info.
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		host, _, err := net.SplitHostPort(p.Addr.String())
		if err == nil && isRoutable(host) {
			return host
		}
	}

	if requested != "" {
		return requested
	}
	return "127.0.0.1"
}

func isRoutable(addr string) bool {
	if addr == "" || addr == "0.0.0.0" || addr == "::" {
		return false
	}
	ip := net.ParseIP(addr)
	if ip == nil {
		return true // hostname, assume routable
	}
	return !ip.IsLoopback() && !ip.IsUnspecified()
}

func (s *Server) mergeMetadata(serviceID string, consulMeta map[string]string) map[string]string {
	merged := make(map[string]string)
	for k, v := range consulMeta {
		merged[k] = v
	}

	s.mu.RLock()
	if info, ok := s.tracking[serviceID]; ok {
		for k, v := range info.Metadata {
			if _, exists := merged[k]; !exists {
				merged[k] = v
			}
		}
	}
	s.mu.RUnlock()

	return merged
}

func (s *Server) getTimestamps(serviceID string, fallbackReg time.Time) (registeredAt, lastCheck time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if info, ok := s.tracking[serviceID]; ok {
		registeredAt = info.RegisteredAt
		if info.LastHealthCheck != nil {
			lastCheck = *info.LastHealthCheck
		}
		return
	}

	return fallbackReg, time.Time{}
}

func toProtoHealth(s consul.HealthStatus) pb.HealthStatus {
	switch s {
	case consul.HealthHealthy:
		return pb.HealthStatus_HEALTH_STATUS_HEALTHY
	case consul.HealthUnhealthy:
		return pb.HealthStatus_HEALTH_STATUS_UNHEALTHY
	case consul.HealthDegraded:
		return pb.HealthStatus_HEALTH_STATUS_DEGRADED
	default:
		return pb.HealthStatus_HEALTH_STATUS_UNKNOWN
	}
}

func fromProtoHealth(s pb.HealthStatus) consul.HealthStatus {
	switch s {
	case pb.HealthStatus_HEALTH_STATUS_HEALTHY:
		return consul.HealthHealthy
	case pb.HealthStatus_HEALTH_STATUS_UNHEALTHY:
		return consul.HealthUnhealthy
	case pb.HealthStatus_HEALTH_STATUS_DEGRADED:
		return consul.HealthDegraded
	default:
		return consul.HealthUnknown
	}
}

func healthStatusName(s consul.HealthStatus) string {
	switch s {
	case consul.HealthHealthy:
		return "Healthy"
	case consul.HealthUnhealthy:
		return "Unhealthy"
	case consul.HealthDegraded:
		return "Degraded"
	default:
		return "Unknown"
	}
}
