package gateway

import (
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/toska-mesh/toska-mesh/internal/healthmonitor"
)

// Proxy is the reverse proxy handler that routes requests to backend services
// with retry and circuit breaker resilience.
type Proxy struct {
	routes     *RouteTable
	resilience ResilienceConfig
	logger     *slog.Logger
	transport  http.RoundTripper

	breakers *breakerMap
}

// NewProxy creates a reverse proxy backed by the given route table.
func NewProxy(routes *RouteTable, resilience ResilienceConfig, logger *slog.Logger) *Proxy {
	return &Proxy{
		routes:     routes,
		resilience: resilience,
		logger:     logger,
		transport:  http.DefaultTransport,
		breakers:   newBreakerMap(resilience.BreakerFailureThreshold, resilience.BreakerBreakDuration),
	}
}

// ServeHTTP handles an incoming request by routing it to a backend service.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	prefix := p.routes.Prefix()

	serviceName, remainder, ok := ParseServiceFromPath(prefix, r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	backend := p.routes.Lookup(serviceName)
	if backend == nil {
		http.Error(w, "service not found: "+serviceName, http.StatusBadGateway)
		return
	}

	// Attempt the request with retries.
	var lastErr error
	var lastStatus int

	for attempt := range p.resilience.RetryCount + 1 {
		if attempt > 0 {
			delay := p.retryDelay(attempt)
			p.logger.Warn("retrying upstream request",
				"attempt", attempt+1,
				"max_attempts", p.resilience.RetryCount+1,
				"delay", delay,
				"service", serviceName,
			)
			time.Sleep(delay)

			// Re-lookup in case route table changed.
			if b := p.routes.Lookup(serviceName); b != nil {
				backend = b
			}
		}

		// Circuit breaker check.
		cb := p.breakers.get(backend.ServiceID)
		if !cb.Allow() {
			lastErr = errCircuitOpen
			lastStatus = http.StatusServiceUnavailable
			continue
		}

		status, err := p.forward(w, r, backend, remainder)
		if err == nil && status < 500 {
			cb.RecordSuccess()
			return // response already written
		}

		// Record failure for circuit breaker.
		cb.RecordFailure()
		lastErr = err
		lastStatus = status

		// Don't retry if response was already partially written.
		if err == nil {
			return
		}
	}

	// All attempts exhausted.
	if lastErr != nil {
		p.logger.Error("upstream request failed after retries",
			"service", serviceName,
			"error", lastErr,
		)
	}
	if lastStatus == 0 {
		lastStatus = http.StatusBadGateway
	}
	http.Error(w, "upstream request failed", lastStatus)
}

func (p *Proxy) forward(w http.ResponseWriter, r *http.Request, backend *Backend, remainder string) (int, error) {
	backendURL, err := url.Parse(backend.Address)
	if err != nil {
		return 0, err
	}

	// Build upstream request.
	outReq := r.Clone(r.Context())
	outReq.URL.Scheme = backendURL.Scheme
	outReq.URL.Host = backendURL.Host
	outReq.URL.Path = remainder
	outReq.URL.RawQuery = r.URL.RawQuery
	outReq.Host = backendURL.Host
	outReq.RequestURI = ""

	// Forward hop-by-hop headers.
	outReq.Header.Del("Connection")

	resp, err := p.transport.RoundTrip(outReq)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	// Copy response headers.
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)

	return resp.StatusCode, nil
}

func (p *Proxy) retryDelay(attempt int) time.Duration {
	base := float64(p.resilience.RetryBaseDelay)
	exponential := base * math.Pow(p.resilience.RetryBackoffExponent, float64(attempt-1))
	jitter := rand.Float64() * float64(p.resilience.RetryJitterMax)
	return time.Duration(exponential + jitter)
}

var errCircuitOpen = jwtError("circuit breaker open")

// --- Breaker map ---

type breakerMap struct {
	threshold int
	duration  time.Duration
	mu        sync.Mutex
	breakers  map[string]*healthmonitor.CircuitBreaker
}

func newBreakerMap(threshold int, duration time.Duration) *breakerMap {
	return &breakerMap{
		threshold: threshold,
		duration:  duration,
		breakers:  make(map[string]*healthmonitor.CircuitBreaker),
	}
}

func (bm *breakerMap) get(serviceID string) *healthmonitor.CircuitBreaker {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	cb, ok := bm.breakers[serviceID]
	if !ok {
		cb = healthmonitor.NewCircuitBreaker(bm.threshold, bm.duration)
		bm.breakers[serviceID] = cb
	}
	return cb
}
