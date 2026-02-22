package consul

import (
	"testing"

	"github.com/hashicorp/consul/api"
)

func TestMapHealthStatus(t *testing.T) {
	tests := []struct {
		name   string
		checks api.HealthChecks
		want   HealthStatus
	}{
		{
			name:   "nil checks returns unknown",
			checks: nil,
			want:   HealthUnknown,
		},
		{
			name:   "empty checks returns unknown",
			checks: api.HealthChecks{},
			want:   HealthUnknown,
		},
		{
			name: "all passing returns healthy",
			checks: api.HealthChecks{
				{Status: "passing"},
				{Status: "passing"},
			},
			want: HealthHealthy,
		},
		{
			name: "any critical returns unhealthy",
			checks: api.HealthChecks{
				{Status: "passing"},
				{Status: "critical"},
			},
			want: HealthUnhealthy,
		},
		{
			name: "maintenance returns unhealthy",
			checks: api.HealthChecks{
				{Status: "maintenance"},
			},
			want: HealthUnhealthy,
		},
		{
			name: "warning without critical returns degraded",
			checks: api.HealthChecks{
				{Status: "passing"},
				{Status: "warning"},
			},
			want: HealthDegraded,
		},
		{
			name: "critical takes priority over warning",
			checks: api.HealthChecks{
				{Status: "warning"},
				{Status: "critical"},
			},
			want: HealthUnhealthy,
		},
		{
			name: "unknown status returns unknown",
			checks: api.HealthChecks{
				{Status: "something_else"},
			},
			want: HealthUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapHealthStatus(tt.checks)
			if got != tt.want {
				t.Errorf("mapHealthStatus() = %v, want %v", got, tt.want)
			}
		})
	}
}
