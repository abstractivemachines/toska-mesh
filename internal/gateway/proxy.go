package gateway

import (
	"errors"
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

// bufferedResponse holds a captured upstream response so the proxy can
// inspect the status code before committing bytes to the client.
type bufferedResponse struct {
	statusCode int
	header     http.Header
	body       []byte
}

// writeTo flushes the buffered response to the client.
func (br *bufferedResponse) writeTo(w http.ResponseWriter) {
	for k, vv := range br.header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(br.statusCode)
	w.Write(br.body)
}

// maxRequestBody is the maximum allowed size for incoming client request bodies (10MB).
const maxRequestBody = 10 << 20

// ServeHTTP handles an incoming request by routing it to a backend service.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
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
	var lastResp *bufferedResponse

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

		br, err := p.forward(r, backend, remainder)
		if err == nil && br.statusCode < 500 {
			cb.RecordSuccess()
			br.writeTo(w)
			return
		}

		// Record failure for circuit breaker.
		cb.RecordFailure()
		lastErr = err
		if br != nil {
			lastStatus = br.statusCode
			lastResp = br
		}
	}

	// All attempts exhausted — write the best response we have.
	if lastResp != nil {
		// We got a 5xx from upstream; forward it to the client.
		lastResp.writeTo(w)
		return
	}

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

func (p *Proxy) forward(r *http.Request, backend *Backend, remainder string) (*bufferedResponse, error) {
	backendURL, err := url.Parse(backend.Address)
	if err != nil {
		return nil, err
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

	// Limit the upstream response body to 10MB to prevent memory exhaustion.
	const maxResponseBody = 10 << 20

	resp, err := p.transport.RoundTrip(outReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, err
	}

	return &bufferedResponse{
		statusCode: resp.StatusCode,
		header:     resp.Header.Clone(),
		body:       body,
	}, nil
}


func (p *Proxy) retryDelay(attempt int) time.Duration {
	base := float64(p.resilience.RetryBaseDelay)
	exponential := base * math.Pow(p.resilience.RetryBackoffExponent, float64(attempt-1))
	jitter := rand.Float64() * float64(p.resilience.RetryJitterMax)
	return time.Duration(exponential + jitter)
}

var errCircuitOpen = errors.New("circuit breaker open")

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
