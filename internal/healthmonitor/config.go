package healthmonitor

import "time"

// Config holds HealthMonitor runtime configuration.
type Config struct {
	ProbeInterval    time.Duration
	HTTPTimeout      time.Duration
	TCPTimeout       time.Duration
	FailureThreshold int
	RecoveryThreshold int
	HTTPHeaders      map[string]string
}

// DefaultConfig returns sensible defaults matching the C# HealthMonitorOptions.
func DefaultConfig() Config {
	return Config{
		ProbeInterval:    30 * time.Second,
		HTTPTimeout:      5 * time.Second,
		TCPTimeout:       3 * time.Second,
		FailureThreshold: 3,
		RecoveryThreshold: 2,
		HTTPHeaders:      nil,
	}
}
