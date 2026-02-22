// Package gateway implements the ToskaMesh API gateway — a reverse proxy
// with dynamic Consul-based routing, rate limiting, CORS, JWT auth, and resilience.
package gateway

import "time"

// Config holds all Gateway runtime configuration.
type Config struct {
	Port       string
	ConsulAddr string
	RabbitURL  string

	Routing    RoutingConfig
	RateLimit  RateLimitConfig
	CORS       CORSConfig
	JWT        JWTConfig
	Resilience ResilienceConfig
	Dashboard  DashboardConfig
}

// DefaultConfig returns sensible defaults matching the C# appsettings.json.
func DefaultConfig() Config {
	return Config{
		Port:       "5000",
		ConsulAddr: "http://localhost:8500",
		Routing: RoutingConfig{
			RoutePrefix:     "/api/",
			RefreshInterval: 30 * time.Second,
		},
		RateLimit: RateLimitConfig{
			Enabled:       true,
			PermitLimit:   100,
			WindowSeconds: 60,
		},
		CORS: CORSConfig{
			AllowAnyOrigin: true,
			AllowedHeaders: []string{"Authorization", "Content-Type"},
			AllowedMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		},
		JWT: JWTConfig{
			ValidateIssuer:   true,
			ValidateAudience: true,
		},
		Resilience: ResilienceConfig{
			RetryCount:              3,
			RetryBaseDelay:          200 * time.Millisecond,
			RetryBackoffExponent:    2.0,
			RetryJitterMax:          200 * time.Millisecond,
			BreakerFailureThreshold: 3,
			BreakerBreakDuration:    20 * time.Second,
		},
		Dashboard: DashboardConfig{
			PrometheusBaseURL:    "http://localhost:9090",
			TracingBaseURL:       "http://localhost:5004",
			DiscoveryBaseURL:     "http://localhost:5010",
			HealthMonitorBaseURL: "http://localhost:5005",
		},
	}
}

// RoutingConfig controls dynamic route building from Consul.
type RoutingConfig struct {
	RoutePrefix     string
	RefreshInterval time.Duration
}

// RateLimitConfig controls per-client-IP rate limiting.
type RateLimitConfig struct {
	Enabled       bool
	PermitLimit   int
	WindowSeconds int
}

// CORSConfig controls Cross-Origin Resource Sharing headers.
type CORSConfig struct {
	AllowAnyOrigin bool
	AllowedOrigins []string
	AllowedHeaders []string
	AllowedMethods []string
}

// JWTConfig controls JWT bearer token validation.
type JWTConfig struct {
	SecretKey        string
	Issuer           string
	Audience         string
	ValidateIssuer   bool
	ValidateAudience bool
}

// ResilienceConfig controls retry and circuit breaker behavior.
type ResilienceConfig struct {
	RetryCount              int
	RetryBaseDelay          time.Duration
	RetryBackoffExponent    float64
	RetryJitterMax          time.Duration
	BreakerFailureThreshold int
	BreakerBreakDuration    time.Duration
}

// DashboardConfig holds base URLs for dashboard proxy endpoints.
type DashboardConfig struct {
	PrometheusBaseURL    string
	TracingBaseURL       string
	DiscoveryBaseURL     string
	HealthMonitorBaseURL string
}
