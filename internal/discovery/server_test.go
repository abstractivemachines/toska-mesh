package discovery

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc/peer"

	"github.com/toska-mesh/toska-mesh/internal/consul"
	pb "github.com/toska-mesh/toska-mesh/pkg/meshpb"
)

func TestIsRoutable(t *testing.T) {
	tests := []struct {
		addr     string
		routable bool
	}{
		{"", false},
		{"0.0.0.0", false},
		{"::", false},
		{"127.0.0.1", false},
		{"::1", false},
		{"192.168.1.100", true},
		{"10.0.0.5", true},
		{"my-host.local", true},
	}

	for _, tt := range tests {
		got := isRoutable(tt.addr)
		if got != tt.routable {
			t.Errorf("isRoutable(%q) = %v, want %v", tt.addr, got, tt.routable)
		}
	}
}

func TestResolveAddress_KeepsRoutableAddress(t *testing.T) {
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.99"), Port: 50000},
	})
	got := resolveAddress(ctx, "192.168.1.50")
	if got != "192.168.1.50" {
		t.Errorf("expected 192.168.1.50, got %s", got)
	}
}

func TestResolveAddress_ReplacesLoopbackWithPeerIP(t *testing.T) {
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.99"), Port: 50000},
	})
	got := resolveAddress(ctx, "127.0.0.1")
	if got != "10.0.0.99" {
		t.Errorf("expected 10.0.0.99, got %s", got)
	}
}

func TestResolveAddress_ReplacesEmptyWithPeerIP(t *testing.T) {
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.99"), Port: 50000},
	})
	got := resolveAddress(ctx, "")
	if got != "10.0.0.99" {
		t.Errorf("expected 10.0.0.99, got %s", got)
	}
}

func TestResolveAddress_FallsBackToRequested(t *testing.T) {
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 50000},
	})
	got := resolveAddress(ctx, "0.0.0.0")
	if got != "0.0.0.0" {
		t.Errorf("expected 0.0.0.0, got %s", got)
	}
}

func TestHealthStatusRoundTrip(t *testing.T) {
	cases := []struct {
		proto  pb.HealthStatus
		consul consul.HealthStatus
	}{
		{pb.HealthStatus_HEALTH_STATUS_UNKNOWN, consul.HealthUnknown},
		{pb.HealthStatus_HEALTH_STATUS_HEALTHY, consul.HealthHealthy},
		{pb.HealthStatus_HEALTH_STATUS_UNHEALTHY, consul.HealthUnhealthy},
		{pb.HealthStatus_HEALTH_STATUS_DEGRADED, consul.HealthDegraded},
	}

	for _, tt := range cases {
		gotConsul := fromProtoHealth(tt.proto)
		if gotConsul != tt.consul {
			t.Errorf("fromProtoHealth(%v) = %v, want %v", tt.proto, gotConsul, tt.consul)
		}
		gotProto := toProtoHealth(tt.consul)
		if gotProto != tt.proto {
			t.Errorf("toProtoHealth(%v) = %v, want %v", tt.consul, gotProto, tt.proto)
		}
	}
}

func TestHealthStatusName(t *testing.T) {
	tests := []struct {
		status consul.HealthStatus
		name   string
	}{
		{consul.HealthHealthy, "Healthy"},
		{consul.HealthUnhealthy, "Unhealthy"},
		{consul.HealthDegraded, "Degraded"},
		{consul.HealthUnknown, "Unknown"},
	}

	for _, tt := range tests {
		got := healthStatusName(tt.status)
		if got != tt.name {
			t.Errorf("healthStatusName(%v) = %q, want %q", tt.status, got, tt.name)
		}
	}
}
